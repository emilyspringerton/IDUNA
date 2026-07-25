// Package vault implements IDUNA Vault VS0 (EMILY/BACKLOG.md S170-03b, per
// docs/NORTHSTAR_PASSWORD_MANAGER.md): a founder-only password manager --
// logins, secure notes, API keys/tokens, TOTP seeds, and small documents --
// built on the exact same never-at-rest-unencrypted primitive as the
// okemily.com mailing list (internal/mailinglist.Vault: Argon2id key
// derivation, AES-256-GCM, key held only in server memory after an explicit
// unlock, a canary to detect a wrong passphrase). The northstar is explicit
// that this should reuse the primitive rather than reinvent it -- the
// mailing-list vault already IS "per-item encryption keyed off a single
// master key, never persisted" (every subscriber row gets its own nonce
// under the one shared key), which is exactly the shape a password manager
// needs too, so this package imports mailinglist.Vault directly rather than
// duplicating AES-GCM code in a second place where it could drift.
package vault

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ItemType names the five VS0 vault item categories from the northstar's §2.
type ItemType string

const (
	ItemLogin    ItemType = "login"     // fields: username, password, url, notes
	ItemNote     ItemType = "note"      // fields: content
	ItemAPIKey   ItemType = "api_key"   // fields: key, notes
	ItemTOTP     ItemType = "totp"      // fields: seed, issuer, account
	ItemDocument ItemType = "document"  // fields: filename, mime_type, content_base64
)

// Item is one decrypted vault entry. Fields is a flexible key/value map
// rather than a rigid per-type struct, matching the northstar's five
// different item shapes without five different tables.
type Item struct {
	ID        int64             `json:"id"`
	ItemType  ItemType          `json:"item_type"`
	Name      string            `json:"name"`
	Fields    map[string]string `json:"fields"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// Store persists encrypted vault items in their own SQLite file, same
// isolation convention as internal/mailinglist.Store -- a leaked or
// mis-copied backup of IDUNA's main truestore.db never carries vault
// contents with it.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS vault_meta (
	id                INTEGER PRIMARY KEY CHECK (id = 1),
	salt              BLOB NOT NULL,
	canary_ciphertext BLOB NOT NULL,
	canary_nonce      BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS vault_items (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	item_type         TEXT     NOT NULL,
	data_ciphertext   BLOB     NOT NULL,
	data_nonce        BLOB     NOT NULL,
	created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Open opens (creating if absent) the vault SQLite file at path and ensures
// its schema exists. item_type is the only plaintext column on vault_items --
// a category tag ("login", "note", ...) leaks far less than a name would
// (real item names -- "AWS root password", "Bank of X login" -- are exactly
// the kind of thing a locked vault should reveal nothing about), so name
// lives inside the encrypted data blob along with every other field.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate vault db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Initialized reports whether the vault salt/canary have been set up yet.
func (s *Store) Initialized() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM vault_meta WHERE id = 1`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// InitVault stores the salt + canary for a brand-new vault. Refuses to
// overwrite an existing one -- same guard as mailinglist.Store.InitVault,
// same reason: overwriting would permanently orphan every existing item.
func (s *Store) InitVault(salt, canaryCiphertext, canaryNonce []byte) error {
	initialized, err := s.Initialized()
	if err != nil {
		return err
	}
	if initialized {
		return fmt.Errorf("vault already initialized — refusing to overwrite (existing items would become permanently unreadable)")
	}
	_, err = s.db.Exec(
		`INSERT INTO vault_meta (id, salt, canary_ciphertext, canary_nonce) VALUES (1, ?, ?, ?)`,
		salt, canaryCiphertext, canaryNonce,
	)
	return err
}

// VaultMeta returns the stored salt + canary for Unlock to verify against.
func (s *Store) VaultMeta() (salt, canaryCiphertext, canaryNonce []byte, err error) {
	err = s.db.QueryRow(`SELECT salt, canary_ciphertext, canary_nonce FROM vault_meta WHERE id = 1`).
		Scan(&salt, &canaryCiphertext, &canaryNonce)
	return
}

// AddItem stores one item's pre-encrypted data blob and returns its new ID.
func (s *Store) AddItem(itemType ItemType, dataCiphertext, dataNonce []byte) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO vault_items (item_type, data_ciphertext, data_nonce, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		string(itemType), dataCiphertext, dataNonce, now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RawItem is one row's still-encrypted contents plus its plaintext metadata.
type RawItem struct {
	ID             int64
	ItemType       string
	DataCiphertext []byte
	DataNonce      []byte
	CreatedAt      string
	UpdatedAt      string
}

// ListRaw returns every item still encrypted -- the caller (the HTTP
// handler, which holds the unlocked Vault) decrypts each one. Kept this way
// so Store never needs a decryption key passed into it at all.
func (s *Store) ListRaw() ([]RawItem, error) {
	rows, err := s.db.Query(`SELECT id, item_type, data_ciphertext, data_nonce, created_at, updated_at FROM vault_items ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RawItem
	for rows.Next() {
		var it RawItem
		if err := rows.Scan(&it.ID, &it.ItemType, &it.DataCiphertext, &it.DataNonce, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetRaw returns one item still encrypted, or sql.ErrNoRows if id doesn't exist.
func (s *Store) GetRaw(id int64) (RawItem, error) {
	var it RawItem
	err := s.db.QueryRow(`SELECT id, item_type, data_ciphertext, data_nonce, created_at, updated_at FROM vault_items WHERE id = ?`, id).
		Scan(&it.ID, &it.ItemType, &it.DataCiphertext, &it.DataNonce, &it.CreatedAt, &it.UpdatedAt)
	return it, err
}

// UpdateItem overwrites an existing item's encrypted contents in place.
// Returns sql.ErrNoRows if id doesn't exist.
func (s *Store) UpdateItem(id int64, itemType ItemType, dataCiphertext, dataNonce []byte) error {
	res, err := s.db.Exec(
		`UPDATE vault_items SET item_type = ?, data_ciphertext = ?, data_nonce = ?, updated_at = ? WHERE id = ?`,
		string(itemType), dataCiphertext, dataNonce, time.Now().UTC(), id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteItem removes an item. Returns sql.ErrNoRows if id doesn't exist.
func (s *Store) DeleteItem(id int64) error {
	res, err := s.db.Exec(`DELETE FROM vault_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
