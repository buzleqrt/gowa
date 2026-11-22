package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Thumb menyimpan metadata thumbnail/favicon link yang sudah diunggah ke
// WhatsApp, sehingga pesan preview tautan bisa memakai ulang metadata tersebut.
type Thumb struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Metadata  string `json:"metadata"`
	Expired   *int64 `json:"expired,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ThumbStore adalah akses tabel thumbnails pada database bot.
type ThumbStore struct {
	db *sql.DB
}

var (
	thumbMu     sync.Mutex
	thumbStores = map[string]*ThumbStore{}
)

const thumbSchema = `
CREATE TABLE IF NOT EXISTS thumbnails (
	name TEXT NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('thumbnail', 'favicon')),
	status TEXT NOT NULL DEFAULT 'random' CHECK (status IN ('random', 'private')),
	metadata TEXT NOT NULL,
	expired INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (name, kind)
)`

// OpenThumbStore membuka (dan menyiapkan) tabel thumbnails pada database bot.
// Handle di-cache per path supaya perintah tidak membuka koneksi baru terus.
func OpenThumbStore(dbPath string) (*ThumbStore, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("path database kosong")
	}

	thumbMu.Lock()
	defer thumbMu.Unlock()

	if store, ok := thumbStores[dbPath]; ok {
		return store, nil
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka database thumbnail: %w", err)
	}
	if _, err := db.Exec(thumbSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("gagal menyiapkan tabel thumbnails: %w", err)
	}

	store := &ThumbStore{db: db}
	thumbStores[dbPath] = store
	return store, nil
}

// Save menyimpan atau memperbarui metadata thumbnail dengan nama tertentu.
// Favicon selalu disimpan dengan status random.
func (s *ThumbStore) Save(name, kind, status, metadata string, expired *int64) error {
	name = strings.TrimSpace(name)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if name == "" || metadata == "" {
		return fmt.Errorf("nama dan metadata wajib diisi")
	}
	if kind != "thumbnail" && kind != "favicon" {
		return fmt.Errorf("jenis %q tidak dikenal", kind)
	}

	if kind == "favicon" || status != "private" {
		status = "random"
	}

	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO thumbnails (name, kind, status, metadata, expired, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name, kind) DO UPDATE SET
			status = excluded.status,
			metadata = excluded.metadata,
			expired = excluded.expired,
			updated_at = excluded.updated_at`,
		name, kind, status, metadata, expired, now, now,
	)
	if err != nil {
		return fmt.Errorf("gagal menyimpan thumbnail: %w", err)
	}
	return nil
}

// Get mengambil metadata berdasarkan nama dan jenis. Mengembalikan nil bila
// tidak ada.
func (s *ThumbStore) Get(name, kind string) (*Thumb, error) {
	row := s.db.QueryRow(
		`SELECT name, kind, status, metadata, expired, created_at, updated_at
		 FROM thumbnails WHERE name = ? AND kind = ?`,
		strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(kind)),
	)
	return scanThumb(row)
}

// Random mengambil satu metadata acak yang belum kedaluwarsa dari pool random.
func (s *ThumbStore) Random(kind string) (*Thumb, error) {
	row := s.db.QueryRow(`
		SELECT name, kind, status, metadata, expired, created_at, updated_at
		FROM thumbnails
		WHERE kind = ? AND status = 'random' AND (expired IS NULL OR expired > ?)
		ORDER BY RANDOM() LIMIT 1`,
		strings.ToLower(strings.TrimSpace(kind)), time.Now().Unix(),
	)
	return scanThumb(row)
}

// List mengembalikan semua metadata untuk satu jenis tanpa field metadata.
func (s *ThumbStore) List(kind string) ([]Thumb, error) {
	rows, err := s.db.Query(`
		SELECT name, kind, status, '' AS metadata, expired, created_at, updated_at
		FROM thumbnails WHERE kind = ? ORDER BY updated_at DESC`,
		strings.ToLower(strings.TrimSpace(kind)),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Thumb
	for rows.Next() {
		var t Thumb
		if err := rows.Scan(&t.Name, &t.Kind, &t.Status, &t.Metadata, &t.Expired, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete menghapus metadata berdasarkan nama dan jenis.
func (s *ThumbStore) Delete(name, kind string) (bool, error) {
	res, err := s.db.Exec(
		`DELETE FROM thumbnails WHERE name = ? AND kind = ?`,
		strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(kind)),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func scanThumb(row *sql.Row) (*Thumb, error) {
	var t Thumb
	err := row.Scan(&t.Name, &t.Kind, &t.Status, &t.Metadata, &t.Expired, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
