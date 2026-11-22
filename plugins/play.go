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
	playTimeout       = 4 * time.Minute
	playMaxAudioBytes = 64 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "play",
		Cmd:   "play",
		Categ: "music",
		Desc:  "Cari lagu di YouTube Music lalu kirim audionya",
		Alias: []string{"music", "song", "putar"},
	}, handlePlay)
}

func handlePlay(ctx *command.Context) error {
	query := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if query == "" {
		return ctx.Reply("*Usage:*\n`.play <judul lagu>`\n\n_Contoh:_\n`.play lathi`\n`.play ed sheeran perfect`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, playTimeout)
	defer cancel()

	tracks, err := ytmusic.Search(reqCtx, query, 1)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mencari lagu:*\n%v", err))
	}

	audio, err := ytmusic.FetchAudio(reqCtx, tracks[0].VideoID, playMaxAudioBytes)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil audio:*\n%v", err))
	}

	if err := ctx.ReplyAudio(audio.Data, audio.MimeType, false); err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengirim audio:*\n%v", err))
	}
	return nil
}
