package sticker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func durationSeconds(ctx context.Context, data []byte) uint32 {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil || len(data) == 0 {
		return 0
	}

	f, err := os.CreateTemp("", "gowa-sticker-*.ogg")
	if err != nil {
		return 0
	}
	name := f.Name()
	defer os.Remove(name)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return 0
	}
	if err := f.Close(); err != nil {
		return 0
	}

	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", name,
	)

	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return uint32(math.Round(secs))
}

func coverWebp(ctx context.Context, data []byte, movingHint bool) ([]byte, bool, error) {
	if len(data) == 0 {
		return nil, false, ErrImageEmpty
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, false, ErrNoFFmpeg
	}

	attempts := []struct {
		fps     int
		quality int
		seconds int
	}{
		{fps: 12, quality: 50, seconds: 6},
		{fps: 8, quality: 30, seconds: 4},
	}

	if movingHint || isAnimatedWebP(data) || isGIF(data) {
		for _, attempt := range attempts {
			out, _, err := renderCover(ctx, ffmpeg, data, true, attempt.fps, attempt.quality, attempt.seconds)
			if err != nil || len(out) == 0 {
				continue
			}
			if len(out) <= animatedMaxBytes {
				return out, true, nil
			}
		}

		if isAnimatedWebP(data) {
			frames, delays, err := decodeAnimatedWebP(data)
			if err == nil && len(frames) > 0 {
				for _, attempt := range attempts {
					out, _, err := renderFrames(ctx, ffmpeg, frames, delays, attempt.quality, attempt.seconds)
					if err != nil || len(out) == 0 {
						continue
					}
					if len(out) <= animatedMaxBytes {
						return out, true, nil
					}
				}
				if png, encErr := encodePNG(frames[0]); encErr == nil {
					data = png
				}
			}
		}

	}

	out, stderr, err := renderCover(ctx, ffmpeg, data, false, 0, 80, 0)
	if err != nil {
		return nil, false, fmt.Errorf("konversi gambar stiker gagal: %s", ffmpegError(err, stderr))
	}
	if len(out) == 0 {
		return nil, false, errors.New("konversi gambar stiker menghasilkan data kosong")
	}
	if len(out) > staticMaxBytes {
		return nil, false, fmt.Errorf("gambar stiker %d KB, melebihi batas %d KB", len(out)>>10, staticMaxBytes>>10)
	}
	return out, false, nil
}

func renderCover(ctx context.Context, ffmpeg string, data []byte, animated bool, fps, quality, seconds int) ([]byte, string, error) {
	in, cleanup, err := writeTempInput(data)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()

	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", in}

	if animated {
		args = append(args,
			"-t", strconv.Itoa(seconds),
			"-vf", fmt.Sprintf(
				"fps=%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=0x00000000",
				fps, stickerSize, stickerSize, stickerSize, stickerSize,
			),
		)
	} else {
		args = append(args,
			"-vf", fmt.Sprintf(
				"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=0x00000000",
				stickerSize, stickerSize, stickerSize, stickerSize,
			),
			"-frames:v", "1",
		)
	}

	codec := "libwebp"
	if animated {
		codec = "libwebp_anim"
	}

	args = append(args,
		"-an", "-c:v", codec, "-lossless", "0", "-quality", strconv.Itoa(quality),
	)
	if animated {
		args = append(args, "-loop", "0")
	}
	args = append(args, "-f", "webp", "pipe:1")

	cmd := exec.CommandContext(ctx, ffmpeg, args...)

	out, stderr, err := runFFmpeg(cmd)
	return out, stderr, err
}

func writeTempInput(data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "gowa-sticker-*"+sniffExt(data))
	if err != nil {
		return "", func() {}, err
	}

	name := f.Name()
	cleanup := func() { os.Remove(name) }

	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return name, cleanup, nil
}

func runFFmpeg(cmd *exec.Cmd) ([]byte, string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	return out, strings.TrimSpace(stderr.String()), err
}

func ffmpegError(err error, stderr string) string {
	if stderr == "" {
		return err.Error()
	}
	return stderr
}
