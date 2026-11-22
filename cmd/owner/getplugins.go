package owner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gowa/internal/command"
)

func init() {
	command.Register(&command.Command{
		Name:    "getplugins",
		Cmd:     "getplugins",
		Categ:   "owner",
		Desc:    "Ambil source plugin berdasarkan command atau alias (Khusus Owner)",
		Alias:   []string{"gp"},
		Execute: handleGetPlugins,
	})
}

const getPluginsUsage = "*Usage:*\n" +
	"`getplugins <command/alias>` _(kirim sebagai file)_\n" +
	"`getplugins -cat <command/alias>` _(tampilkan source)_"

func handleGetPlugins(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}

	args := ctx.Args
	showSource := false
	if len(args) > 0 && strings.EqualFold(args[0], "-cat") {
		showSource = true
		args = args[1:]
	}

	if len(args) == 0 {
		return ctx.Reply(getPluginsUsage)
	}

	key := args[0]
	cmd, ok := command.Find(key)
	if !ok {
		return ctx.Reply(fmt.Sprintf("Plugin dengan command/alias `%s` tidak ditemukan.", key))
	}

	if cmd.Source == "" {
		return ctx.Reply(fmt.Sprintf("`%s` adalah perintah bawaan bot, bukan plugin.", cmd.Cmd))
	}

	data, err := os.ReadFile(cmd.Source)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* gagal membaca source plugin: %v", err))
	}

	if showSource {
		source := string(data)
		if len(source) > maxFileOutput {
			source = source[:maxFileOutput] + "\n... (output dipotong)"
		}
		return ctx.Reply(fmt.Sprintf("```go\n%s\n```", strings.TrimSpace(source)))
	}

	return ctx.ReplyDocument(
		data,
		filepath.Base(cmd.Source),
		"text/plain",
		fmt.Sprintf("Source plugin `%s`", cmd.Cmd),
	)
}
