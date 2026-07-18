package mailinglist

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists encrypted subscriber records in their own SQLite file,
// deliberately separate from IDUNA's main truestore.db — a leaked or
// mis-copied backup of the main store never carries this table with it.
// Every column that could identify a person is ciphertext; nothing here is
// ever written as plaintext.
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

CREATE TABLE IF NOT EXISTS subscribers (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	email_ciphertext  BLOB     NOT NULL,
	email_nonce       BLOB     NOT NULL,
	consent_version   TEXT     NOT NULL,
	consented_at      DATETIME NOT NULL,
	mailchimp_synced  INTEGER  NOT NULL DEFAULT 0,
	created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Open opens (creating if absent) the mailing-list SQLite file at path and
// ensures its schema exists. The file itself contains only ciphertext for
// any subscriber PII — see package doc.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open mailinglist db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate mailinglist db: %w", err)
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
// overwrite an existing one — call ResetVault explicitly (and deliberately)
// if you really mean to invalidate all existing encrypted data.
func (s *Store) InitVault(salt, canaryCiphertext, canaryNonce []byte) error {
	initialized, err := s.Initialized()
	if err != nil {
		return err
	}
	if initialized {
		return fmt.Errorf("vault already initialized — refusing to overwrite (existing subscriber data would become permanently unreadable)")
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

// AddSubscriber records one encrypted email. consentVersion identifies which
// exact privacy-policy/consent-copy revision the subscriber agreed to (see
// OKEMILY/privacy.html) — required for GDPR accountability (being able to
// prove what someone actually consented to, not just that they clicked
// something at some point).
func (s *Store) AddSubscriber(emailCiphertext, emailNonce []byte, consentVersion string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO subscribers (email_ciphertext, email_nonce, consent_version, consented_at) VALUES (?, ?, ?, ?)`,
		emailCiphertext, emailNonce, consentVersion, time.Now().UTC(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MarkMailchimpSynced flags a subscriber row as successfully forwarded to
// Mailchimp — best-effort bookkeeping only, never blocks the subscribe path.
func (s *Store) MarkMailchimpSynced(id int64) error {
	_, err := s.db.Exec(`UPDATE subscribers SET mailchimp_synced = 1 WHERE id = ?`, id)
	return err
}
