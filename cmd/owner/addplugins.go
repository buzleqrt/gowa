package owner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gowa/internal/command"
	"gowa/internal/plugin"
)

func init() {
	command.Register(&command.Command{
		Name:    "addplugins",
		Cmd:     "addplugins",
		Categ:   "owner",
		Desc:    "Simpan source plugin baru ke direktori plugins (Khusus Owner)",
		Alias:   []string{"addplugin", "newplugin"},
		Execute: handleAddPlugins,
	})
}

const addPluginsUsage = "*Usage:* reply source plugin (teks/dokumen) lalu:\n" +
	"`addplugins <nama.go>`\n\n" +
	"_Plugin langsung dimuat setelah tersimpan._"

func handleAddPlugins(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}

	name, err := pluginFileName(ctx.RawArgs)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v\n\n%s", err, addPluginsUsage))
	}

	source, err := quotedPluginSource(ctx)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v\n\n%s", err, addPluginsUsage))
	}

	source = stripFileFence(source)
	if source == "" {
		return ctx.Reply("*Error:* source plugin kosong.")
	}
	if !strings.Contains(source, "package main") {
		return ctx.Reply("*Error:* source plugin harus diawali `package main`.")
	}
	if !strings.Contains(source, "plugin.Register(") {
		return ctx.Reply("*Error:* source plugin tidak memanggil `plugin.Register` di `init()`.")
	}

	dir := ctx.Config.PluginDir
	if strings.TrimSpace(dir) == "" {
		dir = "plugins"
	}
	target := filepath.Join(dir, name)

	if _, err := os.Stat(target); err == nil {
		return ctx.Reply(fmt.Sprintf("*Error:* `%s` sudah ada. Hapus dulu dengan `.delplugins`.", target))
	} else if !os.IsNotExist(err) {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	if err := os.WriteFile(target, []byte(source), 0644); err != nil {
		return ctx.Reply(fmt.Sprintf("*Error:* %v", err))
	}

	loaded, err := plugin.LoadFile(target)
	if err != nil {
		_ = os.Remove(target)
		return ctx.Reply(fmt.Sprintf("*Error:* plugin gagal dimuat: %v", err))
	}

	return ctx.Reply(fmt.Sprintf(
		"_Plugin ditambahkan:_ `%s`\n_%d perintah terdaftar._",
		target,
		loaded,
	))
}

func pluginFileName(args string) (string, error) {
	name := strings.TrimSpace(args)
	name = strings.Trim(name, "`\"'")
	if name == "" {
		return "", errors.New("tentukan nama file, contoh: `addplugins tes.go`")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", errors.New("nama file harus berada langsung di direktori plugins")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".go") {
		name += ".go"
	}
	return name, nil
}

func quotedPluginSource(ctx *command.Context) (string, error) {
	quoted := ctx.GetQuotedMessage()
	if quoted == nil {
		if ctx.Event != nil && ctx.Event.Message != nil {
			if doc := command.UnwrapMessage(ctx.Event.Message).GetDocumentMessage(); doc != nil {
				data, err := ctx.DownloadMedia(ctx.Event.Message)
				if err != nil {
					return "", fmt.Errorf("gagal mengunduh dokumen: %w", err)
				}
				return string(data), nil
			}
		}
		return "", errors.New("reply pesan berisi source plugin (teks atau dokumen)")
	}

	if doc := quoted.GetDocumentMessage(); doc != nil {
		data, err := ctx.DownloadMedia(quoted)
		if err != nil {
			return "", fmt.Errorf("gagal mengunduh dokumen: %w", err)
		}
		return string(data), nil
	}

	if text := quoted.GetConversation(); text != "" {
		return text, nil
	}
	if ext := quoted.GetExtendedTextMessage(); ext != nil {
		return ext.GetText(), nil
	}

	return "", errors.New("pesan yang di-reply bukan teks atau dokumen")
}
