package shankpit

// material_store.go -- real CRUD for SHANKPIT block materials (S459-16, founder real-time: "i
// think it makes sense to abstract into material first so it cleanly translates into papercraft
// ... we will need the ability to add new materials and set their textures"). See
// migrations/truestore/202609140004_shankpit_materials.sql for the real schema rationale.
//
// Kept in this package (not a new one) for the same real reason level_store.go itself isn't
// split further -- one small, cohesive SHANKPIT-editor domain, not yet worth its own package
// boundary. The founder's own "cleanly translates into papercraft" framing is a real, named
// future move (a shared materials concept both games draw from), not attempted here -- see this
// file's own DefaultMaterialName doc comment for the one place that future move would touch.

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

// Material is one row of the shankpit_materials table.
type Material struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// ShaderName (S459-16, founder real-time: "build the shaders in to the native and then refer
	// to the shaders from NOCK directly? a shader registry too but for now it will just be like
	// the ffi names or whatever not the real shader code") -- a real, named reference into
	// SHANKPIT's own compiled-in shader registry (packages/render/material_shaders.h's own
	// SHADER_* names), NOT GLSL source stored in the DB. NOCK only ever stores/edits this name;
	// the native client owns the real shader implementation behind it. Unrecognized/empty
	// resolves to ShaderStandard at load time (the native loader's own real, honest fallback,
	// same shape as DefaultMaterialName below).
	ShaderName string  `json:"shader_name"`
	TextureID  *int64  `json:"texture_id,omitempty"`
	Specular   float64 `json:"specular"`
	Shininess  float64 `json:"shininess"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// ShaderStandard is the one real, built-in shader VS0 ships (a real Blinn-Phong specular-
// highlight pass, see apps/lobby's own material_shaders.h) -- the default for every material
// unless/until more named shaders exist (founder: "vs0 are any correct real shaders").
const ShaderStandard = "standard"

// ShaderIPSLight -- founder real-time, 2026-09-14: "can we design a material for an IPS light its
// going to need a special shader build it in." The real, no-longer-deferred emissive-light
// material this file's own comment above used to name as future work. Native side:
// packages/render/material_shaders.h's own SHADER_IPS_LIGHT -- a real, unlit emissive pass (skips
// the normal per-face day/night darkening entirely, draws a bright, mostly lighting-independent
// glow) instead of the additive specular-highlight-on-top-of-lit-color treatment ShaderStandard
// uses, since a light fixture shouldn't itself look dark on its unlit side.
const ShaderIPSLight = "ips_light"

// ShaderHPSLight -- founder real-time, 2026-09-14: "can you do it again for a high pressure
// sodium light with a flicker like in this video" (a YouTube link that couldn't actually be
// fetched/watched from here -- built from well-documented real HPS behavior instead: a dying/
// cycling HPS bulb repeatedly strikes, brightens, dims, nearly extinguishes, and restrikes over a
// few real seconds, not a fast strobe). Native side: packages/render/material_shaders.h's own
// SHADER_HPS_LIGHT -- same real unlit-emissive treatment ShaderIPSLight established, plus a real,
// animated multi-frequency flicker term and a warm amber/orange sodium-vapor color (real HPS
// lamps emit an almost-monochromatic yellow-orange, the sodium D line at ~589nm).
const ShaderHPSLight = "hps_light"

var validShaderNames = map[string]bool{ShaderStandard: true, ShaderIPSLight: true, ShaderHPSLight: true}

// DefaultMaterialName is what a Wall with an empty/unset Material field resolves to -- founder,
// direct: "the default material is brick because thats the texture of blocks by default." Also
// the real fallback the native loader itself uses when a level's own `materials` export array is
// missing entirely (an older, pre-S459-16 level) or doesn't contain the name a wall references.
const DefaultMaterialName = "brick"

var validMaterialName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// MinSpecular/MaxSpecular and MinShininess/MaxShininess bound the real, editable Blinn-Phong
// parameters -- a real, sane range (0 = no highlight at all, 1 = a fully mirror-bright highlight;
// shininess up to 256 comfortably covers "brick" through "polished metal"), not unbounded floats.
const MinSpecular = 0.0
const MaxSpecular = 1.0
const MinShininess = 1.0
const MaxShininess = 256.0

func validateMaterialName(name string) error {
	if !validMaterialName.MatchString(name) {
		return fmt.Errorf("shankpit: invalid material name %q (must match %s -- lowercase, starts with a letter)", name, validMaterialName.String())
	}
	return nil
}

func validateMaterialShading(specular, shininess float64) error {
	if specular < MinSpecular || specular > MaxSpecular {
		return fmt.Errorf("shankpit: material specular must be in [%g, %g], got %g", MinSpecular, MaxSpecular, specular)
	}
	if shininess < MinShininess || shininess > MaxShininess {
		return fmt.Errorf("shankpit: material shininess must be in [%g, %g], got %g", MinShininess, MaxShininess, shininess)
	}
	return nil
}

func validateShaderName(name string) error {
	if !validShaderNames[name] {
		return fmt.Errorf("shankpit: unknown shader_name %q (real, native shaders only -- see ShaderStandard's own doc comment)", name)
	}
	return nil
}

// MaterialStore is the real SQLite-backed CRUD layer.
type MaterialStore struct {
	DB *sql.DB
}

const materialColumns = `id, name, shader_name, texture_id, specular, shininess, created_at, updated_at`

func scanMaterial(row interface {
	Scan(dest ...any) error
}) (*Material, error) {
	var m Material
	var textureID sql.NullInt64
	if err := row.Scan(&m.ID, &m.Name, &m.ShaderName, &textureID, &m.Specular, &m.Shininess, &m.CreatedAt, &m.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: material not found")
		}
		return nil, fmt.Errorf("shankpit: get material: %w", err)
	}
	if textureID.Valid {
		m.TextureID = &textureID.Int64
	}
	return &m, nil
}

// ListMaterials returns every material, alphabetical by name -- the real picker source both the
// NOCK level editor (per-wall material dropdown) and the native client's own fetch use.
func (s *MaterialStore) ListMaterials(ctx context.Context) ([]Material, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+materialColumns+` FROM shankpit_materials ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list materials: %w", err)
	}
	defer rows.Close()
	out := []Material{}
	for rows.Next() {
		m, err := scanMaterial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetMaterialByName is the real lookup Export's own flatten pass uses to resolve each wall's
// Material string into real shading parameters -- see level_store.go's own materialsForExport.
func (s *MaterialStore) GetMaterialByName(ctx context.Context, name string) (*Material, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+materialColumns+` FROM shankpit_materials WHERE name = ?`, name)
	return scanMaterial(row)
}

