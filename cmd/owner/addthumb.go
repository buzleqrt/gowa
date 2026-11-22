package owner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/storage"
	"gowa/internal/thumb"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
)

const addThumbTimeout = 30 * time.Second

func init() {
	command.Register(&command.Command{
		Name:    "addthumb",
		Cmd:     "addthumb",
		Categ:   "owner",
		Desc:    "Simpan metadata thumbnail link dengan nama tertentu",
		Alias:   []string{"addthumbnail", "addthumbail"},
		Execute: func(ctx *command.Context) error { return addThumbFlow(ctx, thumb.KindThumbnail) },
	})
	command.Register(&command.Command{
		Name:    "addfavicon",
		Cmd:     "addfavicon",
		Categ:   "owner",
		Desc:    "Simpan metadata favicon link dengan nama tertentu",
		Alias:   []string{"addfav", "addfavico"},
		Execute: func(ctx *command.Context) error { return addThumbFlow(ctx, thumb.KindFavicon) },
	})
}

func addThumbFlow(ctx *command.Context, kind string) error {
	if !ctx.IsOwner {
		return nil
	}

	private := false
	positional := make([]string, 0, len(ctx.Args))
	for _, arg := range ctx.Args {
		if strings.EqualFold(arg, "-private") {
			private = true
			continue
		}
		positional = append(positional, arg)
	}

	if len(positional) == 0 {
		return ctx.Reply(addThumbUsage(ctx, kind))
	}

	name := strings.TrimSpace(positional[0])
	urlInput := strings.TrimSpace(strings.Join(positional[1:], " "))

	if thumb.IsHTTPURL(name) {
		return ctx.Reply("*Gagal:* nama harus di depan, URL-nya di belakang.\n\n" + addThumbUsage(ctx, kind))
	}
	if strings.EqualFold(name, "random") {
		return ctx.Reply("*Gagal:* nama `random` dipakai sebagai kata kunci khusus untuk mengambil data acak. Pakai nama lain.")
	}

	status := thumb.StatusRandom
	if kind == thumb.KindThumbnail && private {
		status = thumb.StatusPrivate
	}

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(baseCtx, addThumbTimeout)
	defer cancel()

	data, meta, viaUpload, err := addThumbSource(ctx, reqCtx, kind, urlInput)
	if err != nil {
		return ctx.Reply("*Gagal:*\n" + err.Error())
	}

	if viaUpload {
		meta, err = thumb.Upload(reqCtx, ctx.Client, data, kind == thumb.KindFavicon)
		if err != nil {
			return ctx.Reply("*Gagal upload thumbnail:*\n" + err.Error())
		}
	}

	expired := thumb.ExpiryOfMetadata(meta)
	if expired != nil && *expired <= time.Now().Unix() {
		return ctx.Reply(fmt.Sprintf(
			"*Gagal:* metadata sudah expired (%s).\nCari sumber yang lebih segar.",
			formatThumbTime(*expired),
		))
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		return ctx.Reply("*Gagal:* metadata tidak bisa disimpan: " + err.Error())
	}

	store, err := storage.OpenThumbStore(ctx.Config.DBPath)
	if err != nil {
		return ctx.Reply("*Gagal:* " + err.Error())
	}
	if err := store.Save(name, kind, status, string(payload), expired); err != nil {
		return ctx.Reply("*Gagal:* " + err.Error())
	}

	sumber := "upload baru"
	if !viaUpload {
		sumber = "metadata pesan"
	}

	expiryText := "tidak terdeteksi"
	if expired != nil {
		expiryText = fmt.Sprintf("%s *(sisa %s)*", formatThumbTime(*expired), formatThumbRemaining(*expired-time.Now().Unix()))
	}

	label := "Thumbnail"
	if kind == thumb.KindFavicon {
		label = "Favicon"
	}

	return ctx.Reply(fmt.Sprintf(
		"✅ *%s tersimpan!*\n"+
			"• Nama    : `%s`\n"+
			"• Jenis   : %s\n"+
			"• Status  : %s\n"+
			"• Sumber  : %s\n"+
			"• Expired : %s",
		label, name, kind, status, sumber, expiryText,
	))
}

