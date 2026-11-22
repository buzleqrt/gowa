package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrUserNotFound dikembalikan bila akun TikTok tidak ada atau sudah dihapus.
var ErrUserNotFound = errors.New("akun TikTok tidak ditemukan")

var (
	usernamePattern  = regexp.MustCompile(`^[A-Za-z0-9._]{1,64}$`)
	universalPattern = regexp.MustCompile(`(?s)<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">(.*?)</script>`)
)

// Profile berisi informasi publik sebuah akun TikTok.
type Profile struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Signature string `json:"signature"`
	Verified  bool   `json:"verified"`
	Private   bool   `json:"private"`
	Language  string `json:"language"`
	BioLink   string `json:"bio_link"`
	Followers int64  `json:"followers"`
	Following int64  `json:"following"`
	Likes     int64  `json:"likes"`
	Videos    int64  `json:"videos"`
	Friends   int64  `json:"friends"`
	CreatedAt int64  `json:"created_at"` // unix detik
	Profile   string `json:"profile"`
}

// ExtractUsername mengambil nama pengguna dari URL profil, "@nama", atau nama polos.
func ExtractUsername(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if idx := strings.Index(raw, "/@"); idx >= 0 {
		raw = raw[idx+2:]
	} else {
		raw = strings.TrimPrefix(raw, "@")
	}
	if cut := strings.IndexAny(raw, "/?#"); cut >= 0 {
		raw = raw[:cut]
	}
	raw = strings.TrimSuffix(raw, "/")

	if !usernamePattern.MatchString(raw) {
		return ""
	}
	return raw
}

// FetchProfile mengambil informasi publik sebuah akun TikTok dari halaman profil.
// Trailing slash dan parameter lang diperlukan agar halaman tidak ditolak WAF.
func FetchProfile(ctx context.Context, raw string) (*Profile, error) {
	username := ExtractUsername(raw)
	if username == "" {
		return nil, errors.New("username TikTok tidak valid")
	}

	endpoint := "https://www.tiktok.com/@" + username + "/?lang=en"
	body, status, err := getBody(ctx, endpoint, browserUA, "https://www.tiktok.com/")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrUserNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("halaman profil membalas HTTP %d", status)
	}

	match := universalPattern.FindSubmatch(body)
	if match == nil {
		return nil, errors.New("halaman profil tidak memuat data akun")
	}

	var state struct {
		Scope map[string]json.RawMessage `json:"__DEFAULT_SCOPE__"`
	}
	if err := json.Unmarshal(match[1], &state); err != nil {
		return nil, fmt.Errorf("gagal membaca state halaman profil: %w", err)
	}

	rawDetail, ok := state.Scope["webapp.user-detail"]
	if !ok {
		return nil, ErrUserNotFound
	}

	var detail userDetail
	if err := json.Unmarshal(rawDetail, &detail); err != nil {
		return nil, fmt.Errorf("gagal membaca data akun: %w", err)
	}
	if detail.UserInfo == nil || detail.UserInfo.User == nil || detail.UserInfo.User.UniqueID == "" {
		return nil, ErrUserNotFound
	}

	profile := detail.toProfile()
	if !strings.EqualFold(profile.Username, username) {
		profile.Username = username
	}
	profile.Profile = "https://www.tiktok.com/@" + profile.Username
	return profile, nil
}

