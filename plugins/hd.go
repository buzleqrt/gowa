package main

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/hd"
	"gowa/internal/plugin"
)

const (
	hdDownloadTimeout = 2 * time.Minute
	hdMaxMediaBytes   = 64 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "hdmedia",
		Cmd:   "hd",
		Categ: "tools",
		Desc:  "Meningkatkan kualitas gambar (HD) dari reply atau URL",
		Alias: []string{"enhance", "hdimage", "remini"},
	}, handleHD)
}

func handleHD(ctx *command.Context) error {
	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	inputURL := ""
	isVideo := false
	for _, arg := range ctx.Args {
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			inputURL = arg
			isVideo = isVideoURL(arg)
			break
		}
	}

	var mediaBytes []byte
	if inputURL == "" {
		quoted := ctx.GetQuotedMessage()
		if quoted == nil {
			return ctx.Reply("*Usage:*\n`.hd` (reply gambar)\n`.hd <url gambar>`")
		}
		data, err := ctx.DownloadMedia(quoted)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengambil media yang di-reply:*\n%v", err))
		}
		if len(data) == 0 || len(data) > hdMaxMediaBytes {
			return ctx.Reply("*Gagal:* media terlalu besar atau kosong.")
		}
		if quoted.GetVideoMessage() != nil {
			return ctx.Reply("*Video HD belum didukung (gunakan gambar).*")
		}
		if quoted.GetImageMessage() == nil {
			return ctx.Reply("*Gagal:* pesan yang di-reply bukan gambar.")
		}
		mediaBytes = data

		upCtx, cancel := context.WithTimeout(baseCtx, hdDownloadTimeout)
		defer cancel()
		url, err := hd.UploadToCDN(upCtx, mediaBytes, "media.jpg")
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal upload ke CDN:*\n%v", err))
		}
		inputURL = url
	}

	if isVideo {
		return ctx.Reply("*Video HD belum didukung (gunakan gambar).*")
	}

	ctx.Typing()
	reqCtx, cancel := context.WithTimeout(baseCtx, 2*time.Minute)
	defer cancel()
	data, err := hd.EnhanceImage(reqCtx, inputURL)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal enhance gambar:*\n%v", err))
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}
	return ctx.ReplyImage(data, mimeType, "✨ *HD Image*")
}

func isVideoURL(raw string) bool {
	ext := strings.ToLower(path.Ext(raw))
	switch ext {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi":
		return true
	}
	return false
}