package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/soundcloud"
)

const (
	soundcloudTimeout       = 5 * time.Minute
	soundcloudSearchLimit   = 3
	soundcloudMaxAudioBytes = 64 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "soundcloudsearch",
		Cmd:   "scsearch",
		Categ: "music",
		Desc:  "Mencari lagu di SoundCloud lalu mengirim audionya",
		Alias: []string{"sc", "scs", "soundcloud"},
	}, handleSoundCloudSearch)
}

func handleSoundCloudSearch(ctx *command.Context) error {
	query := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if query == "" {
		return ctx.Reply("*Usage:*\n`.sc <judul lagu>`\n\n_Contoh:_\n`.sc lathi`\n`.sc we don't talk anymore`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, soundcloudTimeout)
	defer cancel()

	result, err := soundcloud.Search(reqCtx, query, soundcloudSearchLimit)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mencari di SoundCloud:*\n%v", err))
	}
	if len(result.Tracks) == 0 {
		return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada lagu publik yang cocok dengan _%s_", query))
	}

	var lastErr error
	for _, track := range result.Tracks {
		audio, err := soundcloud.FetchAudio(reqCtx, track.ID, soundcloudMaxAudioBytes)
		if err != nil {
			lastErr = err
			continue
		}

		if err := ctx.ReplyAudio(audio.Data, audio.MimeType, false); err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengirim audio:*\n%v", err))
		}
		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("tidak ada track yang bisa diunduh")
	}
	return ctx.Reply(fmt.Sprintf("*Gagal mengambil audio:*\n%v", lastErr))
}
