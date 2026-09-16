package nock

// door_script_store.go -- real CRUD + SQLite persistence for NOCK's door script repository,
// mirroring texture_store.go/anim_store.go's own established shape exactly. See
// door_script_compile.go's own header comment for the real compile pipeline this wraps.

import (
	"context"
	"database/sql"
	"fmt"
)

// DoorScript is one row of the nock_door_scripts table.
type DoorScript struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	ParenaSource string `json:"parena_source"`
	CompiledSO   []byte `json:"-"` // never inlined into a JSON response -- see DoorScriptSummary
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// DoorScriptSummary is the real, lightweight shape a LIST returns -- no source/blob.
type DoorScriptSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// DoorScriptStore is the real SQLite-backed CRUD layer.
type DoorScriptStore struct {
	DB *sql.DB
}

// CreateDoorScript compiles prnSource (via compileDoorScript, which validates+compiles+never
// touches the DB on failure -- same "a bad edit never destroys a working row" ordering
// texture_store.go's own RegenerateTexture already established) and inserts the result as a new
// row.
func (s *DoorScriptStore) CreateDoorScript(ctx context.Context, name, prnSource string) (*DoorScript, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	so, err := compileDoorScript(prnSource)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_door_scripts (name, parena_source, compiled_so) VALUES (?, ?, ?)`,
		name, prnSource, so)
	if err != nil {
		return nil, fmt.Errorf("nock: create door script: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create door script: %w", err)
	}
	return s.GetDoorScript(ctx, id)
}

// GetDoorScript returns the full row, including its real compiled bytes and source.
func (s *DoorScriptStore) GetDoorScript(ctx context.Context, id int64) (*DoorScript, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, parena_source, compiled_so, created_at, updated_at FROM nock_door_scripts WHERE id = ?`, id)
	var d DoorScript
	if err := row.Scan(&d.ID, &d.Name, &d.ParenaSource, &d.CompiledSO, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("nock: door script not found")
		}
		return nil, fmt.Errorf("nock: get door script: %w", err)
	}
	return &d, nil
}

// ListDoorScripts returns every door script as a real, lightweight summary, newest first.
func (s *DoorScriptStore) ListDoorScripts(ctx context.Context) ([]DoorScriptSummary, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM nock_door_scripts ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("nock: list door scripts: %w", err)
	}
	defer rows.Close()
	out := []DoorScriptSummary{}
	for rows.Next() {
		var d DoorScriptSummary
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("nock: list door scripts: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RegenerateDoorScript re-compiles an existing row from edited PARENA source, replacing its
// compiled_so/parena_source in place. Same real "compile before touching the row" ordering
// CreateDoorScript uses -- a failed edit leaves the existing, working script untouched.
func (s *DoorScriptStore) RegenerateDoorScript(ctx context.Context, id int64, prnSource string) (*DoorScript, error) {
	so, err := compileDoorScript(prnSource)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nock_door_scripts SET parena_source = ?, compiled_so = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		prnSource, so, id)
	if err != nil {
		return nil, fmt.Errorf("nock: regenerate door script: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: door script %d not found", id)
	}
	return s.GetDoorScript(ctx, id)
}

// DeleteDoorScript permanently removes a door script row.
func (s *DoorScriptStore) DeleteDoorScript(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_door_scripts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete door script: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: door script %d not found", id)
	}
	return nil
}
