package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/pinterest"
	"gowa/internal/plugin"
)

const (
	pinFetchTimeout    = 60 * time.Second
	pinDownloadTimeout = 5 * time.Minute
	pinMaxMediaBytes   = 64 << 20
	pinCaptionLimit    = 900
	// Jumlah gambar hasil .pins: tanpa angka hanya 1, angka di akhir perintah
	// menentukan jumlahnya sampai batas maksimum.
	pinSearchDefaultImages = 1
	pinSearchMaxImages     = 10
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "pinterestdownload",
		Cmd:   "pin",
		Categ: "download",
		Desc:  "Mengunduh foto, GIF, video, atau carousel dari link Pinterest",
		Alias: []string{"pindl", "pinterest"},
	}, handlePin)

	plugin.Register(plugin.Info{
		Name:  "pinterestsearch",
		Cmd:   "pins",
		Categ: "search",
		Desc:  "Mencari pin Pinterest berdasarkan kata kunci",
		Alias: []string{"pinsearch", "pinsc"},
	}, handlePinSearch)
}

func handlePin(ctx *command.Context) error {
	input := strings.TrimSpace(strings.Join(ctx.Args, " "))
	if input == "" {
		return ctx.Reply("*Usage:*\n`.pin <link atau ID>`\n\n_Contoh:_\n`.pin https://www.pinterest.com/pin/1234567890/`\n`.pin https://pin.it/abc123`")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, pinFetchTimeout)
	defer cancel()

	pin, err := pinterest.Fetch(reqCtx, input)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengambil pin Pinterest:*\n%v", err))
	}

	dlCtx, dlCancel := context.WithTimeout(baseCtx, pinDownloadTimeout)
	defer dlCancel()

	caption := buildPinCaption(pin)

	if pin.IsVideo && pin.Video != nil && pin.Video.DownloadURL != "" {
		if strings.HasSuffix(pin.Video.DownloadURL, ".m3u8") {
			return ctx.Reply("*Gagal:* URL video langsung dari Pinterest tidak tersedia untuk pin ini.")
		}
		data, err := pinterest.DownloadBytes(dlCtx, pin.Video.DownloadURL, pinMaxMediaBytes)
		if err != nil {
			return ctx.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
		}
		return ctx.ReplyVideo(data, "video/mp4", caption)
	}

	if pin.IsCarousel && len(pin.Carousel) > 0 {
		total := len(pin.Carousel)
		captionSent := false
		for i, item := range pin.Carousel {
			data, err := pinterest.DownloadBytes(dlCtx, item.URL, pinMaxMediaBytes)
			if err != nil {
				return ctx.Send(fmt.Sprintf("*Gagal mengunduh media %d/%d:*\n%v", i+1, total, err))
			}

			itemCaption := ""
			sendFirst := !captionSent
			if sendFirst {
				itemCaption = caption
				captionSent = true
			}
			if sendFirst {
				err = ctx.ReplyImage(data, pinterest.MIME(item.URL), itemCaption)
			} else {
				err = ctx.SendImage(data, pinterest.MIME(item.URL), itemCaption)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	if pin.Image == "" {
		return ctx.Reply("*Gagal:* tidak ada media publik yang bisa diunduh dari pin ini.")
	}

	data, err := pinterest.DownloadBytes(dlCtx, pin.Image, pinMaxMediaBytes)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mengunduh gambar:*\n%v", err))
	}
	return ctx.ReplyImage(data, pinterest.MIME(pin.Image), caption)
}

func handlePinSearch(ctx *command.Context) error {
	query, count := parsePinSearchArgs(ctx.Args)
	if query == "" {
		return ctx.Reply("*Usage:*\n`.pins <kata kunci> [jumlah]`\n\n_Contoh:_\n`.pins aesthetic wallpaper` → 1 gambar\n`.pins outfit hijab 3` → 3 gambar")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, pinFetchTimeout)
	defer cancel()

	pageSize := count * 5
	if pageSize > 25 {
		pageSize = 25
	}

	result, err := pinterest.Search(reqCtx, query, pageSize)
	if err != nil {
		return ctx.Reply(fmt.Sprintf("*Gagal mencari di Pinterest:*\n%v", err))
	}
	if len(result.Pins) == 0 {
		return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada pin publik yang cocok dengan _%s_", query))
	}

	// Kirim hasil sebagai gambar langsung supaya tidak perlu buka link satu
	// per satu. Kalau semua unduhan gagal, baru tampilkan daftar teks.
	dlCtx, dlCancel := context.WithTimeout(baseCtx, pinDownloadTimeout)
	defer dlCancel()

	limit := len(result.Pins)
	if limit > count {
		limit = count
	}

	type pinMedia struct {
		data []byte
		mime string
		pin  pinterest.Pin
	}

	media := make([]pinMedia, 0, limit)
	for i := 0; i < limit; i++ {
		item := result.Pins[i]
		data, err := pinterest.DownloadBytes(dlCtx, item.Image, pinMaxMediaBytes)
		if err != nil {
			continue
		}
		media = append(media, pinMedia{data: data, mime: pinterest.MIME(item.Image), pin: item})
	}

	if len(media) == 0 {
		return ctx.Reply(pinterest.FormatSearch(result))
	}

	// Satu gambar tetap dikirim sebagai balasan biasa; lebih dari satu
	// dikirim sebagai album supaya tidak membanjiri chat.
	if len(media) == 1 {
		return ctx.ReplyImage(media[0].data, media[0].mime, buildPinSearchCaption(media[0].pin, 1, 1))
	}

	images := make([][]byte, len(media))
	for i, item := range media {
		images[i] = item.data
	}

	// Caption album hanya satu baris: baris baru dan daftar bernomor tidak
	// tampil rapi pada tampilan album WhatsApp.
	caption := fmt.Sprintf("*PINTEREST - %s* • %d gambar", truncatePinText(query, 80), len(images))
	return ctx.ReplyAlbum(images, caption)
}

// parsePinSearchArgs memisahkan jumlah gambar di akhir perintah, misalnya
// ".pins cat 2" menjadi kata kunci "cat" dengan jumlah 2. Tanpa angka,
// jumlahnya 1 supaya perintah tidak membanjiri chat.
func parsePinSearchArgs(args []string) (string, int) {
	count := pinSearchDefaultImages
	if len(args) > 1 {
		last := args[len(args)-1]
		if n, err := strconv.Atoi(last); err == nil && n >= 1 {
			if n > pinSearchMaxImages {
				n = pinSearchMaxImages
			}
			count = n
			args = args[:len(args)-1]
		}
	}
	return strings.TrimSpace(strings.Join(args, " ")), count
}

func buildPinSearchCaption(pin pinterest.Pin, index, total int) string {
	title := strings.TrimSpace(pin.Title)
	if title == "" {
		title = strings.TrimSpace(pin.Description)
	}
	if title == "" {
		title = pinSearchTitleFallback(pin)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "*%d/%d. %s*", index, total, truncatePinText(title, 120))

	var meta []string
	if pin.Pinner != nil && pin.Pinner.Username != "" {
		meta = append(meta, "👤 @"+pin.Pinner.Username)
	}
	if pin.Board != nil && pin.Board.Name != "" {
		meta = append(meta, "📌 "+pin.Board.Name)
	}
	if len(meta) > 0 {
		sb.WriteString("\n" + strings.Join(meta, " | "))
	}

	return sb.String()
}

// pinSearchTitleFallback dipakai saat pin tidak punya judul maupun deskripsi.
// ID pin baru Pinterest berbentuk buram (e1_...) sehingga tidak layak
// ditampilkan.
func pinSearchTitleFallback(pin pinterest.Pin) string {
	if pin.Pinner != nil && pin.Pinner.Username != "" {
		return "Pin oleh @" + pin.Pinner.Username
	}
	if pin.Board != nil && pin.Board.Name != "" {
		return "Pin dari " + pin.Board.Name
	}
	return "Pin Pinterest"
}

func buildPinCaption(pin *pinterest.Pin) string {
	var sb strings.Builder

	title := "*Pinterest"
	if pin.Pinner != nil && pin.Pinner.Username != "" {
		title += " - @" + pin.Pinner.Username
	}
	sb.WriteString(title + "*")

	if text := strings.TrimSpace(pin.Title); text != "" {
		sb.WriteString("\n" + truncatePinText(text, pinCaptionLimit))
	}
	if text := strings.TrimSpace(pin.Description); text != "" && text != pin.Title {
		sb.WriteString("\n" + truncatePinText(text, pinCaptionLimit))
	}

	var details []string
	if pin.Board != nil && pin.Board.Name != "" {
		details = append(details, "📌 "+pin.Board.Name)
	}
	switch pin.Type {
	case "video":
		details = append(details, "🎬 video")
	case "carousel":
		details = append(details, fmt.Sprintf("🖼 %d media", len(pin.Carousel)))
	}
	if len(details) > 0 {
		sb.WriteString("\n" + strings.Join(details, " | "))
	}

	if pin.URL != "" {
		sb.WriteString("\n🔗 " + pin.URL)
	}
	return sb.String()
}

func truncatePinText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}
