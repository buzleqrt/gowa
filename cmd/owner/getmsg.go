package owner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gowa/internal/command"
	"gowa/internal/storage"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	// Batas atas jumlah pesan per file, supaya tidak menulis berkas raksasa.
	maxDumpMessages = 500
	maxDumpBytes    = 40 << 20
)

var lidPattern = regexp.MustCompile(`[A-Za-z0-9.\-]+@lid`)

func getMsgHelp(prefix string) string {
	return "> Bongkar pesan tersimpan menjadi file `.js`.\n\n" +
		"*Keterangan Format:*\n" +
		"> `" + prefix + "getmsg <jumlah>` = N pesan terakhir di chat ini\n" +
		"> `" + prefix + "getmsg <jumlah> <target>` = N pesan dari orang tertentu\n\n" +
		"target bisa dari pesan yang di-reply, @mention, atau nomor yang diketik.\n\n" +
		"contoh penggunaan:\n" +
		"> `" + prefix + "getmsg 10`\n" +
		"> `" + prefix + "getmsg 50`\n" +
		"> `" + prefix + "getmsg 20` (reply pesan)\n" +
		"> `" + prefix + "getmsg 20 @628xxx`\n" +
		"> `" + prefix + "getmsg 20 628xxx`\n\n" +
		"File dikirim sebagai dokumen dan otomatis dihapus setelah terkirim."
}

func init() {
	command.Register(&command.Command{
		Name:    "getmsg",
		Cmd:     "getmsg",
		Categ:   "owner",
		Desc:    "Bongkar N pesan tersimpan menjadi file .js (Khusus Owner)",
		Execute: handleGetMsg,
	})
}

type dumpKey struct {
	RemoteJID   string `json:"remoteJid"`
	FromMe      bool   `json:"fromMe"`
	ID          string `json:"id"`
	Participant string `json:"participant,omitempty"`
}

type dumpQuoted struct {
	ID     string  `json:"id"`
	Sender string  `json:"sender"`
	Type   *string `json:"type"`
	Text   *string `json:"text"`
}

type dumpEntry struct {
	Rowid           int64           `json:"rowid"`
	Index           int             `json:"index"`
	Chat            string          `json:"chat"`
	ID              string          `json:"id"`
	Sender          string          `json:"sender"`
	PushName        *string         `json:"pushName"`
	Timestamp       int64           `json:"timestamp"`
	Key             dumpKey         `json:"key"`
	Message         json.RawMessage `json:"message"`
	AdditionalNodes []any           `json:"additionalNodes"`
	SenderLid       *string         `json:"senderLid"`
	Quoted          *dumpQuoted     `json:"quoted"`
}

func handleGetMsg(ctx *command.Context) error {
	if !ctx.IsOwner {
		return nil
	}
	if ctx.Config == nil {
		return ctx.Reply("❌ Konfigurasi bot tidak tersedia.")
	}
	if len(ctx.Args) == 0 {
		return ctx.Reply(getMsgHelp(ctx.BotPrefix))
	}

	limit, err := strconv.Atoi(ctx.Args[0])
	if err != nil || limit < 1 {
		return ctx.Reply("*Masukkan jumlah pesan.*\n\ncontoh: `" + ctx.BotPrefix + "getmsg 10`")
	}
	if limit > maxDumpMessages {
		limit = maxDumpMessages
	}

	store, err := storage.OpenMessageStore(ctx.Config.DBPath)
	if err != nil {
		return ctx.Reply("❌ Gagal membuka store pesan: " + err.Error())
	}

	dump, err := buildDump(ctx, store, ctx.Chat.String(), limit)
	if err != nil {
		return ctx.Reply(err.Error())
	}

	filePath := filepath.Join(os.TempDir(), dump.FileName)
	if err := os.WriteFile(filePath, dump.Body, 0o644); err != nil {
		return ctx.Reply("❌ Gagal menulis file dump: " + err.Error())
	}
	defer os.Remove(filePath)

	info := fmt.Sprintf(
		"• Target : %s\n• Total  : %d pesan\n• File   : %s",
		dump.Target, dump.Total, dump.FileName,
	)

	return ctx.ReplyDocument(dump.Body, dump.FileName, "application/javascript", info)
}

