package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gowa/internal/command"
	"gowa/internal/instagram"
	"gowa/internal/plugin"
)

const (
	igSearchTimeout         = 60 * time.Second
	igSearchDownloadTimeout = 5 * time.Minute
	igSearchMaxMediaBytes   = 64 << 20
	igSearchCaptionLimit    = 700

	igSearchDefaultPosts = 1
	igSearchMaxPosts     = 10
)

func init() {
	plugin.Register(plugin.Info{
		Name:  "instagramsearch",
		Cmd:   "igsearch",
		Categ: "search",
		Desc:  "Mencari postingan akun Instagram (atau hashtag)",
		Alias: []string{"igfind", "igsc"},
	}, handleIGSearch)
}

func handleIGSearch(ctx *command.Context) error {
	query, count := parseIGSearchArgs(ctx.Args)
	if query == "" {
		return ctx.Reply("*Usage:*\n`.igsearch <nama akun> [jumlah]`\n\n_Contoh:_\n`.igsearch avetetsu` → 1 media\n`.igsearch davina karamoy 3` → 3 media\n\n_Mencari postingan dari akun Instagram. Bila nama akun tidak ditemukan, pencarian hashtag digunakan sebagai cadangan._")
	}

	ctx.Typing()

	baseCtx := ctx.Ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}

	reqCtx, cancel := context.WithTimeout(baseCtx, igSearchTimeout)
	defer cancel()

	account := ""
	var result *instagram.SearchResult
	var err error

	if !strings.Contains(query, " ") && instagram.ExtractUsername(query) == query {
		result, err = instagram.FetchUserPosts(reqCtx, query, count)
		if err == nil {
			account = query
		} else if err != nil && err.Error() == instagram.ErrUserNotFound.Error() {
			result, err = instagram.Search(reqCtx, query, count)
			account = ""
		}
	} else {
		resolved, rerr := instagram.ResolveUsername(reqCtx, query)
		if rerr == nil && resolved != "" {
			result, err = instagram.FetchUserPosts(reqCtx, resolved, count)
			if err == nil {
				account = resolved
			}
		} else {
			err = rerr
		}
		if result == nil && (err == nil || err.Error() != instagram.ErrSearchLoginRequired.Error()) {
			fallbackResult, fallbackErr := instagram.Search(reqCtx, query, count)
			if fallbackErr == nil {
				result = fallbackResult
				err = nil
				account = ""
			} else if err == nil {
				err = fallbackErr
			}
		}
	}

	if err != nil {
		if err.Error() == instagram.ErrSearchLoginRequired.Error() {
			return ctx.Reply("*Pencarian Instagram butuh login.*\n\nJalur pencarian Instagram menolak pengunjung tanpa sesi. Isi `ig_sessionid` pada `config.json` dengan nilai cookie `sessionid` akun Instagram, lalu restart bot.\n\n_`.ig` dan `.igstalk` tetap bisa dipakai tanpa sessionid._")
		}
		if err.Error() == instagram.ErrTagNotFound.Error() || err.Error() == instagram.ErrUserNotFound.Error() {
			if account != "" {
				return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada media publik untuk akun _@%s_", account))
			}
			return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada media publik untuk _#%s_", instagramSearchTag(query)))
		}
		return ctx.Reply(fmt.Sprintf("*Gagal mencari di Instagram:*\n%v", err))
	}
	if len(result.Posts) == 0 {
		if account != "" {
			return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada media publik untuk akun _@%s_", account))
		}
		return ctx.Reply(fmt.Sprintf("*Tidak ditemukan:*\ntidak ada media publik untuk _#%s_", result.Tag))
	}

	dlCtx, dlCancel := context.WithTimeout(baseCtx, igSearchDownloadTimeout)
	defer dlCancel()

	type searchMedia struct {
		data  []byte
		mime  string
		video bool
		post  *instagram.Metadata
	}

	media := make([]searchMedia, 0, len(result.Posts))
	images, videos := 0, 0

	for _, post := range result.Posts {
		if len(post.Items) == 0 {
			continue
		}
		item := post.Items[0]
		data, err := instagram.DownloadMedia(dlCtx, item, igSearchMaxMediaBytes)
		if err != nil {
			continue
		}

		mimeType := http.DetectContentType(data)
		if item.Video {
			mimeType = "video/mp4"
			videos++
		} else {
			images++
		}
		media = append(media, searchMedia{data: data, mime: mimeType, video: item.Video, post: post})
	}

	if len(media) == 0 {
		return ctx.Reply(igSearchFallback(result))
	}

	if len(media) == 1 {
		only := media[0]
		if only.video {
			return ctx.SendVideo(only.data, only.mime, igSearchCaption(only.post))
		}
		return ctx.ReplyImage(only.data, only.mime, igSearchCaption(only.post))
	}

	items := make([]command.AlbumItem, len(media))
	for i, entry := range media {
		items[i] = command.AlbumItem{Data: entry.data, Video: entry.video}
	}
	if account != "" {
		parts := make([]string, 0, 2)
		if images > 0 {
			parts = append(parts, fmt.Sprintf("%d gambar", images))
		}
		if videos > 0 {
			parts = append(parts, fmt.Sprintf("%d video", videos))
		}
		return ctx.ReplyAlbumMedia(items, fmt.Sprintf("*INSTAGRAM - @%s* • %s", account, strings.Join(parts, " • ")))
	}
	return ctx.ReplyAlbumMedia(items, igSearchAlbumCaption(result.Tag, images, videos))
}

