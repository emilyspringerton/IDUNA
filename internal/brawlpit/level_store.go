// Package brawlpit implements the real, SQLite-backed BRAWLPIT online level editor store
// (S415-02/03, founder real-time: "get the brawlpit level editor online - web technologies -
// we already started building nock - can we finish building out some of that interface so we
// can kind of parlay it into an online brawlpit level editor?").
//
// A real, standalone-row CRUD store, mirroring internal/nock.TextureStore's own established
// shape (many independent rows, no single "master" with derived views) -- kept in its own
// package rather than folded into internal/nock itself, matching that package's own explicit
// design principle: stay a genuinely reusable engine tool, not coupled to one specific game's
// concepts (the same real reason it isn't coupled to GFD either).
//
// Platform is the EXACT real shape BRAWLPIT/packages/common/protocol.h's own Platform2D struct
// and BRAWLPIT/packages/common/level_format.h's own JSON contract already define (S415-01, the
// real blocking Phase 0 this depends on): x/y/w/h are world-unit floats, Type is 0=SOLID/
// 1=PASSTHROUGH. See migrations/truestore/202609130001_brawlpit_levels.sql for the real schema.
package brawlpit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

// Platform mirrors BRAWLPIT's own real Platform2D struct field-for-field (json tags match
// level_format.h's own real key names exactly, not Go convention, so a level exported from here
// is byte-for-byte what the native loader already parses).
type Platform struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
	Type int     `json:"type"`
}

// Level is one row of the brawlpit_levels table.
type Level struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Width     float64    `json:"width"`
	Height    float64    `json:"height"`
	Platforms []Platform `json:"platforms"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
}

// ExportDoc is the real, native-loader-facing shape (BRAWLPIT/packages/common/level_format.h's
// own JSON contract) -- narrower than Level (no id/timestamps), but DOES include width/height
// (S417-01, founder real-time: "the levels need to be actually playable in brawlpit") since the
// native loader now uses them to derive a real, level-scaled blast zone instead of a fixed one
// tuned only for the 2 original stages.
type ExportDoc struct {
	Version   int        `json:"version"`
	Name      string     `json:"name"`
	Width     float64    `json:"width"`
	Height    float64    `json:"height"`
	Platforms []Platform `json:"platforms"`
}

var validLevelName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]{0,63}$`)

// ValidateName rejects an empty or over-long/oddly-charactered level name -- more permissive
// than internal/nock.ValidateName (spaces allowed: a level name is a real, display-facing title
// like "Final Destination", not a filesystem path component), but still real and bounded.
func ValidateName(name string) error {
	if !validLevelName.MatchString(name) {
		return fmt.Errorf("brawlpit: invalid level name %q (must match %s)", name, validLevelName.String())
	}
	return nil
}

// MaxPlatforms mirrors BRAWLPIT/packages/common/level_format.h's own real MAX_LEVEL_PLATFORMS --
// kept in exact sync so a level saved here can never exceed what the native loader can actually
// read back (a silent truncation there would be a real, confusing gap between "saved fine" here
// and "loads wrong" in the game).
const MaxPlatforms = 64

// LevelStore is the real SQLite-backed CRUD layer.
type LevelStore struct {
	DB *sql.DB
}

func validatePlatforms(platforms []Platform) error {
	if len(platforms) == 0 {
		return fmt.Errorf("brawlpit: a level needs at least one platform")
	}
	if len(platforms) > MaxPlatforms {
		return fmt.Errorf("brawlpit: too many platforms (%d, max %d -- BRAWLPIT's own native loader can't read more)", len(platforms), MaxPlatforms)
	}
	for i, p := range platforms {
		if p.W <= 0 || p.H <= 0 {
			return fmt.Errorf("brawlpit: platform %d has non-positive width/height (%g x %g)", i, p.W, p.H)
		}
		if p.Type != 0 && p.Type != 1 {
			return fmt.Errorf("brawlpit: platform %d has invalid type %d (must be 0=solid or 1=passthrough)", i, p.Type)
		}
	}
	return nil
}

