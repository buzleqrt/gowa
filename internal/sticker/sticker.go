package sticker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"gowa/internal/command"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

const (
	maxAudioBytes    = 16 << 20 // batas media audio/video sumber
	maxCoverBytes    = 25 << 20 // batas unduhan gambar/animation dari link
	staticMaxBytes   = 100 << 10
	animatedMaxBytes = 500 << 10
	stickerSize      = 512
	defaultImgPath   = "data/sticker.webp"
	uploadTimeout    = 2 * time.Minute
	httpTimeout      = 30 * time.Second
)

var (
	ErrNoAudio    = errors.New("tidak ada audio pada pesan yang di-reply")
	ErrNoFFmpeg   = errors.New("ffmpeg tidak tersedia di server, tidak bisa membuat stiker")
	ErrImageEmpty = errors.New("berkas gambar stiker kosong")
	ErrNoLink     = errors.New("link harus diawali http:// atau https://")
)

// Usage adalah teks bantuan perintah stiker audio.
const Usage = "*Cara pakai:*\n" +
	"• Reply voice note / audio / mp3 lalu `.audiosticker`\n" +
	"• Reply video pendek lalu `.audiosticker` — gerakan dan suaranya diambil dari video itu\n" +
	"• Reply audio, lalu kirim foto/video dengan caption `.audiosticker` — gambarnya dari media itu\n" +
	"• Kirim video dengan caption `.audiosticker` — audio dan gerakan sekaligus dari video itu\n" +
	"• Reply audio lalu `.audiosticker <link>` — gambar dari link " +
	"(jpg/png/webp/gif/mp4/stiker webp animasi), audionya tetap dari pesan yang di-reply\n\n" +
	"Stiker animasi otomatis kalau sumber gambarnya bergerak. Gambar bawaan bisa diganti " +
	"lewat env `AUDIOSTICKER_IMG` (default `" + defaultImgPath + "`)."

// audioFileExt adalah ekstensi berkas audio yang diterima saat dikirim sebagai dokumen.
var audioFileExt = map[string]bool{
	".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".oga": true,
	".opus": true, ".wav": true, ".flac": true, ".amr": true, ".wma": true,
}

// videoFileExt adalah ekstensi yang dianggap sumber bergerak (stiker animasi).
var videoFileExt = map[string]bool{
	".mp4": true, ".m4v": true, ".webm": true, ".mov": true, ".mkv": true,
	".gif": true, ".avi": true,
}

// Send membangun dan mengirim stiker bergambar dengan audio dari media yang di-reply.
func Send(c *command.Context) error {
	quoted := c.GetQuotedMessage()
	cmdMsg := command.UnwrapMessage(c.Event.Message)

	base := c.Ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, uploadTimeout)
	defer cancel()

	var (
		audioData  []byte
		audioMime  string
		seconds    uint32
		coverData  []byte
		coverMoves bool
	)

	// Media yang dikirim bersama perintah (foto/video dengan caption
	// `.audiosticker`) dipakai sebagai gambar stiker, tanpa perlu link.
	if img := cmdMsg.GetImageMessage(); img != nil {
		raw, err := c.DownloadMedia(cmdMsg)
		if err != nil {
			return c.Reply(fmt.Sprintf("*Gagal mengunduh gambar:*\n%v", err))
		}
		coverData = raw
	} else if vid := cmdMsg.GetVideoMessage(); vid != nil {
		raw, err := c.DownloadMedia(cmdMsg)
		if err != nil {
			return c.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
		}
		coverData, coverMoves = raw, true
	}

	if quoted != nil {
		if mimeType, secs, ok := quotedAudio(quoted); ok {
			raw, err := c.DownloadMedia(quoted)
			if err != nil {
				return c.Reply(fmt.Sprintf("*Gagal mengunduh audio:*\n%v", err))
			}
			audioData, audioMime, seconds = raw, mimeType, secs
		} else if vid := quoted.GetVideoMessage(); vid != nil {
			raw, err := c.DownloadMedia(quoted)
			if err != nil {
				return c.Reply(fmt.Sprintf("*Gagal mengunduh video:*\n%v", err))
			}
			audioData, audioMime, seconds = raw, vid.GetMimetype(), vid.GetSeconds()
			if coverData == nil {
				coverData, coverMoves = raw, true
			}
		}
	}

	// Video yang dikirim sebagai pesan perintah bisa jadi sumber audio sekaligus gambar.
	if audioData == nil && coverMoves {
		audioData, audioMime = coverData, "video/mp4"
	}

	if audioData == nil {
		return c.Reply(Usage)
	}

	if len(audioData) == 0 {
		return c.Reply(fmt.Sprintf("*Gagal membuat stiker:*\n%v", ErrNoAudio))
	}
	if len(audioData) > maxAudioBytes {
		return c.Reply("*Media terlalu besar* (maksimal 16 MB).")
	}

	c.Typing()

	audioData, err := normalizeAudio(ctx, audioData, audioMime)
	if err != nil {
		return c.Reply(fmt.Sprintf("*Gagal menyiapkan audio:*\n%v", err))
	}

	if seconds == 0 {
		seconds = durationSeconds(ctx, audioData)
	}

	if link := strings.TrimSpace(c.RawArgs); link != "" {
		data, moves, err := fetchSource(ctx, link)
		if err != nil {
			return c.Reply(fmt.Sprintf("*Gagal mengambil link:*\n%v", err))
		}
		coverData, coverMoves = data, moves
	}

	if coverData == nil {
		coverData, err = os.ReadFile(stickerImagePath())
		if err != nil {
			return c.Reply(fmt.Sprintf("*Gagal membaca gambar stiker:*\n%v", err))
		}
	}
	if len(coverData) == 0 {
		return c.Reply(fmt.Sprintf("*Gagal menyiapkan gambar stiker:*\n%v", ErrImageEmpty))
	}

	imageData, animated, err := coverWebp(ctx, coverData, coverMoves)
	if err != nil {
		return c.Reply(fmt.Sprintf("*Gagal menyiapkan gambar stiker:*\n%v", err))
	}

	imgUp, err := c.Client.Upload(ctx, imageData, whatsmeow.MediaImage)
	if err != nil {
		return c.Reply(fmt.Sprintf("*Gagal upload gambar stiker:*\n%v", err))
	}

	audioUp, err := c.Client.Upload(ctx, audioData, whatsmeow.MediaAudio)
	if err != nil {
		return c.Reply(fmt.Sprintf("*Gagal upload audio stiker:*\n%v", err))
	}

	msg := BuildMessage(imgUp, audioUp, seconds, animated)
	if ci := c.QuotedContext(); ci != nil {
		msg.GetStickerMessage().ContextInfo = ci
	}

	if err := c.SendMessage(msg); err != nil {
		return c.Reply(fmt.Sprintf("*Gagal mengirim stiker:*\n%v", err))
	}
	return nil
}

