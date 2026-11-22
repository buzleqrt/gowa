package sticker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os/exec"
	"strconv"

	xwebp "golang.org/x/image/webp"
)

const (
	maxAnimFrames   = 240
	maxAnimDuration = 6000 // ms, cukup untuk stiker WhatsApp
)

// decodeAnimatedWebP mengurai frame webp animasi secara manual karena ffmpeg
// 4.4 di server belum bisa men-decode animasi webp. Setiap frame ANMF diubah
// menjadi webp mandiri lalu di-decode dengan decoder statis.
func decodeAnimatedWebP(data []byte) ([]image.Image, []uint, error) {
	if len(data) < 21 || !bytes.HasPrefix(data, []byte("RIFF")) || string(data[8:12]) != "WEBP" {
		return nil, nil, errors.New("bukan berkas webp")
	}

	var (
		canvasW, canvasH int
		frames           []image.Image
		delays           []uint
	)

	canvas := new(image.RGBA)
	offset := 12
	for offset+8 <= len(data) {
		fourcc := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if size < 0 || offset+8+size > len(data) {
			break
		}
		payload := data[offset+8 : offset+8+size]

		switch fourcc {
		case "VP8X":
			if len(payload) >= 10 {
				canvasW = int(readUint24(payload[4:7])) + 1
				canvasH = int(readUint24(payload[7:10])) + 1
				canvas = image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
			}
		case "ANMF":
			frame, delay, err := decodeANMFFrame(payload)
			if err != nil {
				return nil, nil, err
			}
			if canvasW == 0 || canvasH == 0 {
				canvasW, canvasH = frame.w, frame.h
				canvas = image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
			}
			if frame.noBlend {
				draw.Draw(canvas, frame.rect, frame.img, frame.img.Bounds().Min, draw.Src)
			} else {
				draw.Draw(canvas, frame.rect, frame.img, frame.img.Bounds().Min, draw.Over)
			}

			snapshot := image.NewRGBA(canvas.Bounds())
			copy(snapshot.Pix, canvas.Pix)
			frames = append(frames, snapshot)
			delays = append(delays, delay)

			if frame.disposeBackground {
				draw.Draw(canvas, frame.rect, image.Transparent, image.Point{}, draw.Src)
			}

			total := uint(0)
			for _, d := range delays {
				total += d
			}
			if len(frames) >= maxAnimFrames || total >= maxAnimDuration {
				return frames, delays, nil
			}
		}

		offset += 8 + size
		if size%2 == 1 {
			offset++
		}
	}

	if len(frames) == 0 {
		return nil, nil, errors.New("webp animasi tidak punya frame")
	}
	return frames, delays, nil
}

type animFrame struct {
	img               image.Image
	rect              image.Rectangle
	w, h              int
	noBlend           bool
	disposeBackground bool
}

