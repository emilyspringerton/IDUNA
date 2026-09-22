// checkpoint_store.go -- S459-49, a real, shared RL checkpoint registry for SHANKPIT. Founder
// real-time: "bring in the bot registry affordances on NOCK all the same - ability to disable -
// hide disabled - set default (defer this...) - for shankpit". Mirrors
// internal/brawlpit/checkpoint_store.go field-for-field (see that file's own doc comment for the
// full "why a remote, IDUNA-hosted registry, not just a local scripts/rl_league.py directory"
// rationale -- identical here: SHANKPIT/scripts/rl_train_packet.py, S459-48, can run from this
// box, a Colab runtime, or anywhere else, and those need a real, shared, network-reachable
// source of truth the same way BRAWLPIT's own training pipeline did).
//
// Real, deliberate scope-down from BRAWLPIT's own final store: no SetWeights/ReadWeights (native-
// inference weight export has no SHANKPIT equivalent yet -- real, separate, future work) and no
// RecordMatchResult/Elo-update (nothing calls it yet -- S459-48's own training script doesn't
// push to a remote registry). `Elo` stays a real field, just immobile until that mechanism exists.
package shankpit

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

	"iduna/internal/modelgit"
)

// ValidCheckpointRoles mirrors SHANKPIT/scripts/rl_league.py's own real LeagueRole enum values
// exactly (main/main_exploiter/league_exploiter, verbatim-ported from BRAWLPIT's own registry,
// S459-35) -- kept in sync by hand since this is a cross-language (Go/Python) boundary with no
// shared schema to generate from.
var ValidCheckpointRoles = map[string]bool{
	"main":             true,
	"main_exploiter":   true,
	"league_exploiter": true,
}

