package shankpit_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

func newTestMaterialStore(t *testing.T) *shankpit.MaterialStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE shankpit_materials (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			shader_name TEXT NOT NULL DEFAULT 'standard',
			texture_id  INTEGER,
			specular    REAL NOT NULL DEFAULT 0,
			shininess   REAL NOT NULL DEFAULT 8,
			friction    REAL NOT NULL DEFAULT 0.30,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_materials: %v", err)
	}
	return &shankpit.MaterialStore{DB: db}
}

func TestCreateMaterial_DefaultsShaderNameToStandard(t *testing.T) {
	s := newTestMaterialStore(t)
	m, err := s.CreateMaterial(context.Background(), "glass", "", nil, 0.5, 32, 0.30)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ShaderName != shankpit.ShaderStandard {
		t.Fatalf("expected shader_name to default to %q, got %q", shankpit.ShaderStandard, m.ShaderName)
	}
}

func TestCreateMaterial_RejectsUnknownShader(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "glowstone", "flourescent_light", nil, 0, 8, 0.30); err == nil {
		t.Fatal("expected an error for an unknown shader_name (VS1's own emissive shader doesn't exist yet)")
	}
}

func TestCreateMaterial_RejectsOutOfRangeShading(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "glass", "", nil, 1.5, 32, 0.30); err == nil {
		t.Fatal("expected an error for specular above the real maximum")
	}
	if _, err := s.CreateMaterial(context.Background(), "glass", "", nil, 0.5, 0, 0.30); err == nil {
		t.Fatal("expected an error for shininess below the real minimum")
	}
}

func TestCreateMaterial_RejectsOutOfRangeFriction(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "ice", "", nil, 0.5, 32, -0.1); err == nil {
		t.Fatal("expected an error for friction below the real minimum")
	}
	if _, err := s.CreateMaterial(context.Background(), "ice", "", nil, 0.5, 32, 1.5); err == nil {
		t.Fatal("expected an error for friction above the real maximum")
	}
}

func TestGetMaterialByName_RealLookup(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "metal", "", nil, 0.85, 64, 0.14); err != nil {
		t.Fatalf("create: %v", err)
	}
	m, err := s.GetMaterialByName(context.Background(), "metal")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if m.Specular != 0.85 || m.Shininess != 64 {
		t.Fatalf("unexpected material: %+v", m)
	}
	if m.Friction != 0.14 {
		t.Fatalf("expected friction 0.14, got %v", m.Friction)
	}
}

func TestUpdateMaterial_SetsTextureOverride(t *testing.T) {
	s := newTestMaterialStore(t)
	created, err := s.CreateMaterial(context.Background(), "wood", "", nil, 0.06, 8, 0.34)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	texID := int64(42)
	updated, err := s.UpdateMaterial(context.Background(), created.ID, "", &texID, 0.06, 8, 0.34)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.TextureID == nil || *updated.TextureID != 42 {
		t.Fatalf("expected texture_id override to stick, got %+v", updated.TextureID)
	}
}

func TestUpdateMaterial_SetsFriction(t *testing.T) {
	s := newTestMaterialStore(t)
	created, err := s.CreateMaterial(context.Background(), "wood", "", nil, 0.06, 8, 0.30)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := s.UpdateMaterial(context.Background(), created.ID, "", nil, 0.06, 8, 0.5)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Friction != 0.5 {
		t.Fatalf("expected friction 0.5, got %v", updated.Friction)
	}
}

func TestDeleteMaterial(t *testing.T) {
	s := newTestMaterialStore(t)
	created, err := s.CreateMaterial(context.Background(), "concrete", "", nil, 0.03, 4, 0.30)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteMaterial(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetMaterialByName(context.Background(), "concrete"); err == nil {
		t.Fatal("expected material to be gone after delete")
	}
}
