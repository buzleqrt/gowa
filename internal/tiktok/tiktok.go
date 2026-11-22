package tiktok

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
	"path/filepath"
	"regexp"
	"strconv"
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

// fetchMobilePlayURL mengambil URL stream video tanpa watermark dari API
// mobile TikTok. Endpoint ini tidak selalu tersedia (membalas HTTP 429 dari
// sebagian IP), jadi hasil kosong berarti pemanggil perlu memakai cadangan.
func fetchMobilePlayURL(ctx context.Context, id string) string {
	query := url.Values{}
	query.Set("aweme_id", id)
	query.Set("aid", "1988")
	query.Set("app_name", "musical_ly")
	query.Set("channel", "googleplay")
	query.Set("device_platform", "android")
	query.Set("device_type", "Pixel 4")
	query.Set("os_version", "10")
	query.Set("version_code", "300904")
	query.Set("iid", "7318518857994389254")
	query.Set("device_id", "7318517321748022790")

	endpoint := "/aweme/v1/feed/?" + query.Encode()
	for _, host := range mobileEndpointHosts {
		body, status, err := getBody(ctx, "https://"+host+endpoint, mobileUA, "https://www.tiktok.com/")
		if err != nil || status != http.StatusOK {
			continue
		}

		var feed mobileFeed
		if err := json.Unmarshal(body, &feed); err != nil {
			continue
		}
		for i := range feed.AwemeList {
			if feed.AwemeList[i].AwemeID != id {
				continue
			}
			if u := firstURL(feed.AwemeList[i].Video.PlayAddr.URLList); u != "" {
				return u
			}
		}
	}
	return ""
}

// fetchEmbed mengambil metadata dari halaman embed resmi TikTok. Halaman ini
// memuat state JSON lengkap dan tidak menerapkan tanda tangan X-Argus seperti
// API mobile, sehingga bisa dipakai tanpa kredensial.
func fetchEmbed(ctx context.Context, id string) (*Metadata, error) {
	body, status, err := getBody(ctx, "https://www.tiktok.com/embed/"+id, browserUA, "https://www.tiktok.com/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || status == http.StatusBadRequest {
		return nil, ErrPostUnavailable
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("halaman embed membalas HTTP %d", status)
	}

	match := frontityPattern.FindSubmatch(body)
	if match == nil {
		return nil, errors.New("halaman embed tidak memuat data post")
	}

	var state embedState
	if err := json.Unmarshal(match[1], &state); err != nil {
		return nil, fmt.Errorf("gagal membaca state halaman embed: %w", err)
	}

	for key, raw := range state.Source.Data {
		if !strings.HasPrefix(key, "/embed/") {
			continue
		}
		var route embedRoute
		if err := json.Unmarshal(raw, &route); err != nil {
			continue
		}
		if route.VideoData == nil {
			return nil, ErrPostUnavailable
		}
		md := route.VideoData.toMetadata()
		if md.ID != id {
			return nil, fmt.Errorf("halaman embed mengembalikan post lain (%s)", md.ID)
		}
		return md, nil
	}

	return nil, errors.New("data post tidak ditemukan pada halaman embed")
}

func fetchTikWM(ctx context.Context, pageURL string) (*Metadata, error) {
	body, status, err := getBody(ctx, tikwmEndpoint+"?url="+url.QueryEscape(pageURL), browserUA, "https://tikwm.com/")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("layanan cadangan membalas HTTP %d", status)
	}

	var resp tikwmResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("gagal membaca respons layanan cadangan: %w", err)
	}
	if resp.Code != 0 {
		msg := resp.Msg
		if msg == "" {
			msg = "post tidak tersedia atau bersifat privat"
		}
		return nil, errors.New(msg)
	}

	return resp.Data.toMetadata(), nil
}

