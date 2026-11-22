package soundcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	homeURL  = "https://soundcloud.com/"
	apiURL   = "https://api-v2.soundcloud.com/search/tracks"
	trackURL = "https://api-v2.soundcloud.com/tracks/"

	browserUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	maxResponseBytes = 16 << 20

	defaultSearchLimit = 5
	maxSearchLimit     = 15
	clientIDTTL        = 6 * time.Hour
	maxAudioBytes      = 64 << 20
)

var (
	// ErrEmptyQuery dikembalikan bila judul lagu yang dicari kosong.
	ErrEmptyQuery = errors.New("judul lagu tidak boleh kosong")

	// ErrInvalidTrack dikembalikan bila ID track tidak masuk akal.
	ErrInvalidTrack = errors.New("ID track SoundCloud tidak valid")

	// errInvalidClientID menandai client_id yang sudah tidak diterima API,
	// sehingga cache perlu dibuang dan diambil ulang.
	errInvalidClientID = errors.New("client_id SoundCloud sudah tidak valid")

	assetPattern    = regexp.MustCompile(`https://a-v2\.sndcdn\.com/assets/[^"'\s]+\.js`)
	clientIDPattern = regexp.MustCompile(`client_id["']?\s*[:=]\s*["']?([A-Za-z0-9]{32})`)
	extMapPattern   = regexp.MustCompile(`URI="([^"]+)"`)

	// Batas jumlah segmen HLS yang diunduh agar satu permintaan tidak
	// berubah menjadi ratusan request tanpa kendali.
	maxHLSSegments = 600

	// Audio hasil konversi disimpan per track agar pemutaran berikutnya tidak
	// perlu mengunduh ulang segmen HLS dan meng-encode ulang.
	cacheDir = "data/cache/soundcloud"

	httpClient     = &http.Client{Timeout: 30 * time.Second}
	downloadClient = &http.Client{Timeout: 5 * time.Minute}

	clientIDMu  sync.Mutex
	clientIDVal string
	clientIDExp time.Time
)

// Track adalah satu hasil pencarian lagu SoundCloud.
type Track struct {
	ID                int64  `json:"id"`
	Title             string `json:"title"`
	Artist            string `json:"artist"`
	ArtistURL         string `json:"artist_url"`
	URL               string `json:"url"`
	Artwork           string `json:"artwork"`
	Genre             string `json:"genre"`
	Duration          int    `json:"duration"` // detik
	FormattedDuration string `json:"formatted_duration"`
	Plays             int64  `json:"plays"`
	Likes             int64  `json:"likes"`
	CreatedAt         string `json:"created_at"`
}

// SearchResult adalah hasil pencarian SoundCloud untuk satu kata kunci.
type SearchResult struct {
	Query  string  `json:"query"`
	Total  int64   `json:"total"`
	Tracks []Track `json:"tracks"`
}

// Search mencari lagu SoundCloud berdasarkan judul.
//
// SoundCloud tidak lagi menyajikan hasil pencarian di HTML (dirender lewat
// JavaScript), jadi fungsi ini memakai API internal yang dipakai web SoundCloud
// sendiri. API tersebut membutuhkan client_id publik yang ditanam pada bundle
// JavaScript halaman depan, dan nilai itu berubah berkala sehingga diambil
// ulang otomatis saat API menolaknya.
func Search(ctx context.Context, query string, limit int) (*SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cid, err := getClientID(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		result, err := searchWithClientID(ctx, cid, query, limit)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !errors.Is(err, errInvalidClientID) {
			return nil, err
		}
	}

	if lastErr == nil {
		lastErr = errors.New("pencarian SoundCloud gagal")
	}
	return nil, lastErr
}

// Audio adalah berkas audio siap kirim dari sebuah track SoundCloud.
type Audio struct {
	TrackID  int64  `json:"track_id"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"-"`
}

