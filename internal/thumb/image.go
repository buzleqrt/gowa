package thumb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"

	_ "image/gif"
	_ "image/png"
)

const thumbWidth = 120

func Dimensions(data []byte) (uint32, uint32) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0
	}
	return uint32(cfg.Width), uint32(cfg.Height)
}

func JPEGThumb(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, errors.New("gambar tidak punya ukuran")
	}

	if width > thumbWidth {
		height = int(math.Round(float64(height) * float64(thumbWidth) / float64(width)))
		width = thumbWidth
	}
	if height < 1 {
		height = 1
	}

	scaled := scaleBilinear(img, width, height)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 40}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func scaleBilinear(src image.Image, width, height int) *image.RGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	xRatio := float64(sw) / float64(width)
	yRatio := float64(sh) / float64(height)

	for y := 0; y < height; y++ {
		sy := (float64(y)+0.5)*yRatio - 0.5
		y0 := int(math.Floor(sy))
		fy := sy - float64(y0)
		if y0 < 0 {
			y0, fy = 0, 0
		}
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}

		for x := 0; x < width; x++ {
			sx := (float64(x)+0.5)*xRatio - 0.5
			x0 := int(math.Floor(sx))
			fx := sx - float64(x0)
			if x0 < 0 {
				x0, fx = 0, 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}

			c00 := toRGBA(src.At(bounds.Min.X+x0, bounds.Min.Y+y0))
			c10 := toRGBA(src.At(bounds.Min.X+x1, bounds.Min.Y+y0))
			c01 := toRGBA(src.At(bounds.Min.X+x0, bounds.Min.Y+y1))
			c11 := toRGBA(src.At(bounds.Min.X+x1, bounds.Min.Y+y1))

			dst.SetRGBA(x, y, color.RGBA{
				R: blend(c00.R, c10.R, c01.R, c11.R, fx, fy),
				G: blend(c00.G, c10.G, c01.G, c11.G, fx, fy),
				B: blend(c00.B, c10.B, c01.B, c11.B, fx, fy),
				A: blend(c00.A, c10.A, c01.A, c11.A, fx, fy),
			})
		}
	}

	return dst
}

func toRGBA(c color.Color) color.RGBA {
	return color.RGBAModel.Convert(c).(color.RGBA)
}

func blend(c00, c10, c01, c11 uint8, fx, fy float64) uint8 {
	top := float64(c00)*(1-fx) + float64(c10)*fx
	bottom := float64(c01)*(1-fx) + float64(c11)*fx
	value := top*(1-fy) + bottom*fy
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(math.Round(value))
}