type dumpResult struct {
	Body     []byte
	FileName string
	Total    int
	Target   string
}

// buildDump menyusun isi file `.js` dari arsip pesan.
func buildDump(ctx *command.Context, store *storage.MessageStore, chat string, limit int) (*dumpResult, error) {
	target := extractTarget(ctx, ctx.Args[1:])
	scan := limit * 3

	var scoped []storage.Message
	targetLabel := ""
	if !target.IsEmpty() {
		for _, candidate := range senderCandidates(ctx, target) {
			rows, err := store.BySenders(chat, []string{candidate}, limit)
			if err != nil {
				return nil, fmt.Errorf("❌ Gagal membaca store pesan: %v", err)
			}
			if filtered := filterDumpable(rows); len(filtered) > 0 {
				scoped = filtered
				break
			}
		}
		if len(scoped) == 0 {
			return nil, fmt.Errorf(
				"❌ Tidak ada pesan dari %s di chat ini.\n\n_Kalau targetnya LID, pastikan nomor pengirimnya sudah dikenal bot._",
				plainNumber(target.String()),
			)
		}
		targetLabel = plainNumber(target.String())
	} else {
		// Hanya baris percakapan yang dihitung, sehingga jendela pemindaian
		// tidak habis oleh pesan sistem atau perintah bot sendiri.
		rows, err := store.LatestConversation(chat, scan)
		if err != nil {
			return nil, fmt.Errorf("❌ Gagal membaca store pesan: %v", err)
		}

		rows = excludeMessages(rows, botIdentities(ctx))
		rows = filterDumpable(rows)
		if len(rows) == 0 {
			return nil, fmt.Errorf("❌ Tidak ada pesan tersimpan di %s", chat)
		}

		// Mode tanpa target: pengirim pesan terbaru jadi acuan, lalu pesan
		// pengirim itu diambil sampai jumlah yang diminta walaupun jendela
		// pemindaian lebih sempit.
		newest := rows[0]
		resolved := senderKey(&newest)
		if resolved == "" {
			return nil, fmt.Errorf("❌ Pengirim pesan terbaru tidak bisa dibaca.")
		}
		bentuk := []string{resolved}
		if pn := normalizeJID(newest.SenderPN); pn != "" && pn != resolved {
			bentuk = append(bentuk, pn)
		}
		scoped, err = store.BySenders(chat, bentuk, limit)
		if err != nil {
			return nil, fmt.Errorf("❌ Gagal membaca store pesan: %v", err)
		}
		scoped = filterDumpable(scoped)
		if len(scoped) == 0 {
			return nil, fmt.Errorf("❌ Pengirim pesan terbaru tidak bisa dibaca.")
		}
		targetLabel = plainNumber(chat)
	}

	if len(scoped) > limit {
		scoped = scoped[:limit]
	}

	entries := make([]dumpEntry, 0, len(scoped))
	for i := range scoped {
		entry, err := buildDumpEntry(ctx, chat, scoped[i], i+1)
		if err != nil {
			return nil, fmt.Errorf("❌ Gagal menyusun dump: %v", err)
		}
		entries = append(entries, entry)
	}

	payload, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("❌ Gagal menyusun dump: %v", err)
	}

	fileName := fmt.Sprintf("store-%d.js", entries[0].Rowid)
	body := []byte("module.exports = " + string(payload) + ";\n")
	if len(body) > maxDumpBytes {
		return nil, fmt.Errorf(
			"❌ Hasil dump terlalu besar (%s), batas %s. Kurangi jumlah pesan.",
			fileSize(int64(len(body))), fileSize(maxDumpBytes),
		)
	}

	return &dumpResult{Body: body, FileName: fileName, Total: len(entries), Target: targetLabel}, nil
}

// extractTarget mengambil satu target dari reply, mention, atau nomor yang
// diketik — berurutan seperti itu.
func extractTarget(ctx *command.Context, args []string) types.JID {
	if ci := ctx.GetContextInfo(); ci != nil {
		quoted := ci.GetParticipant()
		if quoted == "" && !ctx.IsGroup {
			quoted = ctx.Chat.String()
		}
		if jid, ok := parseUserJID(quoted); ok {
			return jid
		}

		for _, raw := range ci.GetMentionedJID() {
			if jid, ok := parseUserJID(raw); ok {
				return jid
			}
		}
	}

	for _, arg := range args {
		raw := strings.TrimPrefix(strings.TrimSpace(arg), "@")
		if jid, ok := parseUserJID(raw); ok {
			return jid
		}
	}

	return types.EmptyJID
}

