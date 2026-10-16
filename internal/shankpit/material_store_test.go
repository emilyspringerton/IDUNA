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
			density     REAL NOT NULL DEFAULT 0,
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
	m, err := s.CreateMaterial(context.Background(), "glass", "", nil, 0.5, 32, 0.30, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ShaderName != shankpit.ShaderStandard {
		t.Fatalf("expected shader_name to default to %q, got %q", shankpit.ShaderStandard, m.ShaderName)
	}
}

func TestCreateMaterial_RejectsUnknownShader(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "glowstone", "flourescent_light", nil, 0, 8, 0.30, 0); err == nil {
		t.Fatal("expected an error for an unknown shader_name (VS1's own emissive shader doesn't exist yet)")
	}
}

func TestCreateMaterial_RejectsOutOfRangeShading(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "glass", "", nil, 1.5, 32, 0.30, 0); err == nil {
		t.Fatal("expected an error for specular above the real maximum")
	}
	if _, err := s.CreateMaterial(context.Background(), "glass", "", nil, 0.5, 0, 0.30, 0); err == nil {
		t.Fatal("expected an error for shininess below the real minimum")
	}
}

func TestCreateMaterial_RejectsOutOfRangeFriction(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "ice", "", nil, 0.5, 32, -0.1, 0); err == nil {
		t.Fatal("expected an error for friction below the real minimum")
	}
	if _, err := s.CreateMaterial(context.Background(), "ice", "", nil, 0.5, 32, 1.5, 0); err == nil {
		t.Fatal("expected an error for friction above the real maximum")
	}
}

func TestGetMaterialByName_RealLookup(t *testing.T) {
	s := newTestMaterialStore(t)
	if _, err := s.CreateMaterial(context.Background(), "metal", "", nil, 0.85, 64, 0.14, 0); err != nil {
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
	created, err := s.CreateMaterial(context.Background(), "wood", "", nil, 0.06, 8, 0.34, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	texID := int64(42)
	updated, err := s.UpdateMaterial(context.Background(), created.ID, "", &texID, 0.06, 8, 0.34, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.TextureID == nil || *updated.TextureID != 42 {
		t.Fatalf("expected texture_id override to stick, got %+v", updated.TextureID)
	}
}

func TestUpdateMaterial_SetsFriction(t *testing.T) {
	s := newTestMaterialStore(t)
	created, err := s.CreateMaterial(context.Background(), "wood", "", nil, 0.06, 8, 0.30, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := s.UpdateMaterial(context.Background(), created.ID, "", nil, 0.06, 8, 0.5, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Friction != 0.5 {
		t.Fatalf("expected friction 0.5, got %v", updated.Friction)
	}
}

func TestDeleteMaterial(t *testing.T) {
	s := newTestMaterialStore(t)
	created, err := s.CreateMaterial(context.Background(), "concrete", "", nil, 0.03, 4, 0.30, 0)
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

func TestCreateMaterial_AcceptsGlassShader(t *testing.T) {
	s := newTestMaterialStore(t)
	m, err := s.CreateMaterial(context.Background(), "windowpane", shankpit.ShaderGlass, nil, 0.8, 64, 0.30, 0)
	if err != nil {
		t.Fatalf("create glass-shader material: %v", err)
	}
	if m.ShaderName != "glass" {
		t.Fatalf("shader_name = %q, want glass", m.ShaderName)
	}
	if _, err := s.CreateMaterial(context.Background(), "bogus", "nope", nil, 0.5, 32, 0.30, 0); err == nil {
		t.Fatal("unknown shader must be rejected")
	}
}

func TestMaterialDensity_CreateUpdateKeep(t *testing.T) {
	s := newTestMaterialStore(t)
	ctx := context.Background()
	created, err := s.CreateMaterial(ctx, "glass2", "", nil, 0.05, 8, 0.30, 2500)
	if err != nil {
		t.Fatalf("create with density: %v", err)
	}
	if created.Density != 2500 {
		t.Fatalf("density = %v, want 2500", created.Density)
	}
	// nil keeps: an older editor build that never sends density must not reset a configured mass
	kept, err := s.UpdateMaterial(ctx, created.ID, "", nil, 0.05, 8, 0.30, nil)
	if err != nil || kept.Density != 2500 {
		t.Fatalf("update with nil density: density=%v err=%v, want 2500 kept", kept, err)
	}
	d := 1900.0
	set, err := s.UpdateMaterial(ctx, created.ID, "", nil, 0.05, 8, 0.30, &d)
	if err != nil || set.Density != 1900 {
		t.Fatalf("update with density 1900: %v %v", set, err)
	}
	zero := 0.0
	reset, err := s.UpdateMaterial(ctx, created.ID, "", nil, 0.05, 8, 0.30, &zero)
	if err != nil || reset.Density != 0 {
		t.Fatalf("explicit 0 must mean engine default: %v %v", reset, err)
	}
}

func TestMaterialDensity_RejectsOutOfRange(t *testing.T) {
	s := newTestMaterialStore(t)
	ctx := context.Background()
	if _, err := s.CreateMaterial(ctx, "neg", "", nil, 0.05, 8, 0.30, -1); err == nil {
		t.Fatal("negative density must be rejected")
	}
	if _, err := s.CreateMaterial(ctx, "heavy", "", nil, 0.05, 8, 0.30, 99999); err == nil {
		t.Fatal("absurd density must be rejected")
	}
	created, err := s.CreateMaterial(ctx, "ok", "", nil, 0.05, 8, 0.30, 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := -5.0
	if _, err := s.UpdateMaterial(ctx, created.ID, "", nil, 0.05, 8, 0.30, &bad); err == nil {
		t.Fatal("negative density on update must be rejected")
	}
}
