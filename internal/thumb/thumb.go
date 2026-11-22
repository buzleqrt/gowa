package thumb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
)

const (
	CDNBase = "https://mmg.whatsapp.net"

	KindThumbnail = "thumbnail"

	KindFavicon = "favicon"

	StatusRandom = "random"

	StatusPrivate = "private"
)

type Meta struct {
	ThumbnailDirectPath string `json:"thumbnailDirectPath,omitempty"`
	ThumbnailSHA256     []byte `json:"thumbnailSha256,omitempty"`
	ThumbnailEncSHA256  []byte `json:"thumbnailEncSha256,omitempty"`
	MediaKey            []byte `json:"mediaKey,omitempty"`
	MediaKeyTimestamp   int64  `json:"mediaKeyTimestamp,omitempty"`
	ThumbnailWidth      uint32 `json:"thumbnailWidth,omitempty"`
	ThumbnailHeight     uint32 `json:"thumbnailHeight,omitempty"`
	Mimetype            string `json:"mimetype,omitempty"`
	JPEGThumbnail       []byte `json:"jpegThumbnail,omitempty"`
	URL                 string `json:"url,omitempty"`
}

func Upload(ctx context.Context, cli *whatsmeow.Client, image []byte, favicon bool) (*Meta, error) {
	if cli == nil {
		return nil, errors.New("client WhatsApp belum siap")
	}
	if len(image) == 0 {
		return nil, errors.New("gambar kosong")
	}

	resp, err := cli.Upload(ctx, image, whatsmeow.MediaLinkThumbnail)
	if err != nil {
		return nil, fmt.Errorf("upload thumbnail gagal: %w", err)
	}

	width, height := Dimensions(image)
	meta := &Meta{
		ThumbnailDirectPath: resp.DirectPath,
		ThumbnailSHA256:     resp.FileSHA256,
		ThumbnailEncSHA256:  resp.FileEncSHA256,
		MediaKey:            resp.MediaKey,
		MediaKeyTimestamp:   time.Now().Unix(),
		ThumbnailWidth:      width,
		ThumbnailHeight:     height,
		Mimetype:            MimeType(image),
		URL:                 resp.URL,
	}
	if meta.URL == "" {
		meta.URL = CDNURL(resp.DirectPath)
	}

	if !favicon {
		if jpeg, err := JPEGThumb(image); err == nil {
			meta.JPEGThumbnail = jpeg
		}
	}

	return meta, nil
}

func MimeType(image []byte) string {
	mime := http.DetectContentType(image)
	if strings.HasPrefix(mime, "image/") {
		return mime
	}
	return "image/jpeg"
}

func CDNURL(directPath string) string {
	directPath = strings.TrimSpace(directPath)
	if directPath == "" {
		return ""
	}
	if strings.HasPrefix(directPath, "http://") || strings.HasPrefix(directPath, "https://") {
		return directPath
	}
	return CDNBase + "/" + strings.TrimPrefix(directPath, "/")
}

func Expiry(rawURL string) *int64 {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	oe := parsed.Query().Get("oe")
	if oe == "" {
		return nil
	}
	sec, err := strconv.ParseInt(oe, 16, 64)
	if err != nil || sec <= 0 {
		return nil
	}
	return &sec
}

func ExpiryOfMetadata(meta *Meta) *int64 {
	if meta == nil {
		return nil
	}
	if exp := Expiry(meta.URL); exp != nil {
		return exp
	}
	return Expiry(CDNURL(meta.ThumbnailDirectPath))
}
