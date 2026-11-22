package soundcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
)

func toPlayableAudio(ctx context.Context, data []byte) ([]byte, string) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return data, "audio/mp4"
	}

	cmd := exec.CommandContext(ctx, path,
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn", "-c:a", "libmp3lame", "-b:a", "128k", "-compression_level", "9",
		"-f", "mp3", "pipe:1",
	)
	cmd.Stdin = bytes.NewReader(data)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return data, "audio/mp4"
	}
	return out.Bytes(), "audio/mpeg"
}

// pickHLS memilih transcoding HLS yang tidak terenkripsi, karena segmen
// terenkripsi butuh kunci AES yang tidak disediakan API publik.
func pickHLS(items []apiTranscoding) *apiTranscoding {
	var fallback *apiTranscoding
	for i := range items {
		item := &items[i]
		protocol := strings.ToLower(item.Format.Protocol)
		if strings.Contains(protocol, "encrypted") {
			continue
		}
		if protocol != "hls" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(item.Format.MimeType), "audio/mp4") {
			return item
		}
		if fallback == nil {
			fallback = item
		}
	}
	return fallback
}

func resolveTranscoding(ctx context.Context, transcodingURL, cid, trackAuth string) (string, error) {
	target := transcodingURL
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	target += sep + "client_id=" + url.QueryEscape(cid)
	if strings.TrimSpace(trackAuth) != "" {
		target += "&track_authorization=" + url.QueryEscape(trackAuth)
	}

	body, status, err := getBody(ctx, target, homeURL)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "", errInvalidClientID
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("transcoding SoundCloud membalas HTTP %d", status)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("gagal membaca URL media SoundCloud: %w", err)
	}
	if strings.TrimSpace(payload.URL) == "" {
		return "", errors.New("URL media SoundCloud kosong")
	}
	return payload.URL, nil
}

// downloadHLS mengunduh init segment dan seluruh segmen playlist lalu
// menggabungkannya menjadi satu berkas fMP4.
func downloadHLS(ctx context.Context, playlistURL string, maxBytes int64) ([]byte, error) {
	body, status, err := getBody(ctx, playlistURL, homeURL)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("playlist audio SoundCloud membalas HTTP %d", status)
	}

	base, err := url.Parse(playlistURL)
	if err != nil {
		return nil, err
	}

	resolve := func(ref string) string {
		if parsed, err := base.Parse(ref); err == nil {
			return parsed.String()
		}
		return ref
	}

	initURL := ""
	var segments []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			if m := extMapPattern.FindStringSubmatch(line); m != nil {
				initURL = resolve(m[1])
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		segments = append(segments, resolve(line))
	}

	if len(segments) == 0 {
		return nil, errors.New("playlist audio SoundCloud tidak memuat segmen")
	}
	if len(segments) > maxHLSSegments {
		segments = segments[:maxHLSSegments]
	}

	// Init segment harus paling depan, segmen media tetap berurutan.
	urls := make([]string, 0, len(segments)+1)
	if initURL != "" {
		urls = append(urls, initURL)
	}
	urls = append(urls, segments...)

	parts, err := downloadParts(ctx, urls, maxBytes)
	if err != nil {
		return nil, err
	}

	total := 0
	for _, part := range parts {
		total += len(part)
	}
	buf := bytes.NewBuffer(make([]byte, 0, total))
	for _, part := range parts {
		buf.Write(part)
	}
	return buf.Bytes(), nil
}

// downloadParts mengunduh banyak segmen secara paralel tetapi tetap
// mengembalikan hasil sesuai urutan aslinya, karena segmen HLS tidak boleh
// digabung acak.
func downloadParts(ctx context.Context, urls []string, maxBytes int64) ([][]byte, error) {
	parts := make([][]byte, len(urls))
	errs := make([]error, len(urls))

	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, target := range urls {
		wg.Add(1)
		go func(i int, target string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			parts[i], errs[i] = downloadMedia(ctx, target, maxBytes)
		}(i, target)
	}
	wg.Wait()

	var total int64
	for i := range parts {
		if errs[i] != nil {
			return nil, errs[i]
		}
		total += int64(len(parts[i]))
		if total > maxBytes {
			return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
		}
	}
	return parts, nil
}

func downloadMedia(ctx context.Context, mediaURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", homeURL)

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unduhan audio SoundCloud gagal: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}
	return data, nil
}