// FormatProfile merangkum profil menjadi teks siap kirim.
func FormatProfile(p *Profile) string {
	if p == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("*TIKTOK USER INFORMATION*\n\n*ACCOUNT*")
	sb.WriteString("\n• Username : @" + p.Username)

	if p.Nickname != "" {
		sb.WriteString("\n• Nickname : " + p.Nickname)
	}
	if p.ID != "" {
		sb.WriteString("\n• User ID : " + p.ID)
	}
	if p.Language != "" {
		sb.WriteString("\n• Bahasa : " + p.Language)
	}
	sb.WriteString("\n• Terverifikasi : " + yesNo(p.Verified))
	sb.WriteString("\n• Akun Privat : " + yesNo(p.Private))

	if p.CreatedAt > 0 {
		sb.WriteString("\n• Umur Akun : " + formatAccountAge(p.CreatedAt))
		sb.WriteString("\n• Dibuat : " + formatUploadDate(p.CreatedAt))
	}
	if p.BioLink != "" {
		sb.WriteString("\n• Bio Link : " + p.BioLink)
	}

	profileURL := p.Profile
	if profileURL == "" {
		profileURL = "https://www.tiktok.com/@" + p.Username
	}
	sb.WriteString("\n• Profil : " + profileURL)

	sb.WriteString("\n\n*STATISTICS*")
	sb.WriteString("\n• Pengikut : " + formatThousands(p.Followers))
	sb.WriteString("\n• Mengikuti : " + formatThousands(p.Following))
	sb.WriteString("\n• Total Suka : " + formatThousands(p.Likes))
	sb.WriteString("\n• Total Video : " + formatThousands(p.Videos))
	sb.WriteString("\n• Teman : " + formatThousands(p.Friends))

	sb.WriteString("\n\n*BIO / SIGNATURE*\n")
	if sig := strings.TrimSpace(p.Signature); sig != "" {
		sb.WriteString(sig)
	} else {
		sb.WriteString("_Tidak ada bio_")
	}

	return sb.String()
}

func yesNo(v bool) string {
	if v {
		return "Ya"
	}
	return "Tidak"
}

func formatAccountAge(createdUnix int64) string {
	created := time.Unix(createdUnix, 0).UTC()
	if created.After(time.Now()) {
		return "0 Hari"
	}

	days := int64(time.Since(created).Hours() / 24)
	years := float64(days) / 365.25
	return fmt.Sprintf("%s Hari (~%.1f Tahun)", formatThousands(days), years)
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

type userDetail struct {
	StatusCode int `json:"statusCode"`
	UserInfo   *struct {
		User  *webUser     `json:"user"`
		Stats *webUserStat `json:"stats"`
	} `json:"userInfo"`
}

type webUser struct {
	ID             string    `json:"id"`
	UniqueID       string    `json:"uniqueId"`
	Nickname       string    `json:"nickname"`
	Signature      string    `json:"signature"`
	Verified       bool      `json:"verified"`
	PrivateAccount bool      `json:"privateAccount"`
	Language       string    `json:"language"`
	CreateTime     flexInt64 `json:"createTime"`
	AvatarLarger   string    `json:"avatarLarger"`
	AvatarMedium   string    `json:"avatarMedium"`
	AvatarThumb    string    `json:"avatarThumb"`
	BioLink        *struct {
		Link string `json:"link"`
	} `json:"bioLink"`
}

type webUserStat struct {
	FollowerCount  flexInt64 `json:"followerCount"`
	FollowingCount flexInt64 `json:"followingCount"`
	HeartCount     flexInt64 `json:"heartCount"`
	VideoCount     flexInt64 `json:"videoCount"`
	FriendCount    flexInt64 `json:"friendCount"`
}

func (d userDetail) toProfile() *Profile {
	u := d.UserInfo.User
	stats := d.UserInfo.Stats
	if stats == nil {
		stats = &webUserStat{}
	}

	profile := &Profile{
		ID:        u.ID,
		Username:  u.UniqueID,
		Nickname:  u.Nickname,
		Avatar:    firstNonEmpty(u.AvatarLarger, u.AvatarMedium, u.AvatarThumb),
		Signature: u.Signature,
		Verified:  u.Verified,
		Private:   u.PrivateAccount,
		Language:  u.Language,
		Followers: int64(stats.FollowerCount),
		Following: int64(stats.FollowingCount),
		Likes:     int64(stats.HeartCount),
		Videos:    int64(stats.VideoCount),
		Friends:   int64(stats.FriendCount),
		CreatedAt: int64(u.CreateTime),
	}

	if u.BioLink != nil {
		profile.BioLink = strings.TrimSpace(u.BioLink.Link)
	}
	return profile
}