// FetchAudio mengambil berkas audio dari sebuah track.
//
// SoundCloud tidak lagi menyediakan transcoding progresif, hanya HLS (fMP4).
// Karena itu segmen playlist diunduh lalu digabung menjadi satu berkas
// audio/mp4 yang bisa dikirim langsung sebagai audio WhatsApp.
func FetchAudio(ctx context.Context, trackID int64, maxBytes int64) (*Audio, error) {
	if trackID <= 0 {
		return nil, ErrInvalidTrack
	}
	if maxBytes <= 0 {
		maxBytes = maxAudioBytes
	}

	if data, ok := readCache(trackID, maxBytes); ok {
		return &Audio{TrackID: trackID, MimeType: "audio/mpeg", Data: data}, nil
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cid, err := getClientID(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		audio, err := fetchAudioWithClientID(ctx, cid, trackID, maxBytes)
		if err == nil {
			return audio, nil
		}
		lastErr = err
		if !errors.Is(err, errInvalidClientID) {
			return nil, err
		}
	}

	if lastErr == nil {
		lastErr = errors.New("pengambilan audio SoundCloud gagal")
	}
	return nil, lastErr
}

func fetchAudioWithClientID(ctx context.Context, cid string, trackID int64, maxBytes int64) (*Audio, error) {
	detailURL := fmt.Sprintf("%s%d?client_id=%s", trackURL, trackID, url.QueryEscape(cid))
	body, status, err := getBody(ctx, detailURL, homeURL)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errInvalidClientID
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("detail track SoundCloud membalas HTTP %d", status)
	}

	var detail apiTrackDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return nil, fmt.Errorf("gagal membaca detail track SoundCloud: %w", err)
	}

	transcoding := pickHLS(detail.Media.Transcodings)
	if transcoding == nil {
		return nil, errors.New("track ini tidak menyediakan audio HLS yang bisa diunduh")
	}

	playlistURL, err := resolveTranscoding(ctx, transcoding.URL, cid, detail.TrackAuth)
	if err != nil {
		return nil, err
	}

	data, err := downloadHLS(ctx, playlistURL, maxBytes)
	if err != nil {
		return nil, err
	}

	// MP4 fragmented hasil gabungan segmen HLS sering diputar sebagian oleh
	// pemutar WhatsApp, jadi diubah ke MP3 lebih dulu bila ffmpeg tersedia.
	data, mimeType := toPlayableAudio(ctx, data)
	if mimeType == "audio/mpeg" {
		writeCache(trackID, data)
	}
	return &Audio{TrackID: trackID, MimeType: mimeType, Data: data}, nil
}

func cacheFile(trackID int64) string {
	return filepath.Join(cacheDir, fmt.Sprintf("%d.mp3", trackID))
}

func readCache(trackID int64, maxBytes int64) ([]byte, bool) {
	path := cacheFile(trackID)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// writeCache menulis lewat berkas sementara lalu rename, supaya pembaca lain
// tidak pernah melihat berkas setengah jadi.
func writeCache(trackID int64, data []byte) {
	path := cacheFile(trackID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.tmp", trackID))
	if err != nil {
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}

func toPlayableAudio(ctx context.Context, data []byte) ([]byte, string) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return data, "audio/mp4"
	}

	cmd := exec.CommandContext(ctx, path,
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn", "-c:a", "libmp3lame", "-b:a", "128k", "-compression_level", "9",
		"-f", "mp3", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return data, "audio/mp4"
	}
	return out.Bytes(), "audio/mpeg"
}

// pickHLS memilih transcoding HLS yang tidak terenkripsi, karena segmen
// terenkripsi butuh kunci AES yang tidak disediakan API publik.
func pickHLS(items []apiTranscoding) *apiTranscoding {
	var fallback *apiTranscoding
	for i := range items {
		item := &items[i]
		protocol := strings.ToLower(item.Format.Protocol)
		if strings.Contains(protocol, "encrypted") {
			continue
		}
		if protocol != "hls" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(item.Format.MimeType), "audio/mp4") {
			return item
		}
		if fallback == nil {
			fallback = item
		}
	}
	return fallback
}

func resolveTranscoding(ctx context.Context, transcodingURL, cid, trackAuth string) (string, error) {
	target := transcodingURL
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	target += sep + "client_id=" + url.QueryEscape(cid)
	if strings.TrimSpace(trackAuth) != "" {
		target += "&track_authorization=" + url.QueryEscape(trackAuth)
	}

	body, status, err := getBody(ctx, target, homeURL)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "", errInvalidClientID
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("transcoding SoundCloud membalas HTTP %d", status)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("gagal membaca URL media SoundCloud: %w", err)
	}
	if strings.TrimSpace(payload.URL) == "" {
		return "", errors.New("URL media SoundCloud kosong")
	}
	return payload.URL, nil
}

