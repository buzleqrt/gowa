package dola

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Reply adalah hasil satu percakapan dengan Dola/Nova AI.
type Reply struct {
	Text         string `json:"text"`
	Conversation string `json:"conversation_id"`
	MessageID    string `json:"message_id"`
	Replies      int    `json:"replies"`
}

type errorResult struct {
	Message string `json:"message"`
}

var progressPrefixes = []string{"Registering", "Core warning", "Limit ("}

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
		"/root/Gowa/scripts/dola.js",
		"scripts/dola.js",
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
	return "/root/Gowa/scripts/dola.js"
}

// proxyList membaca DOLA_PROXY yang boleh berisi beberapa proxy dipisah koma.
func proxyList() []string {
	raw := strings.Split(os.Getenv("DOLA_PROXY"), ",")
	var list []string
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			list = append(list, item)
		}
	}
	return list
}

func isTransportError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, needle := range []string{"fetch failed", "timeout", "socket hang up", "econnreset", "econnrefused", "network"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func run(ctx context.Context, proxy, sessionPath, convPath, stdin string, args ...string) (string, error) {
	script := scriptPath()
	cmd := exec.CommandContext(ctx, nodeBinary(), append([]string{script}, args...)...)
	cmd.Dir = filepath.Dir(script)
	cmd.Env = append(os.Environ(), "DOLA_SESSION_PATH="+sessionPath)
	if convPath != "" {
		cmd.Env = append(cmd.Env, "DOLA_CONV_PATH="+convPath)
	}
	// Layanan ini memblokir sebagian region; kalau DOLA_PROXY diisi, fetch Node
	// (NODE_USE_ENV_PROXY) akan memakai proxy tersebut.
	if proxy != "" {
		cmd.Env = append(cmd.Env,
			"HTTP_PROXY="+proxy,
			"HTTPS_PROXY="+proxy,
			"NODE_USE_ENV_PROXY=1",
		)
	}
	cmd.Stdin = strings.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if msg := errorText(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		if msg := errorText(stdout.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", fmt.Errorf("%v", err)
	}
	return stdout.String(), nil
}

// errorText mengambil baris pesan asli dari stderr, membuang baris progres.
func errorText(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		skip := false
		for _, prefix := range progressPrefixes {
			if strings.HasPrefix(line, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[:3]
	}
	msg := strings.Join(lines, "\n")
	if strings.Contains(msg, "710022017") {
		return "layanan AI menolak region server ini (710022017). Isi env DOLA_PROXY dengan proxy region yang didukung (mis. Indonesia) lalu restart bot"
	}
	return msg
}

// Ask mengirim prompt memakai device pada sessionPath dan konteks chat pada convPath.
func Ask(ctx context.Context, sessionPath, convPath, prompt string) (Reply, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Reply{}, fmt.Errorf("prompt kosong")
	}

	out, err := runWithRetry(ctx, sessionPath, convPath, prompt, "ask", "--json")
	if err != nil {
		return Reply{}, err
	}

	var reply Reply
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reply); err != nil {
		var e errorResult
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &e) == nil && e.Message != "" {
			return Reply{}, fmt.Errorf("%s", e.Message)
		}
		return Reply{}, fmt.Errorf("gagal membaca balasan AI: %v", err)
	}
	reply.Text = strings.TrimSpace(reply.Text)
	if reply.Text == "" {
		return Reply{}, fmt.Errorf("balasan AI kosong")
	}
	return reply, nil
}

// runWithRetry mencoba tiap proxy pada DOLA_PROXY saat koneksi gagal.
func runWithRetry(ctx context.Context, sessionPath, convPath, stdin string, args ...string) (string, error) {
	proxies := proxyList()
	attempts := len(proxies)
	if attempts == 0 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		proxy := ""
		if len(proxies) > 0 {
			proxy = proxies[i%len(proxies)]
		}
		out, err := run(ctx, proxy, sessionPath, convPath, stdin, args...)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isTransportError(err.Error()) {
			return "", err
		}
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tidak ada proxy yang bisa dipakai")
	}
	if isTransportError(lastErr.Error()) {
		return "", fmt.Errorf("gagal menghubungi layanan AI lewat proxy (semua proxy tidak merespons). Coba lagi, atau perbarui env DOLA_PROXY")
	}
	return "", lastErr
}

// Reset memulai percakapan baru untuk satu chat (device tetap dipakai ulang).
func Reset(ctx context.Context, sessionPath, convPath string) error {
	_, err := runWithRetry(ctx, sessionPath, convPath, "", "--new")
	return err
}
