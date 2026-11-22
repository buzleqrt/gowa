package instagram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// ErrSearchLoginRequired dikembalikan bila permintaan pencarian ditolak
// Instagram karena tidak ada sesi login yang valid.
var ErrSearchLoginRequired = errors.New("pencarian Instagram membutuhkan cookie sessionid akun (isi \"ig_sessionid\" di config.json)")

// ErrTagNotFound dikembalikan bila pencarian tidak menghasilkan media.
var ErrTagNotFound = errors.New("hasil pencarian Instagram tidak ditemukan")

const (
	// browserUA dipakai untuk jalur web karena Instagram hanya menyajikan data
	// pencarian ke klien yang terlihat seperti browser biasa.
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

	// igAppID dan igASBDID adalah application id web Instagram yang wajib
	// dikirim pada setiap permintaan API.
	igAppID   = "936619743392459"
	igASBDID  = "129477"
	igAppVer  = "17841474779645119"
	graphqlEP = "https://www.instagram.com/api/graphql"
	tagPageEP = "https://www.instagram.com/explore/tags/%s/"
	homeEP    = "https://www.instagram.com/"

	// keywordSearchDocID adalah doc_id query PolarisKeywordSearchExplorePageRelayQuery.
	// Query ini menggantikan endpoint web_info yang sudah dihapus Instagram.
	keywordSearchDocID        = "30002314729354907"
	keywordSearchFriendlyName = "PolarisKeywordSearchExplorePageRelayQuery"
)

var (
	sessionMu sync.RWMutex
	sessionID string

	lsdPattern   = regexp.MustCompile(`"LSD",\[\],\{"token":"([^"]+)"`)
	csrfPattern  = regexp.MustCompile(`"csrf_token":"([^"]+)"`)
	dtsgPattern  = regexp.MustCompile(`"DTSGInitialData",\[\],\{"token":"([^"]+)"`)
	claimPattern = regexp.MustCompile(`"claim":"([^"]+)"`)
	hsiPattern   = regexp.MustCompile(`"hsi":"(\d+)"`)
	datrPattern  = regexp.MustCompile(`"_js_datr":\{"value":"([^"]+)"`)
	igDIDPattern = regexp.MustCompile(`"_js_ig_did":\{"value":"([^"]+)"`)
)

// SetSessionID menyimpan nilai cookie sessionid akun Instagram yang dipakai
// untuk pencarian. Nilai boleh berupa nilai sessionid saja atau string cookie
// utuh (misalnya "sessionid=...; csrftoken=...").
func SetSessionID(value string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionID = normalizeSessionID(value)
}

func currentSessionID() string {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	return sessionID
}

func normalizeSessionID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "sessionid="); idx >= 0 {
		value = value[idx+len("sessionid="):]
		if end := strings.IndexAny(value, "; \t\r\n"); end >= 0 {
			value = value[:end]
		}
	}
	return strings.TrimSpace(value)
}

// dsUserID mengambil angka user id dari sessionid (bagian sebelum pemisah %3A).
func dsUserID() string {
	value := currentSessionID()
	if idx := strings.Index(value, "%3A"); idx > 0 {
		return value[:idx]
	}
	if idx := strings.Index(value, ":"); idx > 0 {
		return value[:idx]
	}
	return ""
}

// SearchResult adalah hasil pencarian Instagram, baik dari hashtag maupun dari
// postingan sebuah akun.
type SearchResult struct {
	Query string      `json:"query"`
	Tag   string      `json:"tag"`
	// Account berisi username bila hasil berasal dari postingan akun.
	Account string      `json:"account,omitempty"`
	Posts   []*Metadata `json:"posts"`
}

