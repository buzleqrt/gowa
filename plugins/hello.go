package main

import (
	"fmt"
	"strings"

	"gowa/internal/command"
	"gowa/internal/plugin"
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "hello",
		Cmd:   "hello",
		Categ: "plugin",
		Desc:  "Contoh plugin: menyapa pengirim atau nama yang ditulis",
		Alias: []string{"hi", "halo"},
	}, handleHello)
}

func handleHello(ctx *command.Context) error {
	target := "kak"
	if len(ctx.Args) > 0 {
		target = strings.Join(ctx.Args, " ")
	}
	return ctx.Reply(fmt.Sprintf("Halo %s! Perintah ini berasal dari plugin %s.", target, "hello.go"))
}
