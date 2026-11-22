package command

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"gowa/internal/thumb"
)

// AlbumItem adalah satu media dalam album WhatsApp. Video didukung lewat
// ExpectedVideoCount, jadi hasil pencarian campuran foto dan video tetap bisa
// dikirim sebagai satu album.
type AlbumItem struct {
	Data  []byte
	Video bool
}

// ReplyAlbum mengirim beberapa gambar sebagai satu album WhatsApp.
func (c *Context) ReplyAlbum(images [][]byte, caption string) error {
	items := make([]AlbumItem, len(images))
	for i, data := range images {
		items[i] = AlbumItem{Data: data}
	}
	return c.ReplyAlbumMedia(items, caption)
}

// ReplyAlbumMedia mengirim campuran gambar dan video sebagai satu album.
//
// Album dikirim dalam dua tahap: pesan induk berisi jumlah media yang
// diharapkan, lalu setiap media sebagai pesan anak yang terhubung ke induk
// lewat MessageAssociation. Caption hanya dipasang pada media pertama.
func (c *Context) ReplyAlbumMedia(items []AlbumItem, caption string) error {
	if len(items) == 0 {
		return errors.New("album tidak berisi media")
	}

	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	upCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	target := c.Chat.ToNonAD()

	type uploadedMedia struct {
		resp     whatsmeow.UploadResponse
		video    bool
		mimeType string
		width    uint32
		height   uint32
		thumb    []byte
	}

	uploads := make([]uploadedMedia, 0, len(items))
	var imageCount, videoCount uint32
	for i, item := range items {
		entry := uploadedMedia{video: item.Video}

		if item.Video {
			mediaType := whatsmeow.MediaVideo
			resp, err := c.Client.Upload(upCtx, item.Data, mediaType)
			if err != nil {
				return fmt.Errorf("upload video %d album gagal: %w", i+1, err)
			}
			entry.resp = resp
			entry.mimeType = "video/mp4"
			videoCount++
		} else {
			data, mimeType := normalizeImage(item.Data, "")
			resp, err := c.Client.Upload(upCtx, data, whatsmeow.MediaImage)
			if err != nil {
				return fmt.Errorf("upload gambar %d album gagal: %w", i+1, err)
			}
			entry.resp = resp
			entry.mimeType = mimeType
			entry.width, entry.height = thumb.Dimensions(data)
			if small, terr := thumb.JPEGThumb(data); terr == nil {
				entry.thumb = small
			}
			imageCount++
		}

		uploads = append(uploads, entry)
	}

	albumSecret := make([]byte, 32)
	if _, err := rand.Read(albumSecret); err != nil {
		return err
	}
	albumID := c.Client.GenerateMessageID()

	parent := &waProto.Message{
		AlbumMessage: &waProto.AlbumMessage{
			ExpectedImageCount: proto.Uint32(imageCount),
			ExpectedVideoCount: proto.Uint32(videoCount),
		},
		MessageContextInfo: &waProto.MessageContextInfo{
			MessageSecret: albumSecret,
		},
	}

	parentResp, err := c.Client.SendMessage(upCtx, target, parent, whatsmeow.SendRequestExtra{ID: albumID})
	if err != nil {
		return fmt.Errorf("kirim album gagal: %w", err)
	}
	if c.SentCache != nil && parentResp.ID != "" {
		c.SentCache.Add(string(parentResp.ID))
	}

	parentKey := c.Client.BuildMessageKey(target, types.EmptyJID, parentResp.ID)

	for i, upload := range uploads {
		childSecret := make([]byte, 32)
		if _, err := rand.Read(childSecret); err != nil {
			return err
		}

		childCaption := ""
		if i == 0 {
			childCaption = caption
		}

		child := &waProto.Message{
			MessageContextInfo: &waProto.MessageContextInfo{
				MessageSecret: childSecret,
				MessageAssociation: &waProto.MessageAssociation{
					AssociationType:  waProto.MessageAssociation_MEDIA_ALBUM.Enum(),
					ParentMessageKey: parentKey,
					MessageIndex:     proto.Int32(int32(i)),
				},
			},
		}

		if upload.video {
			child.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(childCaption),
				Mimetype:      proto.String(upload.mimeType),
				URL:           &upload.resp.URL,
				DirectPath:    &upload.resp.DirectPath,
				MediaKey:      upload.resp.MediaKey,
				FileEncSHA256: upload.resp.FileEncSHA256,
				FileSHA256:    upload.resp.FileSHA256,
				FileLength:    &upload.resp.FileLength,
			}
		} else {
			child.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(childCaption),
				Mimetype:      proto.String(upload.mimeType),
				URL:           &upload.resp.URL,
				DirectPath:    &upload.resp.DirectPath,
				MediaKey:      upload.resp.MediaKey,
				FileEncSHA256: upload.resp.FileEncSHA256,
				FileSHA256:    upload.resp.FileSHA256,
				FileLength:    &upload.resp.FileLength,
				Width:         proto.Uint32(upload.width),
				Height:        proto.Uint32(upload.height),
				JPEGThumbnail: upload.thumb,
			}
		}

		childResp, err := c.Client.SendMessage(upCtx, target, child)
		if err != nil {
			return fmt.Errorf("kirim media %d album gagal: %w", i+1, err)
		}
		if c.SentCache != nil && childResp.ID != "" {
			c.SentCache.Add(string(childResp.ID))
		}

		time.Sleep(20 * time.Millisecond)
	}

	return nil
}
