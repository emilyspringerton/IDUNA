// Package shankpit implements the real, SQLite-backed SHANKPIT NOCK level editor store (v0,
// EMILY/BACKLOG.md SECTION 459, founder real-time: "so v0 it and start working dont worry about
// the current levels lets just go full level select brawlpit repo exact model for now").
//
// A real, standalone-row CRUD store, mirroring internal/brawlpit.LevelStore's own established
// shape field-for-field (list/create/get/update/rename/clone/delete/export, one row per level, no
// single "master" with derived views) -- kept in its own package rather than folded into
// internal/nock itself, same design principle that already keeps NOCK un-coupled from any one
// specific game's concepts, and kept separate from internal/brawlpit since this is a different
// game's own domain object, not a variant of BRAWLPIT's 2D Platform2D.
//
// Wall is the EXACT real shape SHANKPIT/packages/map/map.h's own `Wall` struct already defines --
// center x/y/z, FULL extents sx/sy/sz (packages/map/map.c's own collision code computes bounds as
// center +/- size/2, confirmed by reading that file directly, not assumed), r/g/b as a real
// OpenGL-convention [0,1] color (every existing glColor3f/glColor4f call in this monorepo's own
// SHANKPIT client uses that same range), plus friction. See
// migrations/truestore/202609140001_shankpit_levels.sql for the real schema.
package shankpit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

// Wall mirrors SHANKPIT's own real Wall struct field-for-field (json tags match map.h's own real
// field names exactly, not Go convention, so a level exported from here is byte-for-byte what
// GameMap's own native loader already expects once wired up).
type Wall struct {
	ID       int     `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	SX       float64 `json:"sx"`
	SY       float64 `json:"sy"`
	SZ       float64 `json:"sz"`
	R        float64 `json:"r"`
	G        float64 `json:"g"`
	B        float64 `json:"b"`
	Friction float64 `json:"friction"`
}

// GridCellSize is the real, fixed, constant world-unit size of one ground-plane grid square --
// founder, direct: "the squares are always the same size" / "so the units needs to be the number
// of squares in the grid." The plane's own real editable field is a SQUARE COUNT
// (GroundPlaneSquares), not a raw world-unit length -- the actual world-unit footprint is always
// GroundPlaneSquares * GridCellSize.
//
// REAL, FOUND, LIVE value, not invented: SHANKPIT's own native client (apps/lobby/src/main.c)
// already has a real, working "Matrix floor" grid + a magenta-glow footstep trail effect
// (draw_grid/update_and_draw_trails) at a fixed real cell size, `#define GRID_SIZE 50.0f` --
// founder, direct: "shankpit has it built in that the grid lights up when you touch it... it
// would be great if we integrated with that." This constant matches that exactly (not 1.0) so a
// level authored here lines up, square-for-square, with the real in-game glowing-trail floor
// instead of introducing a second, mismatched grid convention. Shared, by convention (not by
// import -- this is a Go/C/TS boundary with no shared schema to generate from), with the
// identical constant in ShankpitLevelEditor.tsx and packages/world/level_boxes.h -- kept in sync
// by hand.
const GridCellSize = 50.0

// Level is one row of the shankpit_levels table.
type Level struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	Depth              float64 `json:"depth"`
	// GroundPlaneEnabled/GroundPlaneSquares (founder real-time: "i want there to be a plane by
	// default that the player collides with - the checkerboard in the level editor - that
	// should constitute the plane for that level... configurable in terms of size... turn on
	// able and off able per level") -- a real, first-class, per-level, persisted property, not a
	// Wall and not a hardcoded engine default. GroundPlaneSquares is a real square COUNT (see
	// GridCellSize's own doc comment for why), not a raw length.
	GroundPlaneEnabled bool    `json:"ground_plane_enabled"`
	GroundPlaneSquares int     `json:"ground_plane_squares"`
	Walls              []Wall  `json:"walls"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ExportDoc is the real, native-loader-facing shape (SHANKPIT's own real physics.h `Box`/
// `phys_set_custom_level` contract -- see packages/world/level_boxes.h's own doc comment for the
// real, found-live correction on which native format this actually targets) -- narrower than
// Level (no id/timestamps), matching internal/brawlpit's own ExportDoc precedent exactly.
type ExportDoc struct {
	Version            int     `json:"version"`
	Name               string  `json:"name"`
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	Depth              float64 `json:"depth"`
	GroundPlaneEnabled bool    `json:"ground_plane_enabled"`
	GroundPlaneSquares int     `json:"ground_plane_squares"`
	Walls              []Wall  `json:"walls"`
}

var validLevelName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]{0,63}$`)

