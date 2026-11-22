package pinterest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

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