func igSearchAlbumCaption(tag string, images, videos int) string {
	parts := make([]string, 0, 2)
	if images > 0 {
		parts = append(parts, fmt.Sprintf("%d gambar", images))
	}
	if videos > 0 {
		parts = append(parts, fmt.Sprintf("%d video", videos))
	}
	return fmt.Sprintf("*INSTAGRAM - #%s* • %s", tag, strings.Join(parts, " • "))
}

func parseIGSearchArgs(args []string) (string, int) {
	count := igSearchDefaultPosts
	if len(args) > 1 {
		last := args[len(args)-1]
		if n, err := strconv.Atoi(last); err == nil && n >= 1 {
			if n > igSearchMaxPosts {
				n = igSearchMaxPosts
			}
			count = n
			args = args[:len(args)-1]
		}
	}
	return strings.TrimSpace(strings.Join(args, " ")), count
}

func instagramSearchTag(query string) string {
	tag := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "#")))
	var b strings.Builder
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func igSearchCaption(post *instagram.Metadata) string {
	var sb strings.Builder

	author := post.Author
	if author == "" {
		author = "unknown"
	}
	sb.WriteString("*Instagram - @" + author + "*")

	text := strings.TrimSpace(post.Caption)
	if len(text) > igSearchCaptionLimit {
		text = strings.TrimSpace(text[:igSearchCaptionLimit]) + "…"
	}
	if text != "" {
		sb.WriteString("\n" + text)
	}

	if post.Code != "" {
		sb.WriteString("\n🔗 https://www.instagram.com/p/" + post.Code + "/")
	}
	return sb.String()
}

func igSearchFallback(result *instagram.SearchResult) string {
	var sb strings.Builder
	label := result.Tag
	if result.Account != "" {
		label = "@" + result.Account
	} else {
		label = "#" + result.Tag
	}
	sb.WriteString(fmt.Sprintf("*INSTAGRAM - %s*\n", label))
	for i, post := range result.Posts {
		if i >= 10 {
			break
		}
		author := post.Author
		if author == "" {
			author = "unknown"
		}
		sb.WriteString(fmt.Sprintf("%d. @%s — https://www.instagram.com/p/%s/\n", i+1, author, post.Code))
	}
	return strings.TrimSpace(sb.String())
}
