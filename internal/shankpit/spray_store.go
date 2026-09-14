package shankpit

// spray_store.go -- real CRUD for SHANKPIT sprays (S459-19, founder real-time: "can we implement
// sprays? ... export to spray goes to sprays registry same treatment ... we need a nock sprays
// interface right now just to set the default"). Same real shape as internal/nock.TextureStore
// (independent rows, no shared master, real BLOB png_data) -- see
// migrations/truestore/202609140005_shankpit_sprays.sql for the full schema rationale.

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

// Spray is one row of the shankpit_sprays table.
type Spray struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	PNGData   []byte `json:"-"` // never inlined into a JSON list response -- see SpraySummary
	IsDefault bool   `json:"is_default"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SpraySummary is the real, lightweight shape a spray LIST returns -- everything except the raw
// PNG bytes, matching TextureSummary's own precedent.
type SpraySummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	IsDefault bool   `json:"is_default"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

var validSprayName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]{0,63}$`)

func validateSprayName(name string) error {
	if !validSprayName.MatchString(name) {
		return fmt.Errorf("shankpit: invalid spray name %q (must match %s)", name, validSprayName.String())
	}
	return nil
}

// SprayStore is the real SQLite-backed CRUD layer.
type SprayStore struct {
	DB *sql.DB
}

// CreateSpray inserts a new, real, independent spray row -- the real target of NOCK's own
// "Export to Spray" button (founder: "CREATE SPRAYS FROM THE NOCK TEXTURE GENERATOR"). The very
// first spray ever created becomes the real default automatically (a fresh install otherwise has
// no default spray for the native client to fall back to).
func (s *SprayStore) CreateSpray(ctx context.Context, name string, width, height int, pngData []byte) (*Spray, error) {
	if err := validateSprayName(name); err != nil {
		return nil, err
	}
	if len(pngData) == 0 {
		return nil, fmt.Errorf("shankpit: spray png data is empty")
	}
	var existingCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM shankpit_sprays`).Scan(&existingCount); err != nil {
		return nil, fmt.Errorf("shankpit: create spray: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_sprays (name, width, height, png_data, is_default) VALUES (?, ?, ?, ?, ?)`,
		name, width, height, pngData, existingCount == 0)
	if err != nil {
		return nil, fmt.Errorf("shankpit: create spray: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create spray: %w", err)
	}
	return s.GetSpray(ctx, id)
}

// GetSpray returns the full row, including its real PNG bytes.
func (s *SprayStore) GetSpray(ctx context.Context, id int64) (*Spray, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, png_data, is_default, created_at, updated_at FROM shankpit_sprays WHERE id = ?`, id)
	return scanSpray(row)
}

func scanSpray(row *sql.Row) (*Spray, error) {
	var sp Spray
	if err := row.Scan(&sp.ID, &sp.Name, &sp.Width, &sp.Height, &sp.PNGData, &sp.IsDefault, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: spray not found")
		}
		return nil, fmt.Errorf("shankpit: get spray: %w", err)
	}
	return &sp, nil
}

// ListSprays returns every spray as a real, lightweight summary, newest first -- the real
// registry primitive the founder asked for ("registries for everything").
func (s *SprayStore) ListSprays(ctx context.Context) ([]SpraySummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, width, height, is_default, created_at, updated_at FROM shankpit_sprays ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list sprays: %w", err)
	}
	defer rows.Close()
	out := []SpraySummary{}
	for rows.Next() {
		var sum SpraySummary
		if err := rows.Scan(&sum.ID, &sum.Name, &sum.Width, &sum.Height, &sum.IsDefault, &sum.CreatedAt, &sum.UpdatedAt); err != nil {
			return nil, fmt.Errorf("shankpit: list sprays: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// GetDefaultSpray returns the one real spray currently flagged is_default, or nil (no error) if
// none exists yet (a fresh install, or every spray was deleted) -- a real, honest empty state the
// native client must handle, not an error condition.
func (s *SprayStore) GetDefaultSpray(ctx context.Context) (*Spray, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, png_data, is_default, created_at, updated_at FROM shankpit_sprays WHERE is_default = 1 LIMIT 1`)
	sp, err := scanSpray(row)
	if err != nil {
		if err.Error() == "shankpit: spray not found" {
			return nil, nil
		}
		return nil, err
	}
	return sp, nil
}

// SetDefaultSpray marks id as the one real, global default spray, clearing every other row's own
// flag inside one transaction -- founder: "we need a nock sprays interface right now just to set
// the default." A real, deliberate v0 narrowing to ONE global default, not per-player selection
// persisted server-side yet.
func (s *SprayStore) SetDefaultSpray(ctx context.Context, id int64) (*Spray, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set default spray: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE shankpit_sprays SET is_default = 0`); err != nil {
		return nil, fmt.Errorf("shankpit: set default spray: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE shankpit_sprays SET is_default = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set default spray: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: spray %d not found", id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("shankpit: set default spray: %w", err)
	}
	return s.GetSpray(ctx, id)
}

// DeleteSpray permanently removes a spray row. If it was the current default, no new default is
// auto-promoted (a real, honest "no default until someone sets one" state -- GetDefaultSpray's
// own nil-no-error contract already handles this cleanly for the native client).
func (s *SprayStore) DeleteSpray(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_sprays WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete spray: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: spray %d not found", id)
	}
	return nil
}
