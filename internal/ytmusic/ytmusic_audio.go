package ytmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func callInnerTube(ctx context.Context, api string, payload map[string]any, client map[string]any) ([]byte, error) {
	body, err := json.Marshal(mergeClient(client, payload))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, innertube+api+"?key="+apiKey, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", musicBase)
	req.Header.Set("Referer", musicBase+"/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube Music membalas HTTP %d", resp.StatusCode)
	}
	if !json.Valid(raw) {
		return nil, errors.New("respons YouTube Music bukan JSON")
	}
	return raw, nil
}

func mergeClient(client map[string]any, payload map[string]any) map[string]any {
	merged := map[string]any{
		"context": map[string]any{"client": client},
	}
	for k, v := range payload {
		merged[k] = v
	}
	return merged
}

func pickAudioFormat(formats []apiFormat) (apiFormat, bool) {
	best := apiFormat{}
	found := false

	// Utamakan audio/mp4 (m4a/AAC) karena paling aman diputar di WhatsApp;
	// audio/webm (opus) hanya dipakai bila m4a tidak tersedia.
	for _, format := range formats {
		if strings.TrimSpace(format.URL) == "" {
			continue
		}
		mime := baseMime(format.MimeType)
		if !strings.HasPrefix(mime, "audio/") {
			continue
		}
		if !found || formatScore(format) > formatScore(best) {
			best = format
			found = true
		}
	}
	return best, found
}

func formatScore(format apiFormat) int {
	score := format.AverageBitrate
	if score == 0 {
		score = format.Bitrate
	}
	if baseMime(format.MimeType) == "audio/mp4" {
		score += 1_000_000
	}
	// YouTube menyediakan beberapa trek audio (bahasa asli + hasil
	// auto-dubbing). Tanpa prioritas ini yang terambil bisa trek dub.
	if format.AudioTrack.AudioIsDefault {
		score += 10_000_000
	}
	return score
}

func downloadAudio(ctx context.Context, mediaURL, contentLength string, maxBytes int64) ([]byte, error) {
	size, _ := strconv.ParseInt(strings.TrimSpace(contentLength), 10, 64)
	if size > 0 && maxBytes > 0 && size > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", visionosUA)
	req.Header.Set("Referer", musicBase+"/")
	req.Header.Set("Accept", "*/*")
	// Tanpa Range, server audio YouTube berhenti di sekitar 576 KB dan
	// koneksi menggantung sampai timeout.
	if size > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", size-1))
	} else {
		req.Header.Set("Range", "bytes=0-")
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("unduhan audio gagal: HTTP %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("berkas audio kosong")
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("ukuran audio melebihi batas %d MB", maxBytes/(1<<20))
	}
	return data, nil
}

func baseMime(mimeType string) string {
	if idx := strings.Index(mimeType, ";"); idx >= 0 {
		mimeType = mimeType[:idx]
	}
	return strings.TrimSpace(mimeType)
}
