package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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
