package ytmusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	ErrEmptyQuery = errors.New("judul lagu tidak boleh kosong")

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

type Audio struct {
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"-"`
}

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