// Search mencari post Instagram berdasarkan kata kunci hashtag.
//
// Instagram tidak lagi menyediakan endpoint web_info untuk pencarian. Proses
// yang dipakai sekarang meniru halaman /explore/tags/<tag>/:
//  1. buka halaman tag untuk mengambil token lsd, csrf, fb_dtsg, dan claim;
//  2. kirim query GraphQL PolarisKeywordSearchExplorePageRelayQuery;
//  3. urai media hasilnya dengan parser yang sama seperti Fetch.
//
// Langkah 1 membutuhkan cookie sessionid karena Instagram memberi token claim
// hanya untuk pengunjung yang login.
func Search(ctx context.Context, query string, limit int) (*SearchResult, error) {
	tag := normalizeTag(query)
	if tag == "" {
		return nil, errors.New("kata kunci pencarian tidak valid")
	}
	limit = clampLimit(limit)
	if currentSessionID() == "" {
		return nil, ErrSearchLoginRequired
	}

	session, err := loadWebSession(ctx, fmt.Sprintf(tagPageEP, tag))
	if err != nil {
		return nil, err
	}

	root, err := session.keywordSearch(ctx, fmt.Sprintf(tagPageEP, tag), "#"+tag)
	if err != nil {
		return nil, err
	}

	posts := collectSearchPosts(root, limit)
	if len(posts) == 0 {
		return nil, ErrTagNotFound
	}

	return &SearchResult{Query: strings.TrimSpace(query), Tag: tag, Posts: posts}, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 1
	}
	if limit > 20 {
		return 20
	}
	return limit
}

// normalizeTag mengubah "anime", "#anime", atau "one piece" menjadi nama
// hashtag yang valid. Spasi dan tanda baca dibuang karena hashtag Instagram
// tidak boleh memuatnya.
func normalizeTag(query string) string {
	query = strings.TrimSpace(query)
	query = strings.TrimPrefix(query, "#")

	var b strings.Builder
	for _, r := range strings.ToLower(query) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		}
	}

	tag := b.String()
	if len(tag) > 100 {
		tag = tag[:100]
	}
	return tag
}

// webSession menyimpan token yang dibutuhkan untuk memanggil GraphQL, diambil
// dari HTML halaman Instagram yang dibuka dengan cookie sesi.
type webSession struct {
	LSD   string
	CSRF  string
	DTSG  string
	Claim string
	HSI   string
	Datr  string
	IGDID string
}

func loadWebSession(ctx context.Context, pageURL string) (*webSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req, pageURL)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Cookie", sessionCookie())

	// Jangan ikuti pengalihan: bila sesi tidak valid, Instagram mengalihkan
	// ke halaman login dan bisa berakhir pada redirect loop.
	client := *httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("membuka halaman Instagram: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrUserNotFound
	}
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {
		return nil, ErrSearchLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("halaman Instagram membalas HTTP %d", resp.StatusCode)
	}

	page := string(body)
	session := &webSession{
		LSD:   firstMatch(lsdPattern, page),
		CSRF:  firstMatch(csrfPattern, page),
		DTSG:  firstMatch(dtsgPattern, page),
		Claim: firstMatch(claimPattern, page),
		HSI:   firstMatch(hsiPattern, page),
		Datr:  firstMatch(datrPattern, page),
		IGDID: firstMatch(igDIDPattern, page),
	}

	// Claim hanya dikirim ke pengunjung yang login, jadi ketiadaannya berarti
	// sessionid tidak berlaku lagi.
	if session.LSD == "" || session.DTSG == "" || session.Claim == "" {
		return nil, ErrSearchLoginRequired
	}
	return session, nil
}

// keywordSearch menjalankan query pencarian kata kunci Instagram.
func (s *webSession) keywordSearch(ctx context.Context, referer, query string) (map[string]any, error) {
	requestID := randomID()
	variables := map[string]string{
		"query":             query,
		"search_session_id": requestID,
		"serp_session_id":   requestID,
	}
	return s.graphql(ctx, referer, keywordSearchDocID, keywordSearchFriendlyName, variables)
}