// downloadHLS mengunduh init segment dan seluruh segmen playlist lalu
// menggabungkannya menjadi satu berkas fMP4.
func downloadHLS(ctx context.Context, playlistURL string, maxBytes int64) ([]byte, error) {
	body, status, err := getBody(ctx, playlistURL, homeURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("playlist audio SoundCloud membalas HTTP %d", status)
	}

	base, err := url.Parse(playlistURL)
	if err != nil {
		return nil, err
	}

	resolve := func(ref string) string {
		if parsed, err := base.Parse(ref); err == nil {
			return parsed.String()
		}
		return ref
	}

	initURL := ""
	var segments []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			if m := extMapPattern.FindStringSubmatch(line); m != nil {
				initURL = resolve(m[1])
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		segments = append(segments, resolve(line))
	}

	if len(segments) == 0 {
		return nil, errors.New("playlist audio SoundCloud tidak memuat segmen")
	}
	if len(segments) > maxHLSSegments {
		segments = segments[:maxHLSSegments]
	}

	// Init segment harus paling depan, segmen media tetap berurutan.
	urls := make([]string, 0, len(segments)+1)
	if initURL != "" {
		urls = append(urls, initURL)
	}
	urls = append(urls, segments...)

	parts, err := downloadParts(ctx, urls, maxBytes)
	if err != nil {
		return nil, err
	}

	total := 0
	for _, part := range parts {
		total += len(part)
	}
	buf := bytes.NewBuffer(make([]byte, 0, total))
	for _, part := range parts {
		buf.Write(part)
	}
	return buf.Bytes(), nil
}

// downloadParts mengunduh banyak segmen secara paralel tetapi tetap
// mengembalikan hasil sesuai urutan aslinya, karena segmen HLS tidak boleh
// digabung acak.
func downloadParts(ctx context.Context, urls []string, maxBytes int64) ([][]byte, error) {
	parts := make([][]byte, len(urls))
	errs := make([]error, len(urls))

	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, target := range urls {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			parts[i], errs[i] = downloadMedia(ctx, target, maxBytes)
		}(i, target)
	}
	wg.Wait()

	var total int64
	for i := range parts {
		if errs[i] != nil {
			return nil, errs[i]
		}
		total += int64(len(parts[i]))
		if total > maxBytes {
			return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
		}
	}
	return parts, nil
}

func downloadMedia(ctx context.Context, mediaURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", homeURL)

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unduhan audio SoundCloud gagal: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}
	return data, nil
}

