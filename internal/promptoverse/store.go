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
	"strings"
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

-- mashup_nominations: "build out mashup nomination as a social tool" --
-- a logged-in (Google OAuth via IDUNA), honor-code-accepted user proposes
-- combining two EXISTING subjects into a new mashup subject. This is a
-- real generation-spend request once approved, so nominations sit pending
-- until an EINHORN_INDUSTRIAL admin reviews them -- matches the founder's
-- own rule ("all promotion approvals run through admins before hitting
-- the gen pipeline until we have revenue from promptoverse"). Approval
-- itself does NOT auto-trigger generation -- an admin still runs the
-- existing 'emily promptoverse promote-subject' command by hand, same as
-- any other subject promotion; this table only tracks the social layer
-- (who asked for what, and whether it's been reviewed).
CREATE TABLE IF NOT EXISTS mashup_nominations (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	subject_a       TEXT     NOT NULL,
	subject_b       TEXT     NOT NULL,
	nominated_by    TEXT     NOT NULL,
	status          TEXT     NOT NULL DEFAULT 'pending',
	reviewed_by     TEXT     NOT NULL DEFAULT '',
	reviewed_at     DATETIME,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(subject_a, subject_b, nominated_by)
);
CREATE INDEX IF NOT EXISTS idx_nominations_status ON mashup_nominations(status);
CREATE INDEX IF NOT EXISTS idx_nominations_nominated_by ON mashup_nominations(nominated_by);

-- node_variants: "regenerate with variation" (S176-30). Founder,
-- real-time: "we need to keep both and i think for seo reasons we should
-- condense the forced feature leaf nodes onto the same html page" -- a
-- correction (e.g. "red hoodie instead of grey") is an ADDITIONAL image
-- attached to the SAME node/slug, never an overwrite and never a new
-- leaf page. node_slug is a plain string FK (SQLite doesn't enforce it
-- without PRAGMA foreign_keys, which this connection does enable, but
-- kept loose/no ON DELETE behavior since nodes are never deleted today).
CREATE TABLE IF NOT EXISTS node_variants (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	node_slug       TEXT     NOT NULL,
	image_file      TEXT     NOT NULL,
	ez_prompt       TEXT     NOT NULL DEFAULT '',
	expanded_prompt TEXT     NOT NULL,
	note            TEXT     NOT NULL DEFAULT '',
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_node_variants_slug ON node_variants(node_slug);
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

// MergeTags overlays extra onto a node's existing Tags (extra wins on key
// collisions) and persists the result. Used for backfilling metadata onto
// already-published nodes without touching image/prompt data -- e.g.
// stamping a "pre_annotation" marker onto generations of a subject that
// existed before a subject-level prompt annotation was introduced for it
// (founder, real-time: "gens that did not include the annotation need to
// be marked as pre annotated with a link to the annotation now attached
// to the top level subject" -- see emily.cli's `promptoverse
// backfill-annotation`). Returns sql.ErrNoRows if the slug doesn't exist.
func (s *Store) MergeTags(slug string, extra Tags) (Tags, error) {
	n, err := s.GetBySlug(slug)
	if err != nil {
		return nil, err
	}
	merged := Tags{}
	for k, v := range n.Tags {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	tagsJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal tags: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE nodes SET tags_json = ? WHERE slug = ?`, string(tagsJSON), slug); err != nil {
		return nil, err
	}
	return merged, nil
}

// NodeVariant is an ADDITIONAL generated image for an existing leaf node,
// e.g. a correction like "red hoodie instead of grey" -- "regenerate with
// variation" (S176-30). Founder, real-time, correcting an earlier
// overwrite-in-place design: "we need to keep both and i think for seo
// reasons we should condense the forced feature leaf nodes onto the same
// html page" -- variants are additive and rendered alongside the
// original on the SAME node page/URL, never a separate leaf page and
// never a destructive replace.
type NodeVariant struct {
	ID             int64
	NodeSlug       string
	ImageFile      string
	EZPrompt       string
	ExpandedPrompt string
	Note           string
	CreatedAt      time.Time
}

// AddVariant appends a new variant image+prompt for an existing node.
// Returns sql.ErrNoRows if the slug doesn't exist (variants can't be
// orphaned from a real leaf page).
func (s *Store) AddVariant(nodeSlug, imageFile, ezPrompt, expandedPrompt, note string) (int64, error) {
	if _, err := s.GetBySlug(nodeSlug); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`INSERT INTO node_variants (node_slug, image_file, ez_prompt, expanded_prompt, note) VALUES (?, ?, ?, ?, ?)`,
		nodeSlug, imageFile, ezPrompt, expandedPrompt, note,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListVariants returns a node's additional variants, oldest first.
func (s *Store) ListVariants(nodeSlug string) ([]NodeVariant, error) {
	rows, err := s.db.Query(
		`SELECT id, node_slug, image_file, ez_prompt, expanded_prompt, note, created_at FROM node_variants WHERE node_slug = ? ORDER BY created_at ASC`,
		nodeSlug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeVariant
	for rows.Next() {
		var v NodeVariant
		if err := rows.Scan(&v.ID, &v.NodeSlug, &v.ImageFile, &v.EZPrompt, &v.ExpandedPrompt, &v.Note, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DistinctSubjects returns every distinct non-empty Subject that has at
// least 2 published nodes -- the same >=2 threshold render.go's
// renderSubjectPages already uses for "has a real page." Nominations are
// only accepted between subjects that actually have a page to combine.
func (s *Store) DistinctSubjects() ([]string, error) {
	rows, err := s.db.Query(`SELECT subject FROM nodes WHERE subject != '' GROUP BY subject HAVING COUNT(*) >= 2 ORDER BY subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var subjects []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		subjects = append(subjects, s)
	}
	return subjects, rows.Err()
}

// MashupNomination is a user's proposal to combine two existing subjects
// into a new mashup subject -- see the mashup_nominations schema comment
// for the full design rationale.
type MashupNomination struct {
	ID          int64
	SubjectA    string
	SubjectB    string
	NominatedBy string
	Status      string // "pending" | "approved" | "rejected"
	ReviewedBy  string
	ReviewedAt  *time.Time
	CreatedAt   time.Time
}

const maxPendingNominationsPerUser = 5

// CreateMashupNomination inserts a pending nomination. Returns a distinct
// sentinel error if the user already has too many pending nominations
// (abuse/spam guard, per the open question flagged when S176-27 was
// originally scoped) or already nominated this exact pair (UNIQUE
// constraint on subject_a, subject_b, nominated_by).
var (
	ErrTooManyPendingNominations = fmt.Errorf("too many pending nominations")
	ErrDuplicateNomination       = fmt.Errorf("already nominated this pair")
)

func (s *Store) CreateMashupNomination(subjectA, subjectB, nominatedBy string) (int64, error) {
	var pending int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM mashup_nominations WHERE nominated_by = ? AND status = 'pending'`,
		nominatedBy,
	).Scan(&pending); err != nil {
		return 0, err
	}
	if pending >= maxPendingNominationsPerUser {
		return 0, ErrTooManyPendingNominations
	}

	res, err := s.db.Exec(
		`INSERT INTO mashup_nominations (subject_a, subject_b, nominated_by, status) VALUES (?, ?, ?, 'pending')`,
		subjectA, subjectB, nominatedBy,
	)
	if err != nil {
		if isUniqueConstraintErr(err) {
			return 0, ErrDuplicateNomination
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ListMashupNominations returns nominations, most recent first, optionally
// filtered by status ("" means all).
func (s *Store) ListMashupNominations(status string) ([]MashupNomination, error) {
	query := `SELECT id, subject_a, subject_b, nominated_by, status, reviewed_by, reviewed_at, created_at FROM mashup_nominations`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MashupNomination
	for rows.Next() {
		var n MashupNomination
		var reviewedAt sql.NullTime
		if err := rows.Scan(&n.ID, &n.SubjectA, &n.SubjectB, &n.NominatedBy, &n.Status, &n.ReviewedBy, &reviewedAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if reviewedAt.Valid {
			t := reviewedAt.Time
			n.ReviewedAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ReviewMashupNomination sets a pending nomination's status to "approved"
// or "rejected" and records who reviewed it. Returns sql.ErrNoRows if the
// nomination doesn't exist or isn't currently pending (an admin can't
// re-review something already decided).
func (s *Store) ReviewMashupNomination(id int64, status, reviewedBy string) error {
	res, err := s.db.Exec(
		`UPDATE mashup_nominations SET status = ?, reviewed_by = ?, reviewed_at = ? WHERE id = ? AND status = 'pending'`,
		status, reviewedBy, time.Now().UTC(), id,
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

func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
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