var validCheckpointSourceLocation = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{0,199}$`)

// Checkpoint is one row of the shankpit_rl_checkpoints table.
type Checkpoint struct {
	ID int64 `json:"id"`
	// Name is a real, human-readable identifier -- "<role>_<YYYYMMDD>_<HHMMSS>" (UTC, to the
	// second), generated server-side from the row's own real creation time, matching
	// BRAWLPIT's own Checkpoint.Name convention exactly. Never trusted from the uploader.
	Name             string  `json:"name"`
	Role             string  `json:"role"`
	Generation       int     `json:"generation"`
	Elo              float64 `json:"elo"`
	SourceLocation   string  `json:"source_location"`
	Filename         string  `json:"filename"`
	SHA256           string  `json:"sha256"`
	SizeBytes        int64   `json:"size_bytes"`
	IsActiveOpponent bool    `json:"is_active_opponent"`
	// IsDisabled marks this checkpoint as excluded from the league -- a real, per-checkpoint,
	// reversible flag, distinct from IsActiveOpponent (one global in-game selection). The row and
	// its blob stay intact so it can be re-enabled or inspected later.
	IsDisabled bool `json:"is_disabled"`
	// EvalNote (S459-63, founder real-time: "how the fuck is my colab log gonna help it just says
	// training") -- a real, plain-text summary of this generation's own per-role evaluation match
	// against its prior generation (real kill counts, or a captured crash/report-failure reason),
	// pushed alongside the checkpoint itself so it's visible through this SAME registry API
	// without needing any access to the training process's own stdout. Empty is a real, honest
	// "nothing evaluated" state (generation 0 has no prior to compare against; every pre-S459-63
	// row has none either) -- purely diagnostic, never fed back into Elo or any other behavior.
	EvalNote  string `json:"eval_note"`
	CreatedAt string `json:"created_at"`
}

const checkpointColumns = `id, name, role, generation, elo, source_location, filename, sha256, size_bytes,
	is_active_opponent, is_disabled, eval_note, created_at`

// scanCheckpointRow reads one real row matching checkpointColumns' own exact column order --
// shared by every query below so the column list and the Scan() call can never silently drift
// apart from each other (same real convention checkpointColumns' own BRAWLPIT precedent uses).
func scanCheckpointRow(scan func(...any) error) (*Checkpoint, error) {
	var c Checkpoint
	if err := scan(&c.ID, &c.Name, &c.Role, &c.Generation, &c.Elo, &c.SourceLocation, &c.Filename, &c.SHA256, &c.SizeBytes,
		&c.IsActiveOpponent, &c.IsDisabled, &c.EvalNote, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// CheckpointStore is the real, SQLite-metadata + on-disk-blob backed registry.
type CheckpointStore struct {
	DB      *sql.DB
	BlobDir string // e.g. var/shankpit-checkpoints -- real .zip files, named by this row's own id
	// GitSync -- see internal/brawlpit.CheckpointStore's own identical field for the full
	// reasoning (founder real-time, 2026-09-22: "we need to integrate the model repository with
	// git lfs and each model repository should have a git integration that can be turned off (on
	// by default)"). Zero value is a real no-op.
	GitSync modelgit.Syncer
}

func validateCheckpointInput(role, sourceLocation, filename string, data []byte) error {
	if !ValidCheckpointRoles[role] {
		return fmt.Errorf("shankpit: invalid checkpoint role %q (must be main, main_exploiter, or league_exploiter)", role)
	}
	if !validCheckpointSourceLocation.MatchString(sourceLocation) {
		return fmt.Errorf("shankpit: invalid source_location %q", sourceLocation)
	}
	if filename == "" {
		return fmt.Errorf("shankpit: filename is required")
	}
	if len(data) == 0 {
		return fmt.Errorf("shankpit: checkpoint file is empty")
	}
	// Real, sane bound -- a PPO MlpPolicy checkpoint is a few hundred KB to a few MB (see
	// S459-48's own real smoke checkpoint, ~260KB); matches BRAWLPIT's own identical cap.
	const maxCheckpointBytes = 200 * 1024 * 1024
	if len(data) > maxCheckpointBytes {
		return fmt.Errorf("shankpit: checkpoint file too large (%d bytes, max %d)", len(data), maxCheckpointBytes)
	}
	return nil
}

// Create validates, hashes, writes the real blob to disk, and inserts the metadata row -- in
// that order, so a failed DB insert never leaves an orphaned blob file with no matching row.
func (s *CheckpointStore) Create(ctx context.Context, role string, generation int, elo float64, sourceLocation, filename string, data []byte, evalNote string) (*Checkpoint, error) {
	if err := validateCheckpointInput(role, sourceLocation, filename, data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	if err := os.MkdirAll(s.BlobDir, 0o755); err != nil {
		return nil, fmt.Errorf("shankpit: create blob dir: %w", err)
	}

	name := fmt.Sprintf("%s_%s", role, time.Now().UTC().Format("20060102_150405"))

	// evalNote is real, optional, purely diagnostic text (S459-63) -- clamped to the column's own
	// real 500-char bound rather than trusting the uploader.
	const maxEvalNoteLen = 500
	if len(evalNote) > maxEvalNoteLen {
		evalNote = evalNote[:maxEvalNoteLen]
	}

	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_rl_checkpoints (name, role, generation, elo, source_location, filename, sha256, size_bytes, blob_path, eval_note)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
		name, role, generation, elo, sourceLocation, filename, sha, len(data), evalNote)
	if err != nil {
		return nil, fmt.Errorf("shankpit: create checkpoint row: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create checkpoint row: %w", err)
	}

	blobPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.zip", id))
	if err := os.WriteFile(blobPath, data, 0o644); err != nil {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM shankpit_rl_checkpoints WHERE id = ?`, id)
		return nil, fmt.Errorf("shankpit: write checkpoint blob: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE shankpit_rl_checkpoints SET blob_path = ? WHERE id = ?`, blobPath, id); err != nil {
		return nil, fmt.Errorf("shankpit: record blob path: %w", err)
	}

	// Fire-and-forget, matches apples.go/kanban.go's own established idiom: a slow/failed git
	// sync must never hold up or fail the real DB write above, which has already succeeded.
	go s.GitSync.SyncBlob(blobPath, fmt.Sprintf("%d.zip", id),
		fmt.Sprintf("model: sync checkpoint %s #%d (%s, gen %d)", role, id, name, generation))

	return s.Get(ctx, id)
}

func (s *CheckpointStore) Get(ctx context.Context, id int64) (*Checkpoint, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+checkpointColumns+` FROM shankpit_rl_checkpoints WHERE id = ?`, id)
	c, err := scanCheckpointRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: checkpoint not found")
		}
		return nil, fmt.Errorf("shankpit: get checkpoint: %w", err)
	}
	return c, nil
}

