package pinterest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func getResource(ctx context.Context, endpoint, sourceURL, handler string, options map[string]any) ([]byte, error) {
	payload := map[string]any{
		"options": options,
		"context": map[string]any{},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("source_url", sourceURL)
	params.Set("data", string(raw))
	target := resourceURL + "/" + endpoint + "/?" + params.Encode()
	referer := baseURL + sourceURL

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		sess, err := getSession(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		body, status, err := doResourceRequest(ctx, target, referer, handler, sess)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return body, nil
		}
		if status == http.StatusNotFound {
			return nil, ErrPinNotFound
		}

		lastErr = fmt.Errorf("Pinterest membalas HTTP %d", status)
		// Sesi lama bisa ditolak (403/401), jadi ambil ulang sekali.
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			break
		}
	}

	if lastErr == nil {
		lastErr = errors.New("permintaan ke Pinterest gagal")
	}
	return nil, lastErr
}

func doResourceRequest(ctx context.Context, target, referer, handler string, sess *session) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json, text/javascript, */*, q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Pinterest-AppState", "active")
	req.Header.Set("X-Pinterest-PWS-Handler", handler)
	req.Header.Set("Referer", referer)
	req.Header.Set("Cookie", sess.cookies)
	if sess.csrf != "" {
		req.Header.Set("X-CSRFToken", sess.csrf)
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

func getSession(ctx context.Context, refresh bool) (*session, error) {
	sessionMu.Lock()
	if !refresh && sessionVal != nil && time.Now().Before(sessionVal.expires) {
		sess := sessionVal
		sessionMu.Unlock()
		return sess, nil
	}
	sessionMu.Unlock()

	sess, err := newSession(ctx)
	if err != nil {
		return nil, err
	}

	sessionMu.Lock()
	sessionVal = sess
	sessionMu.Unlock()
	return sess, nil
}

func newSession(ctx context.Context) (*session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("halaman Pinterest membalas HTTP %d", resp.StatusCode)
	}

	csrf := ""
	parts := make([]string, 0, len(resp.Cookies()))
	for _, cookie := range resp.Cookies() {
		parts = append(parts, cookie.Name+"="+cookie.Value)
		if cookie.Name == "csrftoken" {
			csrf = cookie.Value
		}
	}

	return &session{
		cookies: strings.Join(parts, "; "),
		csrf:    csrf,
		expires: time.Now().Add(sessionTTL),
	}, nil
}

func followRedirect(ctx context.Context, raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal membuka link: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String(), nil
	}
	return raw, nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
