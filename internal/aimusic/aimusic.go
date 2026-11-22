package aimusic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Song struct {
	Title    string  `json:"title"`
	AudioURL string  `json:"audio_url"`
	ImageURL string  `json:"image_url"`
	Lyrics   string  `json:"lyrics"`
	Duration float64 `json:"duration"`
}

type generateResult struct {
	Songs []Song `json:"songs"`
	Error string `json:"error_code"`
	Msg   string `json:"message"`
}

func nodeBinary() string {
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	return "node"
}

func scriptPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Join("scripts", "aimusic.js")
	}
	return filepath.Join(wd, "scripts", "aimusic.js")
}

func Generate(ctx context.Context, desc string) (Song, error) {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return Song{}, fmt.Errorf("deskripsi lagu kosong")
	}

	script := scriptPath()
	if _, err := os.Stat(script); err != nil {
		return Song{}, fmt.Errorf("skrip aimusic tidak ditemukan di %s", script)
	}

	node := nodeBinary()
	cmd := exec.CommandContext(ctx, node, script, "create", "--desc", desc)
	cmd.Dir = filepath.Dir(script)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return Song{}, fmt.Errorf("%s", friendlyFailure(err, msg))
	}

	var res generateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return Song{}, fmt.Errorf("gagal parse hasil: %v\n%s", err, stdout.String())
	}
	if res.Error != "" {
		return Song{}, fmt.Errorf("%s", friendlyFailure(nil, res.Error+": "+res.Msg))
	}
	if len(res.Songs) == 0 {
		return Song{}, fmt.Errorf("tidak ada lagu yang dihasilkan: %s", stdout.String())
	}
	return res.Songs[0], nil
}

// friendlyFailure menerjemahkan kode error SonivaMusic yang sering muncul
// menjadi kalimat yang bisa dimengerti pengguna perintah.
func friendlyFailure(runErr error, raw string) string {
	switch {
	case strings.Contains(raw, "INSUFFICIENT_CREDITS"):
		return "kredit akun SonivaMusic habis. Isi ulang kredit atau pakai akun lain, lalu coba lagi."
	case strings.Contains(raw, "bind_required"):
		return "sesi SonivaMusic perlu di-bind ulang. Hapus scripts/aimusic_session.json lalu coba lagi."
	case runErr != nil:
		return fmt.Sprintf("gagal generate lagu: %v: %s", runErr, raw)
	default:
		return raw
	}
}

func Download(ctx context.Context, mediaURL string, maxBytes int64) ([]byte, error) {
	mediaURL = strings.TrimSpace(mediaURL)
	if mediaURL == "" {
		return nil, fmt.Errorf("URL audio kosong")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
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
		return nil, fmt.Errorf("audio terlalu besar (>%d bytes)", maxBytes)
	}
	return data, nil
}
