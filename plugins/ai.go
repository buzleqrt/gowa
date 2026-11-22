package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/dola"
	"gowa/internal/plugin"
)

const (
	dolaTimeout      = 4 * time.Minute
	dolaMaxChunk     = 3500
	dolaMaxReplyChar = 12000
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "dolaai",
		Cmd:   "ai",
		Categ: "ai",
		Desc:  "Tanya AI (Nova) dengan riwayat percakapan per chat",
		Alias: []string{"nova", "dola", "tanyaai"},
	}, handleAI)
}

func aiPaths(ctx *command.Context) (string, string) {
	chat := "unknown"
	if !ctx.Chat.IsEmpty() {
		chat = ctx.Chat.String()
	}
	var sb strings.Builder
	for _, r := range chat {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	name := strings.Trim(sb.String(), "_")
	if name == "" {
		name = "unknown"
	}
	dir := filepath.Join("scripts", "dola_sessions")
	return filepath.Join(dir, "device.json"), filepath.Join(dir, "conv-"+name+".json")
}

func handleAI(ctx *command.Context) error {
	if len(ctx.Args) == 0 {
		return ctx.Reply("*Usage:*\n`.ai <pertanyaan>`\n`.ai reset` — mulai percakapan baru\n\n_Contoh:_\n`.ai jelaskan apa itu goroutine`\n`.ai buatkan puisi tentang hujan`")
	}

	prompt := strings.TrimSpace(strings.Join(ctx.Args, " "))

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	sessionPath, convPath := aiPaths(ctx)

	switch strings.ToLower(prompt) {
	case "reset", "new", "clear":
		reqCtx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
		defer cancel()
		if err := dola.Reset(reqCtx, sessionPath, convPath); err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal reset percakapan:*\n%v", err))
		}
		return ctx.Reply("✅ Percakapan AI dimulai dari awal.")
	case "help", "usage":
		return ctx.Reply("*Usage:*\n`.ai <pertanyaan>`\n`.ai reset` — mulai percakapan baru")
	}

	ctx.Typing()

	reqCtx, cancel := context.WithTimeout(baseCtx, dolaTimeout)
	defer cancel()

	reply, err := dola.Ask(reqCtx, sessionPath, convPath, prompt)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal menjawab:*\n%v", err))
	}

	text := reply.Text
	if len(text) > dolaMaxReplyChar {
		text = strings.TrimSpace(text[:dolaMaxReplyChar]) + "…"
	}

	parts := splitAIText(text, dolaMaxChunk)
	for i, part := range parts {
		if i == 0 {
			if err := ctx.Reply(part); err != nil {
				return err
			}
			continue
		}
		if err := ctx.Send(part); err != nil {
			return err
		}
	}
	return nil
}

func splitAIText(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var parts []string
	for len(text) > limit {
		cut := strings.LastIndexAny(text[:limit], "\n ")
		if cut <= 0 {
			cut = limit
		}
		part := strings.TrimSpace(text[:cut])
		if part != "" {
			parts = append(parts, part)
		}
		text = strings.TrimLeft(text[cut:], "\n ")
	}
	if strings.TrimSpace(text) != "" {
		parts = append(parts, strings.TrimSpace(text))
	}
	return parts
}
