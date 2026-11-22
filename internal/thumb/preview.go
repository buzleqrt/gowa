package thumb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gowa/internal/storage"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Preview adalah isi pesan link-preview: URL tujuan, judul, deskripsi, dan
// teks tambahan yang ikut dikirim di bawah URL.
type Preview struct {
	URL   string
	Title string
	Body  string
	Text  string
	Quote *waProto.ContextInfo
}

// BuildMessage menyusun pesan ExtendedTextMessage dengan preview tautan yang
// memakai metadata thumbnail dan (opsional) favicon.
func BuildMessage(p Preview, meta, favicon *Meta) *waProto.Message {
	if meta == nil {
		return nil
	}

	url := p.URL
	text := url
	if p.Text != "" {
		text = url + "\n" + p.Text
	}

	ext := &waProto.ExtendedTextMessage{
		Text:                  proto.String(text),
		MatchedText:           proto.String(url),
		PreviewType:           waProto.ExtendedTextMessage_NONE.Enum(),
		InviteLinkGroupTypeV2: waProto.ExtendedTextMessage_DEFAULT.Enum(),
		ThumbnailDirectPath:   proto.String(meta.ThumbnailDirectPath),
		ThumbnailSHA256:       meta.ThumbnailSHA256,
		ThumbnailEncSHA256:    meta.ThumbnailEncSHA256,
		MediaKey:              meta.MediaKey,
		MediaKeyTimestamp:     proto.Int64(meta.MediaKeyTimestamp),
		ThumbnailWidth:        proto.Uint32(meta.ThumbnailWidth),
		ThumbnailHeight:       proto.Uint32(meta.ThumbnailHeight),
		JPEGThumbnail:         meta.JPEGThumbnail,
		ContextInfo:           p.Quote,
	}
	if p.Title != "" {
		ext.Title = proto.String(p.Title)
	}
	if p.Body != "" {
		ext.Description = proto.String(p.Body)
	}
	if favicon != nil && favicon.ThumbnailDirectPath != "" {
		ext.FaviconMmsMetadata = &waProto.MMSThumbnailMetadata{
			ThumbnailDirectPath: proto.String(favicon.ThumbnailDirectPath),
			ThumbnailSHA256:     favicon.ThumbnailSHA256,
			ThumbnailEncSHA256:  favicon.ThumbnailEncSHA256,
			MediaKey:            favicon.MediaKey,
			MediaKeyTimestamp:   proto.Int64(favicon.MediaKeyTimestamp),
			ThumbnailWidth:      proto.Uint32(favicon.ThumbnailWidth),
			ThumbnailHeight:     proto.Uint32(favicon.ThumbnailHeight),
		}
	}

	return &waProto.Message{ExtendedTextMessage: ext}
}

// Resolve mengambil metadata berdasarkan nama di database, kata kunci
// "random", atau URL gambar yang diunggah saat itu juga.
func Resolve(ctx context.Context, cli *whatsmeow.Client, store *storage.ThumbStore, value, kind string) (*Meta, error) {
	if store == nil {
		return nil, errors.New("database thumbnail tidak tersedia")
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s: isi dengan nama, URL gambar, atau \"random\"", kind)
	}

	if strings.EqualFold(value, "random") {
		row, err := store.Random(kind)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, fmt.Errorf("tidak ada %s random yang tersedia di database", kind)
		}
		return metaFromRow(row)
	}

	if IsHTTPURL(value) {
		data, err := Fetch(ctx, value)
		if err != nil {
			return nil, err
		}
		return Upload(ctx, cli, data, kind == KindFavicon)
	}

	row, err := store.Get(value, kind)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("%s `%s` tidak ditemukan di database", kind, value)
	}
	return metaFromRow(row)
}

func metaFromRow(row *storage.Thumb) (*Meta, error) {
	var meta Meta
	if err := json.Unmarshal([]byte(row.Metadata), &meta); err != nil {
		return nil, fmt.Errorf("%s `%s` rusak: %w", row.Kind, row.Name, err)
	}
	if meta.ThumbnailDirectPath == "" || len(meta.MediaKey) == 0 {
		return nil, fmt.Errorf("%s `%s` tidak punya metadata lengkap", row.Kind, row.Name)
	}
	if expired := ExpiryOfMetadata(&meta); expired != nil && *expired <= time.Now().Unix() {
		return nil, fmt.Errorf("%s `%s` sudah kedaluwarsa, perbarui dengan .addthumb", row.Kind, row.Name)
	}
	return &meta, nil
}