func decodeANMFFrame(payload []byte) (*animFrame, uint, error) {
	if len(payload) < 16 {
		return nil, 0, errors.New("frame webp animasi tidak lengkap")
	}

	x := int(readUint24(payload[0:3])) * 2
	y := int(readUint24(payload[3:6])) * 2
	w := int(readUint24(payload[6:9])) + 1
	h := int(readUint24(payload[9:12])) + 1
	delay := readUint24(payload[12:15])
	flags := payload[15]

	sub := payload[16:]
	var alpha, bitstream []byte
	var bitstreamType string

	pos := 0
	for pos+8 <= len(sub) {
		fourcc := string(sub[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(sub[pos+4 : pos+8]))
		if size < 0 || pos+8+size > len(sub) {
			break
		}

		switch fourcc {
		case "ALPH":
			alpha = sub[pos+8 : pos+8+size]
		case "VP8 ", "VP8L":
			bitstream = sub[pos+8 : pos+8+size]
			bitstreamType = fourcc
		}

		pos += 8 + size
		if size%2 == 1 {
			pos++
		}
	}

	if bitstream == nil {
		return nil, 0, errors.New("frame webp animasi tanpa data gambar")
	}

	container := buildWebP(alpha, bitstreamType, bitstream, w, h)
	img, err := xwebp.Decode(bytes.NewReader(container))
	if err != nil {
		return nil, 0, fmt.Errorf("decode frame webp gagal: %w", err)
	}

	return &animFrame{
		img:               img,
		rect:              image.Rect(x, y, x+w, y+h),
		w:                 w,
		h:                 h,
		noBlend:           flags&0x02 != 0,
		disposeBackground: flags&0x01 != 0,
	}, uint(delay), nil
}

// buildWebP membungkus satu frame menjadi berkas webp mandiri.
func buildWebP(alpha []byte, bitstreamType string, bitstream []byte, w, h int) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")

	writeChunk := func(fourcc string, payload []byte) {
		body.WriteString(fourcc)
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(payload)))
		body.Write(size[:])
		body.Write(payload)
		if len(payload)%2 == 1 {
			body.WriteByte(0)
		}
	}

	if alpha != nil {
		vp8x := make([]byte, 10)
		vp8x[0] = 0x10 // flag alpha
		writeUint24(vp8x[4:7], uint32(w-1))
		writeUint24(vp8x[7:10], uint32(h-1))
		writeChunk("VP8X", vp8x)
		writeChunk("ALPH", alpha)
	}

	writeChunk(bitstreamType, bitstream)

	out := make([]byte, 0, body.Len()+12)
	out = append(out, "RIFF"...)
	var riffSize [4]byte
	binary.LittleEndian.PutUint32(riffSize[:], uint32(body.Len()+4))
	out = append(out, riffSize[:]...)
	out = append(out, body.Bytes()...)
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readUint24(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

func writeUint24(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
}

// renderFrames mengubah frame hasil decode menjadi webp animasi 512x512 lewat
// stdin ffmpeg dalam bentuk rawvideo RGBA.
func renderFrames(ctx context.Context, ffmpeg string, frames []image.Image, delays []uint, quality, seconds int) ([]byte, string, error) {
	if len(frames) == 0 {
		return nil, "", errors.New("tidak ada frame")
	}

	w := frames[0].Bounds().Dx()
	h := frames[0].Bounds().Dy()

	var totalDelay uint
	for _, d := range delays {
		totalDelay += d
	}
	if totalDelay == 0 {
		totalDelay = uint(len(frames)) * 100
	}

	fps := float64(len(frames)) * 1000 / float64(totalDelay)
	if fps < 5 {
		fps = 5
	}
	if fps > 30 {
		fps = 30
	}

	// Batasi jumlah frame agar tidak melebihi durasi maksimal.
	limit := seconds * 1000
	var raw bytes.Buffer
	elapsed := 0
	for i, frame := range frames {
		if i > 0 && elapsed >= limit {
			break
		}
		rgba, ok := frame.(*image.RGBA)
		if !ok {
			bounds := frame.Bounds()
			rgba = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
			draw.Draw(rgba, rgba.Bounds(), frame, bounds.Min, draw.Src)
		}
		raw.Write(rgba.Pix)
		if i < len(delays) {
			elapsed += int(delays[i])
		}
	}

	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"-s", strconv.Itoa(w) + "x" + strconv.Itoa(h),
		"-framerate", strconv.FormatFloat(fps, 'f', 3, 64),
		"-i", "pipe:0",
		"-t", strconv.Itoa(seconds),
		"-vf", fmt.Sprintf(
			"fps=12,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=0x00000000",
			stickerSize, stickerSize, stickerSize, stickerSize,
		),
		"-an", "-c:v", "libwebp_anim", "-lossless", "0", "-quality", strconv.Itoa(quality),
		"-loop", "0", "-f", "webp", "pipe:1",
	}

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stdin = bytes.NewReader(raw.Bytes())

	out, stderr, err := runFFmpeg(cmd)
	return out, stderr, err
}