func addThumbSource(ctx *command.Context, reqCtx context.Context, kind, urlInput string) ([]byte, *thumb.Meta, bool, error) {
	if urlInput != "" {
		if !thumb.IsHTTPURL(urlInput) {
			return nil, nil, false, errors.New("URL tidak valid")
		}
		data, err := thumb.Fetch(reqCtx, urlInput)
		if err != nil {
			return nil, nil, false, err
		}
		return data, nil, true, nil
	}

	if quoted := ctx.GetQuotedMessage(); quoted != nil {
		if meta := metaFromQuoted(quoted, kind); meta != nil {
			return nil, meta, false, nil
		}
		if isMediaMessage(quoted) {
			data, err := ctx.DownloadQuotedMedia()
			if err != nil {
				return nil, nil, false, fmt.Errorf("gagal mengunduh media: %w", err)
			}
			return data, nil, true, nil
		}
	}

	if ctx.Event != nil && ctx.Event.Message != nil {
		current := command.UnwrapMessage(ctx.Event.Message)
		if current != nil && isMediaMessage(current) {
			data, err := ctx.DownloadMedia(current)
			if err != nil {
				return nil, nil, false, fmt.Errorf("gagal mengunduh media: %w", err)
			}
			return data, nil, true, nil
		}
	}

	return nil, nil, false, errors.New("reply gambar atau pesan link-preview, atau sertakan URL gambar")
}

func metaFromQuoted(msg *waProto.Message, kind string) *thumb.Meta {
	ext := msg.GetExtendedTextMessage()
	if ext == nil {
		return nil
	}

	if kind == thumb.KindFavicon {
		return metaFromMMS(ext.GetFaviconMmsMetadata())
	}

	if ext.GetThumbnailDirectPath() == "" || len(ext.GetMediaKey()) == 0 {
		return nil
	}
	return &thumb.Meta{
		ThumbnailDirectPath: ext.GetThumbnailDirectPath(),
		ThumbnailSHA256:     ext.GetThumbnailSHA256(),
		ThumbnailEncSHA256:  ext.GetThumbnailEncSHA256(),
		MediaKey:            ext.GetMediaKey(),
		MediaKeyTimestamp:   ext.GetMediaKeyTimestamp(),
		ThumbnailWidth:      ext.GetThumbnailWidth(),
		ThumbnailHeight:     ext.GetThumbnailHeight(),
		JPEGThumbnail:       ext.GetJPEGThumbnail(),
	}
}

func metaFromMMS(mms *waProto.MMSThumbnailMetadata) *thumb.Meta {
	if mms == nil || mms.GetThumbnailDirectPath() == "" || len(mms.GetMediaKey()) == 0 {
		return nil
	}
	return &thumb.Meta{
		ThumbnailDirectPath: mms.GetThumbnailDirectPath(),
		ThumbnailSHA256:     mms.GetThumbnailSHA256(),
		ThumbnailEncSHA256:  mms.GetThumbnailEncSHA256(),
		MediaKey:            mms.GetMediaKey(),
		MediaKeyTimestamp:   mms.GetMediaKeyTimestamp(),
		ThumbnailWidth:      mms.GetThumbnailWidth(),
		ThumbnailHeight:     mms.GetThumbnailHeight(),
	}
}

func isMediaMessage(msg *waProto.Message) bool {
	if msg == nil {
		return false
	}
	return msg.GetImageMessage() != nil || msg.GetStickerMessage() != nil
}

func addThumbUsage(ctx *command.Context, kind string) string {
	cmd := ctx.BotPrefix + "addthumb"
	extra := " [-private]"
	if kind == thumb.KindFavicon {
		cmd = ctx.BotPrefix + "addfavicon"
		extra = ""
	}
	return fmt.Sprintf(
		"*Format Penggunaan:*\n"+
			"> `Reply gambar`\n> `%s%s <nama>`\n\n"+
			"> `Reply pesan link-preview` (metadata diambil dari pesan, tanpa upload ulang)\n> `%s%s <nama>`\n\n"+
			"> `Kirim gambar dengan caption`\n> `%s%s <nama>`\n\n"+
			"> `Dari URL gambar`\n> `%s%s <nama> <url>`\n\n"+
			"`-private` = tidak ikut pool random. Tanpa flag, default *random*.",
		cmd, extra, cmd, extra, cmd, extra, cmd, extra,
	)
}

func formatThumbTime(unix int64) string {
	return time.Unix(unix, 0).In(time.FixedZone("WIB", 7*3600)).Format("02/01/2006 15:04")
}

func formatThumbRemaining(seconds int64) string {
	if seconds < 0 {
		return "habis"
	}
	units := []struct {
		seconds int64
		label   string
	}{
		{86_400, "hari"},
		{3_600, "jam"},
		{60, "menit"},
		{1, "detik"},
	}
	parts := make([]string, 0, 2)
	rem := seconds
	for _, unit := range units {
		if value := rem / unit.seconds; value > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", value, unit.label))
			rem %= unit.seconds
		}
	}
	if len(parts) == 0 {
		return "0 detik"
	}
	return strings.Join(parts, ", ")
}