// graphql mengirim satu query Relay ke Instagram dan mengurai hasilnya.
func (s *webSession) graphql(ctx context.Context, referer, docID, friendlyName string, variables any) (map[string]any, error) {
	variablesJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("av", igAppVer)
	form.Set("__d", "www")
	form.Set("__user", "0")
	form.Set("__a", "1")
	form.Set("__req", "1")
	form.Set("dpr", "1")
	if s.HSI != "" {
		form.Set("__hsi", s.HSI)
	}
	form.Set("doc_id", docID)
	form.Set("fb_api_caller_class", "RelayModern")
	form.Set("fb_api_req_friendly_name", friendlyName)
	form.Set("server_timestamps", "true")
	form.Set("lsd", s.LSD)
	form.Set("fb_dtsg", s.DTSG)
	form.Set("jazoest", jazoest(s.DTSG))
	form.Set("variables", string(variablesJSON))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEP, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req, referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://www.instagram.com")
	req.Header.Set("X-CSRFToken", s.CSRF)
	req.Header.Set("X-FB-LSD", s.LSD)
	req.Header.Set("X-FB-Friendly-Name", friendlyName)
	req.Header.Set("X-IG-WWW-Claim", s.Claim)
	req.Header.Set("Cookie", s.cookieHeader())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mengirim permintaan Instagram: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrSearchLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("permintaan Instagram membalas HTTP %d", resp.StatusCode)
	}

	// Respons GraphQL web diawali penanda anti-JSON-hijacking.
	trimmed := strings.TrimPrefix(string(body), "for (;;);")

	var root map[string]any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return nil, fmt.Errorf("membaca hasil Instagram: %w", err)
	}

	if message := stringValue(root["message"]); message != "" {
		if loginRequired, _ := root["require_login"].(bool); loginRequired {
			return nil, ErrSearchLoginRequired
		}
		return nil, fmt.Errorf("permintaan Instagram gagal: %s", message)
	}
	return root, nil
}

// setBrowserHeaders melengkapi header yang diperiksa Instagram untuk memastikan
// permintaan berasal dari browser, bukan skrip.
func setBrowserHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", referer)
	req.Header.Set("X-IG-App-ID", igAppID)
	req.Header.Set("X-ASBD-ID", igASBDID)
	req.Header.Set("Sec-CH-UA", `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
}

// sessionCookie menyusun cookie untuk membuka halaman Instagram.
func sessionCookie() string {
	parts := []string{"sessionid=" + currentSessionID()}
	if id := dsUserID(); id != "" {
		parts = append(parts, "ds_user_id="+id)
	}
	return strings.Join(parts, "; ")
}

// cookieHeader melengkapi cookie dengan nilai dari halaman yang dibuka (csrf,
// datr, ig_did) supaya sidik klien GraphQL konsisten dengan halaman yang
// membuka tokennya.
func (s *webSession) cookieHeader() string {
	parts := []string{sessionCookie()}
	if s.CSRF != "" {
		parts = append(parts, "csrftoken="+s.CSRF)
	}
	if s.Datr != "" {
		parts = append(parts, "datr="+s.Datr)
	}
	if s.IGDID != "" {
		parts = append(parts, "ig_did="+s.IGDID)
	}
	return strings.Join(parts, "; ")
}

// collectSearchPosts menelusuri hasil GraphQL untuk mengambil objek media.
// Setiap node media berisi "code" serta image_versions2/video_versions, jadi
// penelusuran umum cukup untuk berbagai bentuk respons pencarian.
func collectSearchPosts(root map[string]any, limit int) []*Metadata {
	var posts []*Metadata
	seen := make(map[string]bool)

	add := func(m map[string]any) bool {
		code := stringValue(m["code"])
		if code == "" || seen[code] {
			return false
		}
		md := mediaToMetadata(m, code)
		if len(md.Items) == 0 {
			return false
		}
		seen[code] = true
		posts = append(posts, md)
		return len(posts) >= limit
	}

	walkForMedia(root, add)
	if len(posts) > limit {
		posts = posts[:limit]
	}
	return posts
}

func walkForMedia(node any, add func(map[string]any) bool) {
	switch value := node.(type) {
	case map[string]any:
		// Media carousel memuat media anak dengan kode berbeda; berhenti di
		// node media supaya satu post tidak terhitung beberapa kali.
		if looksLikeMedia(value) {
			add(value)
			return
		}
		for _, child := range value {
			walkForMedia(child, add)
		}
	case []any:
		for _, child := range value {
			walkForMedia(child, add)
		}
	}
}

func looksLikeMedia(m map[string]any) bool {
	if _, ok := m["code"]; !ok {
		return false
	}
	for _, key := range []string{"image_versions2", "video_versions", "carousel_media"} {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func firstMatch(pattern *regexp.Regexp, value string) string {
	if m := pattern.FindStringSubmatch(value); m != nil {
		return m[1]
	}
	return ""
}

// jazoest diturunkan dari fb_dtsg dengan menjumlahkan kode setiap karakternya,
// lalu diberi awalan "2". Instagram menolak permintaan tanpa nilai ini.
func jazoest(dtsg string) string {
	sum := 0
	for _, r := range dtsg {
		sum += int(r)
	}
	return "2" + fmt.Sprint(sum)
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(buf)
}
