package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/ytmusic"
)

const (
	ytVideoTimeout  = 5 * time.Minute
	ytVideoMaxBytes = 15 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "ytmp4",
		Cmd:   "yt",
		Categ: "downloader",
		Desc:  "Unduh video YouTube dari tautan atau ID",
		Alias: []string{"ytmp4", "ytdl", "ytvideo"},
	}, handleYouTubeVideo)
}

func handleYouTubeVideo(ctx *command.Context) error {
	arg := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if arg == "" {
		return ctx.Reply("*Usage:*\n`.yt <tautan YouTube>`\n\n_Contoh:_\n`.yt https://youtu.be/3RZ6Eir-QyI`")
	}

	videoID, err := ytmusic.ParseVideoID(arg)
	if err != nil {
		return ctx.Reply("*Tautan YouTube tidak valid.*\nKirim tautan video, Shorts, atau ID 11 karakter.")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, ytVideoTimeout)
	defer cancel()

	video, err := ytmusic.FetchVideo(reqCtx, videoID, ytVideoMaxBytes)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
	}

	if err := ctx.ReplyVideo(video.Data, video.MimeType, buildYouTubeVideoCaption(video)); err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengirim video:*\n%v", err))
	}
	return nil
}

func buildYouTubeVideoCaption(video *ytmusic.Video) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%s*", truncateYouTubeText(video.Title, 120))

	var meta []string
	if video.Author != "" {
		meta = append(meta, "👤 "+video.Author)
	}
	if video.Duration > 0 {
		meta = append(meta, "⏱ "+formatYouTubeDuration(video.Duration))
	}
	if len(meta) > 0 {
		sb.WriteString("\n" + strings.Join(meta, " | "))
	}

	sb.WriteString(fmt.Sprintf("\n📦 %.1f MB", float64(len(video.Data))/(1<<20)))
	return sb.String()
}

func truncateYouTubeText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}

func formatYouTubeDuration(seconds int) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}
