package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/tiktok"
)

const (
	tiktokStalkTimeout    = 45 * time.Second
	tiktokStalkAvatarSize = 8 << 20
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "tiktokstalk",
		Cmd:   "ttstalk",
		Categ: "stalk",
		Desc:  "Menampilkan informasi publik akun TikTok",
		Alias: []string{"stalktt", "ttinfo"},
	}, handleTikTokStalk)
}

func handleTikTokStalk(ctx *command.Context) error {
	if len(ctx.Args) == 0 {
		return ctx.Reply("*Usage:*\n`.ttstalk <username>`\n\n_Contoh:_\n`.ttstalk sanzyverse`\n`.ttstalk https://www.tiktok.com/@sanzyverse`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, tiktokStalkTimeout)
	defer cancel()

	profile, err := tiktok.FetchProfile(reqCtx, ctx.Args[0])
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil profil TikTok:*\n%v", err))
	}

	caption := tiktok.FormatProfile(profile)
	if profile.Avatar != "" {
		if avatar, err := tiktok.DownloadBytes(reqCtx, profile.Avatar, tiktokStalkAvatarSize); err == nil && len(avatar) > 0 {
			mimeType := http.DetectContentType(avatar)
			if !strings.HasPrefix(mimeType, "image/") {
				mimeType = "image/jpeg"
			}
			return ctx.ReplyImage(avatar, mimeType, caption)
		}
	}

	return ctx.Reply(caption)
}
