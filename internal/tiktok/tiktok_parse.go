package tiktok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func formatUploadDate(unixSeconds int64) string {
	if unixSeconds <= 0 {
		return ""
	}
	return time.Unix(unixSeconds, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func determineResolution(ratio string, width, height int) string {
	lower := strings.ToLower(ratio)
	if ratio != "" && (strings.Contains(lower, "p") || strings.Contains(lower, "k")) {
		return ratio
	}
	minDim := width
	if height < minDim {
		minDim = height
	}
	switch {
	case minDim <= 0:
		return ""
	case minDim >= 1080:
		return "1080p"
	case minDim >= 720:
		return "720p"
	case minDim >= 480:
		return "480p"
	case minDim >= 360:
		return "360p"
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func buildTitle(description, author string) string {
	if line := strings.TrimSpace(strings.Split(description, "\n")[0]); line != "" {
		return line
	}
	if author != "" {
		return "Video by @" + author
	}
	return "TikTok Post"
}

type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(string(bytes.TrimSpace(data)), `"`)
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		f64, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil {
			return ferr
		}
		n = int64(f64)
	}
	*f = flexInt64(n)
	return nil
}

type mobileFeed struct {
	AwemeList []struct {
		AwemeID string `json:"aweme_id"`
		Video   struct {
			PlayAddr struct {
				URLList []string `json:"url_list"`
			} `json:"play_addr"`
		} `json:"video"`
	} `json:"aweme_list"`
}

type embedState struct {
	Source struct {
		Data map[string]json.RawMessage `json:"data"`
	} `json:"source"`
}

type embedRoute struct {
	Code      int             `json:"code"`
	IsError   bool            `json:"isError"`
	VideoData *embedVideoData `json:"videoData"`
}

type embedVideoData struct {
	AuthorInfos struct {
		UniqueID     string   `json:"uniqueId"`
		NickName     string   `json:"nickName"`
		Covers       []string `json:"covers"`
		CoversMedium []string `json:"coversMedium"`
		CoversLarger []string `json:"coversLarger"`
	} `json:"authorInfos"`
	ItemInfos struct {
		ID           string    `json:"id"`
		Text         string    `json:"text"`
		CreateTime   flexInt64 `json:"createTime"`
		Covers       []string  `json:"covers"`
		CoversOrigin []string  `json:"coversOrigin"`
		PlayCount    flexInt64 `json:"playCount"`
		DiggCount    flexInt64 `json:"diggCount"`
		CommentCount flexInt64 `json:"commentCount"`
		ShareCount   flexInt64 `json:"shareCount"`
		Video        struct {
			URLs      []string `json:"urls"`
			VideoMeta struct {
				Width    int `json:"width"`
				Height   int `json:"height"`
				Duration int `json:"duration"`
			} `json:"videoMeta"`
		} `json:"video"`
	} `json:"itemInfos"`
	MusicInfos struct {
		MusicName  string   `json:"musicName"`
		AuthorName string   `json:"authorName"`
		PlayURL    []string `json:"playUrl"`
	} `json:"musicInfos"`
	ImagePostInfo *struct {
		DisplayImages []struct {
			Width   int      `json:"width"`
			Height  int      `json:"height"`
			URLList []string `json:"urlList"`
		} `json:"displayImages"`
	} `json:"imagePostInfo"`
}

func (d embedVideoData) toMetadata() *Metadata {
	var photos []string
	if d.ImagePostInfo != nil {
		for _, img := range d.ImagePostInfo.DisplayImages {
			if u := firstURL(img.URLList); u != "" {
				photos = append(photos, u)
			}
		}
	}

	author := d.AuthorInfos.UniqueID
	duration := d.ItemInfos.Video.VideoMeta.Duration
	width := d.ItemInfos.Video.VideoMeta.Width
	height := d.ItemInfos.Video.VideoMeta.Height
	audioURL := firstNonEmpty(firstURL(d.MusicInfos.PlayURL))

	thumbnail := firstNonEmpty(firstURL(d.ItemInfos.CoversOrigin), firstURL(d.ItemInfos.Covers))
	if thumbnail == "" && len(photos) > 0 {
		thumbnail = photos[0]
	}

	md := &Metadata{
		ID:                d.ItemInfos.ID,
		Author:            author,
		Nickname:          d.AuthorInfos.NickName,
		Title:             buildTitle(d.ItemInfos.Text, author),
		Description:       d.ItemInfos.Text,
		Duration:          duration,
		FormattedDuration: FormatDuration(duration),
		Resolution:        determineResolution("", width, height),
		Width:             width,
		Height:            height,
		Thumbnail:         thumbnail,
		UploadDate:        formatUploadDate(int64(d.ItemInfos.CreateTime)),
		Views:             int64(d.ItemInfos.PlayCount),
		Likes:             int64(d.ItemInfos.DiggCount),
		Comments:          int64(d.ItemInfos.CommentCount),
		Shares:            int64(d.ItemInfos.ShareCount),
		VideoURL:          firstURL(d.ItemInfos.Video.URLs),
		AudioURL:          audioURL,
		Photos:            photos,
	}

	if audioURL != "" {
		md.Music = &Music{Title: d.MusicInfos.MusicName, Author: d.MusicInfos.AuthorName, URL: audioURL}
	}

	if len(photos) > 0 {
		md.VideoURL = ""
	}
	return md
}

type tikwmResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data tikwmDetail `json:"data"`
}

type tikwmDetail struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Cover        string   `json:"cover"`
	OriginCover  string   `json:"origin_cover"`
	Duration     int      `json:"duration"`
	Play         string   `json:"play"`
	WMPlay       string   `json:"wmplay"`
	HDPlay       string   `json:"hdplay"`
	PlayCount    int64    `json:"play_count"`
	DiggCount    int64    `json:"digg_count"`
	CommentCount int64    `json:"comment_count"`
	ShareCount   int64    `json:"share_count"`
	CreateTime   int64    `json:"create_time"`
	Images       []string `json:"images"`
	Author       struct {
		UniqueID string `json:"unique_id"`
		Nickname string `json:"nickname"`
	} `json:"author"`
	MusicInfo struct {
		Title  string `json:"title"`
		Author string `json:"author"`
		Play   string `json:"play"`
	} `json:"music_info"`
}

func (d tikwmDetail) toMetadata() *Metadata {
	photos := make([]string, 0, len(d.Images))
	for _, img := range d.Images {
		if strings.TrimSpace(img) != "" {
			photos = append(photos, img)
		}
	}

	videoURL := firstNonEmpty(d.HDPlay, d.Play, d.WMPlay)
	audioURL := strings.TrimSpace(d.MusicInfo.Play)
	author := d.Author.UniqueID

	md := &Metadata{
		ID:                d.ID,
		Author:            author,
		Nickname:          d.Author.Nickname,
		Title:             buildTitle(d.Title, author),
		Description:       d.Title,
		Duration:          d.Duration,
		FormattedDuration: FormatDuration(d.Duration),
		Thumbnail:         firstNonEmpty(d.OriginCover, d.Cover),
		UploadDate:        formatUploadDate(d.CreateTime),
		Views:             d.PlayCount,
		Likes:             d.DiggCount,
		Comments:          d.CommentCount,
		Shares:            d.ShareCount,
		VideoURL:          videoURL,
		AudioURL:          audioURL,
		Photos:            photos,
	}

	if audioURL != "" {
		md.Music = &Music{Title: d.MusicInfo.Title, Author: d.MusicInfo.Author, URL: audioURL}
	}
	if len(photos) > 0 {
		md.VideoURL = ""
	}
	return md
}
