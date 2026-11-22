package facebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36"

	maxPageBytes = 8 << 20
)

var (
	ErrInvalidURL = errors.New("link Facebook tidak valid")

	ErrNoMedia = errors.New("tidak ada media publik yang bisa diunduh (post privat, teks saja, atau bukan post)")

	urlPattern   = regexp.MustCompile(`(?i)https?://(?:www\.|m\.|web\.|lm\.)?(?:facebook\.com|fb\.watch|fb\.com|fb\.me)/[^\s"'<>]+`)
	sharePattern = regexp.MustCompile(`^[A-Za-z0-9]{8,20}$`)

	tagTitlePattern      = regexp.MustCompile(`<title>([^<]*)`)
	reactionCountPattern = regexp.MustCompile(`"i18n_reaction_count":"(\d+)"`)
	reactionLegacyPatt   = regexp.MustCompile(`"reaction_count":\{"count":(\d+)`)
	commentCountPattern  = regexp.MustCompile(`"comments":\{"total_count":(\d+)\}`)

	photoTypePattern = regexp.MustCompile(`"__typename":"Photo"|"photo_id"`)
	albumStartPatt   = regexp.MustCompile(`"all_subattachments":\{"count":\d+,"nodes":\[`)

	albumViewerPattern = regexp.MustCompile(`"viewer_image":\{"height":(\d+),"width":(\d+),"uri":"((?:[^"\\]|\\.)*)"`)
	albumImagePattern  = regexp.MustCompile(`"image":\{"uri":"((?:[^"\\]|\\.)*)"`)

	groupNamePattern  = regexp.MustCompile(`"group_name":"((?:[^"\\]|\\.)*)"`)
	groupPathPattern  = regexp.MustCompile(`facebook\.com/groups/([^/?#]+)`)
	profileSegPattern = regexp.MustCompile(`facebook\.com/([A-Za-z][A-Za-z0-9._]{1,40})(?:/|[?#]|$)`)
	permalinkNamePatt = regexp.MustCompile(`facebook\.com/([^/]+)/(?:videos|reel|watch|photo)`)
	permalinkRootPatt = regexp.MustCompile(`facebook\.com/([^/?#]+)`)

	pageClient     = &http.Client{Timeout: 25 * time.Second}
	downloadClient = &http.Client{Timeout: 5 * time.Minute}
)

type Metadata struct {
	Type       string
	Title      string
	Caption    string
	Username   string
	Fullname   string
	ProfileURL string
	Permalink  string
	Likes      int64
	Comments   int64
	Poster     string
	VideoHD    string
	VideoSD    string
	Images     []string
}

func ExtractURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if found := urlPattern.FindString(input); found != "" {
		return strings.TrimRight(found, ".,;)")
	}
	if sharePattern.MatchString(input) {
		return "https://www.facebook.com/share/p/" + input + "/"
	}
	return ""
}

func Fetch(ctx context.Context, raw string) (*Metadata, error) {
	target := ExtractURL(raw)
	if target == "" {
		return nil, ErrInvalidURL
	}

	page, permalink, err := fetchPage(ctx, target)
	if err != nil {
		return nil, err
	}

	md := parse(page, permalink)
	if md.Type == "none" {
		return nil, ErrNoMedia
	}
	return md, nil
}

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
	req.Header.Set("Referer", "https://www.facebook.com/")
	req.Header.Set("Accept", "*/*")

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

func fetchPage(ctx context.Context, target string) (string, string, error) {
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}

		page, permalink, err := requestPage(ctx, target)
		if err == nil {
			return page, permalink, nil
		}
		lastErr = err
	}

	return "", "", fmt.Errorf("gagal membuka halaman Facebook: %w", lastErr)
}

func requestPage(ctx context.Context, target string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Cookie", "wd=1920x1080; _fbp=fb.1.1758800000000.1234567890")

	resp, err := pageClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", "", err
	}

	page := string(body)
	if !strings.Contains(page, "<title>") {
		return "", "", errors.New("halaman tidak memuat konten")
	}

	permalink := target
	if resp.Request != nil && resp.Request.URL != nil {
		permalink = resp.Request.URL.String()
	}
	return page, permalink, nil
}

func parse(page, permalink string) *Metadata {
	hd := jsonStringField(page, "browser_native_hd_url")
	sd := jsonStringField(page, "browser_native_sd_url")

	title := decodeText(metaContent(page, "og:title"))
	if title == "" {
		if m := tagTitlePattern.FindStringSubmatch(page); m != nil {
			title = decodeText(m[1])
		}
	}
	title = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(title), "| Facebook"))

	description := strings.TrimSpace(decodeText(metaContent(page, "og:description")))
	poster := strings.TrimSpace(decodeText(metaContent(page, "og:image")))
	ogURL := metaContent(page, "og:url")

	images := albumImages(page)
	isPhoto := len(hd) == 0 && len(sd) == 0 && poster != "" &&
		(photoTypePattern.MatchString(page) || albumStartPatt.MatchString(page))

	if len(hd) == 0 && len(sd) == 0 && !isPhoto {

		return &Metadata{Type: "none", Title: title, Caption: description, Permalink: permalink}
	}

	if len(images) == 0 && poster != "" {
		images = []string{poster}
	}

	username, fullname, profileURL := parseAuthor(page, permalink, ogURL, title)

	md := &Metadata{
		Type:       "image",
		Title:      title,
		Caption:    firstNonEmpty(description, title),
		Username:   username,
		Fullname:   fullname,
		ProfileURL: profileURL,
		Permalink:  permalink,
		Likes:      parseCount(page, reactionCountPattern, reactionLegacyPatt),
		Comments:   parseCount(page, commentCountPattern),
		Poster:     poster,
		VideoHD:    hd,
		VideoSD:    sd,
		Images:     images,
	}

	if len(hd) > 0 || len(sd) > 0 {
		md.Type = "video"
	}
	return md
}

