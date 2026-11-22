// Package pinterest mengambil data pin dan hasil pencarian Pinterest lewat
// endpoint resource yang dipakai situs web Pinterest sendiri.
package pinterest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	baseURL     = "https://www.pinterest.com"
	resourceURL = baseURL + "/resource"

	browserUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"
	maxResponseBytes = 32 << 20

	// Query hash internal situs Pinterest untuk detail pin. Hash v3GetPinQuery
	// bisa sesekali dibalas AuthGateBlocked, jadi ada retry dan dua cadangan
	// (v3GetPinQueryv2 dan payload relay di halaman pin).
	graphqlURL     = baseURL + "/_/graphql/"
	pinQueryHash   = "91dc7817f1acf1c2fb8d505d1c79dedebcb3baa1794065ba3602b843099f8ff7"
	pinQueryV2Hash = "4b1b1b5c0de8d6466166acf4e86ac8198a2f7f7729c0ec4fe52843d61e689eba"
	relayMarker    = `__PWS_RELAY_REGISTER_COMPLETED_REQUEST__("`

	defaultPageSize = 20
	maxPageSize     = 50
	sessionTTL      = 30 * time.Minute
)

var (
	// ErrInvalidInput dikembalikan bila input bukan link atau ID pin.
	ErrInvalidInput = errors.New("link atau ID pin Pinterest tidak valid")

	// ErrPinNotFound dikembalikan bila pin tidak ada atau tidak publik.
	ErrPinNotFound = errors.New("pin Pinterest tidak ditemukan atau tidak publik")

	// ErrUnsupportedPinID dikembalikan bila input memakai ID buram baru
	// Pinterest (misalnya "e1_AfqPSh...") yang hanya bisa dibuka lewat
	// browser, bukan lewat endpoint data publik.
	ErrUnsupportedPinID = errors.New("pin ini memakai ID baru Pinterest (bukan angka) sehingga tidak bisa diunduh; gunakan link pin dengan ID angka")

	pinIDPattern    = regexp.MustCompile(`(?i)/pin/([0-9]+)`)
	barePinPattern  = regexp.MustCompile(`^[0-9]{8,}$`)
	playlistPattern = regexp.MustCompile(`[A-Za-z0-9_-]+\.m3u8`)
	cmfvPattern     = regexp.MustCompile(`URI="([^"]+\.cmfv)"`)

	httpClient     = &http.Client{Timeout: 30 * time.Second}
	downloadClient = &http.Client{Timeout: 5 * time.Minute}

	sessionMu  sync.Mutex
	sessionVal *session
)

type session struct {
	cookies string
	csrf    string
	expires time.Time
}

// Pinner adalah pemilik pin.
type Pinner struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Avatar   string `json:"avatar"`
}