// ValidateName rejects an empty or over-long/oddly-charactered level name -- same real bound
// internal/brawlpit.ValidateName already uses (a level name is a real, display-facing title, not
// a filesystem path component).
func ValidateName(name string) error {
	if !validLevelName.MatchString(name) {
		return fmt.Errorf("shankpit: invalid level name %q (must match %s)", name, validLevelName.String())
	}
	return nil
}

// MaxWalls mirrors SHANKPIT/packages/map/map.h's own real `Wall walls[100]` fixed-array capacity
// -- kept in exact sync so a level saved here can never exceed what the native GameMap can
// actually hold, same real reason internal/brawlpit.MaxPlatforms exists.
const MaxWalls = 100

// MinGroundPlaneSquares/MaxGroundPlaneSquares bound the real, editable square-count field --
// a real, sane range (1 = a single 1x1 square, 2000 = a 2000x2000-unit plane, comfortably larger
// than any real level authored here yet) rather than an unbounded integer.
const MinGroundPlaneSquares = 1
const MaxGroundPlaneSquares = 2000

func validateGroundPlane(squares int) error {
	if squares < MinGroundPlaneSquares || squares > MaxGroundPlaneSquares {
		return fmt.Errorf("shankpit: ground_plane_squares must be in [%d, %d], got %d", MinGroundPlaneSquares, MaxGroundPlaneSquares, squares)
	}
	return nil
}

func validateWalls(walls []Wall) error {
	if len(walls) > MaxWalls {
		return fmt.Errorf("shankpit: too many walls (%d, max %d -- SHANKPIT's own native GameMap can't hold more)", len(walls), MaxWalls)
	}
	for i, w := range walls {
		if w.SX <= 0 || w.SY <= 0 || w.SZ <= 0 {
			return fmt.Errorf("shankpit: wall %d has non-positive size (%g x %g x %g)", i, w.SX, w.SY, w.SZ)
		}
		for _, c := range []struct {
			name string
			v    float64
		}{{"r", w.R}, {"g", w.G}, {"b", w.B}} {
			if c.v < 0 || c.v > 1 {
				return fmt.Errorf("shankpit: wall %d has out-of-range %s %g (must be in [0, 1], matching this codebase's own real OpenGL color convention)", i, c.name, c.v)
			}
		}
	}
	return nil
}

// LevelStore is the real SQLite-backed CRUD layer.
type LevelStore struct {
	DB *sql.DB
}

// CreateLevel inserts a new, real, independent level row. A level may start with zero walls (v0's
// own real "create a level, then add a cube" flow, S459-01 before S459-04) -- unlike BRAWLPIT's
// own CreateLevel, an empty wall list is not an error here, since there is no equivalent real
// native-loader requirement forcing "at least one platform" the way BRAWLPIT's 2D format does.
func (s *LevelStore) CreateLevel(ctx context.Context, name string, width, height, depth float64, groundPlaneEnabled bool, groundPlaneSquares int, walls []Wall) (*Level, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := validateGroundPlane(groundPlaneSquares); err != nil {
		return nil, err
	}
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal walls: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_levels (name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, width, height, depth, groundPlaneEnabled, groundPlaneSquares, string(wallsJSON))
	if err != nil {
		return nil, fmt.Errorf("shankpit: create level: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create level: %w", err)
	}
	return s.GetLevel(ctx, id)
}

