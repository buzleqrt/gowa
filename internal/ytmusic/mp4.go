package ytmusic

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"gowa/internal/util"
)

func normalizeAudio(data []byte) []byte {
	if !isFragmentedMP4(data) {
		return data
	}

	out, err := remuxM4A(data)
	if err != nil {
		util.Warn("YTMusic", "Gagal merapikan audio DASH (ffmpeg): %v", err)
		return data
	}
	return out
}

func isFragmentedMP4(data []byte) bool {
	for i := 0; i+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[i : i+4]))
		typ := string(data[i+4 : i+8])
		if typ == "moof" {
			return true
		}
		if size < 8 {
			return false
		}
		i += size
	}
	return false
}

func remuxM4A(data []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "gowa-ytmusic-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	outPath := filepath.Join(dir, "audio.m4a")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "mp4", "-i", "pipe:0",
		"-c", "copy", "-movflags", "+faststart",
		"-f", "ipod", outPath, "-y",
	)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 0 {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, err
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("hasil remux kosong")
	}
	return out, nil
}
