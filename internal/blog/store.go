// Package blog is the system of record for okemily.com's blog posts.
//
// Deliberately NOT WordPress+MySQL — the host was already down to ~400MB
// free RAM with swap essentially full when this was built, and a second
// full PHP-FPM+MySQL stack risked recreating the exact OOM-kill incident
// this codebase spent a whole session fixing (EMILY BACKLOG SECTION 152).
// Posts live in their own small SQLite file; a render step turns them into
// static HTML with zero ongoing runtime cost (see render.go). Both
// programmatic posting (an authenticated IDUNA agent, e.g. EMILY-PRIME) and
// manual posting (curl/emily CLI with the same auth) go through the same
// store and the same render path.
package blog

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Post struct {
	ID          int64
	Slug        string
	Title       string
	Author      string
	Body        string // plain text; render.go does minimal paragraph formatting
	AdLine      string // per-post STINKIES hoodie ad flavor text; falls back to a default if empty
	AdCTA       string // per-post link text for the ad, e.g. "Join the waiting list →"; falls back if empty
	PublishedAt time.Time
	CreatedAt   time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS posts (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	slug          TEXT     NOT NULL UNIQUE,
	title         TEXT     NOT NULL,
	author        TEXT     NOT NULL,
	body          TEXT     NOT NULL,
	ad_line       TEXT     NOT NULL DEFAULT '',
	ad_cta        TEXT     NOT NULL DEFAULT '',
	published_at  DATETIME NOT NULL,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_posts_published_at ON posts(published_at);
`

// addColumnIfMissing runs an ALTER TABLE for a DB created before ad_line/
// ad_cta existed. CREATE TABLE IF NOT EXISTS above is a no-op against an
// already-existing table, so new columns on an old table need this instead.
// SQLite has no "ADD COLUMN IF NOT EXISTS" on the version modernc.org/sqlite
// vendors here, so the duplicate-column error (already migrated) is the
// expected, ignored case.
func addColumnIfMissing(db *sql.DB, column, ddl string) error {
	_, err := db.Exec(ddl)
	if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("add column %s: %w", column, err)
	}
	return nil
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open blog db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate blog db: %w", err)
	}
	if err := addColumnIfMissing(db, "ad_line", `ALTER TABLE posts ADD COLUMN ad_line TEXT NOT NULL DEFAULT ''`); err != nil {
		db.Close()
		return nil, err
	}
	if err := addColumnIfMissing(db, "ad_cta", `ALTER TABLE posts ADD COLUMN ad_cta TEXT NOT NULL DEFAULT ''`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Create inserts a new post. Slugs must be unique — returns an error
// (SQLite UNIQUE constraint) if the slug is already taken.
func (s *Store) Create(p Post) (int64, error) {
	if p.PublishedAt.IsZero() {
		p.PublishedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(
		`INSERT INTO posts (slug, title, author, body, ad_line, ad_cta, published_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Title, p.Author, p.Body, p.AdLine, p.AdCTA, p.PublishedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns all posts, most recent first.
func (s *Store) List() ([]Post, error) {
	rows, err := s.db.Query(`SELECT id, slug, title, author, body, ad_line, ad_cta, published_at, created_at FROM posts ORDER BY published_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Author, &p.Body, &p.AdLine, &p.AdCTA, &p.PublishedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// GetBySlug returns one post, or sql.ErrNoRows if not found.
func (s *Store) GetBySlug(slug string) (Post, error) {
	var p Post
	err := s.db.QueryRow(
		`SELECT id, slug, title, author, body, ad_line, ad_cta, published_at, created_at FROM posts WHERE slug = ?`, slug,
	).Scan(&p.ID, &p.Slug, &p.Title, &p.Author, &p.Body, &p.AdLine, &p.AdCTA, &p.PublishedAt, &p.CreatedAt)
	return p, err
}

// Update sets ad_line/ad_cta for an existing post by slug. Used by the
// one-off backfill for already-published posts (see cmd/blog-adlines) — the
// public API has no post-edit endpoint, this is intentionally narrow.
func (s *Store) UpdateAdLine(slug, adLine, adCTA string) error {
	_, err := s.db.Exec(`UPDATE posts SET ad_line = ?, ad_cta = ? WHERE slug = ?`, adLine, adCTA, slug)
	return err
}
