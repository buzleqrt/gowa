package main

import (
	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/sticker"
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "audiosticker",
		Cmd:   "audiosticker",
		Categ: "tools",
		Desc:  "Stiker bersuara dari voice note/audio/video yang di-reply, gambar bisa dari link",
		Alias: []string{"stickeraudio", "figurinhasom", "stikerbernyanyi"},
	}, func(ctx *command.Context) error {
		return sticker.Send(ctx)
	})
}
