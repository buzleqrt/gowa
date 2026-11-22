package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/instagram"
	"gowa/internal/plugin"
)

const (
	instagramStalkTimeout    = 45 * time.Second
	instagramStalkAvatarSize = 8 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "igstalk",
		Cmd:   "igstalk",
		Categ: "stalk",
		Desc:  "Menampilkan informasi publik akun Instagram",
		Alias: []string{"igs", "iginfo"},
	}, handleInstagramStalk)
}

func handleInstagramStalk(ctx *command.Context) error {
	if len(ctx.Args) == 0 {
		return ctx.Reply("*Usage:*\n`.igstalk <username>`\n\n_Contoh:_\n`.igstalk nasa`\n`.igstalk https://www.instagram.com/nasa`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, instagramStalkTimeout)
	defer cancel()

	profile, err := instagram.FetchProfile(reqCtx, ctx.Args[0])
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil profil Instagram:*\n%v", err))
	}

	caption := instagram.FormatProfile(profile)
	if profile.Avatar != "" {
		if avatar, err := instagram.DownloadBytes(reqCtx, profile.Avatar, instagramStalkAvatarSize); err == nil && len(avatar) > 0 {
			mimeType := http.DetectContentType(avatar)
			if !strings.HasPrefix(mimeType, "image/") {
				mimeType = "image/jpeg"
			}
			return ctx.ReplyImage(avatar, mimeType, caption)
		}
	}

	return ctx.Reply(caption)
}