// CreateMaterial adds a new, real, addable material -- founder, direct: "we will need the ability
// to add new materials and set their textures." textureID may be nil (use the native built-in
// procedural default for this name, or -- for a name with no built-in generator at all -- the
// native client's own real, honest "brick" fallback; see level_boxes.h's own doc comment).
// shaderName references SHANKPIT's own real, native, compiled-in shader registry by name (see
// Material's own ShaderName doc comment) -- empty defaults to ShaderStandard.
func (s *MaterialStore) CreateMaterial(ctx context.Context, name, shaderName string, textureID *int64, specular, shininess float64) (*Material, error) {
	if err := validateMaterialName(name); err != nil {
		return nil, err
	}
	if shaderName == "" {
		shaderName = ShaderStandard
	}
	if err := validateShaderName(shaderName); err != nil {
		return nil, err
	}
	if err := validateMaterialShading(specular, shininess); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_materials (name, shader_name, texture_id, specular, shininess) VALUES (?, ?, ?, ?, ?)`,
		name, shaderName, textureID, specular, shininess)
	if err != nil {
		return nil, fmt.Errorf("shankpit: create material: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create material: %w", err)
	}
	return s.getMaterialByID(ctx, id)
}

func (s *MaterialStore) getMaterialByID(ctx context.Context, id int64) (*Material, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+materialColumns+` FROM shankpit_materials WHERE id = ?`, id)
	return scanMaterial(row)
}

// UpdateMaterial replaces a material's own real editable fields -- founder: "set their textures"
// (textureID nil clears back to the native procedural default; non-nil sets a real NOCK-managed
// override -- see this file's own DefaultMaterialName / package doc comment on the native
// loader's own real, current "override stored but not yet fetched/decoded" limitation).
func (s *MaterialStore) UpdateMaterial(ctx context.Context, id int64, shaderName string, textureID *int64, specular, shininess float64) (*Material, error) {
	if shaderName == "" {
		shaderName = ShaderStandard
	}
	if err := validateShaderName(shaderName); err != nil {
		return nil, err
	}
	if err := validateMaterialShading(specular, shininess); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_materials SET shader_name = ?, texture_id = ?, specular = ?, shininess = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		shaderName, textureID, specular, shininess, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: update material: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: material %d not found", id)
	}
	return s.getMaterialByID(ctx, id)
}

// DeleteMaterial removes a material row. The 4 seeded defaults (brick/concrete/wood/metal) are
// real, ordinary rows, not specially protected -- deleting one just means any wall still
// referencing that name falls back to DefaultMaterialName at export/load time (the same real,
// honest fallback an unrecognized/missing material name already gets).
func (s *MaterialStore) DeleteMaterial(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_materials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete material: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: material %d not found", id)
	}
	return nil
}