// BuildMessage merangkai stiker bergambar + audio memakai media yang sudah diunggah.
//
// Baileys melakukan ini dengan menambal encoder protobuf; di whatsmeow kedua field
// sudah tersedia natively: StickerMessage.audioMessage (oneof audio, tag 26) dan
// Message.audioStickerMessage (FutureProofMessage, tag 134).
func BuildMessage(img whatsmeow.UploadResponse, audio whatsmeow.UploadResponse, seconds uint32, animated bool) *waProto.Message {
	sticker := &waProto.StickerMessage{
		URL:           &img.URL,
		DirectPath:    &img.DirectPath,
		MediaKey:      img.MediaKey,
		FileEncSHA256: img.FileEncSHA256,
		FileSHA256:    img.FileSHA256,
		FileLength:    &img.FileLength,
		Mimetype:      proto.String("image/webp"),
		IsAnimated:    proto.Bool(animated),
		Width:         proto.Uint32(stickerSize),
		Height:        proto.Uint32(stickerSize),
		Audio: &waProto.StickerMessage_AudioMessage{
			AudioMessage: &waProto.AudioMessage{
				URL:           &audio.URL,
				DirectPath:    &audio.DirectPath,
				MediaKey:      audio.MediaKey,
				FileEncSHA256: audio.FileEncSHA256,
				FileSHA256:    audio.FileSHA256,
				FileLength:    &audio.FileLength,
				Mimetype:      proto.String("audio/ogg; codecs=opus"),
				PTT:           proto.Bool(true),
				Seconds:       proto.Uint32(seconds),
			},
		},
	}

	return &waProto.Message{
		StickerMessage:      sticker,
		AudioStickerMessage: &waProto.FutureProofMessage{Message: &waProto.Message{StickerMessage: sticker}},
		MessageContextInfo:  &waProto.MessageContextInfo{},
	}
}

// quotedAudio mengambil audio dari pesan yang di-reply, baik voice note, audio
// biasa, maupun berkas audio yang dikirim sebagai dokumen.
func quotedAudio(msg *waProto.Message) (mimeType string, seconds uint32, ok bool) {
	if audio := msg.GetAudioMessage(); audio != nil {
		return audio.GetMimetype(), audio.GetSeconds(), true
	}

	doc := msg.GetDocumentMessage()
	if doc == nil {
		return "", 0, false
	}

	mimeType = strings.ToLower(strings.TrimSpace(doc.GetMimetype()))
	if strings.HasPrefix(mimeType, "audio/") {
		return mimeType, 0, true
	}

	name := strings.ToLower(doc.GetFileName())
	if i := strings.LastIndex(name, "."); i >= 0 && audioFileExt[name[i:]] {
		return mimeType, 0, true
	}

	return "", 0, false
}

