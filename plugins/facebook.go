package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/facebook"
	"gowa/internal/plugin"
)

const (
	facebookMaxMediaBytes = 64 << 20
	facebookFetchTimeout  = 60 * time.Second
	facebookCaptionLimit  = 900
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "facebook",
		Cmd:   "facebook",
		Categ: "download",
		Desc:  "Unduh video atau foto dari link Facebook",
		Alias: []string{"fb", "fbdl"},
	}, handleFacebook)
}

func handleFacebook(ctx *command.Context) error {
	link := facebook.ExtractURL(ctx.RawArgs)
	if link == "" {
		return ctx.Reply("*Usage:*\n`.fb <link>`\n\n_Contoh:_\n`.fb https://www.facebook.com/watch/?v=1234567890`\n`.fb https://fb.watch/xxxxxxx/`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, facebookFetchTimeout)
	defer cancel()

	md, err := facebook.Fetch(reqCtx, link)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil data Facebook:*\n%v", err))
	}

	dlCtx, dlCancel := context.WithTimeout(baseCtx, 10*time.Minute)
	defer dlCancel()

	caption := buildFacebookCaption(md)

	if md.Type == "video" {
		var lastErr error
		for _, mediaURL := range []string{md.VideoHD, md.VideoSD} {
			if mediaURL == "" {
				continue
			}
			data, err := facebook.DownloadBytes(dlCtx, mediaURL, facebookMaxMediaBytes)
			if err != nil {
				lastErr = err
				continue
			}
			return ctx.ReplyVideo(data, "video/mp4", caption)
		}
		return ctx.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", lastErr))
	}

	if len(md.Images) == 0 {
		return ctx.Reply("*Gagal:* tidak ada media publik yang bisa diunduh dari post ini.")
	}

	if len(md.Images) == 1 {
		data, err := facebook.DownloadBytes(dlCtx, md.Images[0], facebookMaxMediaBytes)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengunduh foto:*\n%v", err))
		}
		return ctx.ReplyImage(data, "image/jpeg", caption)
	}

	if err := ctx.Send(caption); err != nil {
		return err
	}

	for i, imageURL := range md.Images {
		data, err := facebook.DownloadBytes(dlCtx, imageURL, facebookMaxMediaBytes)
		if err != nil {
			return ctx.Send(fmt.Sprintf("*Gagal mengunduh foto %d/%d:*\n%v", i+1, len(md.Images), err))
		}
		if err := ctx.SendImage(data, "image/jpeg", ""); err != nil {
			return err
		}
	}
	return nil
}

func buildFacebookCaption(md *facebook.Metadata) string {
	var sb strings.Builder

	name := md.Fullname
	if name == "" {
		name = md.Username
	}
	if name == "" {
		name = "unknown"
	}
	sb.WriteString("*Facebook - " + name + "*")

	text := strings.TrimSpace(md.Caption)
	if text == "" {
		text = strings.TrimSpace(md.Title)
	}
	if len(text) > facebookCaptionLimit {
		text = strings.TrimSpace(text[:facebookCaptionLimit]) + "…"
	}
	if text != "" {
		sb.WriteString("\n" + text)
	}

	var stats []string
	if md.Likes > 0 {
		stats = append(stats, "❤️ "+formatFacebookCount(md.Likes))
	}
	if md.Comments > 0 {
		stats = append(stats, "💬 "+formatFacebookCount(md.Comments))
	}
	if len(md.Images) > 1 {
		stats = append(stats, fmt.Sprintf("🖼 %d foto", len(md.Images)))
	}
	if len(stats) > 0 {
		sb.WriteString("\n" + strings.Join(stats, " | "))
	}

	if md.Permalink != "" {
		sb.WriteString("\n🔗 " + md.Permalink)
	}

	return sb.String()
}

func formatFacebookCount(n int64) string {
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