// CreateLevel inserts a new, real, independent level row.
func (s *LevelStore) CreateLevel(ctx context.Context, name string, width, height float64, platforms []Platform) (*Level, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := validatePlatforms(platforms); err != nil {
		return nil, err
	}
	platformsJSON, err := json.Marshal(platforms)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: marshal platforms: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO brawlpit_levels (name, width, height, platforms_json) VALUES (?, ?, ?, ?)`,
		name, width, height, string(platformsJSON))
	if err != nil {
		return nil, fmt.Errorf("brawlpit: create level: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("brawlpit: create level: %w", err)
	}
	return s.GetLevel(ctx, id)
}

// GetLevel returns the full row, including its real platform list.
func (s *LevelStore) GetLevel(ctx context.Context, id int64) (*Level, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, platforms_json, created_at, updated_at
		 FROM brawlpit_levels WHERE id = ?`, id)
	return scanLevel(row)
}

func scanLevel(row *sql.Row) (*Level, error) {
	var l Level
	var platformsJSON string
	if err := row.Scan(&l.ID, &l.Name, &l.Width, &l.Height, &platformsJSON, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("brawlpit: level not found")
		}
		return nil, fmt.Errorf("brawlpit: get level: %w", err)
	}
	if err := json.Unmarshal([]byte(platformsJSON), &l.Platforms); err != nil {
		return nil, fmt.Errorf("brawlpit: decode stored platforms: %w", err)
	}
	return &l, nil
}

// LevelSummary is the real, lightweight shape a level LIST returns -- everything about a Level
// except its own full platform array (a listing view only needs to show a level exists and how
// big it is; PlatformCount is a real number rather than omitting the field entirely).
type LevelSummary struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Width         float64 `json:"width"`
	Height        float64 `json:"height"`
	PlatformCount int     `json:"platform_count"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// ListLevels returns every level as a real, lightweight summary, newest first -- the real
// registry primitive (#290: "a user can browse the user contributed maps... default at top").
func (s *LevelStore) ListLevels(ctx context.Context) ([]LevelSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, width, height, platforms_json, created_at, updated_at
		 FROM brawlpit_levels ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: list levels: %w", err)
	}
	defer rows.Close()

	out := []LevelSummary{}
	for rows.Next() {
		var sum LevelSummary
		var platformsJSON string
		if err := rows.Scan(&sum.ID, &sum.Name, &sum.Width, &sum.Height, &platformsJSON, &sum.CreatedAt, &sum.UpdatedAt); err != nil {
			return nil, fmt.Errorf("brawlpit: list levels: %w", err)
		}
		var platforms []Platform
		if err := json.Unmarshal([]byte(platformsJSON), &platforms); err != nil {
			return nil, fmt.Errorf("brawlpit: decode stored platforms: %w", err)
		}
		sum.PlatformCount = len(platforms)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// UpdateLevel replaces a level's own real editable fields in place -- the web editor's own real
// "save" action (size + platform layout can both change together in one save).
func (s *LevelStore) UpdateLevel(ctx context.Context, id int64, width, height float64, platforms []Platform) (*Level, error) {
	if err := validatePlatforms(platforms); err != nil {
		return nil, err
	}
	platformsJSON, err := json.Marshal(platforms)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: marshal platforms: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE brawlpit_levels SET width = ?, height = ?, platforms_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		width, height, string(platformsJSON), id)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: update level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("brawlpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// RenameLevel updates a level's own name in place.
func (s *LevelStore) RenameLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE brawlpit_levels SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("brawlpit: rename level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("brawlpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// CloneLevel makes a real, full, independent copy of an existing level under a new name -- same
// real shape as nock_textures' own CloneTexture (many independent levels, not one master with
// derived views).
func (s *LevelStore) CloneLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	src, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateLevel(ctx, newName, src.Width, src.Height, src.Platforms)
}

// DeleteLevel permanently removes a level row.
func (s *LevelStore) DeleteLevel(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM brawlpit_levels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("brawlpit: delete level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("brawlpit: level %d not found", id)
	}
	return nil
}

// Export returns the real, native-loader-facing document for a level -- exactly the JSON shape
// BRAWLPIT/packages/common/level_format.h's own level_parse_json reads (S415-01/S415-04: the
// real end-to-end proof that a web-authored level actually loads in the native client).
func (s *LevelStore) Export(ctx context.Context, id int64) (*ExportDoc, error) {
	lvl, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ExportDoc{Version: 1, Name: lvl.Name, Width: lvl.Width, Height: lvl.Height, Platforms: lvl.Platforms}, nil
}
