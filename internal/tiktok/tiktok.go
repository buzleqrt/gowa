package tiktok

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	mobileUA  = "com.ss.android.ugc.trill/2613 (Linux; U; Android 10; en_US; Pixel 4; Build/QQ3A.200805.001; Cronet/58.0.2991.0)"

	maxResponseBytes = 32 << 20
)

var (
	// ErrInvalidURL dikembalikan bila ID post tidak bisa diambil dari input.
	ErrInvalidURL = errors.New("link TikTok tidak valid")

	// ErrPostUnavailable dikembalikan bila TikTok memastikan post tidak ada,
	// sehingga tidak perlu mencoba layanan cadangan.
	ErrPostUnavailable = errors.New("post TikTok tidak tersedia, bersifat privat, atau sudah dihapus")

	idPattern            = regexp.MustCompile(`(?i)/(?:video|v|photo)/(\d{15,22})`)
	bareIDPattern        = regexp.MustCompile(`^\d{15,22}$`)
	invalidFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	frontityPattern      = regexp.MustCompile(`(?s)<script id="__FRONTITY_CONNECT_STATE__" type="application/json">(.*?)</script>`)
	mobileEndpointHosts  = []string{"api16-normal-c-alisg.tiktokv.com", "api16-normal-c-useast1a.tiktokv.com"}
	tikwmEndpoint        = "https://tikwm.com/api/"
	httpClient           = &http.Client{Timeout: 30 * time.Second}
	downloadClient       = &http.Client{Timeout: 5 * time.Minute}
)

// Music berisi informasi trek suara yang dipakai pada sebuah post.
type Music struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	URL    string `json:"url"`
}

