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
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"iduna/internal/modelgit"
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
	// IsDisabled marks this checkpoint as excluded from the league (S428, founder real-time:
	// "i want to reset training but not include certain models from the registry - can you add
	// a checkbox to the registry backend to disable those models from the league?"). A real,
	// per-checkpoint, reversible flag -- distinct from IsActiveOpponent (one global in-game
	// selection). Training/resume/bot-pool logic skips a disabled checkpoint entirely; the row
	// and its blob stay intact so it can be re-enabled or inspected later.
	IsDisabled bool   `json:"is_disabled"`
	CreatedAt  string `json:"created_at"`
}

const checkpointColumns = `id, name, role, generation, elo, source_location, filename, sha256, size_bytes,
	is_active_opponent, weights_blob_path, weights_size_bytes, weights_sha256, is_disabled, created_at`

// scanCheckpointRow reads one real row matching checkpointColumns' own exact column order --
// shared by every query below so the column list and the Scan() call can never silently drift
// apart from each other.
func scanCheckpointRow(scan func(...any) error) (*Checkpoint, error) {
	var c Checkpoint
	var weightsBlobPath string
	if err := scan(&c.ID, &c.Name, &c.Role, &c.Generation, &c.Elo, &c.SourceLocation, &c.Filename, &c.SHA256, &c.SizeBytes,
		&c.IsActiveOpponent, &weightsBlobPath, &c.WeightsSizeBytes, &c.WeightsSHA256, &c.IsDisabled, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.HasWeights = weightsBlobPath != ""
	return &c, nil
}

// CheckpointStore is the real, SQLite-metadata + on-disk-blob backed registry.
type CheckpointStore struct {
	DB      *sql.DB
	BlobDir string // e.g. var/brawlpit-checkpoints -- real .zip/.bin files, named by this row's own id
	// Game scopes every query to one game's rows (S503-06: this registry is game-generic; brawlpit is
	// just the default so every pre-existing construction site keeps working unchanged). Rows of other
	// games are invisible: Get/List/Set*/Record* on another game's id behave as "not found".
	Game string
	// GitSync (founder real-time, 2026-09-22: "we need to integrate the model repository with
	// git lfs and each model repository should have a git integration that can be turned off (on
	// by default)") -- syncs every newly-created checkpoint blob into a real sibling git working
	// tree, git-lfs-tracked. Zero value (Syncer{}) is a real no-op (RepoDir unset), not an error
	// -- existing construction sites (tests, or a deployment that never wires this) behave
	// exactly as before this field existed. See internal/modelgit's own doc comment for the full
	// "on by default" reasoning.
	GitSync modelgit.Syncer
}

// DefaultGame is the slug pre-existing (brawlpit) rows carry and an empty CheckpointStore.Game means.
const DefaultGame = "brawlpit"

func (s *CheckpointStore) game() string {
	if s.Game == "" {
		return DefaultGame
	}
	return s.Game
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
		`INSERT INTO brawlpit_rl_checkpoints (name, role, generation, elo, source_location, filename, sha256, size_bytes, blob_path, game)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
		name, role, generation, elo, sourceLocation, filename, sha, len(data), s.game())
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
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM brawlpit_rl_checkpoints WHERE id = ? AND game = ?`, id, s.game())
		return nil, fmt.Errorf("brawlpit: write checkpoint blob: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET blob_path = ? WHERE id = ?`, blobPath, id); err != nil {
		return nil, fmt.Errorf("brawlpit: record blob path: %w", err)
	}

	// Fire-and-forget, matches apples.go/kanban.go's own established idiom: a slow/failed git
	// sync must never hold up or fail the real DB write above, which has already succeeded.
	go s.GitSync.SyncBlob(blobPath, fmt.Sprintf("%d.zip", id),
		fmt.Sprintf("model(%s): sync checkpoint %s #%d (%s, gen %d)", s.game(), role, id, name, generation))

	return s.Get(ctx, id)
}

