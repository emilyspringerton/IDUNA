package shankpit

import (
	"context"
	"database/sql"
	"strings"
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
		CREATE TABLE shankpit_rl_checkpoints (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			name               TEXT NOT NULL DEFAULT '',
			role               TEXT NOT NULL,
			generation         INTEGER NOT NULL,
			elo                REAL NOT NULL DEFAULT 1500,
			source_location    TEXT NOT NULL DEFAULT '',
			filename           TEXT NOT NULL,
			sha256             TEXT NOT NULL,
			size_bytes         INTEGER NOT NULL,
			blob_path          TEXT NOT NULL,
			is_active_opponent INTEGER NOT NULL DEFAULT 0,
			is_disabled        INTEGER NOT NULL DEFAULT 0,
			eval_note          TEXT NOT NULL DEFAULT '',
			created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_rl_checkpoints: %v", err)
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

	c, err := store.Create(ctx, "main", 1, 1500, "colab", "main_gen1.zip", []byte("fake ppo checkpoint bytes"), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == 0 {
		t.Fatalf("expected a real nonzero id")
	}
	if !strings.HasPrefix(c.Name, "main_") {
		t.Fatalf("expected name to start with role prefix, got %q", c.Name)
	}
	if c.Role != "main" || c.Generation != 1 || c.SourceLocation != "colab" {
		t.Fatalf("unexpected fields: %+v", c)
	}
	if c.IsActiveOpponent || c.IsDisabled {
		t.Fatalf("new checkpoint should start neither active nor disabled: %+v", c)
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SHA256 != c.SHA256 || got.SizeBytes != c.SizeBytes {
		t.Fatalf("Get returned different data than Create: %+v vs %+v", got, c)
	}
}

func TestCheckpointStore_Create_InvalidRole(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.Create(context.Background(), "not_a_real_role", 1, 1500, "colab", "f.zip", []byte("x"), ""); err == nil {
		t.Fatalf("expected an error for an invalid role")
	}
}

func TestCheckpointStore_Create_EmptyFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.Create(context.Background(), "main", 1, 1500, "colab", "f.zip", []byte{}, ""); err == nil {
		t.Fatalf("expected an error for an empty checkpoint file")
	}
}

func TestCheckpointStore_List_RoleFilter(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	mustCreate := func(role string) {
		if _, err := store.Create(ctx, role, 1, 1500, "colab", role+".zip", []byte("data"), ""); err != nil {
			t.Fatalf("Create(%s): %v", role, err)
		}
	}
	mustCreate("main")
	mustCreate("main_exploiter")
	mustCreate("main")

	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 checkpoints total, got %d", len(all))
	}

	mainOnly, err := store.List(ctx, "main")
	if err != nil {
		t.Fatalf("List(main): %v", err)
	}
	if len(mainOnly) != 2 {
		t.Fatalf("expected 2 'main' checkpoints, got %d", len(mainOnly))
	}
}

func TestCheckpointStore_SetActiveOpponent_SingleSelectionInvariant(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 1, 1500, "colab", "a.zip", []byte("data"), "")
	b, _ := store.Create(ctx, "main", 2, 1500, "colab", "b.zip", []byte("data"), "")

	if _, err := store.SetActiveOpponent(ctx, a.ID); err != nil {
		t.Fatalf("SetActiveOpponent(a): %v", err)
	}
	active, err := store.GetActiveOpponent(ctx)
	if err != nil || active == nil || active.ID != a.ID {
		t.Fatalf("expected a to be active, got %+v err=%v", active, err)
	}

	if _, err := store.SetActiveOpponent(ctx, b.ID); err != nil {
		t.Fatalf("SetActiveOpponent(b): %v", err)
	}
	active, err = store.GetActiveOpponent(ctx)
	if err != nil || active == nil || active.ID != b.ID {
		t.Fatalf("expected b to be the ONLY active opponent, got %+v err=%v", active, err)
	}

	aAfter, err := store.Get(ctx, a.ID)
	if err != nil || aAfter.IsActiveOpponent {
		t.Fatalf("expected a to no longer be active: %+v err=%v", aAfter, err)
	}
}

func TestCheckpointStore_GetActiveOpponent_NoneSetYet(t *testing.T) {
	store := newCheckpointTestStore(t)
	active, err := store.GetActiveOpponent(context.Background())
	if err != nil {
		t.Fatalf("GetActiveOpponent: %v", err)
	}
	if active != nil {
		t.Fatalf("expected nil (no real error) when no opponent has ever been selected, got %+v", active)
	}
}

func TestCheckpointStore_SetDisabled_ReversibleFlag(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, "main", 1, 1500, "colab", "c.zip", []byte("data"), "")

	disabled, err := store.SetDisabled(ctx, c.ID, true)
	if err != nil || !disabled.IsDisabled {
		t.Fatalf("expected IsDisabled=true: %+v err=%v", disabled, err)
	}
	reenabled, err := store.SetDisabled(ctx, c.ID, false)
	if err != nil || reenabled.IsDisabled {
		t.Fatalf("expected IsDisabled=false after re-enabling: %+v err=%v", reenabled, err)
	}
}

func TestCheckpointStore_UpdateElo_MovesAnAlreadyPushedCheckpoint(t *testing.T) {
	// S459-76: a checkpoint's own real elo keeps moving every time a later generation evaluates
	// against it -- this real, direct regression guard confirms the registry can actually reflect
	// that after the fact, not just at the moment of its own initial push.
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	c, err := store.Create(ctx, "main", 0, 1500, "colab", "gen0.zip", []byte("data"), "no prior generation to evaluate against yet")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.UpdateElo(ctx, c.ID, 1484, "vs prior gen: kills 9-13")
	if err != nil {
		t.Fatalf("UpdateElo: %v", err)
	}
	if updated.Elo != 1484 {
		t.Fatalf("expected elo=1484 after update, got %v", updated.Elo)
	}
	if updated.EvalNote != "vs prior gen: kills 9-13" {
		t.Fatalf("expected the new eval_note to stick, got %q", updated.EvalNote)
	}

	reread, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if reread.Elo != 1484 {
		t.Fatalf("expected the updated elo to persist across a fresh Get, got %v", reread.Elo)
	}
}

func TestCheckpointStore_UpdateElo_UnknownID(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.UpdateElo(context.Background(), 99999, 1500, ""); err == nil {
		t.Fatalf("expected an error for a nonexistent checkpoint id")
	}
}

func TestCheckpointStore_SetDisabled_UnknownID(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.SetDisabled(context.Background(), 99999, true); err == nil {
		t.Fatalf("expected an error for a nonexistent checkpoint id")
	}
}

func TestCheckpointStore_ReadBlob_RoundTrips(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	original := []byte("real ppo checkpoint bytes, not a placeholder")
	c, err := store.Create(ctx, "main", 1, 1500, "colab", "real.zip", original, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, data, err := store.ReadBlob(ctx, c.ID)
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("ReadBlob returned different bytes than Create wrote")
	}
}