func getBody(ctx context.Context, endpoint, userAgent, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
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

func sanitizeFilename(name string) string {
	name = invalidFilenameChars.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstURL(list []string) string {
	for _, v := range list {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func formatUploadDate(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func determineResolution(ratio string, width, height int) string {
	lower := strings.ToLower(ratio)
	if ratio != "" && (strings.Contains(lower, "p") || strings.Contains(lower, "k")) {
		return ratio
	}
	minDim := width
	if height < minDim {
		minDim = height
	}
	switch {
	case minDim <= 0:
		return ""
	case minDim >= 1080:
		return "1080p"
	case minDim >= 720:
		return "720p"
	case minDim >= 480:
		return "480p"
	case minDim >= 360:
		return "360p"
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func buildTitle(description, author string) string {
	if line := strings.TrimSpace(strings.Split(description, "\n")[0]); line != "" {
		return line
	}
	if author != "" {
		return "Video by @" + author
	}
	return "TikTok Post"
}

// flexInt64 menerima angka JSON yang bisa berupa number maupun string.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(string(bytes.TrimSpace(data)), `"`)
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		f64, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil {
			return ferr
		}
		n = int64(f64)
	}
	*f = flexInt64(n)
	return nil
}

type mobileFeed struct {
	AwemeList []struct {
		AwemeID string `json:"aweme_id"`
		Video   struct {
			PlayAddr struct {
				URLList []string `json:"url_list"`
			} `json:"play_addr"`
		} `json:"video"`
	} `json:"aweme_list"`
}

type embedState struct {
	Source struct {
		Data map[string]json.RawMessage `json:"data"`
	} `json:"source"`
}

type embedRoute struct {
	Code      int             `json:"code"`
	IsError   bool            `json:"isError"`
	VideoData *embedVideoData `json:"videoData"`
}

type embedVideoData struct {
	AuthorInfos struct {
		UniqueID     string   `json:"uniqueId"`
		NickName     string   `json:"nickName"`
		Covers       []string `json:"covers"`
		CoversMedium []string `json:"coversMedium"`
		CoversLarger []string `json:"coversLarger"`
	} `json:"authorInfos"`
	ItemInfos struct {
		ID           string    `json:"id"`
		Text         string    `json:"text"`
		CreateTime   flexInt64 `json:"createTime"`
		Covers       []string  `json:"covers"`
		CoversOrigin []string  `json:"coversOrigin"`
		PlayCount    flexInt64 `json:"playCount"`
		DiggCount    flexInt64 `json:"diggCount"`
		CommentCount flexInt64 `json:"commentCount"`
		ShareCount   flexInt64 `json:"shareCount"`
		Video        struct {
			URLs      []string `json:"urls"`
			VideoMeta struct {
				Width    int `json:"width"`
				Height   int `json:"height"`
				Duration int `json:"duration"`
			} `json:"videoMeta"`
		} `json:"video"`
	} `json:"itemInfos"`
	MusicInfos struct {
		MusicName  string   `json:"musicName"`
		AuthorName string   `json:"authorName"`
		PlayURL    []string `json:"playUrl"`
	} `json:"musicInfos"`
	ImagePostInfo *struct {
		DisplayImages []struct {
			Width   int      `json:"width"`
			Height  int      `json:"height"`
			URLList []string `json:"urlList"`
		} `json:"displayImages"`
	} `json:"imagePostInfo"`
}

func (d embedVideoData) toMetadata() *Metadata {
	var photos []string
	if d.ImagePostInfo != nil {
		for _, img := range d.ImagePostInfo.DisplayImages {
			if u := firstURL(img.URLList); u != "" {
				photos = append(photos, u)
			}
		}
	}

	author := d.AuthorInfos.UniqueID
	duration := d.ItemInfos.Video.VideoMeta.Duration
	width := d.ItemInfos.Video.VideoMeta.Width
	height := d.ItemInfos.Video.VideoMeta.Height
	audioURL := firstNonEmpty(firstURL(d.MusicInfos.PlayURL))

	thumbnail := firstNonEmpty(firstURL(d.ItemInfos.CoversOrigin), firstURL(d.ItemInfos.Covers))
	if thumbnail == "" && len(photos) > 0 {
		thumbnail = photos[0]
	}

	md := &Metadata{
		ID:                d.ItemInfos.ID,
		Author:            author,
		Nickname:          d.AuthorInfos.NickName,
		Title:             buildTitle(d.ItemInfos.Text, author),
		Description:       d.ItemInfos.Text,
		Duration:          duration,
		FormattedDuration: FormatDuration(duration),
		Resolution:        determineResolution("", width, height),
		Width:             width,
		Height:            height,
		Thumbnail:         thumbnail,
		UploadDate:        formatUploadDate(int64(d.ItemInfos.CreateTime)),
		Views:             int64(d.ItemInfos.PlayCount),
		Likes:             int64(d.ItemInfos.DiggCount),
		Comments:          int64(d.ItemInfos.CommentCount),
		Shares:            int64(d.ItemInfos.ShareCount),
		VideoURL:          firstURL(d.ItemInfos.Video.URLs),
		AudioURL:          audioURL,
		Photos:            photos,
	}

	if audioURL != "" {
		md.Music = &Music{Title: d.MusicInfos.MusicName, Author: d.MusicInfos.AuthorName, URL: audioURL}
	}
	// Pada post foto, field video bisa berisi stream audio, bukan video.
	if len(photos) > 0 {
		md.VideoURL = ""
	}
	return md
}

type tikwmResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data tikwmDetail `json:"data"`
}

type tikwmDetail struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	OriginCover  string   `json:"origin_cover"`
	Duration     int      `json:"duration"`
	Play         string   `json:"play"`
	WMPlay       string   `json:"wmplay"`
	HDPlay       string   `json:"hdplay"`
	PlayCount    int64    `json:"play_count"`
	DiggCount    int64    `json:"digg_count"`
	CommentCount int64    `json:"comment_count"`
	ShareCount   int64    `json:"share_count"`
	CreateTime   int64    `json:"create_time"`
	Images       []string `json:"images"`
	Author       struct {
		UniqueID string `json:"unique_id"`
		Nickname string `json:"nickname"`
	} `json:"author"`
	MusicInfo struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Play   string `json:"play"`
	} `json:"music_info"`
}

func (d tikwmDetail) toMetadata() *Metadata {
	photos := make([]string, 0, len(d.Images))
	for _, img := range d.Images {
		if strings.TrimSpace(img) != "" {
			photos = append(photos, img)
		}
	}

	videoURL := firstNonEmpty(d.HDPlay, d.Play, d.WMPlay)
	audioURL := strings.TrimSpace(d.MusicInfo.Play)
	author := d.Author.UniqueID

	md := &Metadata{
		ID:                d.ID,
		Author:            author,
		Nickname:          d.Author.Nickname,
		Title:             buildTitle(d.Title, author),
		Description:       d.Title,
		Duration:          d.Duration,
		FormattedDuration: FormatDuration(d.Duration),
		Thumbnail:         firstNonEmpty(d.OriginCover, d.Cover),
		UploadDate:        formatUploadDate(d.CreateTime),
		Views:             d.PlayCount,
		Likes:             d.DiggCount,
		Comments:          d.CommentCount,
		Shares:            d.ShareCount,
		VideoURL:          videoURL,
		AudioURL:          audioURL,
		Photos:            photos,
	}

	if audioURL != "" {
		md.Music = &Music{Title: d.MusicInfo.Title, Author: d.MusicInfo.Author, URL: audioURL}
	}
	// tikwm mengisi field play dengan audio untuk post foto.
	if len(photos) > 0 {
		md.VideoURL = ""
	}
	return md
}
