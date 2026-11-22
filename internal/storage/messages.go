package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

type Message struct {
	Seq       int64
	Chat      string
	StanzaID  string
	Sender    string
	SenderPN  string
	PushName  string
	IsGroup   bool
	FromMe    bool
	Timestamp int64
	Type      string
	Text      string
	Raw       []byte
}

type MessageStore struct {
	db *sql.DB
}

var (
	messageMu     sync.Mutex
	messageStores = map[string]*MessageStore{}
)

const messageSchema = `
CREATE TABLE IF NOT EXISTS messages (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	chat TEXT NOT NULL,
	stanza_id TEXT NOT NULL,
	sender TEXT NOT NULL DEFAULT '',
	sender_pn TEXT NOT NULL DEFAULT '',
	push_name TEXT NOT NULL DEFAULT '',
	is_group INTEGER NOT NULL DEFAULT 0,
	from_me INTEGER NOT NULL DEFAULT 0,
	ts INTEGER NOT NULL,
	msg_type TEXT NOT NULL DEFAULT '',
	text TEXT NOT NULL DEFAULT '',
	raw BLOB,
	UNIQUE(chat, stanza_id)
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages(chat, ts DESC, seq DESC);
CREATE INDEX IF NOT EXISTS idx_messages_sender_ts ON messages(chat, sender, ts DESC);
CREATE INDEX IF NOT EXISTS idx_messages_sender_pn_ts ON messages(chat, sender_pn, ts DESC);`

func OpenMessageStore(dbPath string) (*MessageStore, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("path database kosong")
	}

	messageMu.Lock()
	defer messageMu.Unlock()

	if store, ok := messageStores[dbPath]; ok {
		return store, nil
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka database pesan: %w", err)
	}
	if _, err := db.Exec(messageSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("gagal menyiapkan tabel messages: %w", err)
	}

	_, _ = db.Exec(`ALTER TABLE messages ADD COLUMN push_name TEXT NOT NULL DEFAULT ''`)

	store := &MessageStore{db: db}
	messageStores[dbPath] = store
	return store, nil
}

const messageColumns = `seq, chat, stanza_id, sender, sender_pn, push_name, is_group, from_me, ts, msg_type, text, raw`

func (s *MessageStore) Save(m *Message) error {
	if m == nil {
		return fmt.Errorf("pesan kosong")
	}
	chat := strings.TrimSpace(m.Chat)
	stanzaID := strings.TrimSpace(m.StanzaID)
	if chat == "" || stanzaID == "" {
		return fmt.Errorf("chat dan stanza ID wajib diisi")
	}

	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO messages
			(chat, stanza_id, sender, sender_pn, push_name, is_group, from_me, ts, msg_type, text, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		chat, stanzaID, m.Sender, m.SenderPN, m.PushName, boolToInt(m.IsGroup), boolToInt(m.FromMe),
		m.Timestamp, m.Type, m.Text, m.Raw,
	)
	if err != nil {
		return fmt.Errorf("gagal menyimpan pesan: %w", err)
	}
	return nil
}

func (s *MessageStore) GetByStanzaID(chat, stanzaID string) (*Message, error) {
	row := s.db.QueryRow(
		`SELECT `+messageColumns+` FROM messages WHERE chat = ? AND stanza_id = ?`,
		strings.TrimSpace(chat), strings.TrimSpace(stanzaID),
	)
	return scanMessage(row)
}

func (s *MessageStore) Latest(chat string, n int) ([]Message, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT `+messageColumns+` FROM messages WHERE chat = ?
		 ORDER BY ts DESC, seq DESC LIMIT ?`,
		strings.TrimSpace(chat), n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *MessageStore) LatestConversation(chat string, n int) ([]Message, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT `+messageColumns+` FROM messages WHERE chat = ?
		 AND from_me = 0 AND msg_type NOT IN ('system', 'protocol')
		 ORDER BY ts DESC, seq DESC LIMIT ?`,
		strings.TrimSpace(chat), n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

const deviceSuffixExpr = `CASE WHEN instr(%s, ':') > 0
	THEN substr(%s, 1, instr(%s, ':') - 1) || substr(%s, instr(%s, '@'))
	ELSE %s END`

func normalizedSenderSQL(column string) string {
	return fmt.Sprintf(deviceSuffixExpr, column, column, column, column, column, column)
}

func (s *MessageStore) NthFromSenders(chat string, senders []string, n int) (*Message, error) {
	if n <= 0 {
		return nil, nil
	}

	cleaned := make([]string, 0, len(senders))
	seen := map[string]bool{}
	for _, jid := range senders {
		jid = strings.TrimSpace(jid)
		if jid == "" || seen[jid] {
			continue
		}
		seen[jid] = true
		cleaned = append(cleaned, jid)
	}
	if len(cleaned) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cleaned)), ",")
	args := make([]any, 0, len(cleaned)*2+3)
	args = append(args, strings.TrimSpace(chat))
	for _, jid := range cleaned {
		args = append(args, jid)
	}
	for _, jid := range cleaned {
		args = append(args, jid)
	}
	args = append(args, n-1)

	row := s.db.QueryRow(
		`SELECT `+messageColumns+` FROM messages
		 WHERE chat = ? AND (`+normalizedSenderSQL("sender")+` IN (`+placeholders+`)
		   OR `+normalizedSenderSQL("sender_pn")+` IN (`+placeholders+`))
		 ORDER BY ts DESC, seq DESC LIMIT 1 OFFSET ?`,
		args...,
	)
	return scanMessage(row)
}

func (s *MessageStore) BySenders(chat string, senders []string, limit int) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}

	cleaned := make([]string, 0, len(senders))
	seen := map[string]bool{}
	for _, jid := range senders {
		jid = strings.TrimSpace(jid)
		if jid == "" || seen[jid] {
			continue
		}
		seen[jid] = true
		cleaned = append(cleaned, jid)
	}
	if len(cleaned) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(cleaned)), ", ")
	args := make([]any, 0, len(cleaned)*2+2)
	args = append(args, strings.TrimSpace(chat))
	for _, jid := range cleaned {
		args = append(args, jid)
	}
	for _, jid := range cleaned {
		args = append(args, jid)
	}
	args = append(args, limit)

	rows, err := s.db.Query(
		`SELECT `+messageColumns+` FROM messages
		 WHERE chat = ? AND (`+normalizedSenderSQL("sender")+` IN (`+placeholders+`)
		   OR `+normalizedSenderSQL("sender_pn")+` IN (`+placeholders+`))
		 ORDER BY ts DESC, seq DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *MessageStore) Count(chat string) (int, error) {
	var total int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat = ?`, strings.TrimSpace(chat)).Scan(&total)
	return total, err
}

type messageScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row messageScanner) (*Message, error) {
	var m Message
	var isGroup, fromMe int
	err := row.Scan(
		&m.Seq, &m.Chat, &m.StanzaID, &m.Sender, &m.SenderPN, &m.PushName,
		&isGroup, &fromMe, &m.Timestamp, &m.Type, &m.Text, &m.Raw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.IsGroup = isGroup != 0
	m.FromMe = fromMe != 0
	return &m, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
