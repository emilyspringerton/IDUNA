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
	"time"
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
	ID int64 `json:"id"`
	// Name is a real, human-readable identifier -- "<role>_<YYYYMMDD>_<HHMMSS>" (UTC, to the
	// second), generated server-side from the row's own real creation time (S421-02, founder
	// real-time: "make sure that the models have like some id number or something to identify
	// them maybe datestamp to the second with the archetype type"). Never trusted from the
	// uploader -- this is what the AI Opponents UI and the in-game HUD both display.
	Name             string  `json:"name"`
	Role             string  `json:"role"`
	Generation       int     `json:"generation"`
	Elo              float64 `json:"elo"`
	SourceLocation   string  `json:"source_location"`
	Filename         string  `json:"filename"`
	SHA256           string  `json:"sha256"`
	SizeBytes        int64   `json:"size_bytes"`
	IsActiveOpponent bool    `json:"is_active_opponent"`
	// HasWeights/WeightsSizeBytes/WeightsSHA256 describe the real, separate exported MLP
	// inference blob (scripts/export_policy_weights.py's own "BPMW" binary format) this
	// checkpoint carries, if any -- see SetWeights' own doc comment. HasWeights is real and
	// explicit rather than making callers infer "no weights" from WeightsSizeBytes==0.
	HasWeights       bool   `json:"has_weights"`
	WeightsSizeBytes int64  `json:"weights_size_bytes"`
	WeightsSHA256    string `json:"weights_sha256"`
	CreatedAt        string `json:"created_at"`
}

const checkpointColumns = `id, name, role, generation, elo, source_location, filename, sha256, size_bytes,
	is_active_opponent, weights_blob_path, weights_size_bytes, weights_sha256, created_at`

