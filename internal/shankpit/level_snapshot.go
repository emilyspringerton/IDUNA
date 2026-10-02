package shankpit

// level_snapshot.go -- timestamped player snapshots of a level, carrying destructible-brick damage.
//
// Founder real-time, 2026-10-02: F1 uploads a copy of the level to the shankpit levels registry with an
// ISO timestamp name down to the second (spam it and only one lands), preserving brick damage; leaving
// a level auto-saves it the same way.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// BrickCell is one damaged cell of one destructible wall: the same (parent wall index, packed cell key,
// remaining hp) triple SHANKPIT's brick engine (packages/world/brick_fracture.h BfRec) keeps. hp 0 =
// the cell is gone. Absent = full hp. Wall indexes the exported (flattened) walls array.
type BrickCell struct {
	Wall int    `json:"wall"`
	Key  uint32 `json:"key"`
	HP   int    `json:"hp"`
}

// MaxBrickCells bounds a snapshot; brick_fracture.h's own BF_MAX_RECORDS is the engine-side ceiling.
const MaxBrickCells = 4096

// snapshotSuffix matches the "_20261002T153045Z" tail a snapshot name carries, so a snapshot of a
// snapshot reuses the base name instead of growing a chain of timestamps.
var snapshotSuffix = regexp.MustCompile(`_\d{8}T\d{6}Z$`)

// SnapshotName builds "<base>_<UTC ISO-8601 basic, second precision>", base truncated so the whole name
// fits validLevelName's 64-char limit.
func SnapshotName(sourceName string, at time.Time) string {
	base := snapshotSuffix.ReplaceAllString(sourceName, "")
	stamp := at.UTC().Format("20060102T150405Z")
	if max := 64 - 1 - len(stamp); len(base) > max {
		base = base[:max]
	}
	return base + "_" + stamp
}

func validateBrickDamage(cells []BrickCell, wallCount int) error {
	if len(cells) > MaxBrickCells {
		return fmt.Errorf("shankpit: too many brick damage cells (%d, max %d)", len(cells), MaxBrickCells)
	}
	for i, c := range cells {
		if c.Wall < 0 || c.Wall >= wallCount {
			return fmt.Errorf("shankpit: brick damage cell %d references wall %d, level has %d", i, c.Wall, wallCount)
		}
		if c.HP < 0 || c.HP > 255 {
			return fmt.Errorf("shankpit: brick damage cell %d has hp %d (must be 0-255)", i, c.HP)
		}
	}
	return nil
}

// SnapshotLevel clones level sourceID under a second-precision timestamped name and stores `damage` on
// the clone (replacing, not merging, whatever the source carried -- the client sends its full current
// state). A same-second repeat hits idx_shankpit_levels_name and returns ErrSnapshotExists, which is the
// debounce guarantee: spamming the key can never create a second row.
func (s *LevelStore) SnapshotLevel(ctx context.Context, sourceID int64, damage []BrickCell, at time.Time) (*Level, error) {
	src, err := s.GetLevel(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	exp, err := s.Export(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if err := validateBrickDamage(damage, len(exp.Walls)); err != nil {
		return nil, err
	}
	if damage == nil {
		damage = []BrickCell{}
	}
	name := SnapshotName(src.Name, at)
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM shankpit_levels WHERE name = ?`, name).Scan(&exists); err != nil {
		return nil, fmt.Errorf("shankpit: snapshot: %w", err)
	}
	if exists > 0 {
		return nil, ErrSnapshotExists
	}
	lvl, err := s.CloneLevel(ctx, sourceID, name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrSnapshotExists
		}
		return nil, err
	}
	dmgJSON, err := marshalDamage(damage)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE shankpit_levels SET brick_damage_json = ?, enclosed = ? WHERE id = ?`, dmgJSON, src.Enclosed, lvl.ID); err != nil {
		return nil, fmt.Errorf("shankpit: snapshot: store damage: %w", err)
	}
	return s.GetLevel(ctx, lvl.ID)
}

// ErrSnapshotExists -- a snapshot with this second-precision name is already stored.
var ErrSnapshotExists = fmt.Errorf("shankpit: snapshot already exists for this second")

func marshalDamage(d []BrickCell) (string, error) {
	b, err := json.Marshal(d)
	return string(b), err
}
