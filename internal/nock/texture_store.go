package nock

// texture_store.go — real CRUD + SQLite persistence for NOCK's own texture library (founder
// real-time, 2026-09-12: "we are making a texture generator and manager so it needs to have CRUD
// and all that and also we are gonna want to save them in sqlite or whatever with their parena
// src"). This is a real re-scope, not an addition alongside the old design unchanged: the
// Project/Layer flat-file compositing engine (project.go/service.go) stays exactly as it is for
// the eventual "manual Photoshop affordances" layer, but the primary managed entity going
// forward is a standalone Texture row in `nock_textures` -- see
// migrations/truestore/202609120001_nock_textures.sql's own header comment for the real schema
// rationale, including why png_data is a real BLOB and why there's no parent_id/clone-
// provenance column.
//
// Clone (same real-time thread, modeled on CarePyre's own real master-resume/clone feature, with
// one explicit, real difference the founder named directly: resumes have exactly one master per
// user with many derived target VIEWS; textures have no single master at all -- "there wont be
// just one master texture there will be many master textures"). CloneTexture below is therefore
// a full, independent row copy, not a view-with-overrides.

import (
	"context"
	"database/sql"
	"fmt"
)

// Texture is one row of the nock_textures table -- the real, primary managed entity of the
// texture library (distinct from Project/Layer, which stays the separate compositing-engine
// concept in project.go/service.go).
type Texture struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	PNGData      []byte `json:"-"`          // never inlined into a JSON list response -- see TextureSummary
	ParenaSource string `json:"parena_source,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// TextureSummary is the real, lightweight shape a texture LIST returns -- everything about a
// Texture except its own real PNG bytes and full PARENA source, both of which can be genuinely
// large and are irrelevant to a library listing view. HasSource/HasPrompt are real booleans
// rather than omitting the fields, so a UI can show a real "generated" badge without fetching
// the full row.
type TextureSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	HasSource bool   `json:"has_source"`
	Prompt    string `json:"prompt,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TextureStore is the real SQLite-backed CRUD layer. DB is expected to already have the
// nock_textures table (via a real migration in production, or a test's own inline CREATE TABLE
// -- see texture_store_test.go).
type TextureStore struct {
	DB *sql.DB
}

// CreateTexture inserts a new, real, independent texture row. parenaSource/prompt may be empty
// strings for a plain (non-procedural) texture -- stored as real SQL NULL, not an empty string,
// so HasSource in a listing reflects "was this actually generated" accurately.
func (s *TextureStore) CreateTexture(ctx context.Context, name string, width, height int, pngData []byte, parenaSource, prompt string) (*Texture, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if len(pngData) == 0 {
		return nil, fmt.Errorf("nock: texture png data is empty")
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_textures (name, width, height, png_data, parena_source, prompt) VALUES (?, ?, ?, ?, ?, ?)`,
		name, width, height, pngData, nullIfEmpty(parenaSource), nullIfEmpty(prompt))
	if err != nil {
		return nil, fmt.Errorf("nock: create texture: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create texture: %w", err)
	}
	return s.GetTexture(ctx, id)
}

// CreateProceduralTexture renders prnSource (validated the same way any procedural source is --
// see procgen.go) and inserts the result as a brand-new texture row. prompt is the real,
// original text prompt if this came from GenerateProceduralTextureSource, or "" for hand-written
// source (e.g. cmd/nock's own CLI path).
func (s *TextureStore) CreateProceduralTexture(ctx context.Context, name, prnSource, prompt string, width, height int) (*Texture, error) {
	pngData, err := renderProcTextureToBytes(prnSource, width, height)
	if err != nil {
		return nil, err
	}
	return s.CreateTexture(ctx, name, width, height, pngData, prnSource, prompt)
}

// GetTexture returns the full row, including its real PNG bytes and PARENA source.
func (s *TextureStore) GetTexture(ctx context.Context, id int64) (*Texture, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, png_data, parena_source, prompt, created_at, updated_at
		 FROM nock_textures WHERE id = ?`, id)
	return scanTexture(row)
}

// GetTextureByName looks a texture up by its real, unique name.
func (s *TextureStore) GetTextureByName(ctx context.Context, name string) (*Texture, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, png_data, parena_source, prompt, created_at, updated_at
		 FROM nock_textures WHERE name = ?`, name)
	return scanTexture(row)
}

func scanTexture(row *sql.Row) (*Texture, error) {
	var t Texture
	var parenaSource, prompt sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &t.Width, &t.Height, &t.PNGData, &parenaSource, &prompt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("nock: texture not found")
		}
		return nil, fmt.Errorf("nock: get texture: %w", err)
	}
	t.ParenaSource = parenaSource.String
	t.Prompt = prompt.String
	return &t, nil
}

// ListTextures returns every texture as a real, lightweight summary (no PNG bytes/full source),
// newest first.
func (s *TextureStore) ListTextures(ctx context.Context) ([]TextureSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, width, height, (parena_source IS NOT NULL), prompt, created_at, updated_at
		 FROM nock_textures ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("nock: list textures: %w", err)
	}
	defer rows.Close()

	out := []TextureSummary{}
	for rows.Next() {
		var s2 TextureSummary
		var prompt sql.NullString
		if err := rows.Scan(&s2.ID, &s2.Name, &s2.Width, &s2.Height, &s2.HasSource, &prompt, &s2.CreatedAt, &s2.UpdatedAt); err != nil {
			return nil, fmt.Errorf("nock: list textures: %w", err)
		}
		s2.Prompt = prompt.String
		out = append(out, s2)
	}
	return out, rows.Err()
}

// RenameTexture updates a texture's own name in place.
func (s *TextureStore) RenameTexture(ctx context.Context, id int64, newName string) (*Texture, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nock_textures SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("nock: rename texture: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: texture %d not found", id)
	}
	return s.GetTexture(ctx, id)
}

// RegenerateTexture re-renders an existing procedural texture from edited PARENA source,
// replacing its png_data/parena_source in place. Real, deliberate ordering: renderProcTexture
// runs BEFORE any row is touched, so a bad edit (fails validation or doesn't compile) leaves the
// existing row completely untouched -- same real "a failed re-run never destroys a working
// texture" guarantee Service.RegenerateProceduralLayer already gives the file-backed path.
func (s *TextureStore) RegenerateTexture(ctx context.Context, id int64, prnSource string) (*Texture, error) {
	existing, err := s.GetTexture(ctx, id)
	if err != nil {
		return nil, err
	}
	pngData, err := renderProcTextureToBytes(prnSource, existing.Width, existing.Height)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nock_textures SET png_data = ?, parena_source = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		pngData, prnSource, id)
	if err != nil {
		return nil, fmt.Errorf("nock: regenerate texture: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: texture %d not found", id)
	}
	return s.GetTexture(ctx, id)
}

// CloneTexture makes a real, full, independent copy of an existing texture under a new name --
// same real PNG bytes and PARENA source, a brand new id, no link back to the original ("there
// wont be just one master texture there will be many master textures" -- the founder's own real
// distinction from CarePyre's one-master-many-views resume model). Editing (renaming,
// regenerating) the clone afterward never touches the original row.
func (s *TextureStore) CloneTexture(ctx context.Context, id int64, newName string) (*Texture, error) {
	src, err := s.GetTexture(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateTexture(ctx, newName, src.Width, src.Height, src.PNGData, src.ParenaSource, src.Prompt)
}

// DeleteTexture permanently removes a texture row.
func (s *TextureStore) DeleteTexture(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_textures WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete texture: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: texture %d not found", id)
	}
	return nil
}

func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
