// Package promptoverse is the system of record for the Prompt-o-verse
// gallery on okemily.com — a browsable taxonomy of generated images. Each
// node carries the northstar's two-tier prompt model (§3 of
// EMILY/docs/NORTHSTAR_PROMPT_O_VERSE.md) made concrete: a short EZ prompt
// (e.g. "renaissance oil painting master chief halo") that unfurls into the
// real expanded prompt actually used to generate the image, plus the
// generated image itself and its labeled taxonomy tags. This is VS0's
// first real product surface, not the whole vision.
//
// Same "own small SQLite file, render to static HTML" shape as
// internal/blog and internal/tyler, for the same reason (see blog/store.go's
// own doc comment on the OOM-kill incident this pattern avoids repeating).
// Images themselves are NOT stored in SQLite (would bloat the db file for
// no benefit) — they live as static files alongside the rendered pages,
// referenced by filename only.
package promptoverse

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Tag is one labeled feature of a generated node — e.g. {"era": "1910s"},
// {"medium": "lithograph illustration"}. Stored as a JSON object so the
// tag vocabulary isn't fixed at schema-design time (matches the northstar's
// own "normalize into queryable tags, not one long prose string" data-model
// decision — the JSON blob here is the pre-normalization step; a real
// relational tag table, per §3's still-open storage-shape question, is a
// later migration once there's enough real data to know which axes recur).
type Tags map[string]string

type Node struct {
	ID    int64
	Slug  string
	Label string // the style/taxonomy category, e.g. "Renaissance oil painting" — the gallery groups nodes by this (founder: "stained glass is top level")
	// Subject is what the style was applied to, e.g. "baseball card",
	// "Master Chief (Halo)" — the axis orthogonal to Label/style. The same
	// Label can have multiple Subject variants (founder's own example:
	// Renaissance oil painting of a baseball card AND of Master Chief).
	Subject string
	Kind    string // "historical" | "surreal" — the two taxonomy branches (northstar §1/§2)
	// EZPrompt is the short, bare, top-top-level prompt a casual user would
	// type -- e.g. "renaissance oil painting master chief halo" -- what a
	// normal/vanilla text-to-image pipeline would receive unenriched.
	EZPrompt string
	// ExpandedPrompt is the real, feature-rich prompt this node's image was
	// actually generated from -- what EZPrompt "unfurls into" (northstar §3
	// tier 2). Never invented after the fact: always the literal prompt used.
	ExpandedPrompt string
	ImageFile      string // filename only, e.g. "02-glossy-90s-card.png" — served as a static asset alongside the rendered page
	Tags           Tags   // labeled taxonomy data for the generated output (northstar §2's "label the generated data itself" step)
	PublishedAt    time.Time
	CreatedAt      time.Time
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	slug            TEXT     NOT NULL UNIQUE,
	label           TEXT     NOT NULL,
	subject         TEXT     NOT NULL DEFAULT '',
	kind            TEXT     NOT NULL DEFAULT 'historical',
	ez_prompt       TEXT     NOT NULL DEFAULT '',
	expanded_prompt TEXT     NOT NULL,
	image_file      TEXT     NOT NULL,
	tags_json       TEXT     NOT NULL DEFAULT '{}',
	published_at    DATETIME NOT NULL,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_nodes_published_at ON nodes(published_at);
CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_label ON nodes(label);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open promptoverse db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate promptoverse db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Create inserts a new node. Slugs must be unique — returns an error
// (SQLite UNIQUE constraint) if the slug is already taken.
func (s *Store) Create(n Node) (int64, error) {
	if n.PublishedAt.IsZero() {
		n.PublishedAt = time.Now().UTC()
	}
	tagsJSON, err := json.Marshal(n.Tags)
	if err != nil {
		return 0, fmt.Errorf("marshal tags: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO nodes (slug, label, subject, kind, ez_prompt, expanded_prompt, image_file, tags_json, published_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Slug, n.Label, n.Subject, n.Kind, n.EZPrompt, n.ExpandedPrompt, n.ImageFile, string(tagsJSON), n.PublishedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// List returns all nodes, most recently published first.
func (s *Store) List() ([]Node, error) {
	rows, err := s.db.Query(`SELECT id, slug, label, subject, kind, ez_prompt, expanded_prompt, image_file, tags_json, published_at, created_at FROM nodes ORDER BY published_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetBySlug returns one node, or sql.ErrNoRows if not found.
func (s *Store) GetBySlug(slug string) (Node, error) {
	row := s.db.QueryRow(`SELECT id, slug, label, subject, kind, ez_prompt, expanded_prompt, image_file, tags_json, published_at, created_at FROM nodes WHERE slug = ?`, slug)
	return scanNode(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanNode(row scannable) (Node, error) {
	var n Node
	var tagsJSON string
	if err := row.Scan(&n.ID, &n.Slug, &n.Label, &n.Subject, &n.Kind, &n.EZPrompt, &n.ExpandedPrompt, &n.ImageFile, &tagsJSON, &n.PublishedAt, &n.CreatedAt); err != nil {
		return Node{}, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &n.Tags); err != nil {
		return Node{}, fmt.Errorf("unmarshal tags for %s: %w", n.Slug, err)
	}
	return n, nil
}
