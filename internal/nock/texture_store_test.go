package nock

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newTextureTestDB mirrors gfd_item_proposals_test.go's own real in-memory-SQLite convention --
// an inline CREATE TABLE matching the real migration's schema exactly, not a mock.
func newTextureTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_textures (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			width         INTEGER NOT NULL,
			height        INTEGER NOT NULL,
			png_data      BLOB NOT NULL,
			parena_source TEXT,
			prompt        TEXT,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_textures: %v", err)
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX idx_nock_textures_name ON nock_textures(name)`)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	return db
}

func TestTextureStore_CreateGetList(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateTexture(ctx, "brick", 64, 64, []byte("fake-png-bytes"), "", "")
	if err != nil {
		t.Fatalf("CreateTexture: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a real assigned ID")
	}

	got, err := store.GetTexture(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTexture: %v", err)
	}
	if got.Name != "brick" || string(got.PNGData) != "fake-png-bytes" {
		t.Errorf("unexpected texture: %+v", got)
	}
	if got.ParenaSource != "" {
		t.Errorf("expected empty ParenaSource for a non-procedural texture, got %q", got.ParenaSource)
	}

	list, err := store.ListTextures(ctx)
	if err != nil {
		t.Fatalf("ListTextures: %v", err)
	}
	if len(list) != 1 || list[0].Name != "brick" || list[0].HasSource {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestTextureStore_CreateRejectsDuplicateName(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()

	if _, err := store.CreateTexture(ctx, "brick", 8, 8, []byte("a"), "", ""); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := store.CreateTexture(ctx, "brick", 8, 8, []byte("b"), "", ""); err == nil {
		t.Error("expected duplicate name to be rejected")
	}
}

func TestTextureStore_GetByName(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()
	store.CreateTexture(ctx, "brick", 8, 8, []byte("a"), "", "")

	got, err := store.GetTextureByName(ctx, "brick")
	if err != nil {
		t.Fatalf("GetTextureByName: %v", err)
	}
	if got.Name != "brick" {
		t.Errorf("unexpected texture: %+v", got)
	}

	if _, err := store.GetTextureByName(ctx, "nope"); err == nil {
		t.Error("expected error for unknown name")
	}
}

func TestTextureStore_RenameTexture(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()
	created, _ := store.CreateTexture(ctx, "brick", 8, 8, []byte("a"), "", "")

	updated, err := store.RenameTexture(ctx, created.ID, "brick-v2")
	if err != nil {
		t.Fatalf("RenameTexture: %v", err)
	}
	if updated.Name != "brick-v2" {
		t.Errorf("expected renamed texture, got %+v", updated)
	}
}

func TestTextureStore_RenameTexture_NotFound(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	if _, err := store.RenameTexture(context.Background(), 999, "whatever"); err == nil {
		t.Error("expected error renaming a nonexistent texture")
	}
}

func TestTextureStore_DeleteTexture(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()
	created, _ := store.CreateTexture(ctx, "brick", 8, 8, []byte("a"), "", "")

	if err := store.DeleteTexture(ctx, created.ID); err != nil {
		t.Fatalf("DeleteTexture: %v", err)
	}
	if _, err := store.GetTexture(ctx, created.ID); err == nil {
		t.Error("expected texture to be gone after delete")
	}
	if err := store.DeleteTexture(ctx, created.ID); err == nil {
		t.Error("expected deleting an already-deleted texture to error")
	}
}

// TestTextureStore_CloneTexture documents the founder's own real distinction from CarePyre's
// resume clone feature: this makes a full, independent row copy (own id, own future edits),
// not a view-with-overrides referencing a shared master.
func TestTextureStore_CloneTexture(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()
	original, _ := store.CreateTexture(ctx, "brick", 8, 8, []byte("original-bytes"), "(module gentexture)", "a brick texture")

	clone, err := store.CloneTexture(ctx, original.ID, "brick-variant")
	if err != nil {
		t.Fatalf("CloneTexture: %v", err)
	}
	if clone.ID == original.ID {
		t.Error("expected clone to have its own, different ID")
	}
	if clone.Name != "brick-variant" {
		t.Errorf("expected clone name to be the new name, got %q", clone.Name)
	}
	if string(clone.PNGData) != "original-bytes" || clone.ParenaSource != "(module gentexture)" || clone.Prompt != "a brick texture" {
		t.Errorf("expected clone to copy all real fields from the original, got %+v", clone)
	}

	// Editing the clone must never touch the original -- there is no shared-master relationship.
	if _, err := store.RenameTexture(ctx, clone.ID, "brick-variant-renamed"); err != nil {
		t.Fatalf("rename clone: %v", err)
	}
	stillOriginal, err := store.GetTexture(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetTexture original: %v", err)
	}
	if stillOriginal.Name != "brick" {
		t.Errorf("editing the clone must not affect the original, but original is now %q", stillOriginal.Name)
	}
}

func TestTextureStore_CloneTexture_NotFound(t *testing.T) {
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	if _, err := store.CloneTexture(context.Background(), 999, "whatever"); err == nil {
		t.Error("expected error cloning a nonexistent texture")
	}
}

func TestTextureStore_RegenerateTexture_RealEndToEnd(t *testing.T) {
	requireProcGenTools(t)
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateProceduralTexture(ctx, "checker", validCheckerSource, "a checker pattern", 32, 32)
	if err != nil {
		t.Fatalf("CreateProceduralTexture: %v", err)
	}
	if len(created.PNGData) == 0 {
		t.Fatal("expected real, non-empty PNG data")
	}
	// A real PNG file starts with this exact 8-byte magic header.
	pngMagic := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	for i, b := range pngMagic {
		if created.PNGData[i] != b {
			t.Fatalf("expected real PNG magic header, got %v", created.PNGData[:8])
		}
	}

	edited := created.ParenaSource
	regenerated, err := store.RegenerateTexture(ctx, created.ID, edited)
	if err != nil {
		t.Fatalf("RegenerateTexture: %v", err)
	}
	if len(regenerated.PNGData) == 0 {
		t.Fatal("expected regenerated texture to have real PNG data")
	}
}

func TestTextureStore_RegenerateTexture_BadEditLeavesOriginalIntact(t *testing.T) {
	requireProcGenTools(t)
	db := newTextureTestDB(t)
	store := &TextureStore{DB: db}
	ctx := context.Background()
	created, err := store.CreateProceduralTexture(ctx, "checker", validCheckerSource, "", 16, 16)
	if err != nil {
		t.Fatalf("CreateProceduralTexture: %v", err)
	}
	originalPNG := append([]byte(nil), created.PNGData...)

	_, err = store.RegenerateTexture(ctx, created.ID, `(module gentexture) #target {:c (inline-c "evil()")}`)
	if err == nil {
		t.Fatal("expected regeneration with invalid source to fail")
	}

	stillThere, err := store.GetTexture(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTexture after failed regen: %v", err)
	}
	if string(stillThere.PNGData) != string(originalPNG) {
		t.Error("a failed regenerate must leave the original PNG data untouched")
	}
}
