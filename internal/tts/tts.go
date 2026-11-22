package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	apiURL    = "https://anytospeech.com/api/tools/famous-voice"
	voiceSlug = "anime-girl-shy"
	referer   = "https://anytospeech.com/ai-voice-generator/anime-girl-shy"

	chunkMaxChars = 180
	maxChunkBytes = 8 << 20

	requestTimeout = 25 * time.Second
	userAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

	ffmpeg  = "ffmpeg"
	ffprobe = "ffprobe"
)

var (
	ErrEmptyText = errors.New("teks untuk suara kosong")
	httpClient   = &http.Client{Timeout: requestTimeout}
)

func CleanText(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		switch r {
		case '*', '_', '~', '`', '#', '|', '[', ']', '(', ')':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}

	words := strings.Fields(b.String())
	out := make([]string, 0, len(words))
	for _, w := range words {
		if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
			continue
		}
		out = append(out, w)
	}

	return strings.Join(out, " ")
}

func SplitChunks(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = chunkMaxChars
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	sentences := splitSentences(text)
	if len(sentences) == 0 {
		sentences = []string{text}
	}

	var chunks []string
	current := ""

	for _, s := range sentences {
		switch {
		case current == "":
			if len(s) > maxLen {
				chunks = append(chunks, splitWords(s, maxLen)...)
				continue
			}
			current = s
		case len(current)+1+len(s) <= maxLen:
			current += " " + s
		default:
			chunks = append(chunks, current)
			if len(s) > maxLen {
				chunks = append(chunks, splitWords(s, maxLen)...)
				current = ""
			} else {
				current = s
			}
		}
	}
	if current != "" {
		chunks = append(chunks, current)
	}

	return chunks
}

func splitSentences(text string) []string {

	var out []string
	start := 0
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		if !isSentenceBreak(runes[i]) {
			continue
		}
		if i+1 >= len(runes) || !unicode.IsSpace(runes[i+1]) {
			continue
		}
		if seg := strings.TrimSpace(string(runes[start : i+1])); seg != "" {
			out = append(out, seg)
		}
		start = i + 1
	}
	if seg := strings.TrimSpace(string(runes[start:])); seg != "" {
		out = append(out, seg)
	}

	return out
}

func isSentenceBreak(r rune) bool {
	switch r {
	case '.', '!', '?', ',', '…', '~', '\n', '\r':
		return true
	}
	return false
}

func splitWords(text string, maxLen int) []string {
	var out []string
	current := ""

	for _, w := range strings.Fields(text) {
		for len(w) > maxLen {
			if current != "" {
				out = append(out, current)
				current = ""
			}
			out = append(out, w[:maxLen])
			w = w[maxLen:]
		}
		if current == "" {
			current = w
		} else if len(current)+1+len(w) <= maxLen {
			current += " " + w
		} else {
			out = append(out, current)
			current = w
		}
	}
	if current != "" {
		out = append(out, current)
	}

	return out
}

func Synthesize(ctx context.Context, text string) ([]byte, uint32, error) {
	clean := CleanText(text)
	if clean == "" {
		return nil, 0, ErrEmptyText
	}

	chunks := SplitChunks(clean, chunkMaxChars)
	if len(chunks) == 0 {
		return nil, 0, ErrEmptyText
	}

	audio := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		buf, err := fetchChunk(ctx, chunk)
		if err != nil {
			return nil, 0, err
		}
		audio = append(audio, buf)
	}

	ogg, err := mergeToOpus(ctx, audio)
	if err != nil {
		return nil, 0, err
	}

	return ogg, durationSeconds(ctx, ogg), nil
}

type speechRequest struct {
	Slug string `json:"slug"`
	Text string `json:"text"`
}

type speechResponse struct {
	AudioURL string `json:"audioUrl"`
	Message  string `json:"message"`
}

func fetchChunk(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(speechRequest{Slug: voiceSlug, Text: text})
	if err != nil {
		return nil, err
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	ip := randomIP()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", "https://anytospeech.com")
	req.Header.Set("X-Forwarded-For", ip)
	req.Header.Set("Client-IP", ip)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi layanan suara: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca balasan layanan suara: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("layanan suara membalas %s: %s", resp.Status, snippet(raw))
	}

	var parsed speechResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("balasan layanan suara tidak dikenali: %w", err)
	}
	if parsed.AudioURL == "" {
		if parsed.Message != "" {
			return nil, fmt.Errorf("layanan suara: %s", parsed.Message)
		}
		return nil, errors.New("layanan suara tidak mengembalikan URL audio")
	}

	audioURL := parsed.AudioURL
	if !strings.HasPrefix(audioURL, "http") {
		audioURL = "https://anytospeech.com" + audioURL
	}

	return download(ctx, audioURL)
}

func download(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal mengunduh audio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gagal mengunduh audio: %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChunkBytes))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca audio: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("audio hasil suara kosong")
	}

	return data, nil
}

func mergeToOpus(ctx context.Context, chunks [][]byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "gowa-tts-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	var list strings.Builder
	for i, chunk := range chunks {
		path := filepath.Join(dir, "chunk-"+strconv.Itoa(i)+".mp3")
		if err := os.WriteFile(path, chunk, 0o600); err != nil {
			return nil, err
		}
		fmt.Fprintf(&list, "file '%s'\n", path)
	}

	listPath := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return nil, err
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if len(chunks) == 1 {
		args = append(args, "-i", filepath.Join(dir, "chunk-0.mp3"))
	} else {
		args = append(args, "-f", "concat", "-safe", "0", "-i", listPath)
	}
	args = append(args,
		"-af", "volume=1.2",
		"-vn", "-c:a", "libopus", "-b:a", "64k", "-ar", "48000", "-ac", "1",
		"-f", "ogg", "pipe:1",
	)

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("konversi voice note gagal: %s", msg)
	}
	if len(out) == 0 {
		return nil, errors.New("konversi voice note menghasilkan audio kosong")
	}

	return out, nil
}

func durationSeconds(ctx context.Context, data []byte) uint32 {
	dir, err := os.MkdirTemp("", "gowa-tts-dur-*")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "voice.ogg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return 0
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(probeCtx, ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}

	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 {
		return 0
	}

	return uint32(secs + 0.5)
}

func randomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		rand.Intn(150)+50, rand.Intn(200), rand.Intn(200), rand.Intn(200))
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(kosong)"
	}
	return s
}
