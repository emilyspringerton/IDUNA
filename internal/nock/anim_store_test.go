package nock

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newAnimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_animations (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			tick_rate       INTEGER NOT NULL,
			duration_ticks  INTEGER NOT NULL,
			num_channels    INTEGER NOT NULL,
			content_hash    TEXT NOT NULL,
			gband_data      BLOB NOT NULL,
			manifest_json   TEXT NOT NULL,
			gskel_data      BLOB,
			gmesh_data      BLOB,
			source_location TEXT,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_animations: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_nock_animations_name ON nock_animations(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return db
}

func fakeGBandBytes() []byte {
	return []byte("GBND" + "\x00\x00\x00\x00" + "0123456789...")
}

func TestAnimStore_CreateGetList(t *testing.T) {
	db := newAnimTestDB(t)
	store := &AnimStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateAnimation(ctx, "walk", 30, 64, 4, "deadbeef", fakeGBandBytes(), `{"gband_version":1}`, []byte("GSKL..."), nil, "gbtool import --gltf")
	if err != nil {
		t.Fatalf("CreateAnimation: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a real assigned ID")
	}

	got, err := store.GetAnimation(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAnimation: %v", err)
	}
	if got.Name != "walk" || got.TickRate != 30 || got.DurationTicks != 64 {
		t.Errorf("unexpected animation: %+v", got)
	}
	if string(got.GSkelData) != "GSKL..." {
		t.Errorf("expected gskel data to round-trip, got %q", got.GSkelData)
	}
	if got.GMeshData != nil {
		t.Errorf("expected nil gmesh data, got %q", got.GMeshData)
	}

	list, err := store.ListAnimations(ctx)
	if err != nil {
		t.Fatalf("ListAnimations: %v", err)
	}
	if len(list) != 1 || !list[0].HasSkel || list[0].HasMesh {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestAnimStore_CreateRejectsBadMagic(t *testing.T) {
	db := newAnimTestDB(t)
	store := &AnimStore{DB: db}
	_, err := store.CreateAnimation(context.Background(), "bad", 30, 1, 1, "x", []byte("not a gband file"), "{}", nil, nil, "")
	if err == nil {
		t.Fatal("expected an error for bad gband magic, got nil")
	}
}

func TestAnimStore_RenameCloneDelete(t *testing.T) {
	db := newAnimTestDB(t)
	store := &AnimStore{DB: db}
	ctx := context.Background()

	orig, err := store.CreateAnimation(ctx, "idle", 30, 10, 2, "abc", fakeGBandBytes(), "{}", nil, nil, "")
	if err != nil {
		t.Fatalf("CreateAnimation: %v", err)
	}

	renamed, err := store.RenameAnimation(ctx, orig.ID, "idle_v2")
	if err != nil {
		t.Fatalf("RenameAnimation: %v", err)
	}
	if renamed.Name != "idle_v2" {
		t.Errorf("expected renamed name, got %q", renamed.Name)
	}

	clone, err := store.CloneAnimation(ctx, orig.ID, "idle_clone")
	if err != nil {
		t.Fatalf("CloneAnimation: %v", err)
	}
	if clone.ID == orig.ID || clone.Name != "idle_clone" {
		t.Errorf("unexpected clone: %+v", clone)
	}

	if err := store.DeleteAnimation(ctx, orig.ID); err != nil {
		t.Fatalf("DeleteAnimation: %v", err)
	}
	if _, err := store.GetAnimation(ctx, orig.ID); err == nil {
		t.Error("expected deleted animation to be gone")
	}
	// The clone must survive the original's deletion -- independent rows, no parent_id link.
	if _, err := store.GetAnimation(ctx, clone.ID); err != nil {
		t.Errorf("clone should survive original's deletion: %v", err)
	}
}