// normalizeAudio memastikan audio berformat ogg/opus supaya bisa diputar WhatsApp.
// Sumber video juga diterima: ffmpeg mengambil track audionya saja.
func normalizeAudio(ctx context.Context, data []byte, mimeType string) ([]byte, error) {
	low := strings.ToLower(mimeType)
	if strings.HasPrefix(low, "audio/ogg") && strings.Contains(low, "opus") {
		return data, nil
	}
	if bytes.HasPrefix(data, []byte("OggS")) {
		return data, nil
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, ErrNoFFmpeg
	}

	in, cleanup, err := writeTempInput(data)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", in,
		"-vn", "-c:a", "libopus", "-b:a", "32k", "-ar", "48000", "-ac", "1",
		"-f", "ogg", "pipe:1",
	)

	out, stderr, err := runFFmpeg(cmd)
	if err != nil {
		return nil, fmt.Errorf("konversi audio ke opus gagal: %s", ffmpegError(err, stderr))
	}
	if len(out) == 0 {
		return nil, errors.New("konversi audio ke opus menghasilkan data kosong")
	}
	return out, nil
}

// stickerImagePath mengembalikan gambar stiker bawaan (env AUDIOSTICKER_IMG).
func stickerImagePath() string {
	if p := strings.TrimSpace(os.Getenv("AUDIOSTICKER_IMG")); p != "" {
		return p
	}
	return defaultImgPath
}

// sniffExt menebak ekstensi berkas dari magic bytes-nya.
func sniffExt(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("RIFF")) && len(data) > 12 && bytes.HasPrefix(data[8:], []byte("WEBP")):
		return ".webp"
	case bytes.HasPrefix(data, []byte("RIFF")) && len(data) > 12 && bytes.HasPrefix(data[8:], []byte("WAVE")):
		return ".wav"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return ".gif"
	case bytes.HasPrefix(data, []byte("\x89PNG")):
		return ".png"
	case bytes.HasPrefix(data, []byte("\xff\xd8")):
		return ".jpg"
	case bytes.HasPrefix(data, []byte("OggS")):
		return ".ogg"
	case bytes.HasPrefix(data, []byte("fLaC")):
		return ".flac"
	case bytes.HasPrefix(data, []byte("ID3")):
		return ".mp3"
	case len(data) > 12 && bytes.HasPrefix(data[4:], []byte("ftyp")):
		return ".mp4"
	case bytes.HasPrefix(data, []byte("\x1a\x45\xdf\xa3")):
		return ".webm"
	default:
		return ".bin"
	}
}

// isAnimatedWebP memeriksa chunk ANIM pada header webp.
func isAnimatedWebP(data []byte) bool {
	if len(data) < 16 || !bytes.HasPrefix(data, []byte("RIFF")) {
		return false
	}
	head := data
	if len(head) > 64 {
		head = head[:64]
	}
	return bytes.Contains(head, []byte("ANIM"))
}

func isGIF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("GIF8"))
}

// normalizeLink merapikan link yang ditempel pengguna: tanda kutip/spasi
// berlebih, dan skema https:// yang lupa ditulis.
func normalizeLink(link string) string {
	link = strings.TrimSpace(link)
	link = strings.Trim(link, "<>\"'`")
	link = strings.TrimRight(link, ".,;")

	lower := strings.ToLower(link)
	if strings.HasPrefix(lower, "www.") {
		return "https://" + link
	}
	return link
}

// isMovingMedia mendeteksi media bergerak dari isi berkas, bukan dari header
// Content-Type yang sering salah di CDN.
func isMovingMedia(data []byte) bool {
	switch {
	case isGIF(data), isAnimatedWebP(data):
		return true
	case len(data) > 12 && bytes.HasPrefix(data[4:], []byte("ftyp")):
		return true
	case bytes.HasPrefix(data, []byte("\x1a\x45\xdf\xa3")):
		return true
	case bytes.HasPrefix(data, []byte("RIFF")) && len(data) > 12 && (bytes.HasPrefix(data[8:], []byte("AVI "))):
		return true
	}
	return false
}

// fetchSource mengunduh gambar/video dari link dan menandai apakah isinya bergerak.
func fetchSource(ctx context.Context, link string) ([]byte, bool, error) {
	link = normalizeLink(link)
	lower := strings.ToLower(link)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return nil, false, ErrNoLink
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, link, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,video/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("server membalas %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxCoverBytes {
		return nil, false, fmt.Errorf("file dari link lebih dari %d MB", maxCoverBytes>>20)
	}
	if len(data) == 0 {
		return nil, false, errors.New("link mengembalikan data kosong")
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "text/html") || strings.HasPrefix(contentType, "application/json") ||
		strings.HasPrefix(contentType, "text/plain") {
		return nil, false, errors.New("link bukan berkas gambar/video langsung (mungkin halaman web)")
	}

	urlPath := strings.ToLower(path.Ext(strings.SplitN(link, "?", 2)[0]))

	moving := isMovingMedia(data) ||
		strings.HasPrefix(contentType, "video/") ||
		strings.Contains(contentType, "gif") ||
		videoFileExt[urlPath]

	return data, moving, nil
}