// List returns every registered checkpoint, newest first, optionally filtered to one role.
func (s *CheckpointStore) List(ctx context.Context, roleFilter string) ([]Checkpoint, error) {
	var rows *sql.Rows
	var err error
	if roleFilter != "" {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM shankpit_rl_checkpoints WHERE role = ? ORDER BY created_at DESC`, roleFilter)
	} else {
		rows, err = s.DB.QueryContext(ctx,
			`SELECT `+checkpointColumns+` FROM shankpit_rl_checkpoints ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, fmt.Errorf("shankpit: list checkpoints: %w", err)
	}
	defer rows.Close()

	out := []Checkpoint{}
	for rows.Next() {
		c, err := scanCheckpointRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("shankpit: list checkpoints: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SetActiveOpponent marks checkpoint `id` as the one real, global "selected opponent" and clears
// the flag on every other row -- same real single-selection invariant, enforced the same
// transactional way, as BRAWLPIT's own CheckpointStore.SetActiveOpponent.
func (s *CheckpointStore) SetActiveOpponent(ctx context.Context, id int64) (*Checkpoint, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set active opponent: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE shankpit_rl_checkpoints SET is_active_opponent = 0`); err != nil {
		return nil, fmt.Errorf("shankpit: clear prior active opponent: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE shankpit_rl_checkpoints SET is_active_opponent = 1 WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set active opponent: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: checkpoint %d not found", id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("shankpit: set active opponent: %w", err)
	}
	return s.Get(ctx, id)
}

// GetActiveOpponent returns the current global selection, or (nil, nil) if none has ever been
// set -- a real, honest "no selection yet" state, not an error.
func (s *CheckpointStore) GetActiveOpponent(ctx context.Context) (*Checkpoint, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+checkpointColumns+` FROM shankpit_rl_checkpoints WHERE is_active_opponent = 1 LIMIT 1`)
	c, err := scanCheckpointRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("shankpit: get active opponent: %w", err)
	}
	return c, nil
}

// UpdateElo overwrites checkpoint `id`'s own real elo/eval_note fields in place -- S459-76, real,
// found-live gap: founder real-time "im a little concerned that the elos of the generation 0
// bots arent going up and down... can we make sure the elos are set up to go up and down not
// just whatever the first elo into the registry is?" Checked directly: it wasn't a match-
// scheduling gap (gen 0 genuinely does get evaluated once, by generation 1's own real
// vs-prior-gen match, per rl_train_packet.py's own real league orchestrator) -- the real gap was
// that this registry had NO way to update an already-pushed checkpoint's own real elo at all.
// push_checkpoint (Create) is correctly a one-shot POST for the checkpoint FILE itself (the real
// PPO weights never change after training), but a checkpoint's own real skill rating keeps
// moving every time a LATER generation evaluates against it -- elo is a real, live, mutable
// property of an immutable artifact, and until this method existed there was no way to keep the
// two in sync. Same real, minimal "UPDATE one column, return the fresh row" shape as
// SetDisabled below.
func (s *CheckpointStore) UpdateElo(ctx context.Context, id int64, elo float64, evalNote string) (*Checkpoint, error) {
	const maxEvalNoteLen = 500
	if len(evalNote) > maxEvalNoteLen {
		evalNote = evalNote[:maxEvalNoteLen]
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_rl_checkpoints SET elo = ?, eval_note = ? WHERE id = ?`, elo, evalNote, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: update checkpoint elo: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: checkpoint %d not found", id)
	}
	return s.Get(ctx, id)
}

// SetDisabled marks checkpoint `id` as excluded from the league (or re-enables it).
func (s *CheckpointStore) SetDisabled(ctx context.Context, id int64, disabled bool) (*Checkpoint, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE shankpit_rl_checkpoints SET is_disabled = ? WHERE id = ?`, disabled, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set checkpoint disabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: checkpoint %d not found", id)
	}
	return s.Get(ctx, id)
}

// ReadBlob returns the real, raw checkpoint bytes for downloading.
func (s *CheckpointStore) ReadBlob(ctx context.Context, id int64) (*Checkpoint, []byte, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	blobPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d.zip", id))
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, nil, fmt.Errorf("shankpit: read checkpoint blob: %w", err)
	}
	return c, data, nil
}
