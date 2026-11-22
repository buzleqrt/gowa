package command

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"

	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// normalizeImage mengubah gambar WebP menjadi JPEG. Instagram, Pinterest, dan
// beberapa sumber lain mengirim WebP, sedangkan WhatsApp tidak merender WebP
// sama sekali — termasuk sebagai isi album, yang tampil kosong.
//
// Format lain dibiarkan apa adanya supaya GIF dan PNG tidak berubah.
func normalizeImage(data []byte, mimeType string) ([]byte, string) {
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(data)
	}
	if mimeType != "image/webp" {
		return data, mimeType
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mimeType
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return data, mimeType
	}
	return buf.Bytes(), "image/jpeg"
}