// FormatSearch merangkum hasil pencarian menjadi teks siap kirim.
func FormatSearch(res *SearchResult) string {
	if res == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("*SOUNDCLOUD SEARCH*\n")
	sb.WriteString("🔎 " + res.Query + "\n")
	if res.Total > 0 {
		sb.WriteString("📊 " + formatCount(res.Total) + " lagu ditemukan\n")
	}

	for i, track := range res.Tracks {
		fmt.Fprintf(&sb, "\n*%d. %s*\n", i+1, track.Title)

		artist := track.Artist
		if artist == "" {
			artist = "unknown"
		}
		sb.WriteString("👤 " + artist + "\n")

		var meta []string
		if track.FormattedDuration != "" {
			meta = append(meta, "⏱ "+track.FormattedDuration)
		}
		if track.Plays > 0 {
			meta = append(meta, "▶ "+formatCount(track.Plays))
		}
		if track.Likes > 0 {
			meta = append(meta, "❤ "+formatCount(track.Likes))
		}
		if track.Genre != "" {
			meta = append(meta, "🎵 "+track.Genre)
		}
		if len(meta) > 0 {
			sb.WriteString(strings.Join(meta, " | ") + "\n")
		}

		if track.URL != "" {
			sb.WriteString("🔗 " + track.URL)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func searchWithClientID(ctx context.Context, cid, query string, limit int) (*SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("client_id", cid)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("offset", "0")
	params.Set("app_locale", "en")

	body, status, err := getBody(ctx, apiURL+"?"+params.Encode(), "https://soundcloud.com/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errInvalidClientID
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("API SoundCloud membalas HTTP %d", status)
	}

	var payload apiSearch
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("gagal membaca respons SoundCloud: %w", err)
	}

	result := &SearchResult{
		Query:  query,
		Total:  payload.TotalResults,
		Tracks: make([]Track, 0, len(payload.Collection)),
	}
	for _, item := range payload.Collection {
		track := item.toTrack()
		if track.Title == "" || track.URL == "" {
			continue
		}
		result.Tracks = append(result.Tracks, track)
	}
	// Sebagian respons membawa lebih dari limit yang diminta.
	if len(result.Tracks) > limit {
		result.Tracks = result.Tracks[:limit]
	}
	return result, nil
}

// getClientID mengembalikan client_id yang masih berlaku. refresh=true
// memaksa pengambilan ulang walaupun cache belum kedaluwarsa.
func getClientID(ctx context.Context, refresh bool) (string, error) {
	clientIDMu.Lock()
	if !refresh && clientIDVal != "" && time.Now().Before(clientIDExp) {
		cid := clientIDVal
		clientIDMu.Unlock()
		return cid, nil
	}
	clientIDMu.Unlock()

	cid, err := discoverClientID(ctx)
	if err != nil {
		return "", err
	}

	clientIDMu.Lock()
	clientIDVal = cid
	clientIDExp = time.Now().Add(clientIDTTL)
	clientIDMu.Unlock()

	return cid, nil
}

// discoverClientID mencari client_id di HTML halaman depan, lalu pada bundle
// JavaScript yang dimuat halaman tersebut.
func discoverClientID(ctx context.Context) (string, error) {
	html, status, err := getBody(ctx, homeURL, homeURL)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("halaman SoundCloud membalas HTTP %d", status)
	}

	if m := clientIDPattern.FindSubmatch(html); m != nil {
		return string(m[1]), nil
	}

	assets := dedupe(assetPattern.FindAllString(string(html), -1))
	if len(assets) == 0 {
		return "", errors.New("bundle JavaScript SoundCloud tidak ditemukan")
	}

	// Beberapa bundle diunduh paralel dan yang pertama memuat client_id
	// langsung dipakai, karena hanya satu dari banyak bundle yang menyimpannya.
	findCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, 4)
	found := make(chan string, len(assets))

	var wg sync.WaitGroup
	for _, asset := range assets {
		wg.Add(1)
		go func(asset string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-findCtx.Done():
				return
			}
			defer func() { <-sem }()

			body, status, err := getBody(findCtx, asset, homeURL)
			if err != nil || status != http.StatusOK {
				return
			}
			if m := clientIDPattern.FindSubmatch(body); m != nil {
				select {
				case found <- string(m[1]):
				case <-findCtx.Done():
				}
			}
		}(asset)
	}

	go func() {
		wg.Wait()
		close(found)
	}()

	cid, ok := <-found
	if !ok {
		return "", errors.New("client_id SoundCloud tidak ditemukan pada bundle JavaScript")
	}
	return cid, nil
}

func getBody(ctx context.Context, endpoint, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func dedupe(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func upgradeArtwork(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Replace(raw, "-large", "-t500x500", 1)
}

type apiSearch struct {
	Collection   []apiTrack `json:"collection"`
	TotalResults int64      `json:"total_results"`
}

type apiTranscoding struct {
	URL    string `json:"url"`
	Preset string `json:"preset"`
	Format struct {
		Protocol string `json:"protocol"`
		MimeType string `json:"mime_type"`
	} `json:"format"`
}

type apiTrackDetail struct {
	TrackAuth string `json:"track_authorization"`
	Media     struct {
		Transcodings []apiTranscoding `json:"transcodings"`
	} `json:"media"`
}

type apiTrack struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	PermalinkURL  string `json:"permalink_url"`
	ArtworkURL    string `json:"artwork_url"`
	Genre         string `json:"genre"`
	Duration      int    `json:"duration"` // milidetik
	PlaybackCount int64  `json:"playback_count"`
	LikesCount    int64  `json:"likes_count"`
	CreatedAt     string `json:"created_at"`
	User          struct {
		Username     string `json:"username"`
		PermalinkURL string `json:"permalink_url"`
		AvatarURL    string `json:"avatar_url"`
	} `json:"user"`
}

func (t apiTrack) toTrack() Track {
	seconds := t.Duration / 1000
	artwork := t.ArtworkURL
	if strings.TrimSpace(artwork) == "" {
		artwork = t.User.AvatarURL
	}

	return Track{
		ID:                t.ID,
		Title:             strings.TrimSpace(t.Title),
		Artist:            strings.TrimSpace(t.User.Username),
		ArtistURL:         strings.TrimSpace(t.User.PermalinkURL),
		URL:               strings.TrimSpace(t.PermalinkURL),
		Artwork:           upgradeArtwork(artwork),
		Genre:             strings.TrimSpace(t.Genre),
		Duration:          seconds,
		FormattedDuration: formatDuration(seconds),
		Plays:             t.PlaybackCount,
		Likes:             t.LikesCount,
		CreatedAt:         t.CreatedAt,
	}
}
