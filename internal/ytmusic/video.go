package ytmusic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const VideoDefaultMaxBytes = 15 << 20

var ErrInvalidVideoURL = errors.New("tautan YouTube tidak valid")

type Video struct {
	VideoID  string `json:"video_id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	Data     []byte `json:"-"`
}

func ParseVideoID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if videoIDPattern.MatchString(raw) {
		return raw, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", ErrInvalidVideoURL
	}

	host := strings.ToLower(parsed.Host)
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")

	switch host {
	case "youtu.be":
		if id := firstPathSegment(parsed.Path); videoIDPattern.MatchString(id) {
			return id, nil
		}
	case "youtube.com", "youtube-nocookie.com", "music.youtube.com":
		if id := parsed.Query().Get("v"); videoIDPattern.MatchString(id) {
			return id, nil
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 {
			switch parts[0] {
			case "shorts", "embed", "live", "v":
				if videoIDPattern.MatchString(parts[1]) {
					return parts[1], nil
				}
			}
		}
	}

	return "", ErrInvalidVideoURL
}

func firstPathSegment(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func FetchVideo(ctx context.Context, rawURL string, maxBytes int64) (*Video, error) {
	videoID, err := ParseVideoID(rawURL)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = VideoDefaultMaxBytes
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		visitor, err := getVisitorData(ctx, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		player, err := fetchPlayer(ctx, videoID, visitor)
		if err != nil {
			return nil, err
		}

		if status := player.PlayabilityStatus.Status; status != "OK" {
			reason := player.PlayabilityStatus.Reason
			if reason == "" {
				reason = status
			}
			lastErr = fmt.Errorf("YouTube menolak pemutaran: %s", reason)
			if attempt == 0 && strings.Contains(strings.ToLower(reason), "sign in") {
				continue
			}
			return nil, lastErr
		}

		videoFormat, audioFormat, err := pickVideoAndAudio(player.StreamingData.AdaptiveFormats, maxBytes)
		if err != nil {
			return nil, err
		}

		var (
			videoData, audioData []byte
			videoErr, audioErr   error
			wg                   sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			videoData, videoErr = downloadAudio(ctx, videoFormat.URL, videoFormat.ContentLength, maxBytes)
		}()
		go func() {
			defer wg.Done()
			audioData, audioErr = downloadAudio(ctx, audioFormat.URL, audioFormat.ContentLength, maxBytes)
		}()
		wg.Wait()
		if videoErr != nil {
			return nil, videoErr
		}
		if audioErr != nil {
			return nil, audioErr
		}

		merged, err := muxVideoAudio(videoData, audioData)
		if err != nil {
			return nil, err
		}
		if int64(len(merged)) > maxBytes {
			return nil, fmt.Errorf("video hasil gabungan %d MB melebihi batas %d MB",
				len(merged)/(1<<20), maxBytes/(1<<20))
		}

		details := player.VideoDetails
		return &Video{
			VideoID:  videoID,
			Title:    details.Title,
			Author:   details.Author,
			Duration: atoi(details.LengthSeconds),
			MimeType: "video/mp4",
			Data:     merged,
		}, nil
	}

	if lastErr == nil {
		lastErr = errors.New("gagal mengambil video YouTube")
	}
	return nil, lastErr
}

func pickVideoAndAudio(formats []apiFormat, maxBytes int64) (apiFormat, apiFormat, error) {
	audio, ok := pickAudioFormat(formats)
	if !ok || baseMime(audio.MimeType) != "audio/mp4" {
		return apiFormat{}, apiFormat{}, errors.New("YouTube tidak menyediakan audio MP4")
	}

	var videos []apiFormat
	for _, format := range formats {
		if strings.TrimSpace(format.URL) == "" {
			continue
		}
		if baseMime(format.MimeType) != "video/mp4" {
			continue
		}
		videos = append(videos, format)
	}
	if len(videos) == 0 {
		return apiFormat{}, apiFormat{}, errors.New("YouTube tidak menyediakan video MP4")
	}

	audioSize := parseSize(audio.ContentLength)
	sort.Slice(videos, func(i, j int) bool {
		return videoBitrate(videos[i]) > videoBitrate(videos[j])
	})

	for _, format := range videos {
		size := parseSize(format.ContentLength)
		if size > 0 && size+audioSize <= maxBytes {
			return format, audio, nil
		}
	}
	return videos[len(videos)-1], audio, nil
}

func videoBitrate(format apiFormat) int {
	if format.AverageBitrate > 0 {
		return format.AverageBitrate
	}
	return format.Bitrate
}

func parseSize(text string) int64 {
	size, _ := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	return size
}

func muxVideoAudio(videoData, audioData []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "gowa-ytvideo-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	videoPath := filepath.Join(dir, "video.mp4")
	audioPath := filepath.Join(dir, "audio.m4a")
	outPath := filepath.Join(dir, "out.mp4")

	if err := os.WriteFile(videoPath, videoData, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(audioPath, audioData, 0o600); err != nil {
		return nil, err
	}

	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", videoPath, "-i", audioPath,
		"-c", "copy", "-movflags", "+faststart",
		"-f", "mp4", outPath, "-y",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 0 {
			return nil, fmt.Errorf("gagal menggabungkan video dan audio: %v: %s", err, msg)
		}
		return nil, fmt.Errorf("gagal menggabungkan video dan audio: %w", err)
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("hasil gabungan video kosong")
	}
	return out, nil
}
