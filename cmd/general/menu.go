package general

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/storage"
	"gowa/internal/thumb"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
)

const menuURL = "https://wa.me/6283175676190"

func init() {
	command.Register(&command.Command{
		Name:    "menu",
		Cmd:     "menu",
		Categ:   "general",
		Desc:    "Menampilkan daftar perintah bot",
		Alias:   []string{"help"},
		Execute: handleMenu,
	})
}

func handleMenu(ctx *command.Context) error {
	categories := command.GetCategories()

	var sb strings.Builder
	botTitle := "GOWA"
	if ctx.Config != nil && ctx.Config.BotName != "" {
		botTitle = strings.ToUpper(ctx.Config.BotName)
	}
	sb.WriteString(fmt.Sprintf("*%s - Command List*\n\n", botTitle))

	for i, cat := range categories {
		catTitle := strings.ToUpper(cat[:1]) + strings.ToLower(cat[1:])
		sb.WriteString(fmt.Sprintf("*%s*\n", catTitle))
		cmds := command.GetByCategory(cat)
		for _, cmd := range cmds {
			firstAlias := ""
			if !strings.Contains(cmd.Cmd, "/") && len(cmd.Alias) > 0 {
				for _, a := range cmd.Alias {
					clean := strings.TrimPrefix(a, ".")
					if !strings.EqualFold(clean, cmd.Cmd) {
						firstAlias = fmt.Sprintf(" _(%s%s)_", ctx.BotPrefix, clean)
						break
					}
				}
			}

			if strings.HasPrefix(cmd.Cmd, "$") {
				sb.WriteString(fmt.Sprintf("• `%s <cmd>`%s\n", cmd.Cmd, firstAlias))
			} else {
				sb.WriteString(fmt.Sprintf("• `%s%s`%s\n", ctx.BotPrefix, cmd.Cmd, firstAlias))
			}
		}
		if i < len(categories)-1 {
			sb.WriteString("\n")
		}
	}

	text := strings.TrimSpace(sb.String())

	if preview := buildMenuPreview(ctx, botTitle, text); preview != nil {
		if err := ctx.SendMessage(preview); err == nil {
			return nil
		}
	}

	return ctx.Reply(text)
}

func buildMenuPreview(ctx *command.Context, botTitle, text string) *waProto.Message {
	if ctx.Client == nil || ctx.Config == nil || strings.TrimSpace(ctx.Config.DBPath) == "" {
		return nil
	}

	store, err := storage.OpenThumbStore(ctx.Config.DBPath)
	if err != nil {
		return nil
	}

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(baseCtx, 15*time.Second)
	defer cancel()

	meta, err := thumb.Resolve(reqCtx, ctx.Client, store, "random", thumb.KindThumbnail)
	if err != nil {
		return nil
	}

	favicon, err := thumb.Resolve(reqCtx, ctx.Client, store, "random", thumb.KindFavicon)
	if err != nil {
		favicon = nil
	}

	return thumb.BuildMessage(thumb.Preview{
		URL:   menuURL,
		Title: botTitle,
		Body:  fmt.Sprintf("Daftar %d perintah dalam %d kategori", len(command.GetAll()), len(command.GetCategories())),
		Text:  text,
		Quote: ctx.QuotedContext(),
	}, meta, favicon)
}
