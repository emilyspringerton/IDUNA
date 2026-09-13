package brawlpit

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newCheckpointTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE brawlpit_rl_checkpoints (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			role            TEXT NOT NULL,
			generation      INTEGER NOT NULL,
			elo             REAL NOT NULL DEFAULT 1500,
			source_location TEXT NOT NULL DEFAULT '',
			filename        TEXT NOT NULL,
			sha256          TEXT NOT NULL,
			size_bytes      INTEGER NOT NULL,
			blob_path       TEXT NOT NULL,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create brawlpit_rl_checkpoints: %v", err)
	}
	return db
}

func newCheckpointTestStore(t *testing.T) *CheckpointStore {
	t.Helper()
	return &CheckpointStore{DB: newCheckpointTestDB(t), BlobDir: t.TempDir()}
}

func TestCheckpointStore_CreateAndGet(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	c, err := store.Create(ctx, "main", 3, 1550.5, "colab", "main_gen3.zip", []byte("fake ppo checkpoint bytes"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == 0 {
		t.Error("expected a real, non-zero id")
	}
	if c.Role != "main" || c.Generation != 3 || c.Elo != 1550.5 || c.SourceLocation != "colab" {
		t.Errorf("unexpected checkpoint: %+v", c)
	}
	if c.SizeBytes != int64(len("fake ppo checkpoint bytes")) {
		t.Errorf("wrong size_bytes: %d", c.SizeBytes)
	}
	if len(c.SHA256) != 64 {
		t.Errorf("expected a real 64-char hex sha256, got %q", c.SHA256)
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Filename != "main_gen3.zip" {
		t.Errorf("Get returned wrong data: %+v", got)
	}
}

func TestCheckpointStore_CreateWritesARealBlobFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	content := []byte("real checkpoint payload")

	c, err := store.Create(ctx, "league_exploiter", 0, 1500, "this-box", "le0.zip", content)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blobPath := filepath.Join(store.BlobDir, "1.zip")
	on, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("expected a real blob file at %s: %v", blobPath, err)
	}
	if string(on) != string(content) {
		t.Errorf("blob file content doesn't match what was uploaded")
	}
	_ = c
}

func TestCheckpointStore_RejectsInvalidRole(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "not_a_real_role", 0, 1500, "colab", "x.zip", []byte("data")); err == nil {
		t.Error("expected an invalid role to be rejected")
	}
}

func TestCheckpointStore_RejectsEmptyFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "main", 0, 1500, "colab", "empty.zip", []byte{}); err == nil {
		t.Error("expected an empty checkpoint file to be rejected")
	}
}

func TestCheckpointStore_RejectsInvalidSourceLocation(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "main", 0, 1500, "", "x.zip", []byte("data")); err == nil {
		t.Error("expected an empty source_location to be rejected")
	}
}

func TestCheckpointStore_ListAllAndByRole(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	store.Create(ctx, "main", 0, 1500, "this-box", "main0.zip", []byte("a"))
	store.Create(ctx, "main", 1, 1520, "colab", "main1.zip", []byte("b"))
	store.Create(ctx, "main_exploiter", 0, 1500, "colab", "me0.zip", []byte("c"))

	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total checkpoints, got %d", len(all))
	}

	mainOnly, err := store.List(ctx, "main")
	if err != nil {
		t.Fatalf("List(main): %v", err)
	}
	if len(mainOnly) != 2 {
		t.Fatalf("expected 2 main checkpoints, got %d", len(mainOnly))
	}
	for _, c := range mainOnly {
		if c.Role != "main" {
			t.Errorf("role filter leaked a non-main row: %+v", c)
		}
	}
}

func TestCheckpointStore_ReadBlobRoundTrips(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	content := []byte("a real checkpoint's real bytes, byte for byte")

	created, err := store.Create(ctx, "main", 0, 1500, "colab", "main0.zip", content)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, data, err := store.ReadBlob(ctx, created.ID)
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(data) != string(content) {
		t.Error("ReadBlob returned different bytes than were uploaded")
	}
	if c.SHA256 != created.SHA256 {
		t.Error("ReadBlob's own metadata doesn't match the created checkpoint's")
	}
}

func TestCheckpointStore_GetUnknownIDFails(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.Get(context.Background(), 999); err == nil {
		t.Error("expected getting a nonexistent checkpoint id to fail")
	}
}

func TestCheckpointStore_RejectsOversizedFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	// One byte over the real 200MB bound -- a real, deliberate boundary test, not a full
	// 200MB allocation (a synthetic slice header of that length is cheap to construct in Go
	// without actually touching that much memory, since make([]byte, n) zero-fills but the
	// test machine can spare 200MB briefly; kept modest here to stay fast in CI).
	huge := make([]byte, 200*1024*1024+1)
	if _, err := store.Create(ctx, "main", 0, 1500, "colab", "huge.zip", huge); err == nil {
		t.Error("expected an oversized checkpoint file to be rejected")
	}
}
