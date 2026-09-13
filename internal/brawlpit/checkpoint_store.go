// Package brawlpit (checkpoint_store.go) -- S420, a real, remote, shared RL checkpoint registry.
// Founder real-time: "lets make a checkpoint registry so we can train from multiple locations and
// then we can add checkpoints from colab?"
//
// BRAWLPIT/scripts/rl_league.py's own LeagueManager is a real, working registry, but it's a
// LOCAL filesystem directory -- multiple PROCESSES on the same machine can share it (that's the
// whole reason S419's three-archetype registration works at all), but a Colab runtime (a fresh,
// ephemeral filesystem every session) and this box can't. This store is the real, network-
// reachable source of truth those separate training locations both push to and pull from --
// same real reason the BRAWLPIT online level editor (S415-417) needed IDUNA rather than staying
// a local SQLite file.
package brawlpit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ValidCheckpointRoles mirrors scripts/rl_league.py's own real LeagueRole enum values exactly
// (main/main_exploiter/league_exploiter) -- kept in sync by hand since this is a cross-language
// (Go/Python) boundary with no shared schema to generate from.
var ValidCheckpointRoles = map[string]bool{
	"main":             true,
	"main_exploiter":   true,
	"league_exploiter": true,
}

var validSourceLocation = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{0,199}$`)

// Checkpoint is one row of the brawlpit_rl_checkpoints table.
type Checkpoint struct {
	ID             int64   `json:"id"`
	Role           string  `json:"role"`
	Generation     int     `json:"generation"`
	Elo            float64 `json:"elo"`
	SourceLocation string  `json:"source_location"`
	Filename       string  `json:"filename"`
	SHA256         string  `json:"sha256"`
	SizeBytes      int64   `json:"size_bytes"`
	CreatedAt      string  `json:"created_at"`
}

// CheckpointStore is the real, SQLite-metadata + on-disk-blob backed registry.
type CheckpointStore struct {
	DB      *sql.DB
	BlobDir string // e.g. var/brawlpit-checkpoints -- real .zip files, named by this row's own id
}

func validateCheckpointInput(role, sourceLocation, filename string, data []byte) error {
	if !ValidCheckpointRoles[role] {
		return fmt.Errorf("brawlpit: invalid checkpoint role %q (must be main, main_exploiter, or league_exploiter)", role)
	}
	if !validSourceLocation.MatchString(sourceLocation) {
		return fmt.Errorf("brawlpit: invalid source_location %q", sourceLocation)
	}
	if filename == "" {
		return fmt.Errorf("brawlpit: filename is required")
	}
	if len(data) == 0 {
		return fmt.Errorf("brawlpit: checkpoint file is empty")
	}
	// A real, sane bound -- a PPO MlpPolicy checkpoint is a few MB; this stops a genuinely
	// malformed or hostile upload from filling the disk, matching internal/nock's own established
	// upload-size-sanity convention for image layers.
	const maxCheckpointBytes = 200 * 1024 * 1024
	if len(data) > maxCheckpointBytes {
		return fmt.Errorf("brawlpit: checkpoint file too large (%d bytes, max %d)", len(data), maxCheckpointBytes)
	}
	return nil
}

// Create validates, hashes, writes the real blob to disk, and inserts the metadata row -- in
// that order, so a failed DB insert never leaves an orphaned blob file with no matching row (the
// blob write happens after validation but the DB insert is the real, final commit point; see the
// cleanup-on-failure path below).
func (s *CheckpointStore) Create(ctx context.Context, role string, generation int, elo float64, sourceLocation, filename string, data []byte) (*Checkpoint, error) {
	if err := validateCheckpointInput(role, sourceLocation, filename, data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	if err := os.MkdirAll(s.BlobDir, 0o755); err != nil {
		return nil, fmt.Errorf("brawlpit: create blob dir: %w", err)
	}

	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO brawlpit_rl_checkpoints (role, generation, elo, source_location, filename, sha256, size_bytes, blob_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '')`,
		role, generation, elo, sourceLocation, filename, sha, len(data))
	if err != nil {
		return nil, fmt.Errorf("brawlpit: create checkpoint row: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("brawlpit: create checkpoint row: %w", err)
	}

	blobPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.zip", id))
	if err := os.WriteFile(blobPath, data, 0o644); err != nil {
		// Real, honest cleanup: don't leave a DB row pointing at a blob that was never
		// actually written -- a real failed upload should look entirely absent, not half-real.
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM brawlpit_rl_checkpoints WHERE id = ?`, id)
		return nil, fmt.Errorf("brawlpit: write checkpoint blob: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET blob_path = ? WHERE id = ?`, blobPath, id); err != nil {
		return nil, fmt.Errorf("brawlpit: record blob path: %w", err)
	}

	return s.Get(ctx, id)
}

func (s *CheckpointStore) Get(ctx context.Context, id int64) (*Checkpoint, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, role, generation, elo, source_location, filename, sha256, size_bytes, created_at
		 FROM brawlpit_rl_checkpoints WHERE id = ?`, id)
	var c Checkpoint
	if err := row.Scan(&c.ID, &c.Role, &c.Generation, &c.Elo, &c.SourceLocation, &c.Filename, &c.SHA256, &c.SizeBytes, &c.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("brawlpit: checkpoint not found")
		}
		return nil, fmt.Errorf("brawlpit: get checkpoint: %w", err)
	}
	return &c, nil
}

// List returns every registered checkpoint, newest first, optionally filtered to one role (an
// empty roleFilter returns all roles) -- mirrors LevelStore.ListLevels' own established shape.
func (s *CheckpointStore) List(ctx context.Context, roleFilter string) ([]Checkpoint, error) {
	var rows *sql.Rows
	var err error
	if roleFilter != "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, role, generation, elo, source_location, filename, sha256, size_bytes, created_at
			 FROM brawlpit_rl_checkpoints WHERE role = ? ORDER BY created_at DESC`, roleFilter)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT id, role, generation, elo, source_location, filename, sha256, size_bytes, created_at
			 FROM brawlpit_rl_checkpoints ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("brawlpit: list checkpoints: %w", err)
	}
	defer rows.Close()

	out := []Checkpoint{}
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.ID, &c.Role, &c.Generation, &c.Elo, &c.SourceLocation, &c.Filename, &c.SHA256, &c.SizeBytes, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("brawlpit: list checkpoints: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReadBlob returns the real, raw checkpoint bytes for downloading -- a real, direct file read,
// not re-derived from anything else, so what comes back is byte-identical to what Create()
// originally stored (verified by the caller against the row's own recorded sha256/size_bytes if
// it wants to double-check, same as any content-addressed store's own real contract).
func (s *CheckpointStore) ReadBlob(ctx context.Context, id int64) (*Checkpoint, []byte, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	blobPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.zip", id))
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, nil, fmt.Errorf("brawlpit: read checkpoint blob: %w", err)
	}
	return c, data, nil
}