// GetLevel returns the full row, including its real wall list.
func (s *LevelStore) GetLevel(ctx context.Context, id int64) (*Level, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json, created_at, updated_at
		 FROM shankpit_levels WHERE id = ?`, id)
	return scanLevel(row)
}

func scanLevel(row *sql.Row) (*Level, error) {
	var l Level
	var wallsJSON string
	if err := row.Scan(&l.ID, &l.Name, &l.Width, &l.Height, &l.Depth, &l.GroundPlaneEnabled, &l.GroundPlaneSquares, &wallsJSON, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: level not found")
		}
		return nil, fmt.Errorf("shankpit: get level: %w", err)
	}
	if err := json.Unmarshal([]byte(wallsJSON), &l.Walls); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored walls: %w", err)
	}
	return &l, nil
}

// LevelSummary is the real, lightweight shape a level LIST returns -- everything about a Level
// except its own full wall array, matching internal/brawlpit.LevelSummary's own precedent.
type LevelSummary struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	Depth              float64 `json:"depth"`
	GroundPlaneEnabled bool    `json:"ground_plane_enabled"`
	GroundPlaneSquares int     `json:"ground_plane_squares"`
	WallCount          int     `json:"wall_count"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ListLevels returns every level as a real, lightweight summary, newest first -- the real
// level-select registry primitive this section exists to build.
func (s *LevelStore) ListLevels(ctx context.Context) ([]LevelSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json, created_at, updated_at
		 FROM shankpit_levels ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list levels: %w", err)
	}
	defer rows.Close()

	out := []LevelSummary{}
	for rows.Next() {
		var sum LevelSummary
		var wallsJSON string
		if err := rows.Scan(&sum.ID, &sum.Name, &sum.Width, &sum.Height, &sum.Depth, &sum.GroundPlaneEnabled, &sum.GroundPlaneSquares, &wallsJSON, &sum.CreatedAt, &sum.UpdatedAt); err != nil {
			return nil, fmt.Errorf("shankpit: list levels: %w", err)
		}
		var walls []Wall
		if err := json.Unmarshal([]byte(wallsJSON), &walls); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored walls: %w", err)
		}
		sum.WallCount = len(walls)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// UpdateLevel replaces a level's own real editable fields in place -- the web editor's own real
// "save" action (dimensions + ground plane + the full wall layout can all change together in one
// save). This is the one endpoint both S459-04 "create a cube" (append a default wall to the
// array, save) and S459-05 "face-drag editing" (adjust an existing wall's center/size, save) both
// go through -- matching BRAWLPIT's own LevelStore precedent exactly: there is no separate "add
// one platform" endpoint there either, the whole array is replaced together.
func (s *LevelStore) UpdateLevel(ctx context.Context, id int64, width, height, depth float64, groundPlaneEnabled bool, groundPlaneSquares int, walls []Wall) (*Level, error) {
	if err := validateGroundPlane(groundPlaneSquares); err != nil {
		return nil, err
	}
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal walls: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_levels SET width = ?, height = ?, depth = ?, ground_plane_enabled = ?, ground_plane_squares = ?, walls_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		width, height, depth, groundPlaneEnabled, groundPlaneSquares, string(wallsJSON), id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: update level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// RenameLevel updates a level's own name in place.
func (s *LevelStore) RenameLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_levels SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: rename level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// CloneLevel makes a real, full, independent copy of an existing level under a new name -- same
// real shape as internal/brawlpit.LevelStore's own CloneLevel.
func (s *LevelStore) CloneLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	src, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateLevel(ctx, newName, src.Width, src.Height, src.Depth, src.GroundPlaneEnabled, src.GroundPlaneSquares, src.Walls)
}

// DeleteLevel permanently removes a level row.
func (s *LevelStore) DeleteLevel(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_levels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: level %d not found", id)
	}
	return nil
}

// Export returns the real, native-loader-facing document for a level -- the real end-to-end
// target once SHANKPIT's own map loader gains a JSON path (not built yet -- v0's own real scope
// is the registry + editing surface, matching BRAWLPIT's own S415-01/S415-04 sequencing where the
// web editor and its export shape landed before the native loader consumed it).
func (s *LevelStore) Export(ctx context.Context, id int64) (*ExportDoc, error) {
	lvl, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ExportDoc{
		Version: 1, Name: lvl.Name, Width: lvl.Width, Height: lvl.Height, Depth: lvl.Depth,
		GroundPlaneEnabled: lvl.GroundPlaneEnabled, GroundPlaneSquares: lvl.GroundPlaneSquares,
		Walls: lvl.Walls,
	}, nil
}