// scanCheckpointRow reads one real row matching checkpointColumns' own exact column order --
// shared by every query below so the column list and the Scan() call can never silently drift
// apart from each other.
func scanCheckpointRow(scan func(...any) error) (*Checkpoint, error) {
	var c Checkpoint
	var weightsBlobPath string
	if err := scan(&c.ID, &c.Name, &c.Role, &c.Generation, &c.Elo, &c.SourceLocation, &c.Filename, &c.SHA256, &c.SizeBytes,
		&c.IsActiveOpponent, &weightsBlobPath, &c.WeightsSizeBytes, &c.WeightsSHA256, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.HasWeights = weightsBlobPath != ""
	return &c, nil
}

// CheckpointStore is the real, SQLite-metadata + on-disk-blob backed registry.
type CheckpointStore struct {
	DB      *sql.DB
	BlobDir string // e.g. var/brawlpit-checkpoints -- real .zip/.bin files, named by this row's own id
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

	// "<role>_<YYYYMMDD>_<HHMMSS>" UTC, to the second -- see Checkpoint.Name's own doc comment.
	name := fmt.Sprintf("%s_%s", role, time.Now().UTC().Format("20060102_150405"))

	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO brawlpit_rl_checkpoints (name, role, generation, elo, source_location, filename, sha256, size_bytes, blob_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '')`,
		name, role, generation, elo, sourceLocation, filename, sha, len(data))
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
	row := s.DB.QueryRowContext(ctx, `SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE id = ?`, id)
	c, err := scanCheckpointRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("brawlpit: checkpoint not found")
		}
		return nil, fmt.Errorf("brawlpit: get checkpoint: %w", err)
	}
	return c, nil
}

// List returns every registered checkpoint, newest first, optionally filtered to one role (an
// empty roleFilter returns all roles) -- mirrors LevelStore.ListLevels' own established shape.
func (s *CheckpointStore) List(ctx context.Context, roleFilter string) ([]Checkpoint, error) {
	var rows *sql.Rows
	var err error
	if roleFilter != "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE role = ? ORDER BY created_at DESC`, roleFilter)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("brawlpit: list checkpoints: %w", err)
	}
	defer rows.Close()

	out := []Checkpoint{}
	for rows.Next() {
		c, err := scanCheckpointRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("brawlpit: list checkpoints: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SetActiveOpponent marks checkpoint `id` as the one real, global "selected opponent" (S421,
// founder real-time: "just like the level editor... select a model for the opponent from the
// registry") and clears the flag on every other row -- a real, deliberate single-selection
// invariant enforced in application logic (SQLite has no partial-unique-index shortcut this
// codebase already leans on elsewhere), done as two statements in one transaction so a crash
// between them can never leave two checkpoints simultaneously marked active.
func (s *CheckpointStore) SetActiveOpponent(ctx context.Context, id int64) (*Checkpoint, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: set active opponent: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET is_active_opponent = 0`); err != nil {
		return nil, fmt.Errorf("brawlpit: clear prior active opponent: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET is_active_opponent = 1 WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: set active opponent: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("brawlpit: checkpoint %d not found", id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("brawlpit: set active opponent: %w", err)
	}
	return s.Get(ctx, id)
}

// GetActiveOpponent returns the current global selection, or (nil, nil) if none has ever been
// set -- a real, honest "no selection yet" state, not an error (a fresh registry with only
// checkpoints and no explicit selection is a normal, expected state).
func (s *CheckpointStore) GetActiveOpponent(ctx context.Context) (*Checkpoint, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE is_active_opponent = 1 LIMIT 1`)
	c, err := scanCheckpointRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("brawlpit: get active opponent: %w", err)
	}
	return c, nil
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

// SetWeights attaches the real, exported native-inference weights blob (scripts/
// export_policy_weights.py's own "BPMW" binary format -- see packages/common/mlp_policy.h's own
// matching loader) to an existing checkpoint row (S421-02, founder real-time: "ensure that the
// client actually uses that model"). A real, separate artifact from the .zip ReadBlob/Create
// above manage -- the .zip stays the resumable stable_baselines3 training state; this is the
// small, portable blob BRAWLPIT's native client actually downloads and runs. Stored RAW
// (uncompressed) on disk -- LZ4 compression is a real, separate WIRE concern applied at the HTTP
// handler layer on download (matching brawlpit_levels_public.go's own `?compress=lz4` precedent),
// not baked into how this store keeps its own files.
func (s *CheckpointStore) SetWeights(ctx context.Context, id int64, data []byte) (*Checkpoint, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("brawlpit: weights file is empty")
	}
	const maxWeightsBytes = 20 * 1024 * 1024 // real, generous bound -- a real export is tens of KB
	if len(data) > maxWeightsBytes {
		return nil, fmt.Errorf("brawlpit: weights file too large (%d bytes, max %d)", len(data), maxWeightsBytes)
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(s.BlobDir, 0o755); err != nil {
		return nil, fmt.Errorf("brawlpit: create blob dir: %w", err)
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	weightsPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.weights.bin", id))
	if err := os.WriteFile(weightsPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("brawlpit: write weights blob: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE brawlpit_rl_checkpoints SET weights_blob_path = ?, weights_size_bytes = ?, weights_sha256 = ? WHERE id = ?`,
		weightsPath, len(data), sha, id); err != nil {
		return nil, fmt.Errorf("brawlpit: record weights blob: %w", err)
	}
	return s.Get(ctx, id)
}

// ReadWeights returns the real, raw (uncompressed) exported weights bytes for a checkpoint, or a
// real, checked error if this checkpoint has no weights attached yet (an older checkpoint from
// before S421-02, or one never exported) -- callers (the download handler) apply LZ4 compression
// on top for the wire, matching this file's own SetWeights doc comment.
func (s *CheckpointStore) ReadWeights(ctx context.Context, id int64) (*Checkpoint, []byte, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !c.HasWeights {
		return nil, nil, fmt.Errorf("brawlpit: checkpoint %d has no exported weights", id)
	}
	weightsPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.weights.bin", id))
	data, err := os.ReadFile(weightsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("brawlpit: read weights blob: %w", err)
	}
	return c, data, nil
}
