package hd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"
)

func EnhanceImage(ctx context.Context, source string) ([]byte, error) {
	// Siapkan bytes & file input lokal.
	var data []byte
	inputPath := source
	cleanupInput := false

	if isURL(source) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 2 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp("", "hd-in-*")
		if err != nil {
			return nil, err
		}
		tmp.Close()
		if err := os.WriteFile(tmp.Name(), data, 0644); err != nil {
			os.Remove(tmp.Name())
			return nil, err
		}
		inputPath = tmp.Name()
		cleanupInput = true
	} else {
		b, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		data = b
	}
	if cleanupInput {
		defer os.Remove(inputPath)
	}

	// Coba IHancer (gratis) terlebih dahulu.
	ext := detectImageExt(data)
	if enhanced, err := ihancerEnhance(ctx, data, "image"+ext); err == nil && len(enhanced) > 0 {
		return enhanced, nil
	}

	// Coba scraper eksternal (imageupscaler.com).
	outFile, err := os.CreateTemp("", "hd-out-*")
	if err != nil {
		return nil, err
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	scriptPath := filepath.Join("scripts", "imageupscaler.mjs")
	if _, err := os.Stat(scriptPath); err == nil {
		cmd := exec.CommandContext(ctx, "node", scriptPath, inputPath, "4x", outPath)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			if out, rerr := os.ReadFile(outPath); rerr == nil && len(out) > 0 {
				return out, nil
			}
		}
		// fallback ke lokal jika scraper gagal
	}

	// Fallback lokal: upscale 2x + enhance.
	if len(data) == 0 {
		b, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, err
		}
		data = b
	}
	return localEnhance(data)
}

func detectImageExt(data []byte) string {
	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func ihancerEnhance(ctx context.Context, data []byte, filename string) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := map[string]string{
		"method":            "1",
		"is_pro_version":    "false",
		"is_enhancing_more": "false",
		"max_image_size":    "high",
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://ihancer.com/api/enhance", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", "Dart/3.5 (dart:io)")
	req.Header.Set("Accept-Encoding", "gzip")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("IHancer HTTP %d: %s", resp.StatusCode, string(body))
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("IHancer response kosong")
	}
	return body, nil
}

func localEnhance(data []byte) ([]byte, error) {
	src, format, err := decodeImage(data)
	if err != nil {
		return nil, fmt.Errorf("gagal decode gambar: %v", err)
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("dimensi gambar tidak valid")
	}

	// Skala adaptif: gambar kecil di-upscale 4x, gambar besar 2x,
	// dengan batas maksimum 4096 px agar tidak kehabisan memori.
	scale := 4
	if w >= 1024 || h >= 1024 {
		scale = 2
	}
	if w*scale > 4096 || h*scale > 4096 {
		scale = 2
	}
	if w*scale > 4096 || h*scale > 4096 {
		scale = 1
	}
	if scale < 1 {
		scale = 1
	}
	nw, nh := w*scale, h*scale

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	enhance(dst)

	var out bytes.Buffer
	if format == "png" {
		if err := png.Encode(&out, dst); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 95}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decodeImage(data []byte) (image.Image, string, error) {
	if img, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
		return img, "jpeg", nil
	}
	if img, err := png.Decode(bytes.NewReader(data)); err == nil {
		return img, "png", nil
	}
	return nil, "", fmt.Errorf("format gambar tidak didukung")
}

func enhance(img *image.RGBA) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	blur := image.NewRGBA(b)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb, n int
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					xx, yy := x+dx, y+dy
					if xx < 0 || yy < 0 || xx >= w || yy >= h {
						continue
					}
					c := img.RGBAAt(xx, yy)
					sr += int(c.R)
					sg += int(c.G)
					sb += int(c.B)
					n++
				}
			}
			blur.SetRGBA(x, y, color.RGBA{R: uint8(sr / n), G: uint8(sg / n), B: uint8(sb / n), A: 255})
		}
	}

	tmp := image.NewRGBA(b)
	copy(tmp.Pix, img.Pix)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := tmp.RGBAAt(x, y)
			bl := blur.RGBAAt(x, y)
			r := clamp(int(c.R) + int((int(c.R)-int(bl.R))*20/10))
			g := clamp(int(c.G) + int((int(c.G)-int(bl.G))*20/10))
			b2 := clamp(int(c.B) + int((int(c.B)-int(bl.B))*20/10))
			r = clamp((r-128)*130/100 + 128)
			g = clamp((g-128)*130/100 + 128)
			b2 = clamp((b2-128)*130/100 + 128)
			gray := (r + g + b2) / 3
			r = clamp(gray + (r-gray)*120/100)
			g = clamp(gray + (g-gray)*120/100)
			b2 = clamp(gray + (b2-gray)*120/100)
			img.SetRGBA(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b2), A: c.A})
		}
	}
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func UploadToCDN(ctx context.Context, data []byte, filename string) (string, error) {
	if filename == "" {
		filename = "media.jpg"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://cdn.zass.in/upload", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload CDN gagal: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var r struct {
		Success bool   `json:"success"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("gagal parse response CDN: %v", err)
	}
	if !r.Success || r.URL == "" {
		return "", fmt.Errorf("upload CDN tidak mengembalikan URL")
	}
	return r.URL, nil
}

func isURL(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || (len(s) > 8 && s[:8] == "https://"))
}
