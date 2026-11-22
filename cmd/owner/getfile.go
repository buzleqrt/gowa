package owner

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"gowa/internal/command"
)

const maxGetFileSize = 64 << 20

func init() {
	command.Register(&command.Command{
		Name:    "getfile",
		Cmd:     "getfile",
		Categ:   "owner",
		Desc:    "Kirim file dari server sebagai dokumen (Khusus Owner)",
		Alias:   []string{"gf", "ambilfile", "sendfile"},
		Execute: handleGetFile,
	})
}

func handleGetFile(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}

	_, path, _ := parseFileArgs(ctx.RawArgs)
	if path == "" {
		return ctx.Reply("*Usage:* `getfile <path file>`")
	}

	abs, err := resolveFilePath(path)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return ctx.Reply(fmt.Sprintf("*Error:* file tidak ditemukan: `%s`", path))
		}
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	if info.IsDir() {
		return ctx.Reply(fmt.Sprintf("*Error:* `%s` adalah direktori, bukan file.", abs))
	}

	if info.Size() > maxGetFileSize {
		return ctx.Reply(fmt.Sprintf(
			"*Error:* file terlalu besar (%s), batas %s.",
			fileSize(info.Size()),
			fileSize(maxGetFileSize),
		))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	return ctx.ReplyDocument(data, filepath.Base(abs), detectFileMime(abs), "")
}

func detectFileMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".md":
		return "text/markdown"
	case ".yml", ".yaml":
		return "text/yaml"
	case ".go", ".txt", ".log", ".env", ".sql":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".js", ".mjs", ".cjs":
		return "application/javascript"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".csv":
		return "text/csv"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	}

	if detected := mime.TypeByExtension(ext); detected != "" {
		return detected
	}

	return "application/octet-stream"
}