// Board adalah papan tempat pin disimpan.
type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Video adalah informasi video pada pin.
type Video struct {
	HLSURL      string `json:"hls_url"`
	DownloadURL string `json:"download_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Duration    int64  `json:"duration"`
}

// Item adalah satu gambar pada pin carousel.
type Item struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Pin adalah hasil scraping sebuah pin Pinterest.
type Pin struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Type          string  `json:"type"`
	IsCarousel    bool    `json:"is_carousel"`
	IsVideo       bool    `json:"is_video"`
	IsGif         bool    `json:"is_gif"`
	Image         string  `json:"image"`
	Video         *Video  `json:"video"`
	Carousel      []Item  `json:"carousel"`
	Pinner        *Pinner `json:"pinner"`
	Board         *Board  `json:"board"`
	DominantColor string  `json:"dominant_color"`
	CreatedAt     string  `json:"created_at"`
}

// SearchResult adalah hasil pencarian pin.
type SearchResult struct {
	Query string `json:"query"`
	Count int    `json:"count"`
	Pins  []Pin  `json:"pins"`
}

// FormatSearch merangkum hasil pencarian menjadi teks siap kirim.
func FormatSearch(res *SearchResult) string {
	if res == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("*PINTEREST SEARCH*\n")
	sb.WriteString("🔎 " + res.Query + "\n")

	for i, pin := range res.Pins {
		if i >= 10 {
			break
		}
		title := strings.TrimSpace(pin.Title)
		if title == "" {
			title = strings.TrimSpace(pin.Description)
		}
		if title == "" {
			title = "Pin Pinterest"
		}
		fmt.Fprintf(&sb, "\n*%d. %s*\n", i+1, truncate(title, 120))

		kind := "🖼 foto"
		switch pin.Type {
		case "video":
			kind = "🎬 video"
		case "gif":
			kind = "🎞 gif"
		case "carousel":
			kind = fmt.Sprintf("🖼 %d media", len(pin.Carousel))
		}
		sb.WriteString(kind)
		if pin.Pinner != nil && pin.Pinner.Username != "" {
			sb.WriteString(" | 👤 @" + pin.Pinner.Username)
		}
		sb.WriteString("\n🔗 " + pin.URL)
	}

	return strings.TrimRight(sb.String(), "\n")
}

// ExtractPinID mengambil ID numerik dari link Pinterest atau ID mentah.
func ExtractPinID(ctx context.Context, input string) (string, error) {
	clean := strings.TrimSpace(input)
	if clean == "" {
		return "", ErrInvalidInput
	}
	if isDigits(clean) || barePinPattern.MatchString(clean) {
		return clean, nil
	}
	if m := pinIDPattern.FindStringSubmatch(clean); m != nil {
		return m[1], nil
	}
	if strings.Contains(clean, "pin.it/") {
		target, err := followRedirect(ctx, clean)
		if err != nil {
			return "", err
		}
		if m := pinIDPattern.FindStringSubmatch(target); m != nil {
			return m[1], nil
		}
		return "", ErrUnsupportedPinID
	}
	if strings.Contains(clean, "/pin/") {
		return "", ErrUnsupportedPinID
	}
	return "", ErrInvalidInput
}

// Fetch mengambil metadata lengkap sebuah pin lewat GraphQL internal
// Pinterest, dengan fallback ke payload relay di halaman pin.
func Fetch(ctx context.Context, input string) (*Pin, error) {
	pinID, err := ExtractPinID(ctx, input)
	if err != nil {
		return nil, err
	}

	fields, err := fetchPinGraphQL(ctx, pinID)
	if err != nil {
		fields, err = fetchPinRelay(ctx, pinID)
		if err != nil {
			return nil, err
		}
	}

	pin := mapDetailPin(ctx, fields, pinID)
	if pin == nil || (pin.Image == "" && pin.Video == nil && len(pin.Carousel) == 0) {
		return nil, ErrPinNotFound
	}
	return pin, nil
}

// Search mencari pin Pinterest berdasarkan kata kunci.
func Search(ctx context.Context, query string, pageSize int) (*SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("kata kunci pencarian tidak boleh kosong")
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	options := map[string]any{
		"query":     query,
		"scope":     "pins",
		"page_size": pageSize,
		"bookmarks": []string{},
	}
	sourceURL := "/search/pins/?q=" + url.QueryEscape(query)

	body, err := getResource(ctx, "BaseSearchResource/get", sourceURL, "www/search/pins.js", options)
	if err != nil {
		return nil, err
	}

	var payload searchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("gagal membaca respons Pinterest: %w", err)
	}

	result := &SearchResult{Query: query, Pins: make([]Pin, 0, len(payload.ResourceResponse.Data.Results))}
	for _, item := range payload.ResourceResponse.Data.Results {
		pin := item.toPin()
		// Hasil pencarian juga memuat modul non-pin (misalnya "Explore
		// featured boards") yang tidak punya media.
		if pin.ID == "" || pin.URL == "" || pin.Image == "" {
			continue
		}
		// Pin video tidak membawa URL video pada hasil pencarian, jadi
		// hanya ditandai sebagai video tanpa data unduhan.
		result.Pins = append(result.Pins, *pin)
	}
	result.Count = len(result.Pins)
	return result, nil
}

// DownloadBytes mengunduh media ke memori. maxBytes <= 0 berarti tanpa batas.
func DownloadBytes(ctx context.Context, mediaURL string, maxBytes int64) ([]byte, error) {
	mediaURL = strings.TrimSpace(mediaURL)
	if mediaURL == "" {
		return nil, errors.New("URL media kosong")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", baseURL)

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unduhan media gagal: HTTP %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ukuran media melebihi batas %d MB", maxBytes/(1<<20))
	}
	return data, nil
}

// MIME menebak tipe media dari ekstensi URL.
func MIME(mediaURL string) string {
	path := mediaURL
	if parsed, err := url.Parse(mediaURL); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	}
	return "image/jpeg"
}
