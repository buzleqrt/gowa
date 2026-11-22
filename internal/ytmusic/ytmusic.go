package ytmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	musicBase = "https://music.youtube.com"
	innertube = musicBase + "/youtubei/v1/"

	apiKey     = "AIzaSyC9XL3ZjWddXya6X74dJoCTL-WEYFDNX30"
	webVersion = "1.20261115.14.00"

	browserUA  = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
	visionosUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15"

	maxResponseBytes  = 16 << 20
	visitorDataTTL    = time.Hour
	defaultSearchSize = 5
)

var (
	// ErrEmptyQuery dikembalikan bila kata kunci pencarian kosong.
	ErrEmptyQuery = errors.New("judul lagu tidak boleh kosong")

	// ErrNotFound dikembalikan bila YouTube Music tidak memberi hasil.
	ErrNotFound = errors.New("lagu tidak ditemukan di YouTube Music")

	videoIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	visitorPattern  = regexp.MustCompile(`"visitorData":"([^"]+)"`)
	durationPattern = regexp.MustCompile(`^\d+:\d{2}(?::\d{2})?$`)

	httpClient     = &http.Client{Timeout: 30 * time.Second}
	downloadClient = &http.Client{Timeout: 5 * time.Minute}

	visitorMu  sync.Mutex
	visitorVal string
	visitorExp time.Time
)

// Track adalah satu hasil pencarian lagu YouTube Music.
type Track struct {
	VideoID           string `json:"video_id"`
	Title             string `json:"title"`
	Artist            string `json:"artist"`
	Duration          int    `json:"duration"`
	FormattedDuration string `json:"formatted_duration"`
	Plays             string `json:"plays"`
	Thumbnail         string `json:"thumbnail"`
	URL               string `json:"url"`
}

