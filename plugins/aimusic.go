package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/aimusic"
	"gowa/internal/command"
	"gowa/internal/plugin"
)

const (
	aimusicTimeout       = 6 * time.Minute
	aimusicMaxAudioBytes = 64 << 20
	aimusicLyricsLimit   = 1500
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "aimusic",
		Cmd:   "aimusic",
		Categ: "music",
		Desc:  "Generate lagu AI dari deskripsi, lalu kirim audionya",
		Alias: []string{"aimusik", "aimusicgen"},
	}, handleAIMusic)
}

func handleAIMusic(ctx *command.Context) error {
	desc := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if desc == "" {
		return ctx.Reply("*Usage:*\n`.aimusic <deskripsi lagu>`\n\n_Contoh:_\n`.aimusic lagu pop ceria tentang coding`\n`.aimusic sad piano song about rain`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, aimusicTimeout)
	defer cancel()

	song, err := aimusic.Generate(reqCtx, desc)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal generate lagu:*\n%v", err))
	}

	audio, err := aimusic.Download(reqCtx, song.AudioURL, aimusicMaxAudioBytes)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Lagu berhasil dibuat, tapi gagal mengunduh audio:*\n%v\n%s", err, song.AudioURL))
	}

	if err := ctx.ReplyAudio(audio, "audio/mpeg", false); err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengirim audio:*\n%v", err))
	}

	var sb strings.Builder
	if song.Title != "" {
		sb.WriteString("*" + song.Title + "*\n")
	}
	if song.Duration > 0 {
		sb.WriteString(fmt.Sprintf("⏱ %.0f detik\n", song.Duration))
	}
	lyrics := strings.TrimSpace(song.Lyrics)
	if len(lyrics) > aimusicLyricsLimit {
		lyrics = strings.TrimSpace(lyrics[:aimusicLyricsLimit]) + "…"
	}
	if lyrics != "" {
		sb.WriteString("\n" + lyrics)
	}
	if sb.Len() > 0 {
		_ = ctx.Send(sb.String())
	}
	return nil
}