func parseUserJID(value string) (types.JID, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "@")
	if value == "" {
		return types.EmptyJID, false
	}

	if isDigits(value) {
		return types.NewJID(value, types.DefaultUserServer), true
	}

	jid, err := types.ParseJID(value)
	if err != nil || jid.IsEmpty() {
		return types.EmptyJID, false
	}
	if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
		return types.EmptyJID, false
	}
	return jid, true
}

// senderCandidates mencoba bentuk JID yang diminta lalu pasangannya (LID ↔
// nomor), karena reply/tag sering memberi LID sementara arsip bisa menyimpan
// salah satunya.
func senderCandidates(ctx *command.Context, target types.JID) []string {
	target = target.ToNonAD()
	list := []string{target.String()}

	if ctx.Client == nil || ctx.Client.Store == nil {
		return list
	}

	if target.Server == types.HiddenUserServer {
		if pn, err := ctx.ResolvePN(target); err == nil && !pn.IsEmpty() {
			list = append(list, pn.ToNonAD().String())
		}
	} else if lid, err := ctx.ResolveLID(target); err == nil && !lid.IsEmpty() {
		list = append(list, lid.ToNonAD().String())
	}

	return list
}

func buildDumpEntry(ctx *command.Context, chat string, m storage.Message, index int) (dumpEntry, error) {
	var rawJSON json.RawMessage
	var quoted *dumpQuoted

	if len(m.Raw) > 0 {
		var pb waProto.Message
		if err := proto.Unmarshal(m.Raw, &pb); err != nil {
			return dumpEntry{}, fmt.Errorf("pesan %s: %w", m.StanzaID, err)
		}
		encoded, err := protojson.Marshal(&pb)
		if err != nil {
			return dumpEntry{}, fmt.Errorf("pesan %s: %w", m.StanzaID, err)
		}
		rawJSON = encoded
		quoted = quotedSummary(&pb)
	} else {
		rawJSON = json.RawMessage("null")
	}

	key := dumpKey{RemoteJID: chat, FromMe: m.FromMe, ID: m.StanzaID}
	if m.IsGroup {
		key.Participant = m.Sender
	}

	var pushName *string
	if m.PushName != "" {
		push := m.PushName
		pushName = &push
	}

	return dumpEntry{
		Rowid:           m.Seq,
		Index:           index,
		Chat:            chat,
		ID:              m.StanzaID,
		Sender:          m.Sender,
		PushName:        pushName,
		Timestamp:       m.Timestamp / 1000,
		Key:             key,
		Message:         rawJSON,
		AdditionalNodes: []any{},
		SenderLid:       resolveSenderLID(ctx, m, rawJSON),
		Quoted:          quoted,
	}, nil
}

func quotedSummary(pb *waProto.Message) *dumpQuoted {
	ci := command.ContextInfoOf(command.UnwrapMessage(pb))
	if ci == nil || (ci.GetStanzaID() == "" && ci.GetQuotedMessage() == nil) {
		return nil
	}

	quoted := &dumpQuoted{ID: ci.GetStanzaID(), Sender: ci.GetParticipant()}
	if msg := command.UnwrapMessage(ci.GetQuotedMessage()); msg != nil {
		if kind, text := quotedContent(msg); kind != "" {
			quoted.Type = &kind
			if text != "" {
				quoted.Text = &text
			}
		}
	}
	return quoted
}

func quotedContent(msg *waProto.Message) (kind, text string) {
	switch {
	case msg.GetConversation() != "":
		return "conversation", msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		return "extendedTextMessage", msg.GetExtendedTextMessage().GetText()
	case msg.GetImageMessage() != nil:
		return "imageMessage", msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		return "videoMessage", msg.GetVideoMessage().GetCaption()
	case msg.GetAudioMessage() != nil:
		return "audioMessage", ""
	case msg.GetDocumentMessage() != nil:
		return "documentMessage", msg.GetDocumentMessage().GetCaption()
	case msg.GetStickerMessage() != nil:
		return "stickerMessage", ""
	}
	return "", ""
}