func (s *CheckpointStore) Get(ctx context.Context, id int64) (*Checkpoint, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE id = ? AND game = ?`, id, s.game())
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
			`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE game = ? AND role = ? ORDER BY created_at DESC`, s.game(), roleFilter)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE game = ? ORDER BY created_at DESC`, s.game())
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

	if _, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET is_active_opponent = 0 WHERE game = ?`, s.game()); err != nil {
		return nil, fmt.Errorf("brawlpit: clear prior active opponent: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET is_active_opponent = 1 WHERE id = ? AND game = ?`, id, s.game())
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
		`SELECT `+checkpointColumns+` FROM brawlpit_rl_checkpoints WHERE is_active_opponent = 1 AND game = ? LIMIT 1`, s.game())
	c, err := scanCheckpointRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("brawlpit: get active opponent: %w", err)
	}
	return c, nil
}

// SetDisabled marks checkpoint `id` as excluded from the league (or re-enables it) -- see
// Checkpoint.IsDisabled's own doc comment. Unlike SetActiveOpponent, this is a real, independent,
// per-row flag -- no global single-selection invariant to enforce, so a plain single UPDATE is
// enough (no transaction needed).
func (s *CheckpointStore) SetDisabled(ctx context.Context, id int64, disabled bool) (*Checkpoint, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET is_disabled = ? WHERE id = ? AND game = ?`, disabled, id, s.game())
	if err != nil {
		return nil, fmt.Errorf("brawlpit: set checkpoint disabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("brawlpit: checkpoint %d not found", id)
	}
	return s.Get(ctx, id)
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

// EloK is the real, standard "fast-moving" K-factor (USCF uses 32 for players under ~2100
// rating) -- the exact same constant BRAWLPIT/scripts/rl_league.py's own ELO_K already uses, so
// a rating computed one way and moved the other stays comparable.
const EloK = 32.0

// eloExpected is the standard Elo expected-score formula: the probability A beats B, in [0, 1].
// Unexported -- RecordMatchResult below is the real, only public entry point (mirrors
// rl_league.py's own elo_expected/elo_update split, kept private here since nothing outside this
// file has a real reason to call the raw formula directly yet).
func eloExpected(ratingA, ratingB float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (ratingB-ratingA)/400.0))
}

// RecordMatchResult applies one real match outcome (S421-04, founder real-time: "can we start
// recording the match results with the actual outcomes?") -- scoreA is 1.0 (A won), 0.5 (draw),
// or 0.0 (A lost); B's score is always the complement, matching Elo's own zero-sum design (same
// real formula BRAWLPIT/scripts/rl_league.py's own elo_update already uses, so a rating computed
// by a local evaluation script and one recorded here stay on the same real scale). Real,
// deliberate design point: this is genuinely the ONLY thing that ever MOVES a checkpoint's Elo
// off its inherited value -- Create()'s own Elo parameter and register_generation_snapshot's own
// "inherit forward" behavior (rl_league.py) both just carry a rating along; only a real,
// evaluated match outcome changes it. Both updates happen in one transaction so a crash between
// them can never leave only one side's rating moved.
func (s *CheckpointStore) RecordMatchResult(ctx context.Context, idA, idB int64, scoreA float64) (*Checkpoint, *Checkpoint, error) {
	if scoreA < 0 || scoreA > 1 {
		return nil, nil, fmt.Errorf("brawlpit: score_a must be in [0, 1], got %v", scoreA)
	}
	if idA == idB {
		return nil, nil, fmt.Errorf("brawlpit: a checkpoint cannot play a match against itself")
	}
	a, err := s.Get(ctx, idA)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Get(ctx, idB)
	if err != nil {
		return nil, nil, err
	}

	expectedA := eloExpected(a.Elo, b.Elo)
	scoreB := 1.0 - scoreA
	expectedB := 1.0 - expectedA
	newEloA := a.Elo + EloK*(scoreA-expectedA)
	newEloB := b.Elo + EloK*(scoreB-expectedB)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("brawlpit: record match result: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET elo = ? WHERE id = ?`, newEloA, idA); err != nil {
		return nil, nil, fmt.Errorf("brawlpit: record match result: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE brawlpit_rl_checkpoints SET elo = ? WHERE id = ?`, newEloB, idB); err != nil {
		return nil, nil, fmt.Errorf("brawlpit: record match result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("brawlpit: record match result: %w", err)
	}

	updatedA, err := s.Get(ctx, idA)
	if err != nil {
		return nil, nil, err
	}
	updatedB, err := s.Get(ctx, idB)
	if err != nil {
		return nil, nil, err
	}
	return updatedA, updatedB, nil
}
