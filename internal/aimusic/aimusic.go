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
	candidates := []string{
		"node",
		"/root/.nvm/versions/node/v26.13.0/bin/node",
		"/usr/bin/node",
		"/usr/local/bin/node",
	}
	for _, c := range candidates {
		if c == "node" {
			if p, err := exec.LookPath(c); err == nil {
				return p
			}
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "node"
}

func scriptPath() string {
	candidates := []string{
		"/root/Gowa/scripts/aimusic.js",
		"scripts/aimusic.js",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if filepath.IsAbs(c) {
				return c
			}
			if wd, err := os.Getwd(); err == nil {
				return filepath.Join(wd, c)
			}
			return c
		}
	}
	return "/root/Gowa/scripts/aimusic.js"
}

func Generate(ctx context.Context, desc string) (Song, error) {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return Song{}, fmt.Errorf("deskripsi lagu kosong")
	}

	node := nodeBinary()
	script := scriptPath()
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
		return Song{}, fmt.Errorf("gagal generate lagu: %v: %s", err, msg)
	}

	var res generateResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return Song{}, fmt.Errorf("gagal parse hasil: %v\n%s", err, stdout.String())
	}
	if res.Error != "" {
		return Song{}, fmt.Errorf("%s: %s", res.Error, res.Msg)
	}
	if len(res.Songs) == 0 {
		return Song{}, fmt.Errorf("tidak ada lagu yang dihasilkan: %s", stdout.String())
	}
	return res.Songs[0], nil
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