// resolveSenderLID mengembalikan LID pengirim. Arsip bisa menyimpan nomor saja,
// jadi pasangannya dicari lewat pemetaan LID bot; kalau tetap tidak ada, LID
// yang tersembunyi di payload pesan dipakai sebagai cadangan.
func resolveSenderLID(ctx *command.Context, m storage.Message, rawJSON []byte) *string {
	for _, candidate := range []string{m.Sender, m.SenderPN} {
		jid, err := types.ParseJID(candidate)
		if err != nil || jid.IsEmpty() {
			continue
		}
		if jid.Server == types.HiddenUserServer {
			lid := jid.ToNonAD().String()
			return &lid
		}
	}

	for _, candidate := range []string{m.SenderPN, m.Sender} {
		if ctx.Client == nil || ctx.Client.Store == nil {
			break
		}
		jid, err := types.ParseJID(candidate)
		if err != nil || jid.IsEmpty() || jid.Server != types.DefaultUserServer {
			continue
		}
		if lid, err := ctx.ResolveLID(jid); err == nil && !lid.IsEmpty() {
			value := lid.ToNonAD().String()
			return &value
		}
	}

	if match := lidPattern.Find(rawJSON); match != nil {
		value := string(match)
		return &value
	}
	return nil
}

// filterDumpable membuang pesan sistem (protokol, kunci grup, riwayat) yang
// bukan percakapan. Kolom msg_type diperiksa dulu, lalu isi payload-nya, karena
// baris lama bisa tercatat dengan tipe lain padahal isinya kunci grup.
func filterDumpable(rows []storage.Message) []storage.Message {
	out := make([]storage.Message, 0, len(rows))
	for _, row := range rows {
		if isSystemRow(row) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func isSystemRow(row storage.Message) bool {
	if row.Type == "system" || row.Type == "protocol" {
		return true
	}
	if len(row.Raw) == 0 {
		return false
	}

	var pb waProto.Message
	if err := proto.Unmarshal(row.Raw, &pb); err != nil {
		return false
	}

	return pb.GetProtocolMessage() != nil ||
		pb.GetDeviceSentMessage() != nil ||
		pb.GetBotInvokeMessage() != nil ||
		pb.GetSenderKeyDistributionMessage() != nil ||
		pb.GetFastRatchetKeySenderKeyDistributionMessage() != nil ||
		pb.GetMessageHistoryNotice() != nil ||
		pb.GetMessageHistoryBundle() != nil
}

func excludeMessages(rows []storage.Message, excluded map[string]bool) []storage.Message {
	out := make([]storage.Message, 0, len(rows))
	for _, row := range rows {
		if row.FromMe {
			continue
		}
		if excluded[normalizeJID(row.Sender)] || excluded[normalizeJID(row.SenderPN)] {
			continue
		}
		out = append(out, row)
	}
	return out
}

// botIdentities adalah JID akun bot sendiri dan pasangannya, supaya balasan
// bot tidak ikut ter-dump.
func botIdentities(ctx *command.Context) map[string]bool {
	out := map[string]bool{}
	if ctx.Client == nil || ctx.Client.Store == nil {
		return out
	}
	if ctx.Client.Store.ID != nil {
		out[normalizeJID(ctx.Client.Store.ID.String())] = true
	}
	if !ctx.Client.Store.LID.IsEmpty() {
		out[normalizeJID(ctx.Client.Store.LID.String())] = true
	}
	return out
}

func senderKey(m *storage.Message) string {
	if m.Sender != "" {
		return normalizeJID(m.Sender)
	}
	return normalizeJID(m.SenderPN)
}

func normalizeJID(jid string) string {
	return strings.Split(strings.TrimSpace(jid), ":")[0]
}

// plainNumber mengambil digit nomor dari sebuah JID; sufiks device dibuang
// dulu supaya `628123:5@s.whatsapp.net` tidak jadi `6281235`.
func plainNumber(jid string) string {
	withoutDevice := strings.Split(jid, ":")[0]
	var digits strings.Builder
	for _, r := range withoutDevice {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return "-"
	}
	return digits.String()
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
