package nock

// anim_store.go — real CRUD + SQLite persistence for NOCK's animation repository (founder
// real-time, 2026-09-16: "need animation repository" — the storage/browse half of "let's start
// iterating towards nock tools modeler (blender) and golden band"). Same real BLOB-in-SQLite
// pattern texture_store.go already established for the texture library; see
// migrations/truestore/202609160001_nock_animations.sql's own header comment for the schema
// rationale, including why gskel_data/gmesh_data are nullable and manifest_json stays JSON
// rather than being split into columns.

import (
	"context"
	"database/sql"
	"fmt"
)

// Animation is one row of the nock_animations table.
type Animation struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	TickRate       int    `json:"tick_rate"`
	DurationTicks  int    `json:"duration_ticks"`
	NumChannels    int    `json:"num_channels"`
	ContentHash    string `json:"content_hash"`
	GBandData      []byte `json:"-"` // never inlined into a JSON response -- see AnimationSummary
	ManifestJSON   string `json:"manifest_json"`
	GSkelData      []byte `json:"-"`
	GMeshData      []byte `json:"-"`
	SourceLocation string `json:"source_location,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// AnimationSummary is the real, lightweight shape a LIST returns -- everything about an
// Animation except its own potentially-large blobs and manifest text. HasSkel/HasMesh are real
// booleans so a UI can show what a row actually carries without fetching the full row.
type AnimationSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	TickRate       int    `json:"tick_rate"`
	DurationTicks  int    `json:"duration_ticks"`
	NumChannels    int    `json:"num_channels"`
	HasSkel        bool   `json:"has_skel"`
	HasMesh        bool   `json:"has_mesh"`
	SourceLocation string `json:"source_location,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// AnimStore is the real SQLite-backed CRUD layer. DB is expected to already have the
// nock_animations table (via a real migration in production, or a test's own inline CREATE
// TABLE -- see anim_store_test.go).
type AnimStore struct {
	DB *sql.DB
}

// CreateAnimation inserts a new, real, independent animation row. gskelData/gmeshData may be nil
// for a clip uploaded without a paired skeleton/mesh.
func (s *AnimStore) CreateAnimation(ctx context.Context, name string, tickRate, durationTicks, numChannels int, contentHash string, gbandData []byte, manifestJSON string, gskelData, gmeshData []byte, sourceLocation string) (*Animation, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if len(gbandData) == 0 {
		return nil, fmt.Errorf("nock: animation gband data is empty")
	}
	if len(gbandData) < 4 || string(gbandData[0:4]) != "GBND" {
		return nil, fmt.Errorf("nock: gband data has bad magic, expected a real .gband file starting with \"GBND\"")
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_animations (name, tick_rate, duration_ticks, num_channels, content_hash, gband_data, manifest_json, gskel_data, gmesh_data, source_location)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, tickRate, durationTicks, numChannels, contentHash, gbandData, manifestJSON, nullBytesIfEmpty(gskelData), nullBytesIfEmpty(gmeshData), nullIfEmpty(sourceLocation))
	if err != nil {
		return nil, fmt.Errorf("nock: create animation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create animation: %w", err)
	}
	return s.GetAnimation(ctx, id)
}

// GetAnimation returns the full row, including its real blobs and manifest text.
func (s *AnimStore) GetAnimation(ctx context.Context, id int64) (*Animation, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, tick_rate, duration_ticks, num_channels, content_hash, gband_data, manifest_json, gskel_data, gmesh_data, source_location, created_at, updated_at
		 FROM nock_animations WHERE id = ?`, id)
	return scanAnimation(row)
}

func scanAnimation(row *sql.Row) (*Animation, error) {
	var a Animation
	var gskel, gmesh []byte
	var sourceLocation sql.NullString
	if err := row.Scan(&a.ID, &a.Name, &a.TickRate, &a.DurationTicks, &a.NumChannels, &a.ContentHash,
		&a.GBandData, &a.ManifestJSON, &gskel, &gmesh, &sourceLocation, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("nock: animation not found")
		}
		return nil, fmt.Errorf("nock: get animation: %w", err)
	}
	a.GSkelData = gskel
	a.GMeshData = gmesh
	a.SourceLocation = sourceLocation.String
	return &a, nil
}

// ListAnimations returns every animation as a real, lightweight summary (no blobs/manifest
// text), newest first.
func (s *AnimStore) ListAnimations(ctx context.Context) ([]AnimationSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, tick_rate, duration_ticks, num_channels, (gskel_data IS NOT NULL), (gmesh_data IS NOT NULL), source_location, created_at, updated_at
		 FROM nock_animations ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("nock: list animations: %w", err)
	}
	defer rows.Close()

	out := []AnimationSummary{}
	for rows.Next() {
		var s2 AnimationSummary
		var sourceLocation sql.NullString
		if err := rows.Scan(&s2.ID, &s2.Name, &s2.TickRate, &s2.DurationTicks, &s2.NumChannels, &s2.HasSkel, &s2.HasMesh, &sourceLocation, &s2.CreatedAt, &s2.UpdatedAt); err != nil {
			return nil, fmt.Errorf("nock: list animations: %w", err)
		}
		s2.SourceLocation = sourceLocation.String
		out = append(out, s2)
	}
	return out, rows.Err()
}

// RenameAnimation updates an animation's own name in place.
func (s *AnimStore) RenameAnimation(ctx context.Context, id int64, newName string) (*Animation, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nock_animations SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("nock: rename animation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: animation %d not found", id)
	}
	return s.GetAnimation(ctx, id)
}

// CloneAnimation makes a real, full, independent copy of an existing animation under a new name
// -- same real bytes/manifest, a brand new id, no link back to the original (same "many
// independent masters, no parent_id" convention texture_store.go's own CloneTexture already
// uses).
func (s *AnimStore) CloneAnimation(ctx context.Context, id int64, newName string) (*Animation, error) {
	src, err := s.GetAnimation(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateAnimation(ctx, newName, src.TickRate, src.DurationTicks, src.NumChannels, src.ContentHash, src.GBandData, src.ManifestJSON, src.GSkelData, src.GMeshData, src.SourceLocation)
}

// DeleteAnimation permanently removes an animation row.
func (s *AnimStore) DeleteAnimation(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_animations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete animation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: animation %d not found", id)
	}
	return nil
}

func nullBytesIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
