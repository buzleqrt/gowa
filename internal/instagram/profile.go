package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ErrUserNotFound dikembalikan bila akun Instagram tidak ada atau sudah dihapus.
var ErrUserNotFound = errors.New("akun Instagram tidak ditemukan")

// ErrPrivateAccount dikembalikan bila akun bersifat privat.
var ErrPrivateAccount = errors.New("akun Instagram bersifat privat")

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`)
	// Path Instagram yang bukan nama pengguna.
	reservedPaths = map[string]bool{
		"p": true, "reel": true, "reels": true, "tv": true, "explore": true,
		"accounts": true, "stories": true, "share": true, "about": true,
		"developer": true, "api": true, "web": true, "direct": true, "legal": true,
	}
)

// BioLink adalah tautan pada bio profil Instagram.
type BioLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// LatestPost adalah postingan terbaru pada profil Instagram.
type LatestPost struct {
	ID           string `json:"id"`
	Shortcode    string `json:"shortcode"`
	URL          string `json:"url"`
	MediaType    int    `json:"media_type"`
	Caption      string `json:"caption"`
	LikeCount    int64  `json:"like_count"`
	CommentCount int64  `json:"comment_count"`
}

// Profile berisi informasi publik sebuah akun Instagram.
type Profile struct {
	Source         string       `json:"source"`
	ID             string       `json:"id"`
	Username       string       `json:"username"`
	FullName       string       `json:"full_name"`
	Biography      string       `json:"biography"`
	ProfilePicURL  string       `json:"profile_pic_url"`
	IsPrivate      bool         `json:"is_private"`
	IsVerified     bool         `json:"is_verified"`
	IsMemorialized bool         `json:"is_memorialized"`
	FollowerCount  int64        `json:"follower_count"`
	FollowingCount int64        `json:"following_count"`
	MediaCount     int64        `json:"media_count"`
	Pronouns       []string     `json:"pronouns"`
	BioLinks       []BioLink    `json:"bio_links"`
	LatestPosts    []LatestPost `json:"latest_posts"`

	// Field lama (tetap diisi untuk kompatibilitas).
	Avatar    string `json:"-"`
	Verified  bool   `json:"-"`
	Private   bool   `json:"-"`
	Followers int64  `json:"-"`
	Posts     int64  `json:"-"`
	Profile   string `json:"-"`
}

// ExtractUsername mengambil nama pengguna dari URL profil, "@nama", atau nama polos.
func ExtractUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if strings.Contains(raw, "instagram.com/") {
		if idx := strings.Index(raw, "instagram.com/"); idx >= 0 {
			raw = raw[idx+len("instagram.com/"):]
		}
	}
	raw = strings.TrimPrefix(raw, "@")
	if cut := strings.IndexAny(raw, "/?#"); cut >= 0 {
		raw = raw[:cut]
	}

	if raw == "" || reservedPaths[strings.ToLower(raw)] || !usernamePattern.MatchString(raw) {
		return ""
	}
	return raw
}

// FetchProfile mengambil informasi publik sebuah akun Instagram.
//
// Halaman profil biasa (`/<username>/`) dialihkan ke halaman login, jadi data
// diambil dari halaman embed profil yang tetap disajikan untuk perayap.
func FetchProfile(ctx context.Context, raw string) (*Profile, error) {
	username := ExtractUsername(raw)
	if username == "" {
		return nil, errors.New("username Instagram tidak valid")
	}

	endpoint := "https://www.instagram.com/" + username + "/embed/"
	body, status, err := getBody(ctx, endpoint, crawlerUA, "https://www.instagram.com/"+username+"/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrUserNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("halaman profil membalas HTTP %d", status)
	}

	context, err := extractContext(body)
	if err != nil {
		text := strings.ToLower(string(body))
		switch {
		case strings.Contains(text, "is private"):
			return nil, ErrPrivateAccount
		case strings.Contains(text, "may be broken"), strings.Contains(text, "profile may have been removed"):
			return nil, ErrUserNotFound
		}
		return nil, err
	}

	profile := &Profile{
		Source:        "https://www.instagram.com/" + username + "/",
		ID:            fmt.Sprintf("%.0f", float64(context.number("owner_id"))),
		Username:      firstNonEmpty(context.string("username"), username),
		FullName:      context.string("full_name"),
		ProfilePicURL: context.string("profile_pic_url"),
		IsVerified:    context.boolean("verified") || context.boolean("is_verified"),
		FollowerCount: context.number("followers_count"),
		MediaCount:    context.number("posts_count"),
		Pronouns:      stringSlice(context["pronouns"]),
		BioLinks:      []BioLink{},
		LatestPosts:   []LatestPost{},
	}

	// is_private kadang tersedia di level atas, kadang di owner graphql.
	if v, ok := context["is_private"].(bool); ok {
		profile.IsPrivate = v
	}

	// Ambil latest posts dari graphql_media.
	if gm, ok := context["graphql_media"].([]any); ok {
		for _, entry := range gm {
			m, _ := entry.(map[string]any)
			sm, _ := m["shortcode_media"].(map[string]any)
			if sm == nil {
				continue
			}
			post := LatestPost{
				ID:        stringValue(sm["id"]),
				Shortcode: stringValue(sm["shortcode"]),
			}
			post.URL = "https://www.instagram.com/p/" + post.Shortcode + "/"
			if isVideo, _ := sm["is_video"].(bool); isVideo {
				post.MediaType = 2
			} else {
				post.MediaType = 1
			}
			if capObj, ok := sm["edge_media_to_caption"].(map[string]any); ok {
				if edges, ok := capObj["edges"].([]any); ok && len(edges) > 0 {
					if edge, ok := edges[0].(map[string]any); ok {
						if node, ok := edge["node"].(map[string]any); ok {
							post.Caption = stringValue(node["text"])
						}
					}
				}
			}
			if liked, ok := sm["edge_liked_by"].(map[string]any); ok {
				post.LikeCount = intValue(liked["count"])
			}
			if commented, ok := sm["edge_media_to_comment"].(map[string]any); ok {
				post.CommentCount = intValue(commented["count"])
			}
			profile.LatestPosts = append(profile.LatestPosts, post)
		}
	}

	// Sinkronkan field lama.
	profile.Avatar = profile.ProfilePicURL
	profile.Verified = profile.IsVerified
	profile.Private = profile.IsPrivate
	profile.Followers = profile.FollowerCount
	profile.Posts = profile.MediaCount
	profile.Profile = profile.Source
	return profile, nil
}

// FormatProfile merangkum profil menjadi teks siap kirim.
func FormatProfile(p *Profile) string {
	if p == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("*INSTAGRAM USER INFORMATION*\n\n*ACCOUNT*")
	sb.WriteString("\n• Username : @" + p.Username)

	if p.FullName != "" {
		sb.WriteString("\n• Nama : " + p.FullName)
	}
	if p.ID != "" {
		sb.WriteString("\n• User ID : " + p.ID)
	}
	if p.Biography != "" {
		sb.WriteString("\n• Bio : " + p.Biography)
	}
	if len(p.Pronouns) > 0 {
		sb.WriteString("\n• Pronouns : " + strings.Join(p.Pronouns, ", "))
	}
	if len(p.BioLinks) > 0 {
		var links []string
		for _, link := range p.BioLinks {
			if link.URL != "" {
				links = append(links, link.URL)
			}
		}
		if len(links) > 0 {
			sb.WriteString("\n• Bio Links : " + strings.Join(links, ", "))
		}
	}
	sb.WriteString("\n• Terverifikasi : " + yesNo(p.IsVerified))
	sb.WriteString("\n• Akun Privat : " + yesNo(p.IsPrivate))
	sb.WriteString("\n• Memorialized : " + yesNo(p.IsMemorialized))

	profileURL := p.Source
	if profileURL == "" {
		profileURL = "https://www.instagram.com/" + p.Username
	}
	sb.WriteString("\n• Profil : " + profileURL)

	sb.WriteString("\n\n*STATISTICS*")
	sb.WriteString("\n• Pengikut : " + formatThousands(p.FollowerCount))
	sb.WriteString("\n• Mengikuti : " + formatThousands(p.FollowingCount))
	sb.WriteString("\n• Total Post : " + formatThousands(p.MediaCount))

	if len(p.LatestPosts) > 0 {
		sb.WriteString("\n\n*LATEST POSTS*")
		limit := len(p.LatestPosts)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			post := p.LatestPosts[i]
			kind := "Photo"
			if post.MediaType == 2 {
				kind = "Video"
			}
			caption := strings.TrimSpace(post.Caption)
			if caption == "" {
				caption = "(no caption)"
			} else if len(caption) > 70 {
				caption = strings.TrimSpace(caption[:70]) + "…"
			}
			caption = strings.ReplaceAll(caption, "\n", " ")
			sb.WriteString(fmt.Sprintf("\n%d. %s - %s", i+1, kind, caption))
			sb.WriteString(fmt.Sprintf("\n   ❤️ %s | 💬 %s", formatThousands(post.LikeCount), formatThousands(post.CommentCount)))
			sb.WriteString("\n   " + post.URL)
		}
	}

	return sb.String()
}

type profileContext map[string]any

// extractContext mengambil objek contextJSON yang ditanam sebagai string JSON
// berlapis di dalam halaman embed profil.
func extractContext(body []byte) (profileContext, error) {
	const marker = `"contextJSON":`

	idx := bytes.Index(body, []byte(marker))
	if idx < 0 {
		return nil, errors.New("halaman profil tidak memuat data akun")
	}

	decoder := json.NewDecoder(bytes.NewReader(body[idx+len(marker):]))
	var encoded string
	if err := decoder.Decode(&encoded); err != nil {
		return nil, fmt.Errorf("gagal membaca data akun: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		return nil, fmt.Errorf("gagal membaca data akun: %w", err)
	}

	if nested, ok := decoded["context"].(map[string]any); ok {
		return profileContext(nested), nil
	}
	return profileContext(decoded), nil
}

func (c profileContext) string(key string) string {
	return strings.TrimSpace(stringValue(c[key]))
}

func (c profileContext) number(key string) int64 {
	return intValue(c[key])
}

func (c profileContext) boolean(key string) bool {
	v, _ := c[key].(bool)
	return v
}

func yesNo(v bool) string {
	if v {
		return "Ya"
	}
	return "Tidak"
}

func formatThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)

	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
