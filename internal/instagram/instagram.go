package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// crawlerUA dipakai karena Instagram hanya menyajikan HTML lengkap
	// (server-side rendering) untuk perayap seperti Googlebot; UA browser
	// biasa hanya menerima shell JavaScript tanpa data media.
	crawlerUA = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

	maxResponseBytes = 32 << 20
)

var (
	// ErrInvalidURL dikembalikan bila kode post tidak bisa diambil dari input.
	ErrInvalidURL = errors.New("link Instagram tidak valid")

	// ErrPostUnavailable dikembalikan bila post tidak ada, privat, atau dihapus.
	ErrPostUnavailable = errors.New("post Instagram tidak tersedia, bersifat privat, atau sudah dihapus")

	codePattern     = regexp.MustCompile(`(?i)/(?:p|reel|reels|tv)/([A-Za-z0-9_-]{5,20})`)
	bareCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{9,20}$`)
	statePattern    = regexp.MustCompile(`(?s)<script type="application/json"[^>]*data-sjs[^>]*>(.*?)</script>`)

	httpClient     = &http.Client{Timeout: 30 * time.Second}
	downloadClient = &http.Client{Timeout: 5 * time.Minute}
)

// Item adalah satu berkas media di dalam sebuah post (foto atau video).
type Item struct {
	URL      string `json:"url"`
	AudioURL string `json:"audio_url,omitempty"`
	Video    bool   `json:"video"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// Metadata adalah hasil scraping sebuah post Instagram.
type Metadata struct {
	Code       string `json:"code"`
	Author     string `json:"author"`
	FullName   string `json:"full_name"`
	Caption    string `json:"caption"`
	Likes      int64  `json:"likes"`
	Comments   int64  `json:"comments"`
	TakenAt    int64  `json:"taken_at"`
	UploadDate string `json:"upload_date"`
	Thumbnail  string `json:"thumbnail"`
	Items      []Item `json:"items"`
}

// VideoURL mengembalikan URL video pertama pada post, bila ada.
func (m *Metadata) VideoURL() string {
	for _, item := range m.Items {
		if item.Video {
			return item.URL
		}
	}
	return ""
}

// Photos mengembalikan seluruh URL foto pada post.
func (m *Metadata) Photos() []string {
	out := make([]string, 0, len(m.Items))
	for _, item := range m.Items {
		if !item.Video {
			out = append(out, item.URL)
		}
	}
	return out
}

// ExtractCode mengambil kode post dari URL /p/<kode>/, /reel/<kode>/, /tv/<kode>
// atau dari kode mentah.
func ExtractCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := codePattern.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	if bareCodePattern.MatchString(raw) {
		return raw
	}
	return ""
}

// Fetch mengambil metadata sebuah post Instagram.
func Fetch(ctx context.Context, raw string) (*Metadata, error) {
	code, err := resolve(ctx, raw)
	if err != nil {
		return nil, err
	}

	body, status, err := getBody(ctx, "https://www.instagram.com/p/"+code+"/", crawlerUA, "https://www.instagram.com/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrPostUnavailable
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("halaman post membalas HTTP %d", status)
	}

	media := findMedia(body, code)
	if media == nil {
		return nil, ErrPostUnavailable
	}
	return mediaToMetadata(media, code), nil
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
	req.Header.Set("User-Agent", crawlerUA)
	req.Header.Set("Referer", "https://www.instagram.com/")

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

// DownloadMedia mengunduh satu item media. Bila item video memiliki URL audio
// terpisah (format DASH Instagram), audio digabungkan dengan ffmpeg agar
// hasil akhir tetap memiliki suara.
func DownloadMedia(ctx context.Context, item Item, maxBytes int64) ([]byte, error) {
	if !item.Video || strings.TrimSpace(item.AudioURL) == "" {
		return DownloadBytes(ctx, item.URL, maxBytes)
	}

	videoData, err := DownloadBytes(ctx, item.URL, maxBytes)
	if err != nil {
		return nil, err
	}
	audioData, err := DownloadBytes(ctx, item.AudioURL, maxBytes)
	if err != nil {
		// Jangan gagalkan unduhan bila audio terpisah tidak bisa diambil;
		// kembalikan video polos agar perilaku lama tetap tersedia.
		return videoData, nil
	}

	muxed, err := muxVideoAudio(ctx, videoData, audioData, maxBytes)
	if err != nil {
		return videoData, nil
	}
	return muxed, nil
}

func muxVideoAudio(ctx context.Context, videoData, audioData []byte, maxBytes int64) ([]byte, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "ig-av-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	videoPath := filepath.Join(tmpDir, "video.mp4")
	audioPath := filepath.Join(tmpDir, "audio.m4a")
	outputPath := filepath.Join(tmpDir, "output.mp4")

	if err := os.WriteFile(videoPath, videoData, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(audioPath, audioData, 0644); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, "-y", "-i", videoPath, "-i", audioPath, "-c", "copy", "-map", "0:v:0", "-map", "1:a:0", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg gagal menggabungkan audio: %w: %s", err, strings.TrimSpace(string(output)))
	}

	result, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(result)) > maxBytes {
		return nil, fmt.Errorf("ukuran media melebihi batas %d MB", maxBytes/(1<<20))
	}
	return result, nil
}

