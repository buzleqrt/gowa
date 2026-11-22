package soundcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
