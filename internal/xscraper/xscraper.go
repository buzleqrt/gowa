// Package xscraper menjalankan CLI x-scraper-node (Node.js) untuk mengunduh
// media dari post X (Twitter), lalu membaca hasilnya sebagai byte siap kirim.
package xscraper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxMediaBytes = 64 << 20

type Author struct {
	ScreenName string
	Name       string
}

type Metadata struct {
	ID          string
	URL         string
	Text        string
	Author      Author
	Likes       int64
	Retweets    int64
	CreatedAt   string
	CreatedUnix int64
}

type Media struct {
	Data  []byte
	MIME  string
	Video bool
}

type Result struct {
	Metadata *Metadata
	Media    []Media
}

type metadataJSON struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Author      author `json:"author"`
	Likes       int64  `json:"likes"`
	Retweets    int64  `json:"retweets"`
	CreatedAt   string `json:"created_at"`
	CreatedUnix int64  `json:"created_timestamp"`
}

type author struct {
	ScreenName string `json:"screen_name"`
	Name       string `json:"name"`
}

// ParseArgs memisahkan link dari kata kunci filter media ("video"/"image")
// pada argumen perintah.
func ParseArgs(args []string) (link, mediaType string) {
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "video":
			mediaType = "video"
			continue
		case "image", "gambar", "foto":
			mediaType = "image"
			continue
		}
		if link == "" && looksLikeLink(arg) {
			link = arg
		}
	}
	if link == "" && len(args) > 0 {
		link = args[0]
	}
	return link, mediaType
}

// ExtractLink mengambil link X/Twitter dari teks bebas.
func ExtractLink(args []string) string {
	link, _ := ParseArgs(args)
	return link
}

func looksLikeLink(arg string) bool {
	lower := strings.ToLower(arg)
	if strings.Contains(lower, "x.com/") || strings.Contains(lower, "twitter.com/") {
		return true
	}
	if strings.Contains(lower, "/status/") || strings.Contains(lower, "/statuses/") {
		return true
	}
	if _, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return len(arg) >= 5
	}
	return false
}

// Fetch menjalankan scraper pada folder sementara dan mengembalikan media
// yang berhasil diunduh beserta metadatanya.
func Fetch(ctx context.Context, link, mediaType string) (*Result, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, fmt.Errorf("link X kosong")
	}

	script, err := scraperPath()
	if err != nil {
		return nil, err
	}

	node := strings.TrimSpace(os.Getenv("X_SCRAPER_NODE"))
	if node == "" {
		node = "node"
	}
	if _, err := exec.LookPath(node); err != nil {
		return nil, fmt.Errorf("Node.js tidak ditemukan; pasang Node.js atau set X_SCRAPER_NODE")
	}

	workDir, err := os.MkdirTemp("", "gowa-x-")
	if err != nil {
		return nil, fmt.Errorf("gagal membuat folder sementara: %w", err)
	}
	defer os.RemoveAll(workDir)

	args := []string{script, link, "-o", workDir, "-q"}
	if mediaType != "" {
		args = append(args, "--media", mediaType)
	}

	cmd := exec.CommandContext(ctx, node, args...)
	out, runErr := cmd.CombinedOutput()

	paths, meta := collect(workDir)
	if len(paths) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("%s", tailOutput(out, runErr))
		}
		return nil, fmt.Errorf("tidak ada media publik yang bisa diunduh dari post ini")
	}

	result := &Result{Metadata: meta}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > MaxMediaBytes {
			return nil, fmt.Errorf("media %s melebihi batas 64 MB", filepath.Base(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("gagal membaca %s: %w", filepath.Base(path), err)
		}
		if len(data) == 0 {
			continue
		}
		media := Media{Data: data, MIME: imageMime(filepath.Ext(path))}
		if isVideoExt(filepath.Ext(path)) {
			media.Video = true
			media.MIME = "video/mp4"
		}
		result.Media = append(result.Media, media)
	}

	if len(result.Media) == 0 {
		return nil, fmt.Errorf("tidak ada media publik yang bisa diunduh dari post ini")
	}

	return result, nil
}

