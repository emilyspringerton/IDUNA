package shankpit

// style_store.go -- cube styles (InDesign-style shared appearance). A style is a named bundle of the
// appearance a cube can share: material, colour, friction. Global (LevelID nil) or local to one level.
// Walls reference a style by Wall.StyleID; GetLevel overlays the current style values onto them.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type CubeStyle struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	LevelID  *int64  `json:"level_id"` // nil = global
	Material string  `json:"material"`
	R        float64 `json:"r"`
	G        float64 `json:"g"`
	B        float64 `json:"b"`
	Friction float64 `json:"friction"`
}

type StyleStore struct{ DB *sql.DB }

const styleColumns = `id, name, level_id, material, r, g, b, friction`

func scanStyle(row interface{ Scan(dest ...any) error }) (*CubeStyle, error) {
	var st CubeStyle
	var lv sql.NullInt64
	if err := row.Scan(&st.ID, &st.Name, &lv, &st.Material, &st.R, &st.G, &st.B, &st.Friction); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: style not found")
		}
		return nil, fmt.Errorf("shankpit: get style: %w", err)
	}
	if lv.Valid {
		st.LevelID = &lv.Int64
	}
	return &st, nil
}

func validateStyle(st *CubeStyle) error {
	st.Name = strings.TrimSpace(st.Name)
	if st.Name == "" || len(st.Name) > 60 {
		return fmt.Errorf("shankpit: style name must be 1-60 characters")
	}
	if st.Material == "" {
		st.Material = DefaultMaterialName
	}
	for _, c := range []float64{st.R, st.G, st.B} {
		if c < 0 || c > 1 {
			return fmt.Errorf("shankpit: style colour channels must be in [0,1]")
		}
	}
	return validateMaterialFriction(st.Friction)
}

// List returns every global style plus, when levelID is non-zero, that level's local styles.
func (s *StyleStore) List(ctx context.Context, levelID int64) ([]CubeStyle, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+styleColumns+` FROM shankpit_cube_styles WHERE level_id IS NULL OR level_id = ? ORDER BY name ASC`, levelID)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list styles: %w", err)
	}
	defer rows.Close()
	out := []CubeStyle{}
	for rows.Next() {
		st, err := scanStyle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

func (s *StyleStore) Get(ctx context.Context, id int64) (*CubeStyle, error) {
	return scanStyle(s.DB.QueryRowContext(ctx, `SELECT `+styleColumns+` FROM shankpit_cube_styles WHERE id = ?`, id))
}

func (s *StyleStore) Create(ctx context.Context, st CubeStyle) (*CubeStyle, error) {
	if err := validateStyle(&st); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO shankpit_cube_styles (name, level_id, material, r, g, b, friction) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		st.Name, st.LevelID, st.Material, st.R, st.G, st.B, st.Friction)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("shankpit: a style named %q already exists in that scope", st.Name)
		}
		return nil, fmt.Errorf("shankpit: create style: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.Get(ctx, id)
}

// Update replaces a style's name, scope and appearance (flipping LevelID is how a style becomes
// global or local).
func (s *StyleStore) Update(ctx context.Context, id int64, st CubeStyle) (*CubeStyle, error) {
	if err := validateStyle(&st); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE shankpit_cube_styles SET name = ?, level_id = ?, material = ?, r = ?, g = ?, b = ?, friction = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		st.Name, st.LevelID, st.Material, st.R, st.G, st.B, st.Friction, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("shankpit: a style named %q already exists in that scope", st.Name)
		}
		return nil, fmt.Errorf("shankpit: update style: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: style %d not found", id)
	}
	return s.Get(ctx, id)
}

func (s *StyleStore) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_cube_styles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete style: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: style %d not found", id)
	}
	return nil
}

// applyStyles overlays each styled wall's current style values onto the wall. A wall whose style is
// gone, or is a local style of a different level, keeps the values it has (it is simply detached).
func (s *LevelStore) applyStyles(ctx context.Context, l *Level) {
	need := false
	for _, w := range l.Walls {
		if w.StyleID != 0 {
			need = true
			break
		}
	}
	if !need {
		return
	}
	styles, err := (&StyleStore{DB: s.DB}).List(ctx, l.ID)
	if err != nil {
		return
	}
	byID := make(map[int64]CubeStyle, len(styles))
	for _, st := range styles {
		byID[st.ID] = st
	}
	for i := range l.Walls {
		if st, ok := byID[l.Walls[i].StyleID]; ok {
			l.Walls[i].Material, l.Walls[i].R, l.Walls[i].G, l.Walls[i].B, l.Walls[i].Friction = st.Material, st.R, st.G, st.B, st.Friction
		}
	}
}
