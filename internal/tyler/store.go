// Package tyler is the system of record for the TYLER reading experience —
// a dedicated, book-styled home for TYLER episode scripts on okemily.com,
// separate from the generic blog (which only does "poor man's markdown"
// paragraph splitting — TYLER scripts have real headers/bold/tables/code
// fences that need actual rendering, plus a nicer, IDUNA-style-guide
// reading layout instead of the blog's dark developer-blog theme).
//
// Same "own small SQLite file, render to static HTML" shape as
// internal/blog, for the same reason (see blog/store.go's own doc comment
// on the OOM-kill incident this pattern avoids repeating).
package tyler

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Episode struct {
	ID          int64
	Slug        string
	Title       string // e.g. `"Ask the Frog (Not the Tree)"`
	Series      string // e.g. "SERIES X"
	EpisodeTag  string // e.g. "INTERLUDE, UNNUMBERED" or "S01E01"
	Build       string // e.g. "0133"
	Body        string // raw markdown, same content as the TYLER repo's own episode file
	PublishedAt time.Time
	CreatedAt   time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS episodes (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	slug          TEXT     NOT NULL UNIQUE,
	title         TEXT     NOT NULL,
	series        TEXT     NOT NULL DEFAULT '',
	episode_tag   TEXT     NOT NULL DEFAULT '',
	build         TEXT     NOT NULL DEFAULT '',
	body          TEXT     NOT NULL,
	published_at  DATETIME NOT NULL,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_episodes_published_at ON episodes(published_at);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open tyler db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate tyler db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Create inserts a new episode. Slugs must be unique — returns an error
// (SQLite UNIQUE constraint) if the slug is already taken; callers should
// use Update for re-publishing an edited episode under the same slug.
func (s *Store) Create(e Episode) (int64, error) {
	if e.PublishedAt.IsZero() {
		e.PublishedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(
		`INSERT INTO episodes (slug, title, series, episode_tag, build, body, published_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Slug, e.Title, e.Series, e.EpisodeTag, e.Build, e.Body, e.PublishedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns all episodes, most recently published first.
func (s *Store) List() ([]Episode, error) {
	rows, err := s.db.Query(`SELECT id, slug, title, series, episode_tag, build, body, published_at, created_at FROM episodes ORDER BY published_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var episodes []Episode
	for rows.Next() {
		var e Episode
		if err := rows.Scan(&e.ID, &e.Slug, &e.Title, &e.Series, &e.EpisodeTag, &e.Build, &e.Body, &e.PublishedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		episodes = append(episodes, e)
	}
	return episodes, rows.Err()
}

// GetBySlug returns one episode, or sql.ErrNoRows if not found.
func (s *Store) GetBySlug(slug string) (Episode, error) {
	var e Episode
	err := s.db.QueryRow(
		`SELECT id, slug, title, series, episode_tag, build, body, published_at, created_at FROM episodes WHERE slug = ?`, slug,
	).Scan(&e.ID, &e.Slug, &e.Title, &e.Series, &e.EpisodeTag, &e.Build, &e.Body, &e.PublishedAt, &e.CreatedAt)
	return e, err
}