func firstAudioURL(m map[string]any) string {
	versions, ok := m["audioOnly"].([]any)
	if !ok || len(versions) == 0 {
		return ""
	}
	if entry, ok := versions[0].(map[string]any); ok {
		return stringValue(entry["url"])
	}
	return ""
}

func resolve(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidURL
	}
	if code := ExtractCode(raw); code != "" {
		return code, nil
	}

	// Link pendek seperti instagr.am atau /share/reel/... baru menyimpan kode
	// setelah dialihkan, jadi ikuti dulu pengalihannya.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	if !strings.Contains(raw, "instagram.com") && !strings.Contains(raw, "instagr.am") {
		return "", ErrInvalidURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.Header.Set("User-Agent", crawlerUA)
	req.Header.Set("Referer", "https://www.instagram.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal membuka link: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	target := raw
	if resp.Request != nil && resp.Request.URL != nil {
		target = resp.Request.URL.String()
	}
	if code := ExtractCode(target); code != "" {
		return code, nil
	}
	return "", ErrInvalidURL
}

func getBody(ctx context.Context, endpoint, userAgent, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", referer)

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

// findMedia menelusuri state JSON halaman post untuk mencari objek media
// dengan kode yang diminta.
func findMedia(body []byte, code string) map[string]any {
	var fallback map[string]any
	for _, match := range statePattern.FindAllSubmatch(body, -1) {
		raw := match[1]
		if !bytes.Contains(raw, []byte("media_type")) {
			continue
		}

		var state any
		if err := json.Unmarshal(raw, &state); err != nil {
			continue
		}

		// Objek utama post berada di dalam "if_not_gated_logged_out";
		// objek lain di halaman yang sama adalah post terkait.
		if found := walkState(state, code, true); found != nil {
			return found
		}
		if fallback == nil {
			fallback = walkState(state, code, false)
		}
	}
	return fallback
}

func walkState(node any, code string, gated bool) map[string]any {
	switch value := node.(type) {
	case map[string]any:
		if gated {
			if inner, ok := value["if_not_gated_logged_out"].(map[string]any); ok && isMedia(inner, code) {
				return inner
			}
		} else if isMedia(value, code) {
			return value
		}
		for _, child := range value {
			if found := walkState(child, code, gated); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range value {
			if found := walkState(child, code, gated); found != nil {
				return found
			}
		}
	}
	return nil
}

func isMedia(value map[string]any, code string) bool {
	if _, ok := value["media_type"]; !ok {
		return false
	}
	if code == "" {
		return true
	}
	return stringValue(value["code"]) == code
}

func mediaToMetadata(m map[string]any, code string) *Metadata {
	user, _ := m["user"].(map[string]any)
	caption, _ := m["caption"].(map[string]any)

	md := &Metadata{
		Code:      firstNonEmpty(stringValue(m["code"]), code),
		Author:    stringValue(user["username"]),
		FullName:  stringValue(user["full_name"]),
		Caption:   stringValue(caption["text"]),
		Likes:     intValue(m["like_count"]),
		Comments:  intValue(m["comment_count"]),
		TakenAt:   intValue(m["taken_at"]),
		Thumbnail: bestImage(m["image_versions2"]),
	}
	md.UploadDate = formatUploadDate(md.TakenAt)

	if children, ok := m["carousel_media"].([]any); ok && len(children) > 0 {
		for _, child := range children {
			item, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if entry, ok := buildItem(item); ok {
				md.Items = append(md.Items, entry)
			}
		}
	} else if entry, ok := buildItem(m); ok {
		md.Items = append(md.Items, entry)
	}

	return md
}

func buildItem(m map[string]any) (Item, bool) {
	if versions, ok := m["video_versions"].([]any); ok && len(versions) > 0 {
		best := bestVideo(versions)
		if best == nil {
			return Item{}, false
		}
		return Item{
			URL:      stringValue(best["url"]),
			AudioURL: firstAudioURL(m),
			Video:    true,
			Width:    int(intValue(best["width"])),
			Height:   int(intValue(best["height"])),
		}, true
	}

	if image := bestImage(m["image_versions2"]); image != "" {
		width := int(intValue(m["original_width"]))
		height := int(intValue(m["original_height"]))
		return Item{URL: image, Width: width, Height: height}, true
	}
	return Item{}, false
}

// bestVideo memilih versi video dengan resolusi terbesar.
func bestVideo(versions []any) map[string]any {
	var best map[string]any
	var bestWidth int64
	for _, v := range versions {
		entry, ok := v.(map[string]any)
		if !ok || stringValue(entry["url"]) == "" {
			continue
		}
		width := intValue(entry["width"])
		if best == nil || width > bestWidth {
			best = entry
			bestWidth = width
		}
	}
	return best
}

// bestImage memilih kandidat gambar dengan resolusi terbesar.
func bestImage(raw any) string {
	versions, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	candidates, ok := versions["candidates"].([]any)
	if !ok {
		return ""
	}

	best := ""
	var bestWidth int64
	for _, c := range candidates {
		entry, ok := c.(map[string]any)
		if !ok {
			continue
		}
		url := stringValue(entry["url"])
		if url == "" {
			continue
		}
		width := intValue(entry["width"])
		if best == "" || width > bestWidth {
			best = url
			bestWidth = width
		}
	}
	return best
}

func formatUploadDate(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	}
	return ""
}

func intValue(v any) int64 {
	switch value := v.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return n
		}
	}
	return 0
}

func stringSlice(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, item := range arr {
			if s := stringValue(item); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
