package main

import (
	"context"
	"fmt"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/xscraper"
)

const xFetchTimeout = 3 * time.Minute

func init() {
	plugin.Register(plugin.Info{
		Name:  "xdownload",
		Cmd:   "x",
		Categ: "download",
		Desc:  "Mengunduh video atau foto dari link X (Twitter)",
		Alias: []string{"xdl", "twitter", "twdl"},
	}, handleXDownload)
}

func handleXDownload(ctx *command.Context) error {
	link, mediaType := xscraper.ParseArgs(ctx.Args)
	if link == "" {
		return ctx.Reply("*Usage:*\n`.x <link>`\n`.x video <link>` atau `.x image <link>`\n\n_Contoh:_ `.x https://x.com/NASA/status/2042533098567422255`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, xFetchTimeout)
	defer cancel()

	result, err := xscraper.Fetch(reqCtx, link, mediaType)
	if err != nil {
		return ctx.Reply("*Gagal mengambil media X:*\n" + err.Error())
	}
	if len(result.Media) == 0 {
		return ctx.Reply("*Gagal:* tidak ada media publik yang bisa diunduh dari post ini.")
	}

	caption := xscraper.FormatCaption(result.Metadata)
	total := len(result.Media)

	for i, media := range result.Media {
		itemCaption := ""
		if i == 0 {
			itemCaption = caption
		}

		if i == 0 {
			if media.Video {
				if err := ctx.ReplyVideo(media.Data, media.MIME, itemCaption); err != nil {
					return err
				}
			} else if err := ctx.ReplyImage(media.Data, media.MIME, itemCaption); err != nil {
				return err
			}
			continue
		}

		if media.Video {
			if err := ctx.SendVideo(media.Data, media.MIME, itemCaption); err != nil {
				return fmt.Errorf("media %d/%d: %w", i+1, total, err)
			}
		} else if err := ctx.SendImage(media.Data, media.MIME, itemCaption); err != nil {
			return fmt.Errorf("media %d/%d: %w", i+1, total, err)
		}
	}

	return nil
}
