package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/instagram"
	"gowa/internal/plugin"
)

const (
	instagramMaxMediaBytes = 64 << 20
	instagramFetchTimeout  = 60 * time.Second
	instagramCaptionLimit  = 900
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "instagram",
		Cmd:   "ig",
		Categ: "download",
		Desc:  "Unduh video atau foto dari link Instagram",
		Alias: []string{"igdl", "instagram"},
	}, handleInstagram)
}

func handleInstagram(ctx *command.Context) error {
	link := parseInstagramLink(ctx.Args)
	if link == "" {
		return ctx.Reply("*Usage:*\n`.ig <link>`\n\n_Contoh:_\n`.ig https://www.instagram.com/reel/DfsVTzKsaab/`\n`.ig https://www.instagram.com/p/DfqL5_dlijm/`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, instagramFetchTimeout)
	defer cancel()

	md, err := instagram.Fetch(reqCtx, link)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil data Instagram:*\n%v", err))
	}
	if len(md.Items) == 0 {
		return ctx.Reply("*Gagal:* tidak ada media publik yang bisa diunduh dari post ini.")
	}

	dlCtx, dlCancel := context.WithTimeout(baseCtx, 5*time.Minute)
	defer dlCancel()

	caption := buildInstagramCaption(md)

	if len(md.Items) == 1 && md.Items[0].Video {
		data, err := instagram.DownloadMedia(dlCtx, md.Items[0], instagramMaxMediaBytes)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
		}
		return ctx.ReplyVideo(data, "video/mp4", caption)
	}

	total := len(md.Items)
	captionSent := false
	for i, item := range md.Items {
		data, err := instagram.DownloadMedia(dlCtx, item, instagramMaxMediaBytes)
		if err != nil {
			return ctx.Send(fmt.Sprintf("*Gagal mengunduh media %d/%d:*\n%v", i+1, total, err))
		}

		itemCaption := ""
		if !captionSent {
			itemCaption = caption
			captionSent = true
		}

		if item.Video {
			if err := ctx.ReplyVideo(data, "video/mp4", itemCaption); err != nil {
				return err
			}
			continue
		}
		if err := ctx.SendImage(data, "image/jpeg", itemCaption); err != nil {
			return err
		}
	}
	return nil
}

func parseInstagramLink(args []string) string {
	for _, arg := range args {
		if strings.Contains(arg, "instagram.com") || strings.Contains(arg, "instagr.am") ||
			instagram.ExtractCode(arg) != "" {
			return arg
		}
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func buildInstagramCaption(md *instagram.Metadata) string {
	var sb strings.Builder

	author := md.Author
	if author == "" {
		author = "unknown"
	}
	sb.WriteString("*Instagram - @" + author + "*")

	text := strings.TrimSpace(md.Caption)
	if len(text) > instagramCaptionLimit {
		text = strings.TrimSpace(text[:instagramCaptionLimit]) + "…"
	}
	if text != "" {
		sb.WriteString("\n" + text)
	}

	var details []string
	if md.TakenAt > 0 {
		details = append(details, "📅 "+strings.Split(md.UploadDate, " ")[0])
	}
	if len(md.Items) > 1 {
		details = append(details, fmt.Sprintf("🖼 %d media", len(md.Items)))
	}
	if len(details) > 0 {
		sb.WriteString("\n" + strings.Join(details, " | "))
	}

	var stats []string
	if md.Likes > 0 {
		stats = append(stats, "❤️ "+formatInstagramCount(md.Likes))
	}
	if md.Comments > 0 {
		stats = append(stats, "💬 "+formatInstagramCount(md.Comments))
	}
	if len(stats) > 0 {
		sb.WriteString("\n" + strings.Join(stats, " | "))
	}

	return sb.String()
}

func formatInstagramCount(n int64) string {
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
