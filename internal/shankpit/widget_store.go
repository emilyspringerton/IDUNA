package shankpit

// widget_store.go -- real CRUD for SHANKPIT Widgets (S482, founder real-time, direct correction
// of the earlier S479-follow-up door-composition work: "i still dont know how to add a door...
// i dont want to make doors be levels please - make widget or something they are both objects
// but widgets just dont show up in the levels menu and the geometry of the widget shows up not
// the geometry of the underlying level under the widget - there should be no ground plane and no
// dimension in the widget - a level is a dimension - a widget is just a widget."
//
// A Widget is walls + doors only -- no width/height/depth, no ground plane, no spawners/nav
// nodes/characters/level exits/next_level_id/story flags, and (v0, real, deliberate scope limit)
// no Objects of its own -- a widget is a leaf-level reusable geometry piece, placed into a level
// via LevelObject's own new RefWidgetID field, composed by flattenObjects the same real way a
// referenced level's walls/doors already are, just without recursing further.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Widget is one row of the shankpit_widgets table.
type Widget struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Walls     []Wall  `json:"walls"`
	Doors     []Door  `json:"doors"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// WidgetSummary is the real, lightweight shape a widget LIST returns -- everything about a
// Widget except its own full wall/door arrays, matching LevelSummary's own precedent.
type WidgetSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	WallCount int    `json:"wall_count"`
	DoorCount int    `json:"door_count"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// WidgetStore is the real SQLite-backed CRUD layer.
type WidgetStore struct {
	DB *sql.DB
}

// CreateWidget inserts a new, real, independent widget row. A widget may start with zero walls,
// same real "create then add a cube" flow CreateLevel already establishes.
func (s *WidgetStore) CreateWidget(ctx context.Context, name string, walls []Wall, doors []Door) (*Widget, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if err := validateDoors(doors, walls); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	if doors == nil {
		doors = []Door{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal widget walls: %w", err)
	}
	doorsJSON, err := json.Marshal(doors)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal widget doors: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_widgets (name, walls_json, doors_json) VALUES (?, ?, ?)`,
		name, string(wallsJSON), string(doorsJSON))
	if err != nil {
		return nil, fmt.Errorf("shankpit: create widget: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create widget: %w", err)
	}
	return s.GetWidget(ctx, id)
}

// GetWidget returns the full row, including its real wall/door lists.
func (s *WidgetStore) GetWidget(ctx context.Context, id int64) (*Widget, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, walls_json, doors_json, created_at, updated_at FROM shankpit_widgets WHERE id = ?`, id)
	return scanWidget(row)
}

func scanWidget(row *sql.Row) (*Widget, error) {
	var w Widget
	var wallsJSON, doorsJSON string
	if err := row.Scan(&w.ID, &w.Name, &wallsJSON, &doorsJSON, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: widget not found")
		}
		return nil, fmt.Errorf("shankpit: get widget: %w", err)
	}
	if err := json.Unmarshal([]byte(wallsJSON), &w.Walls); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored widget walls: %w", err)
	}
	if err := json.Unmarshal([]byte(doorsJSON), &w.Doors); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored widget doors: %w", err)
	}
	return &w, nil
}

// ListWidgets returns every widget as a real, lightweight summary, newest first -- the real
// widget-picker registry the level editor's own Objects panel draws from (kept OUT of the
// ordinary Levels list/menu entirely -- a separate table, a separate endpoint, the founder's own
// explicit "widgets just dont show up in the levels menu").
func (s *WidgetStore) ListWidgets(ctx context.Context) ([]WidgetSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, walls_json, doors_json, created_at, updated_at FROM shankpit_widgets ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list widgets: %w", err)
	}
	defer rows.Close()
	out := []WidgetSummary{}
	for rows.Next() {
		var id int64
		var name, wallsJSON, doorsJSON, createdAt, updatedAt string
		if err := rows.Scan(&id, &name, &wallsJSON, &doorsJSON, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("shankpit: list widgets: %w", err)
		}
		var walls []Wall
		var doors []Door
		if err := json.Unmarshal([]byte(wallsJSON), &walls); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored widget walls: %w", err)
		}
		if err := json.Unmarshal([]byte(doorsJSON), &doors); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored widget doors: %w", err)
		}
		out = append(out, WidgetSummary{ID: id, Name: name, WallCount: len(walls), DoorCount: len(doors), CreatedAt: createdAt, UpdatedAt: updatedAt})
	}
	return out, rows.Err()
}

// UpdateWidget replaces a widget's own real editable fields (walls/doors) -- name is immutable
// here for the same reason level names are handled separately (RenameLevel), not attempted for
// widgets in this v0 pass (real, deliberate scope limit -- no rename UI yet either).
func (s *WidgetStore) UpdateWidget(ctx context.Context, id int64, walls []Wall, doors []Door) (*Widget, error) {
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if err := validateDoors(doors, walls); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	if doors == nil {
		doors = []Door{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal widget walls: %w", err)
	}
	doorsJSON, err := json.Marshal(doors)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal widget doors: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_widgets SET walls_json = ?, doors_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(wallsJSON), string(doorsJSON), id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: update widget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: widget %d not found", id)
	}
	return s.GetWidget(ctx, id)
}

// DeleteWidget removes a widget row. Same real, accepted convention DeleteLevel already uses: an
// Object still referencing this widget's id fails lazily at Export time (a real, honest error),
// not eagerly blocked here.
func (s *WidgetStore) DeleteWidget(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_widgets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete widget: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: widget %d not found", id)
	}
	return nil
}