// Metadata adalah hasil scraping sebuah post TikTok (video, foto, atau audio).
type Metadata struct {
	ID                string   `json:"id"`
	Author            string   `json:"author"`
	Nickname          string   `json:"nickname"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Duration          int      `json:"duration"`
	FormattedDuration string   `json:"formatted_duration"`
	Resolution        string   `json:"resolution"`
	Width             int      `json:"width"`
	Height            int      `json:"height"`
	Thumbnail         string   `json:"thumbnail"`
	UploadDate        string   `json:"upload_date"`
	Views             int64    `json:"views"`
	Likes             int64    `json:"likes"`
	Comments          int64    `json:"comments"`
	Shares            int64    `json:"shares"`
	VideoURL          string   `json:"video_url"`
	AudioURL          string   `json:"audio_url"`
	Music             *Music   `json:"music"`
	Photos            []string `json:"photos"`
}

// DownloadOptions mengatur jenis media yang diunduh.
type DownloadOptions struct {
	Audio bool
}

// ExtractID mengambil ID numerik post dari sebuah URL atau ID mentah.
func ExtractID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := idPattern.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	if bareIDPattern.MatchString(raw) {
		return raw
	}
	return ""
}

// FormatDuration mengubah durasi dalam detik menjadi MM:SS atau HH:MM:SS.
func FormatDuration(seconds int) string {
	if seconds <= 0 {
		return "00:00"
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// Fetch mengambil metadata sebuah post TikTok.
// Sumber utama adalah halaman embed resmi TikTok; bila halaman itu tidak
// memuat data, fungsi ini jatuh ke layanan tikwm.com sebagai cadangan.
func Fetch(ctx context.Context, rawURL string) (*Metadata, error) {
	id, pageURL, err := resolve(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	// Cadangan dijalankan paralel: layanan tikwm lambat (~10 detik), jadi bot
	// tidak perlu menunggu percobaan API mobile selesai lebih dulu.
	fallbackCtx, cancelFallback := context.WithCancel(ctx)
	defer cancelFallback()

	type fallbackResult struct {
		md  *Metadata
		err error
	}
	fallback := make(chan fallbackResult, 1)
	go func() {
		md, err := fetchTikWM(fallbackCtx, pageURL)
		fallback <- fallbackResult{md: md, err: err}
	}()

	// URL tanpa watermark hanya tersedia dari API mobile, jadi permintaannya
	// dijalankan bersamaan dengan metadata supaya tidak menumpuk waktunya.
	cleanURL := make(chan string, 1)
	go func() {
		cleanURL <- fetchMobilePlayURL(fallbackCtx, id)
	}()

	md, embedErr := fetchEmbed(ctx, id)
	if embedErr == nil {
		// Gambar post foto dan trek audio tidak pernah berwatermark; hanya
		// stream video dari halaman embed yang membawa watermark TikTok.
		if md.VideoURL == "" {
			cancelFallback()
			return md, nil
		}
		if clean := <-cleanURL; clean != "" {
			md.VideoURL = clean
			cancelFallback()
			return md, nil
		}
		result := <-fallback
		if result.err == nil && result.md.VideoURL != "" {
			md.VideoURL = result.md.VideoURL
		}
		cancelFallback()
		return md, nil
	}
	if errors.Is(embedErr, ErrPostUnavailable) {
		cancelFallback()
		return nil, embedErr
	}

	result := <-fallback
	if result.err == nil {
		return result.md, nil
	}
	return nil, fmt.Errorf("%w (embed: %v)", result.err, embedErr)
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
	req.Header.Set("Referer", "https://www.tiktok.com/")

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

// DownloadTo mengunduh media ke path tujuan.
func DownloadTo(ctx context.Context, mediaURL, dest string) error {
	data, err := DownloadBytes(ctx, mediaURL, 0)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(dest, data, 0o644)
}

// DownloadMedia menyimpan video, seluruh foto, atau audio dari sebuah post.
func DownloadMedia(ctx context.Context, md *Metadata, dir string, opts DownloadOptions) ([]string, error) {
	if md == nil {
		return nil, errors.New("metadata kosong")
	}
	if dir == "" {
		dir = "./downloads"
	}

	author := sanitizeFilename(md.Author)
	if author == "" {
		author = "user"
	}
	id := sanitizeFilename(md.ID)
	if id == "" {
		id = "media"
	}

	if opts.Audio {
		if md.AudioURL == "" {
			return nil, errors.New("audio publik tidak tersedia untuk post ini")
		}
		target := filepath.Join(dir, fmt.Sprintf("%s_%s.mp3", author, id))
		if err := DownloadTo(ctx, md.AudioURL, target); err != nil {
			return nil, err
		}
		return []string{target}, nil
	}

	if md.VideoURL != "" {
		target := filepath.Join(dir, fmt.Sprintf("%s_%s.mp4", author, id))
		if err := DownloadTo(ctx, md.VideoURL, target); err != nil {
			return nil, err
		}
		return []string{target}, nil
	}

	if len(md.Photos) > 0 {
		saved := make([]string, 0, len(md.Photos))
		for i, photo := range md.Photos {
			name := fmt.Sprintf("%s_%s.jpg", author, id)
			if len(md.Photos) > 1 {
				name = fmt.Sprintf("%s_%s_%d.jpg", author, id, i+1)
			}
			target := filepath.Join(dir, name)
			if err := DownloadTo(ctx, photo, target); err != nil {
				return saved, err
			}
			saved = append(saved, target)
		}
		return saved, nil
	}

	return nil, errors.New("tidak ada media publik yang bisa diunduh")
}

func resolve(ctx context.Context, rawURL string) (id string, pageURL string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", ErrInvalidURL
	}

	if found := ExtractID(rawURL); found != "" {
		return found, rawURL, nil
	}

	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if reqErr != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidURL, reqErr)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", "https://www.tiktok.com/")

	resp, doErr := httpClient.Do(req)
	if doErr != nil {
		return "", "", fmt.Errorf("gagal membuka link singkat: %w", doErr)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	pageURL = rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		pageURL = resp.Request.URL.String()
	}

	if found := ExtractID(pageURL); found != "" {
		return found, pageURL, nil
	}
	return "", "", ErrInvalidURL
}