// Audio adalah berkas audio siap kirim untuk satu video YouTube Music.
type Audio struct {
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"-"`
}

// Search mencari lagu berdasarkan judul memakai InnerTube WEB_REMIX.
func Search(ctx context.Context, query string, limit int) ([]Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = defaultSearchSize
	}

	resp, err := callInnerTube(ctx, "search", map[string]any{"query": query}, map[string]any{
		"clientName":    "WEB_REMIX",
		"clientVersion": webVersion,
		"hl":            "en",
		"gl":            "US",
	})
	if err != nil {
		return nil, err
	}

	var tree any
	if err := json.Unmarshal(resp, &tree); err != nil {
		return nil, fmt.Errorf("gagal membaca hasil pencarian: %w", err)
	}

	var tracks []Track
	seen := make(map[string]struct{})
	for _, renderer := range collect(tree, "musicResponsiveListItemRenderer") {
		track := parseTrack(renderer)
		if track == nil {
			continue
		}
		if _, ok := seen[track.VideoID]; ok {
			continue
		}
		seen[track.VideoID] = struct{}{}
		tracks = append(tracks, *track)
		if len(tracks) >= limit {
			break
		}
	}

	if len(tracks) == 0 {
		return nil, ErrNotFound
	}
	return tracks, nil
}

// FetchAudio mengambil URL audio langsung lewat client VISIONOS lalu
// mengunduhnya ke memori. maxBytes <= 0 berarti tanpa batas ukuran.
func FetchAudio(ctx context.Context, videoID string, maxBytes int64) (*Audio, error) {
	videoID = strings.TrimSpace(videoID)
	if !videoIDPattern.MatchString(videoID) {
		return nil, fmt.Errorf("videoId YouTube tidak valid: %q", videoID)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		visitor, err := getVisitorData(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		player, err := fetchPlayer(ctx, videoID, visitor)
		if err != nil {
			return nil, err
		}

		if status := player.PlayabilityStatus.Status; status != "OK" {
			reason := player.PlayabilityStatus.Reason
			if reason == "" {
				reason = status
			}
			lastErr = fmt.Errorf("YouTube Music menolak pemutaran: %s", reason)
			// visitorData basi memicu "Sign in to confirm you're not a bot";
			// ambil ulang sekali sebelum menyerah.
			if attempt == 0 && strings.Contains(strings.ToLower(reason), "sign in") {
				continue
			}
			return nil, lastErr
		}

		format, ok := pickAudioFormat(player.StreamingData.AdaptiveFormats)
		if !ok {
			return nil, errors.New("YouTube Music tidak menyediakan format audio langsung")
		}

		data, err := downloadAudio(ctx, format.URL, format.ContentLength, maxBytes)
		if err != nil {
			return nil, err
		}

		mime := baseMime(format.MimeType)
		if mime == "audio/mp4" {
			data = normalizeAudio(data)
		}

		details := player.VideoDetails
		return &Audio{
			VideoID:  videoID,
			Title:    details.Title,
			Artist:   details.Author,
			Duration: atoi(details.LengthSeconds),
			MimeType: mime,
			Data:     data,
		}, nil
	}

	if lastErr == nil {
		lastErr = errors.New("gagal mengambil audio YouTube Music")
	}
	return nil, lastErr
}

func fetchPlayer(ctx context.Context, videoID, visitor string) (*playerResponse, error) {
	client := map[string]any{
		"clientName":    "VISIONOS",
		"clientVersion": "1.02",
		"deviceMake":    "Apple",
		"deviceModel":   "RealityDevice17,1",
		"userAgent":     visionosUA,
		"osName":        "visionOS",
		"osVersion":     "26.5.23O471",
		"hl":            "en",
		"gl":            "US",
	}
	if visitor != "" {
		client["visitorData"] = visitor
	}

	payload := map[string]any{
		"videoId":        videoID,
		"contentCheckOk": true,
		"racyCheckOk":    true,
		"playbackContext": map[string]any{
			"contentPlaybackContext": map[string]any{"html5Preference": "HTML5_PREF_WANTS"},
		},
	}

	raw, err := callInnerTube(ctx, "player", payload, client)
	if err != nil {
		return nil, err
	}

	var player playerResponse
	if err := json.Unmarshal(raw, &player); err != nil {
		return nil, fmt.Errorf("gagal membaca respons player: %w", err)
	}
	return &player, nil
}

func callInnerTube(ctx context.Context, api string, payload map[string]any, client map[string]any) ([]byte, error) {
	body, err := json.Marshal(mergeClient(client, payload))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, innertube+api+"?key="+apiKey, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", musicBase)
	req.Header.Set("Referer", musicBase+"/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube Music membalas HTTP %d", resp.StatusCode)
	}
	if !json.Valid(raw) {
		return nil, errors.New("respons YouTube Music bukan JSON")
	}
	return raw, nil
}

func mergeClient(client map[string]any, payload map[string]any) map[string]any {
	merged := map[string]any{
		"context": map[string]any{"client": client},
	}
	for k, v := range payload {
		merged[k] = v
	}
	return merged
}

// parseTrack membaca satu musicResponsiveListItemRenderer. Hanya baris yang
// punya videoId (lagu/video) yang dianggap hasil yang bisa diputar.
func parseTrack(renderer map[string]any) *Track {
	videoID := findString(renderer, "watchEndpoint", "videoId")
	if videoID == "" {
		return nil
	}

	columns := flexColumns(renderer)
	var texts []string
	for _, col := range columns {
		texts = append(texts, columnText(col))
	}

	title := ""
	if len(texts) > 0 {
		title = strings.TrimSpace(texts[0])
	}

	artist := ""
	if len(texts) > 1 {
		artist = parseArtist(texts[1])
	}

	var duration string
	var plays string
	walk(renderer, func(m map[string]any) {
		if duration != "" {
			return
		}
		if text, ok := m["text"].(string); ok && durationPattern.MatchString(text) {
			duration = text
		}
	})
	for _, t := range texts {
		parts := strings.Split(t, " • ")
		for _, part := range parts {
			if strings.HasSuffix(strings.ToLower(part), "plays") {
				plays = part
			}
			if duration == "" && durationPattern.MatchString(part) {
				duration = part
			}
		}
	}

	return &Track{
		VideoID:           videoID,
		Title:             title,
		Artist:            artist,
		Duration:          parseDuration(duration),
		FormattedDuration: duration,
		Plays:             plays,
		Thumbnail:         lastThumbnail(renderer),
		URL:               musicBase + "/watch?v=" + videoID,
	}
}

func flexColumns(renderer map[string]any) []map[string]any {
	rawColumns, _ := renderer["flexColumns"].([]any)
	columns := make([]map[string]any, 0, len(rawColumns))
	for _, raw := range rawColumns {
		col, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := col["musicResponsiveListItemFlexColumnRenderer"].(map[string]any)
		if !ok {
			continue
		}
		columns = append(columns, inner)
	}
	return columns
}

func columnText(column map[string]any) string {
	text, _ := column["text"].(map[string]any)
	runs, _ := text["runs"].([]any)

	var sb strings.Builder
	for _, raw := range runs {
		run, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := run["text"].(string); ok {
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// parseArtist mengambil nama artis dari subjudul seperti
// "Song • Weird Genius & Sara Fajira" atau "Song • Artist • Album".
func parseArtist(subtitle string) string {
	parts := strings.Split(subtitle, " • ")
	if len(parts) <= 1 {
		return strings.TrimSpace(subtitle)
	}
	rest := parts[1:]
	// "Song • Artist" -> Artist; "Song • Artist • Album" -> Artist.
	return strings.TrimSpace(rest[0])
}

func lastThumbnail(value any) string {
	var url string
	walk(value, func(m map[string]any) {
		renderer, ok := m["musicThumbnailRenderer"].(map[string]any)
		if !ok {
			return
		}
		thumb, ok := renderer["thumbnail"].(map[string]any)
		if !ok {
			return
		}
		list, ok := thumb["thumbnails"].([]any)
		if !ok || len(list) == 0 {
			return
		}
		last, ok := list[len(list)-1].(map[string]any)
		if !ok {
			return
		}
		if s, ok := last["url"].(string); ok {
			url = s
		}
	})
	return url
}

func getVisitorData(ctx context.Context, refresh bool) (string, error) {
	visitorMu.Lock()
	if !refresh && visitorVal != "" && time.Now().Before(visitorExp) {
		val := visitorVal
		visitorMu.Unlock()
		return val, nil
	}
	visitorMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, musicBase+"/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("halaman YouTube Music membalas HTTP %d", resp.StatusCode)
	}

	match := visitorPattern.FindSubmatch(data)
	if match == nil {
		return "", errors.New("visitorData YouTube Music tidak ditemukan")
	}

	val := string(match[1])
	visitorMu.Lock()
	visitorVal = val
	visitorExp = time.Now().Add(visitorDataTTL)
	visitorMu.Unlock()

	return val, nil
}

func pickAudioFormat(formats []apiFormat) (apiFormat, bool) {
	best := apiFormat{}
	found := false

	// Utamakan audio/mp4 (m4a/AAC) karena paling aman diputar di WhatsApp;
	// audio/webm (opus) hanya dipakai bila m4a tidak tersedia.
	for _, format := range formats {
		if strings.TrimSpace(format.URL) == "" {
			continue
		}
		mime := baseMime(format.MimeType)
		if !strings.HasPrefix(mime, "audio/") {
			continue
		}
		if !found || formatScore(format) > formatScore(best) {
			best = format
			found = true
		}
	}
	return best, found
}

func formatScore(format apiFormat) int {
	score := format.AverageBitrate
	if score == 0 {
		score = format.Bitrate
	}
	if baseMime(format.MimeType) == "audio/mp4" {
		score += 1_000_000
	}
	// YouTube menyediakan beberapa trek audio (bahasa asli + hasil
	// auto-dubbing). Tanpa prioritas ini yang terambil bisa trek dub.
	if format.AudioTrack.AudioIsDefault {
		score += 10_000_000
	}
	return score
}

func downloadAudio(ctx context.Context, mediaURL, contentLength string, maxBytes int64) ([]byte, error) {
	size, _ := strconv.ParseInt(strings.TrimSpace(contentLength), 10, 64)
	if size > 0 && maxBytes > 0 && size > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", visionosUA)
	req.Header.Set("Referer", musicBase+"/")
	req.Header.Set("Accept", "*/*")
	// Tanpa Range, server audio YouTube berhenti di sekitar 576 KB dan
	// koneksi menggantung sampai timeout.
	if size > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", size-1))
	} else {
		req.Header.Set("Range", "bytes=0-")
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unduhan audio gagal: HTTP %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("berkas audio kosong")
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}
	return data, nil
}

func baseMime(mimeType string) string {
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = mimeType[:idx]
	}
	return strings.TrimSpace(mimeType)
}

func parseDuration(text string) int {
	if text == "" {
		return 0
	}
	parts := strings.Split(text, ":")
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

func atoi(text string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(text))
	return n
}

func walk(value any, fn func(map[string]any)) {
	switch node := value.(type) {
	case map[string]any:
		fn(node)
		for _, child := range node {
			walk(child, fn)
		}
	case []any:
		for _, child := range node {
			walk(child, fn)
		}
	}
}

func collect(value any, key string) []map[string]any {
	var out []map[string]any
	walk(value, func(m map[string]any) {
		if inner, ok := m[key].(map[string]any); ok {
			out = append(out, inner)
		}
	})
	return out
}

func findString(value any, path ...string) string {
	var found string
	walk(value, func(m map[string]any) {
		if found != "" {
			return
		}
		var node any = m
		for _, key := range path {
			asMap, ok := node.(map[string]any)
			if !ok {
				return
			}
			node = asMap[key]
		}
		if s, ok := node.(string); ok {
			found = s
		}
	})
	return found
}

type apiFormat struct {
	Itag           int        `json:"itag"`
	MimeType       string     `json:"mimeType"`
	Bitrate        int        `json:"bitrate"`
	AverageBitrate int        `json:"averageBitrate"`
	AudioQuality   string     `json:"audioQuality"`
	URL            string     `json:"url"`
	ContentLength  string     `json:"contentLength"`
	AudioTrack     audioTrack `json:"audioTrack"`
}

type audioTrack struct {
	DisplayName    string `json:"displayName"`
	ID             string `json:"id"`
	AudioIsDefault bool   `json:"audioIsDefault"`
}

type playerResponse struct {
	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	VideoDetails struct {
		Title         string `json:"title"`
		Author        string `json:"author"`
		LengthSeconds string `json:"lengthSeconds"`
	} `json:"videoDetails"`
	StreamingData struct {
		AdaptiveFormats []apiFormat `json:"adaptiveFormats"`
	} `json:"streamingData"`
}
