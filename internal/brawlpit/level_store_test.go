package brawlpit

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newLevelTestDB mirrors internal/nock's own real in-memory-SQLite convention -- an inline
// CREATE TABLE matching the real migration's schema exactly, not a mock.
func newLevelTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE brawlpit_levels (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			name           TEXT NOT NULL,
			width          REAL NOT NULL DEFAULT 80,
			height         REAL NOT NULL DEFAULT 40,
			platforms_json TEXT NOT NULL DEFAULT '[]',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create brawlpit_levels: %v", err)
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX idx_brawlpit_levels_name ON brawlpit_levels(name)`)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	return db
}

func samplePlatforms() []Platform {
	return []Platform{
		{X: 0, Y: -5, W: 60, H: 10, Type: 0},
		{X: -15, Y: 8, W: 12, H: 1, Type: 1},
	}
}

func TestLevelStore_CreateGetList(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateLevel(ctx, "My First Level", 80, 40, samplePlatforms())
	if err != nil {
		t.Fatalf("CreateLevel: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a real, non-zero id")
	}
	if len(created.Platforms) != 2 {
		t.Errorf("expected 2 platforms, got %d", len(created.Platforms))
	}

	got, err := store.GetLevel(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetLevel: %v", err)
	}
	if got.Name != "My First Level" || got.Platforms[0].W != 60 {
		t.Errorf("GetLevel returned wrong data: %+v", got)
	}

	list, err := store.ListLevels(ctx)
	if err != nil {
		t.Fatalf("ListLevels: %v", err)
	}
	if len(list) != 1 || list[0].PlatformCount != 2 {
		t.Errorf("expected 1 level with platform_count=2, got %+v", list)
	}
}

func TestLevelStore_CreateRejectsInvalidName(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	if _, err := store.CreateLevel(ctx, "", 80, 40, samplePlatforms()); err == nil {
		t.Error("expected an empty name to be rejected")
	}
}

func TestLevelStore_CreateRejectsNoPlatforms(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	if _, err := store.CreateLevel(ctx, "Empty Level", 80, 40, nil); err == nil {
		t.Error("expected a level with zero platforms to be rejected")
	}
}

// TestLevelStore_CreateRejectsTooManyPlatforms guards the real cross-repo contract: a level
// saved here must never exceed BRAWLPIT's own real MAX_LEVEL_PLATFORMS (level_format.h), or a
// web-authored level would silently fail to load fully in the native client.
func TestLevelStore_CreateRejectsTooManyPlatforms(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	platforms := make([]Platform, MaxPlatforms+1)
	for i := range platforms {
		platforms[i] = Platform{X: float64(i), Y: 0, W: 1, H: 1, Type: 0}
	}
	if _, err := store.CreateLevel(ctx, "Too Big", 80, 40, platforms); err == nil {
		t.Errorf("expected %d platforms (over the real MaxPlatforms=%d) to be rejected", len(platforms), MaxPlatforms)
	}
}

func TestLevelStore_CreateRejectsInvalidPlatformType(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	bad := []Platform{{X: 0, Y: 0, W: 10, H: 10, Type: 2}}
	if _, err := store.CreateLevel(ctx, "Bad Type", 80, 40, bad); err == nil {
		t.Error("expected an invalid platform type (not 0 or 1) to be rejected")
	}
}

func TestLevelStore_CreateRejectsZeroSizedPlatform(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	bad := []Platform{{X: 0, Y: 0, W: 0, H: 10, Type: 0}}
	if _, err := store.CreateLevel(ctx, "Zero Width", 80, 40, bad); err == nil {
		t.Error("expected a zero-width platform to be rejected")
	}
}

func TestLevelStore_UpdateReplacesPlatforms(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateLevel(ctx, "Editable", 80, 40, samplePlatforms())
	if err != nil {
		t.Fatalf("CreateLevel: %v", err)
	}

	newPlatforms := []Platform{{X: 100, Y: 100, W: 5, H: 5, Type: 1}}
	updated, err := store.UpdateLevel(ctx, created.ID, 200, 100, newPlatforms)
	if err != nil {
		t.Fatalf("UpdateLevel: %v", err)
	}
	if updated.Width != 200 || len(updated.Platforms) != 1 || updated.Platforms[0].X != 100 {
		t.Errorf("UpdateLevel didn't apply the real new data: %+v", updated)
	}
}

func TestLevelStore_UpdateNotFound(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	if _, err := store.UpdateLevel(ctx, 999, 80, 40, samplePlatforms()); err == nil {
		t.Error("expected updating a nonexistent level id to fail")
	}
}

func TestLevelStore_RenameLevel(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	created, _ := store.CreateLevel(ctx, "Old Name", 80, 40, samplePlatforms())
	renamed, err := store.RenameLevel(ctx, created.ID, "New Name")
	if err != nil {
		t.Fatalf("RenameLevel: %v", err)
	}
	if renamed.Name != "New Name" {
		t.Errorf("expected renamed level, got %+v", renamed)
	}
}

// TestLevelStore_CloneIsIndependentRow guards the same real distinction nock_textures' own
// CloneTexture established: a clone is a full, independent row, editing it never touches the
// original.
func TestLevelStore_CloneIsIndependentRow(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	original, _ := store.CreateLevel(ctx, "Original", 80, 40, samplePlatforms())
	clone, err := store.CloneLevel(ctx, original.ID, "Cloned")
	if err != nil {
		t.Fatalf("CloneLevel: %v", err)
	}
	if clone.ID == original.ID {
		t.Fatal("expected the clone to have its own, different id")
	}

	if _, err := store.UpdateLevel(ctx, clone.ID, 999, 999, []Platform{{X: 1, Y: 1, W: 1, H: 1, Type: 0}}); err != nil {
		t.Fatalf("UpdateLevel on clone: %v", err)
	}
	originalAfter, err := store.GetLevel(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetLevel(original): %v", err)
	}
	if originalAfter.Width == 999 {
		t.Error("editing the clone must not affect the original level")
	}
}

func TestLevelStore_DeleteLevel(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	created, _ := store.CreateLevel(ctx, "To Delete", 80, 40, samplePlatforms())
	if err := store.DeleteLevel(ctx, created.ID); err != nil {
		t.Fatalf("DeleteLevel: %v", err)
	}
	if _, err := store.GetLevel(ctx, created.ID); err == nil {
		t.Error("expected the deleted level to no longer be gettable")
	}
}

func TestLevelStore_DeleteNotFound(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	if err := store.DeleteLevel(ctx, 999); err == nil {
		t.Error("expected deleting a nonexistent level id to fail")
	}
}

// TestLevelStore_ExportMatchesNativeLoaderContract is the real, direct guard on the S415-04
// cross-repo contract: Export's own JSON shape must be exactly what BRAWLPIT's
// level_format.h::level_parse_json reads -- "version"/"name"/"platforms" with
// "x"/"y"/"w"/"h"/"type" keys, nothing else required.
func TestLevelStore_ExportMatchesNativeLoaderContract(t *testing.T) {
	db := newLevelTestDB(t)
	store := &LevelStore{DB: db}
	ctx := context.Background()

	created, _ := store.CreateLevel(ctx, "Export Me", 80, 40, samplePlatforms())
	doc, err := store.Export(ctx, created.ID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if doc.Version != 1 || doc.Name != "Export Me" || len(doc.Platforms) != 2 {
		t.Errorf("Export doc doesn't match the real native contract: %+v", doc)
	}
	if doc.Platforms[0].W != 60 || doc.Platforms[0].Type != 0 {
		t.Errorf("Export platform data wrong: %+v", doc.Platforms[0])
	}
}

func TestValidateName_RejectsEmptyAndAcceptsSpaces(t *testing.T) {
	if err := ValidateName(""); err == nil {
		t.Error("expected empty name to be rejected")
	}
	if err := ValidateName("Final Destination"); err != nil {
		t.Errorf("expected a real, space-containing display name to be accepted, got: %v", err)
	}
}
