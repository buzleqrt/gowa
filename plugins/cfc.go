package main

import (
	"fmt"
	"strings"

	"gowa/internal/cfc"
	"gowa/internal/command"
	"gowa/internal/plugin"
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "cfc",
		Cmd:   "cfc",
		Categ: "plugin",
		Desc:  "Create fake chat (group history) - eksperimental",
		Alias: []string{"createfakechat", "fakechat"},
	}, handleCFC)
}

func handleCFC(ctx *command.Context) error {
	if !ctx.IsGroup {
		return ctx.Reply("Perintah ini hanya bisa di grup.")
	}
	if len(ctx.Args) < 2 {
		return ctx.Reply("Usage: .cfc <nomor_target> <teks>")
	}
	targetNum := strings.TrimPrefix(ctx.Args[0], "+")
	targetJIDStr := targetNum + "@s.whatsapp.net"

	text := strings.Join(ctx.Args[1:], " ")
	spec := cfc.Spec{
		Participant: "0@s.whatsapp.net",
		Timestamp:   0,
		Text:        text,
		FromMe:      false,
		PushName:    "Fake",
	}

	if err := cfc.Send(ctx.Ctx, ctx.Client, ctx.Chat.String(), targetJIDStr, []cfc.Spec{spec}); err != nil {
		return ctx.Reply(fmt.Sprintf("Gagal CFC: %v", err))
	}
	return ctx.Reply("Bundle group-history terkirim (eksperimental).")
}
