// Package pinterest mengambil data pin dan hasil pencarian Pinterest lewat
// endpoint resource yang dipakai situs web Pinterest sendiri.
package pinterest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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

func getResource(ctx context.Context, endpoint, sourceURL, handler string, options map[string]any) ([]byte, error) {
	payload := map[string]any{
		"options": options,
		"context": map[string]any{},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("source_url", sourceURL)
	params.Set("data", string(raw))
	target := resourceURL + "/" + endpoint + "/?" + params.Encode()
	referer := baseURL + sourceURL

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		sess, err := getSession(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		body, status, err := doResourceRequest(ctx, target, referer, handler, sess)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return body, nil
		}
		if status == http.StatusNotFound {
			return nil, ErrPinNotFound
		}

		lastErr = fmt.Errorf("Pinterest membalas HTTP %d", status)
		// Sesi lama bisa ditolak (403/401), jadi ambil ulang sekali.
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			break
		}
	}

	if lastErr == nil {
		lastErr = errors.New("permintaan ke Pinterest gagal")
	}
	return nil, lastErr
}

func doResourceRequest(ctx context.Context, target, referer, handler string, sess *session) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json, text/javascript, */*, q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Pinterest-AppState", "active")
	req.Header.Set("X-Pinterest-PWS-Handler", handler)
	req.Header.Set("Referer", referer)
	req.Header.Set("Cookie", sess.cookies)
	if sess.csrf != "" {
		req.Header.Set("X-CSRFToken", sess.csrf)
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

func getSession(ctx context.Context, refresh bool) (*session, error) {
	sessionMu.Lock()
	if !refresh && sessionVal != nil && time.Now().Before(sessionVal.expires) {
		sess := sessionVal
		sessionMu.Unlock()
		return sess, nil
	}
	sessionMu.Unlock()

	sess, err := newSession(ctx)
	if err != nil {
		return nil, err
	}

	sessionMu.Lock()
	sessionVal = sess
	sessionMu.Unlock()
	return sess, nil
}

func newSession(ctx context.Context) (*session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("halaman Pinterest membalas HTTP %d", resp.StatusCode)
	}

	csrf := ""
	parts := make([]string, 0, len(resp.Cookies()))
	for _, cookie := range resp.Cookies() {
		parts = append(parts, cookie.Name+"="+cookie.Value)
		if cookie.Name == "csrftoken" {
			csrf = cookie.Value
		}
	}

	return &session{
		cookies: strings.Join(parts, "; "),
		csrf:    csrf,
		expires: time.Now().Add(sessionTTL),
	}, nil
}

func followRedirect(ctx context.Context, raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal membuka link: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String(), nil
	}
	return raw, nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}

// flexText menerima teks Pinterest yang kadang berupa string biasa dan
// kadang berupa objek {"text": ...} (misalnya judul hasil pencarian).
type flexText string

func (t *flexText) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*t = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = flexText(s)
		return nil
	}

	var obj struct {
		Text   string `json:"text"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*t = flexText(firstNonEmpty(obj.Text, obj.Format))
	return nil
}

func (t flexText) String() string {
	return string(t)
}

type frameImage struct {
	URL string `json:"url"`
}

type frameImages struct {
	Orig  frameImage `json:"orig"`
	Large frameImage `json:"736x"`
	Mid   frameImage `json:"564x"`
	Small frameImage `json:"236x"`
}

func (fi frameImages) best() string {
	switch {
	case fi.Orig.URL != "":
		return fi.Orig.URL
	case fi.Large.URL != "":
		return fi.Large.URL
	case fi.Mid.URL != "":
		return fi.Mid.URL
	}
	return fi.Small.URL
}

type videoVariant struct {
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int64  `json:"duration"`
}

type videosData struct {
	VideoList map[string]videoVariant `json:"video_list"`
	Duration  int64                   `json:"duration"`
}

type carouselSlot struct {
	Title   flexText    `json:"title"`
	Details flexText    `json:"details"`
	Images  frameImages `json:"images"`
}

type carouselData struct {
	Slots []carouselSlot `json:"carousel_slots"`
}

type storyBlock struct {
	Image *struct {
		Images struct {
			Originals frameImage `json:"originals"`
			Large     frameImage `json:"736x"`
		} `json:"images"`
	} `json:"image"`
}

type storyPage struct {
	Title  flexText     `json:"title"`
	Blocks []storyBlock `json:"blocks"`
}

type storyData struct {
	Pages []storyPage `json:"pages"`
}

type framePin struct {
	ID            string        `json:"id"`
	Title         flexText      `json:"title"`
	GridTitle     flexText      `json:"grid_title"`
	Description   flexText      `json:"description"`
	IsVideo       bool          `json:"is_video"`
	Images        frameImages   `json:"images"`
	Videos        *videosData   `json:"videos"`
	CarouselData  *carouselData `json:"carousel_data"`
	StoryPinData  *storyData    `json:"story_pin_data"`
	DominantColor string        `json:"dominant_color"`
	CreatedAt     string        `json:"created_at"`
	Pinner        *struct {
		Username       string   `json:"username"`
		FullName       flexText `json:"full_name"`
		ImageMediumURL string   `json:"image_medium_url"`
	} `json:"pinner"`
	Board *struct {
		ID   string   `json:"id"`
		Name flexText `json:"name"`
		URL  string   `json:"url"`
	} `json:"board"`
}

type searchResponse struct {
	ResourceResponse struct {
		Data struct {
			Results []framePin `json:"results"`
		} `json:"data"`
	} `json:"resource_response"`
}

func (p framePin) toPin() *Pin {
	image := p.Images.best()
	isGif := strings.HasSuffix(strings.ToLower(image), ".gif")

	result := &Pin{
		ID:            p.ID,
		Title:         firstNonEmpty(p.Title.String(), p.GridTitle.String()),
		Description:   p.Description.String(),
		IsVideo:       p.IsVideo || p.Videos != nil,
		IsGif:         isGif,
		Image:         image,
		DominantColor: p.DominantColor,
		CreatedAt:     p.CreatedAt,
	}
	if p.ID != "" {
		result.URL = baseURL + "/pin/" + p.ID + "/"
	}

	if p.Pinner != nil {
		result.Pinner = &Pinner{
			Username: p.Pinner.Username,
			FullName: p.Pinner.FullName.String(),
			Avatar:   p.Pinner.ImageMediumURL,
		}
	}
	if p.Board != nil {
		boardURL := p.Board.URL
		if boardURL != "" && !strings.HasPrefix(boardURL, "http") {
			boardURL = baseURL + boardURL
		}
		result.Board = &Board{ID: p.Board.ID, Name: p.Board.Name.String(), URL: boardURL}
	}

	switch {
	case p.CarouselData != nil && len(p.CarouselData.Slots) > 0:
		appendCarousel(result, p.CarouselData.Slots)
	case p.StoryPinData != nil && len(p.StoryPinData.Pages) > 0:
		appendStory(result, p.StoryPinData.Pages)
	}
	// Slot carousel tanpa URL gambar tidak dihitung sebagai carousel.
	result.IsCarousel = len(result.Carousel) > 0

	switch {
	case result.IsCarousel:
		result.Type = "carousel"
	case result.IsVideo:
		result.Type = "video"
	case isGif:
		result.Type = "gif"
	default:
		result.Type = "image"
	}

	return result
}

func appendCarousel(result *Pin, slots []carouselSlot) {
	for i, slot := range slots {
		image := slot.Images.best()
		if image == "" {
			continue
		}
		result.Carousel = append(result.Carousel, Item{
			Index: i,
			Title: firstNonEmpty(slot.Title.String(), slot.Details.String()),
			URL:   image,
		})
	}
}

func appendStory(result *Pin, pages []storyPage) {
	for i, page := range pages {
		for _, block := range page.Blocks {
			if block.Image == nil {
				continue
			}
			image := firstNonEmpty(block.Image.Images.Originals.URL, block.Image.Images.Large.URL)
			if image == "" {
				continue
			}
			result.Carousel = append(result.Carousel, Item{
				Index: i,
				Title: page.Title.String(),
				URL:   image,
			})
		}
	}
}

func fetchPinGraphQL(ctx context.Context, pinID string) (map[string]json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			sleepWithContext(ctx, time.Duration(attempt)*700*time.Millisecond)
		}
		fields, err := graphqlQuery(ctx, pinQueryHash, map[string]any{
			"pinId":                          pinID,
			"isAuth":                         false,
			"isDesktop":                      true,
			"shouldPrefetchStoryPinFragment": false,
			"isUnauth":                       true,
		})
		if err == nil && len(fields) > 0 {
			return fields, nil
		}
		if err != nil {
			lastErr = err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	fields, err := graphqlQuery(ctx, pinQueryV2Hash, map[string]any{
		"pinId":                          pinID,
		"isAuth":                         false,
		"isAuthDesktop":                  false,
		"isDesktop":                      true,
		"isUnauth":                       true,
		"shouldPrefetchStoryPinFragment": false,
	})
	if err == nil && len(fields) > 0 {
		return fields, nil
	}
	if err != nil {
		lastErr = err
	}
	if lastErr == nil {
		lastErr = ErrPinNotFound
	}
	return nil, lastErr
}

func graphqlQuery(ctx context.Context, queryHash string, variables map[string]any) (map[string]json.RawMessage, error) {
	sess, err := getSession(ctx, false)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"queryHash": queryHash,
		"variables": variables,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", "csrftoken="+sess.csrf)
	req.Header.Set("X-CSRFToken", sess.csrf)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Origin", baseURL)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Pinterest GraphQL membalas HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data map[string]struct {
			Data  json.RawMessage `json:"data"`
			IsErr string          `json:"__isError"`
			Type  string          `json:"__typename"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("gagal membaca respons Pinterest: %w", err)
	}

	for _, entry := range envelope.Data {
		if len(entry.Data) == 0 || string(entry.Data) == "null" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(entry.Data, &fields); err != nil || len(fields) == 0 {
			continue
		}
		return fields, nil
	}

	if len(envelope.Errors) > 0 {
		return nil, fmt.Errorf("Pinterest GraphQL: %s", envelope.Errors[0].Message)
	}
	return nil, fmt.Errorf("Pinterest GraphQL tidak mengembalikan data pin")
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func fetchPinRelay(ctx context.Context, pinID string) (map[string]json.RawMessage, error) {
	target := baseURL + "/pin/" + pinID + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("halaman pin Pinterest membalas HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	html := string(body)

	offset := 0
	for {
		idx := strings.Index(html[offset:], relayMarker)
		if idx < 0 {
			break
		}
		start := offset + idx + len(relayMarker)
		rest := html[start:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			break
		}

		payload := rest[:end]
		candidates := []string{payload}
		if decoded, err := url.QueryUnescape(payload); err == nil && decoded != payload {
			candidates = append(candidates, decoded)
		}
		for _, candidate := range candidates {
			if fields := relayPinData(candidate); fields != nil {
				return fields, nil
			}
		}

		offset = start + end + 1
	}

	return nil, ErrPinNotFound
}

func relayPinData(candidate string) map[string]json.RawMessage {
	var envelope struct {
		Data struct {
			V1 struct {
				Data json.RawMessage `json:"data"`
			} `json:"v3GetPinQuery"`
			V2 struct {
				Data json.RawMessage `json:"data"`
			} `json:"v3GetPinQueryv2"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(candidate), &envelope); err == nil {
		for _, raw := range []json.RawMessage{envelope.Data.V1.Data, envelope.Data.V2.Data} {
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil && len(fields) > 0 {
				return fields
			}
		}
	}

	// Sebagian payload menyimpan objek pin langsung di level atas.
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(candidate), &fields) == nil {
		if _, ok := fields["entityId"]; ok {
			return fields
		}
	}
	return nil
}

// directVideoURL menelusuri playlist HLS untuk menemukan berkas video
// langsung, karena WhatsApp tidak bisa memutar URL .m3u8.
func directVideoURL(ctx context.Context, raw string) string {
	if !strings.HasSuffix(raw, ".m3u8") {
		return raw
	}

	body, ok := fetchPlaylist(ctx, raw)
	if !ok {
		return raw
	}

	subPlaylists := playlistPattern.FindAllString(string(body), -1)
	if len(subPlaylists) == 0 {
		return raw
	}

	base := raw[:strings.LastIndex(raw, "/")+1]
	subURL := base + subPlaylists[len(subPlaylists)-1]

	subBody, ok := fetchPlaylist(ctx, subURL)
	if !ok {
		return raw
	}

	if m := cmfvPattern.FindStringSubmatch(string(subBody)); m != nil {
		return base + m[1]
	}
	return raw
}

func fetchPlaylist(ctx context.Context, target string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", baseURL)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}

type detailImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type detailUser struct {
	Username       string   `json:"username"`
	FullName       flexText `json:"fullName"`
	ImageLargeURL  string   `json:"imageLargeUrl"`
	ImageMediumURL string   `json:"imageMediumUrl"`
}

type detailBoard struct {
	EntityID string   `json:"entityId"`
	Name     flexText `json:"name"`
	URL      string   `json:"url"`
}

type detailVideos struct {
	VideoList map[string]videoVariant `json:"videoList"`
	VideoURLs []string                `json:"videoUrls"`
}

type detailStoryBlock struct {
	Image *struct {
		Images struct {
			Originals frameImage `json:"originals"`
			Large     frameImage `json:"736x"`
			Medium    frameImage `json:"564x"`
			Small     frameImage `json:"236x"`
		} `json:"images"`
	} `json:"image"`
	VideoDataV2 map[string]map[string]videoVariant `json:"videoDataV2"`
}

type detailStoryPage struct {
	Title  flexText           `json:"title"`
	Blocks []detailStoryBlock `json:"blocks"`
}

type detailStory struct {
	Pages []detailStoryPage `json:"pages"`
}

type detailCarousel struct {
	Slots      []carouselSlot `json:"slots"`
	SnakeSlots []carouselSlot `json:"carousel_slots"`
}

type detailPin struct {
	EntityID        string          `json:"entityId"`
	Title           flexText        `json:"title"`
	GridTitle       flexText        `json:"gridTitle"`
	GridTitleSnake  flexText        `json:"grid_title"`
	UnauthTitle     flexText        `json:"unauthOnPageTitle"`
	Description     flexText        `json:"description"`
	CloseupDesc     flexText        `json:"closeupUnifiedDescription"`
	UnauthDesc      flexText        `json:"unauthOnPageDescription"`
	GridDescription flexText        `json:"gridDescription"`
	CreatedAt       string          `json:"createdAt"`
	DominantColor   string          `json:"dominantColor"`
	ImagesOrig      detailImage     `json:"images_orig"`
	Images736       detailImage     `json:"images_736x"`
	Images564       detailImage     `json:"images_564x"`
	Images474       detailImage     `json:"images_474x"`
	Images236       detailImage     `json:"images_236x"`
	Images170       detailImage     `json:"images_170x"`
	Videos          *detailVideos   `json:"videos"`
	IsVideo         bool            `json:"isVideo"`
	StoryPinData    *detailStory    `json:"storyPinData"`
	CarouselData    *detailCarousel `json:"carouselData"`
	Pinner          *detailUser     `json:"pinner"`
	OriginPinner    *detailUser     `json:"originPinner"`
	Board           *detailBoard    `json:"board"`
}

func mapDetailPin(ctx context.Context, fields map[string]json.RawMessage, pinID string) *Pin {
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	var d detailPin
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil
	}

	id := firstNonEmpty(d.EntityID, pinID)
	result := &Pin{
		ID:            id,
		URL:           baseURL + "/pin/" + id + "/",
		Title:         firstNonEmpty(d.UnauthTitle.String(), d.GridTitle.String(), d.GridTitleSnake.String(), d.Title.String()),
		Description:   firstNonEmpty(d.Description.String(), d.CloseupDesc.String(), d.UnauthDesc.String(), d.GridDescription.String()),
		DominantColor: d.DominantColor,
		CreatedAt:     d.CreatedAt,
	}

	for _, image := range []detailImage{d.ImagesOrig, d.Images736, d.Images564, d.Images474, d.Images236, d.Images170} {
		if strings.TrimSpace(image.URL) != "" {
			result.Image = image.URL
			break
		}
	}
	result.IsGif = strings.HasSuffix(strings.ToLower(result.Image), ".gif")

	pinner := d.OriginPinner
	if pinner == nil {
		pinner = d.Pinner
	}
	if pinner != nil {
		result.Pinner = &Pinner{
			Username: pinner.Username,
			FullName: pinner.FullName.String(),
			Avatar:   firstNonEmpty(pinner.ImageLargeURL, pinner.ImageMediumURL),
		}
	}

	if d.Board != nil {
		boardURL := d.Board.URL
		if boardURL != "" && !strings.HasPrefix(boardURL, "http") {
			boardURL = baseURL + boardURL
		}
		result.Board = &Board{ID: d.Board.EntityID, Name: d.Board.Name.String(), URL: boardURL}
	}

	if d.CarouselData != nil {
		slots := d.CarouselData.Slots
		if len(slots) == 0 {
			slots = d.CarouselData.SnakeSlots
		}
		appendCarousel(result, slots)
	}
	if d.StoryPinData != nil {
		appendDetailStory(result, d.StoryPinData)
	}

	if best := buildDetailVideo(ctx, collectDetailVideos(d)); best != nil {
		result.Video = best
	}
	result.IsCarousel = len(result.Carousel) > 0
	result.IsVideo = result.Video != nil || d.IsVideo

	switch {
	case result.IsCarousel:
		result.Type = "carousel"
	case result.IsVideo:
		result.Type = "video"
	case result.IsGif:
		result.Type = "gif"
	default:
		result.Type = "image"
	}
	return result
}

type detailVideoItem struct {
	URL      string
	Width    int
	Height   int
	Duration int64
}

func collectDetailVideos(d detailPin) []detailVideoItem {
	seen := make(map[string]struct{})
	items := make([]detailVideoItem, 0, 4)
	add := func(v videoVariant) {
		url := strings.TrimSpace(v.URL)
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		items = append(items, detailVideoItem{URL: url, Width: v.Width, Height: v.Height, Duration: v.Duration})
	}

	if d.Videos != nil {
		for _, variant := range d.Videos.VideoList {
			add(variant)
		}
		for _, url := range d.Videos.VideoURLs {
			add(videoVariant{URL: url})
		}
	}
	if d.StoryPinData != nil {
		for _, page := range d.StoryPinData.Pages {
			for _, block := range page.Blocks {
				for _, list := range block.VideoDataV2 {
					for _, variant := range list {
						add(variant)
					}
				}
			}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		iMP4 := !isHLS(items[i].URL)
		jMP4 := !isHLS(items[j].URL)
		if iMP4 != jMP4 {
			return iMP4
		}
		if items[i].Height != items[j].Height {
			return items[i].Height > items[j].Height
		}
		return items[i].URL < items[j].URL
	})
	return items
}

func isHLS(raw string) bool {
	path := raw
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	return strings.HasSuffix(strings.ToLower(path), ".m3u8")
}

func buildDetailVideo(ctx context.Context, items []detailVideoItem) *Video {
	if len(items) == 0 {
		return nil
	}

	best := items[0]
	hls := ""
	for _, item := range items {
		if isHLS(item.URL) {
			hls = item.URL
			break
		}
	}

	download := best.URL
	if isHLS(download) {
		download = directVideoURL(ctx, download)
	}
	return &Video{
		HLSURL:      firstNonEmpty(hls, best.URL),
		DownloadURL: download,
		Width:       best.Width,
		Height:      best.Height,
		Duration:    best.Duration,
	}
}

func appendDetailStory(result *Pin, story *detailStory) {
	for i, page := range story.Pages {
		for _, block := range page.Blocks {
			if block.Image == nil {
				continue
			}
			image := firstNonEmpty(
				block.Image.Images.Originals.URL,
				block.Image.Images.Large.URL,
				block.Image.Images.Medium.URL,
				block.Image.Images.Small.URL,
			)
			if image == "" {
				continue
			}
			result.Carousel = append(result.Carousel, Item{
				Index: i,
				Title: page.Title.String(),
				URL:   image,
			})
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
