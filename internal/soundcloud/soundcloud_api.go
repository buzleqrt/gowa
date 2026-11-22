package soundcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func searchWithClientID(ctx context.Context, cid, query string, limit int) (*SearchResult, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("client_id", cid)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("offset", "0")
	params.Set("app_locale", "en")

	body, status, err := getBody(ctx, apiURL+"?"+params.Encode(), "https://soundcloud.com/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errInvalidClientID
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("API SoundCloud membalas HTTP %d", status)
	}

	var payload apiSearch
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("gagal membaca respons SoundCloud: %w", err)
	}

	result := &SearchResult{
		Query:  query,
		Total:  payload.TotalResults,
		Tracks: make([]Track, 0, len(payload.Collection)),
	}
	for _, item := range payload.Collection {
		track := item.toTrack()
		if track.Title == "" || track.URL == "" {
			continue
		}
		result.Tracks = append(result.Tracks, track)
	}

	if len(result.Tracks) > limit {
		result.Tracks = result.Tracks[:limit]
	}
	return result, nil
}

func getClientID(ctx context.Context, refresh bool) (string, error) {
	clientIDMu.Lock()
	if !refresh && clientIDVal != "" && time.Now().Before(clientIDExp) {
		cid := clientIDVal
		clientIDMu.Unlock()
		return cid, nil
	}
	clientIDMu.Unlock()

	cid, err := discoverClientID(ctx)
	if err != nil {
		return "", err
	}

	clientIDMu.Lock()
	clientIDVal = cid
	clientIDExp = time.Now().Add(clientIDTTL)
	clientIDMu.Unlock()

	return cid, nil
}

func discoverClientID(ctx context.Context) (string, error) {
	html, status, err := getBody(ctx, homeURL, homeURL)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("halaman SoundCloud membalas HTTP %d", status)
	}

	if m := clientIDPattern.FindSubmatch(html); m != nil {
		return string(m[1]), nil
	}

	assets := dedupe(assetPattern.FindAllString(string(html), -1))
	if len(assets) == 0 {
		return "", errors.New("bundle JavaScript SoundCloud tidak ditemukan")
	}

	findCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, 4)
	found := make(chan string, len(assets))

	var wg sync.WaitGroup
	for _, asset := range assets {
		wg.Add(1)
		go func(asset string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-findCtx.Done():
				return
			}
			defer func() { <-sem }()

			body, status, err := getBody(findCtx, asset, homeURL)
			if err != nil || status != http.StatusOK {
				return
			}
			if m := clientIDPattern.FindSubmatch(body); m != nil {
				select {
				case found <- string(m[1]):
				case <-findCtx.Done():
				}
			}
		}(asset)
	}

	go func() {
		wg.Wait()
		close(found)
	}()

	cid, ok := <-found
	if !ok {
		return "", errors.New("client_id SoundCloud tidak ditemukan pada bundle JavaScript")
	}
	return cid, nil
}

func getBody(ctx context.Context, endpoint, referer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
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

func dedupe(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

type apiSearch struct {
	Collection   []apiTrack `json:"collection"`
	TotalResults int64      `json:"total_results"`
}

type apiTranscoding struct {
	URL    string `json:"url"`
	Preset string `json:"preset"`
	Format struct {
		Protocol string `json:"protocol"`
		MimeType string `json:"mime_type"`
	} `json:"format"`
}

type apiTrackDetail struct {
	TrackAuth string `json:"track_authorization"`
	Media     struct {
		Transcodings []apiTranscoding `json:"transcodings"`
	} `json:"media"`
}

type apiTrack struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	PermalinkURL  string `json:"permalink_url"`
	ArtworkURL    string `json:"artwork_url"`
	Genre         string `json:"genre"`
	Duration      int    `json:"duration"`
	PlaybackCount int64  `json:"playback_count"`
	LikesCount    int64  `json:"likes_count"`
	CreatedAt     string `json:"created_at"`
	User          struct {
		Username     string `json:"username"`
		PermalinkURL string `json:"permalink_url"`
		AvatarURL    string `json:"avatar_url"`
	} `json:"user"`
}

func (t apiTrack) toTrack() Track {
	seconds := t.Duration / 1000
	artwork := t.ArtworkURL
	if strings.TrimSpace(artwork) == "" {
		artwork = t.User.AvatarURL
	}

	return Track{
		ID:                t.ID,
		Title:             strings.TrimSpace(t.Title),
		Artist:            strings.TrimSpace(t.User.Username),
		ArtistURL:         strings.TrimSpace(t.User.PermalinkURL),
		URL:               strings.TrimSpace(t.PermalinkURL),
		Artwork:           upgradeArtwork(artwork),
		Genre:             strings.TrimSpace(t.Genre),
		Duration:          seconds,
		FormattedDuration: formatDuration(seconds),
		Plays:             t.PlaybackCount,
		Likes:             t.LikesCount,
		CreatedAt:         t.CreatedAt,
	}
}
