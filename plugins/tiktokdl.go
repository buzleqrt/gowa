package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/tiktok"
)

const (
	tiktokMaxMediaBytes = 64 << 20
	tiktokFetchTimeout  = 90 * time.Second
	tiktokTitleLimit    = 280
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "tiktok",
		Cmd:   "tiktok",
		Categ: "download",
		Desc:  "Unduh video, foto, atau audio dari link TikTok",
		Alias: []string{"tt", "ttdl"},
	}, handleTikTok)
}

func handleTikTok(ctx *command.Context) error {
	audioOnly, link := parseTikTokArgs(ctx.Args)
	if link == "" {
		return ctx.Reply("*Usage:*\n`.tiktok <link>`\n`.tiktok audio <link>`\n\n_Contoh:_ `.tiktok https://www.tiktok.com/@user/video/1234567890123456789`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, tiktokFetchTimeout)
	defer cancel()

	md, err := tiktok.Fetch(reqCtx, link)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil data TikTok:*\n%v", err))
	}

	dlCtx, dlCancel := context.WithTimeout(baseCtx, 5*time.Minute)
	defer dlCancel()

	if audioOnly {
		if md.AudioURL == "" {
			return ctx.Reply("*Gagal:* audio publik tidak tersedia untuk post ini.")
		}
		data, err := tiktok.DownloadBytes(dlCtx, md.AudioURL, tiktokMaxMediaBytes)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengunduh audio:*\n%v", err))
		}
		return ctx.ReplyAudio(data, "audio/mpeg", false)
	}

	if md.VideoURL != "" {
		data, err := tiktok.DownloadBytes(dlCtx, md.VideoURL, tiktokMaxMediaBytes)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
		}
		return ctx.ReplyVideo(data, "video/mp4", buildTikTokCaption(md))
	}

	if len(md.Photos) > 0 {
		if err := ctx.Send(buildTikTokCaption(md)); err != nil {
			return err
		}
		for i, photo := range md.Photos {
			data, err := tiktok.DownloadBytes(dlCtx, photo, tiktokMaxMediaBytes)
			if err != nil {
				return ctx.Send(fmt.Sprintf("*Gagal mengunduh foto %d/%d:*\n%v", i+1, len(md.Photos), err))
			}
			if err := ctx.SendImage(data, "image/jpeg", ""); err != nil {
				return err
			}
		}
		return nil
	}

	return ctx.Reply("*Gagal:* tidak ada media publik yang bisa diunduh dari post ini.")
}

func parseTikTokArgs(args []string) (audioOnly bool, link string) {
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "audio", "-a", "--audio", "mp3":
			audioOnly = true
			continue
		}
		if link == "" && (strings.Contains(arg, "tiktok.com") || strings.HasPrefix(arg, "http") || tiktok.ExtractID(arg) != "") {
			link = arg
		}
	}
	if link == "" && len(args) > 0 {
		link = args[0]
	}
	return audioOnly, link
}

func buildTikTokCaption(md *tiktok.Metadata) string {
	var sb strings.Builder

	author := md.Author
	if author == "" {
		author = "unknown"
	}
	sb.WriteString("*TikTok - @" + author + "*")

	title := strings.TrimSpace(strings.Split(md.Title, "\n")[0])
	if len(title) > tiktokTitleLimit {
		title = strings.TrimSpace(title[:tiktokTitleLimit]) + "…"
	}
	if title != "" {
		sb.WriteString("\n" + title)
	}

	var details []string
	if md.Duration > 0 {
		details = append(details, "⏱ "+md.FormattedDuration)
	}
	if md.Resolution != "" {
		details = append(details, "🎞 "+md.Resolution)
	}
	if len(md.Photos) > 0 {
		details = append(details, fmt.Sprintf("🖼 %d foto", len(md.Photos)))
	}
	if len(details) > 0 {
		sb.WriteString("\n" + strings.Join(details, " | "))
	}

	var stats []string
	if md.Views > 0 {
		stats = append(stats, "👁 "+formatTikTokCount(md.Views))
	}
	if md.Likes > 0 {
		stats = append(stats, "❤️ "+formatTikTokCount(md.Likes))
	}
	if md.Comments > 0 {
		stats = append(stats, "💬 "+formatTikTokCount(md.Comments))
	}
	if len(stats) > 0 {
		sb.WriteString("\n" + strings.Join(stats, " | "))
	}

	return sb.String()
}

func formatTikTokCount(n int64) string {
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
