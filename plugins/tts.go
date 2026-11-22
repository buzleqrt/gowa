package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/plugin"
	"gowa/internal/tts"
)

const (
	ttsTimeout  = 3 * time.Minute
	ttsMaxChars = 2000
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "tts",
		Cmd:   "tts",
		Categ: "tools",
		Desc:  "Ubah teks jadi voice note anime girl (reply teks atau ketik langsung)",
		Alias: []string{"say", "ucapkan", "vn"},
	}, handleTTS)
}

func handleTTS(ctx *command.Context) error {
	text := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if text == "" {
		text = quotedText(ctx)
	}
	if text == "" {
		return ctx.Reply("*Usage:*\n`.tts <teks>`\nAtau reply pesan teks lalu ketik `.tts`\n\n_Contoh:_\n`.tts halo, selamat malam semuanya`")
	}

	if len([]rune(text)) > ttsMaxChars {
		text = string([]rune(text)[:ttsMaxChars])
	}

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(baseCtx, ttsTimeout)
	defer cancel()

	ctx.Typing()

	audio, seconds, err := tts.Synthesize(reqCtx, text)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal membuat suara:*\n%v", err))
	}

	return ctx.SendVoiceNote(audio, "audio/ogg; codecs=opus", seconds)
}

// quotedText mengambil teks dari pesan yang di-reply, bila ada.
func quotedText(ctx *command.Context) string {
	quoted := ctx.GetQuotedMessage()
	if quoted == nil {
		return ""
	}
	if t := quoted.GetConversation(); t != "" {
		return t
	}
	if ext := quoted.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if img := quoted.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := quoted.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := quoted.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}