func jsonStringField(page, key string) string {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":"((?:[^"\\]|\\.)*)"`)
	m := pattern.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+m[1]+`"`), &decoded); err != nil {
		return m[1]
	}
	return decoded
}

func metaContent(page, property string) string {
	pattern := regexp.MustCompile(
		`<meta[^>]+(?:property|name)="` + regexp.QuoteMeta(property) + `"[^>]+content="([^"]*)"`,
	)
	if m := pattern.FindStringSubmatch(page); m != nil {
		return m[1]
	}
	return ""
}

func decodeText(s string) string {
	return html.UnescapeString(s)
}

func parseCount(page string, patterns ...*regexp.Regexp) int64 {
	for _, pattern := range patterns {
		m := pattern.FindStringSubmatch(page)
		if m == nil {
			continue
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}

func albumImages(page string) []string {
	loc := albumStartPatt.FindStringIndex(page)
	if loc == nil {
		return nil
	}

	block := page[loc[0]:]
	if end := strings.Index(block, "}],"); end > 0 {
		block = block[:end+3]
	}

	var images []string
	seen := make(map[string]struct{})

	add := func(raw string) {
		u := strings.TrimSpace(unescapeJSONString(raw))
		if u == "" {
			return
		}
		if _, exists := seen[u]; exists {
			return
		}
		seen[u] = struct{}{}
		images = append(images, u)
	}

	if matches := albumViewerPattern.FindAllStringSubmatch(block, -1); len(matches) > 0 {
		for _, m := range matches {
			add(m[3])
		}
		return images
	}

	for _, m := range albumImagePattern.FindAllStringSubmatch(block, -1) {
		add(m[1])
	}
	return images
}

func unescapeJSONString(raw string) string {
	var decoded string
	if err := json.Unmarshal([]byte(`"`+raw+`"`), &decoded); err != nil {
		return raw
	}
	return decoded
}

func parseAuthor(page, permalink, ogURL, title string) (username, fullname, profileURL string) {
	groupID := ""
	if m := groupPathPattern.FindStringSubmatch(permalink); m != nil {
		groupID = m[1]
	} else if m := groupPathPattern.FindStringSubmatch(ogURL); m != nil {
		groupID = m[1]
	}

	groupName := ""
	if m := groupNamePattern.FindStringSubmatch(page); m != nil {
		groupName = unescapeJSONString(m[1])
	}

	if groupID != "" {
		fullname = groupName
		if fullname == "" {
			fullname = strings.TrimSpace(strings.Split(title, " | ")[0])
		}
		return "group:" + decodeURIComponent(groupID), fullname, "https://www.facebook.com/groups/" + groupID
	}

	fullname = title
	if strings.Contains(title, " | ") {
		fullname = strings.TrimSpace(strings.Split(title, " | ")[1])
	}

	blacklist := map[string]struct{}{
		"watch": {}, "share": {}, "reel": {}, "videos": {}, "photo": {}, "photo.php": {},
		"video.php": {}, "permalink": {}, "groups": {}, "stories": {}, "marketplace": {},
		"login": {}, "help": {},
	}

	ogSegment := ""
	if m := profileSegPattern.FindStringSubmatch(ogURL); m != nil {
		ogSegment = m[1]
	}
	if _, blocked := blacklist[strings.ToLower(ogSegment)]; blocked {
		ogSegment = ""
	}

	pathSegment := ""
	if m := permalinkNamePatt.FindStringSubmatch(permalink); m != nil {
		pathSegment = m[1]
	} else if m := permalinkRootPatt.FindStringSubmatch(permalink); m != nil {
		pathSegment = m[1]
	}
	if pathSegment != "" {
		if _, blocked := blacklist[strings.ToLower(pathSegment)]; blocked || isDigits(pathSegment) {
			pathSegment = ""
		}
	}
	pathSegment = decodeURIComponent(pathSegment)

	switch {
	case ogSegment != "":
		username = ogSegment
		profileURL = "https://www.facebook.com/" + ogSegment
	case pathSegment != "":
		username = pathSegment
		profileURL = "https://www.facebook.com/" + pathSegment
	case fullname != "":
		username = fullname
		profileURL = ogURL
	}

	return username, fullname, profileURL
}

func decodeURIComponent(s string) string {
	if s == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
