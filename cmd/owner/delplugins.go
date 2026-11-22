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
		Name:    "delplugins",
		Cmd:     "delplugins",
		Categ:   "owner",
		Desc:    "Hapus plugin berdasarkan command atau alias (Khusus Owner)",
		Alias:   []string{"delplugin", "deleteplugin", "removeplugin"},
		Execute: handleDelPlugins,
	})
}

const delPluginsUsage = "*Usage:* `delplugins <command/alias>`"

func handleDelPlugins(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}

	if len(ctx.Args) == 0 {
		return ctx.Reply(delPluginsUsage)
	}

	key := ctx.Args[0]
	target, ok := command.Find(key)
	if !ok {
		return ctx.Reply(fmt.Sprintf("Plugin dengan command/alias `%s` tidak ditemukan.", key))
	}

	if target.Source == "" {
		return ctx.Reply(fmt.Sprintf("`%s` adalah perintah bawaan bot, tidak bisa dihapus.", target.Cmd))
	}

	dir := ctx.Config.PluginDir
	if strings.TrimSpace(dir) == "" {
		dir = "plugins"
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	if !pathContains(absDir, target.Source) {
		return ctx.Reply("*Error:* berkas plugin berada di luar direktori plugins, dihapus manual saja.")
	}

	var removed []string
	for _, cmd := range command.GetAll() {
		if cmd.Source == target.Source {
			if _, ok := command.Unregister(cmd.Cmd); ok {
				removed = append(removed, cmd.Cmd)
			}
		}
	}

	if err := os.Remove(target.Source); err != nil && !os.IsNotExist(err) {
		return ctx.Reply(fmt.Sprintf("*Error:* gagal menghapus file: %v", err))
	}

	if len(removed) == 0 {
		return ctx.Reply(fmt.Sprintf("_File dihapus:_ `%s`", target.Source))
	}

	return ctx.Reply(fmt.Sprintf(
		"_Plugin dihapus:_ `%s`\n_Perintah dihapus:_ %s",
		target.Source,
		"`"+strings.Join(removed, "`, `")+"`",
	))
}