func scraperPath() (string, error) {
	var candidates []string
	if dir := strings.TrimSpace(os.Getenv("X_SCRAPER_DIR")); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "scraper.js"), dir)
	}
	candidates = append(candidates,
		filepath.Join("x-scraper-node", "scraper.js"),
		filepath.Join("..", "x-scraper-node", "scraper.js"),
	)

	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err == nil && !info.IsDir() {
			return abs, nil
		}
	}

	return "", fmt.Errorf("berkas scraper X tidak ditemukan; letakkan folder x-scraper-node di samping bot atau set X_SCRAPER_DIR")
}

func collect(dir string) ([]string, *Metadata) {
	var paths []string
	var meta *Metadata

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if isVideoExt(ext) || isImageExt(ext) {
			paths = append(paths, path)
			return nil
		}
		if !strings.EqualFold(ext, ".json") || !strings.EqualFold(filepath.Base(path), "metadata.json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var parsed metadataJSON
		if json.Unmarshal(data, &parsed) != nil {
			return nil
		}
		meta = &Metadata{
			ID:          parsed.ID,
			URL:         parsed.URL,
			Text:        parsed.Text,
			Author:      Author{ScreenName: parsed.Author.ScreenName, Name: parsed.Author.Name},
			Likes:       parsed.Likes,
			Retweets:    parsed.Retweets,
			CreatedAt:   parsed.CreatedAt,
			CreatedUnix: parsed.CreatedUnix,
		}
		return nil
	})

	sort.Slice(paths, func(i, j int) bool {
		a, b := mediaIndex(filepath.Base(paths[i])), mediaIndex(filepath.Base(paths[j]))
		if a != b {
			return a < b
		}
		return paths[i] < paths[j]
	})

	return paths, meta
}

func mediaIndex(name string) int {
	digits := 0
	for digits < len(name) && name[digits] >= '0' && name[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 1 << 30
	}
	n, err := strconv.Atoi(name[:digits])
	if err != nil {
		return 1 << 30
	}
	return n
}

func isVideoExt(ext string) bool {
	ext = strings.ToLower(ext)
	return ext == ".mp4" || ext == ".mov"
}

func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

func imageMime(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	}
	return "image/jpeg"
}

// FormatCaption membangun keterangan pesan dari metadata post.
func FormatCaption(meta *Metadata) string {
	if meta == nil {
		return "*X (Twitter)*"
	}

	var sb strings.Builder

	user := meta.Author.ScreenName
	if user == "" {
		user = "unknown"
	}
	sb.WriteString("*X - @" + user + "*")

	text := strings.TrimSpace(meta.Text)
	if len(text) > 900 {
		text = strings.TrimSpace(text[:900]) + "…"
	}
	if text != "" {
		sb.WriteString("\n" + text)
	}

	var stats []string
	if meta.Likes > 0 {
		stats = append(stats, "❤️ "+formatCount(meta.Likes))
	}
	if meta.Retweets > 0 {
		stats = append(stats, "🔁 "+formatCount(meta.Retweets))
	}
	if len(stats) > 0 {
		sb.WriteString("\n" + strings.Join(stats, " | "))
	}

	if date := formatDate(meta); date != "" {
		sb.WriteString("\n📅 " + date)
	}

	if meta.URL != "" {
		sb.WriteString("\n🔗 " + meta.URL)
	}

	return sb.String()
}

func formatDate(meta *Metadata) string {
	if meta.CreatedUnix > 0 {
		return time.Unix(meta.CreatedUnix, 0).UTC().Format("2006-01-02")
	}
	created := strings.TrimSpace(meta.CreatedAt)
	if created == "" {
		return ""
	}
	if parsed, err := time.Parse("Mon Jan 02 15:04:05 -0700 2006", created); err == nil {
		return parsed.Format("2006-01-02")
	}
	fields := strings.Fields(created)
	if len(fields) >= 6 {
		return fields[1] + " " + fields[2] + " " + fields[5]
	}
	return created
}

func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func tailOutput(out []byte, runErr error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < 3; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			kept = append([]string{line}, kept...)
		}
	}
	if len(kept) == 0 {
		return "gagal menjalankan scraper X: " + runErr.Error()
	}
	return "gagal menjalankan scraper X:\n" + strings.Join(kept, "\n")
}
