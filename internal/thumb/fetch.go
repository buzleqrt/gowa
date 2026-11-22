package thumb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxFetchBytes = 10 << 20

	fetchTimeout = 30 * time.Second
)

var fetchClient = &http.Client{Timeout: fetchTimeout}

func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if !IsHTTPURL(rawURL) {
		return nil, errors.New("URL tidak valid")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return nil, fmt.Errorf("URL tidak valid: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/128.0")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal mengunduh gambar: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gagal mengunduh gambar: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca gambar: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("gambar kosong")
	}
	if len(data) > maxFetchBytes {
		return nil, fmt.Errorf("ukuran gambar melebihi batas %d MB", maxFetchBytes>>20)
	}
	if !strings.HasPrefix(MimeType(data), "image/") {
		return nil, errors.New("URL tidak menunjuk ke gambar")
	}

	return data, nil
}

func IsHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
