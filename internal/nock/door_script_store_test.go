package nock

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const testDoorScriptSource = `(module doorscript)
(import math)

(defn door-tick [(dist-to-player : F64) (state : F64)] : F64
  (if (< dist-to-player 3.0)
    1.0
    (if (> dist-to-player 5.0)
      0.0
      state)))
`

func newDoorScriptTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_door_scripts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			parena_source TEXT NOT NULL,
			compiled_so   BLOB NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_door_scripts: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_nock_door_scripts_name ON nock_door_scripts(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return db
}

// TestDoorScriptStore_CreateGetList is a REAL compile, not a fake -- CreateDoorScript runs the
// actual parena build + gcc pipeline (door_script_compile.go), so this test's own pass/fail
// depends on that pipeline genuinely working, same as TestCompileDoorScript_RealSmoke already
// proved standalone.
func TestDoorScriptStore_CreateGetList(t *testing.T) {
	db := newDoorScriptTestDB(t)
	store := &DoorScriptStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateDoorScript(ctx, "hallway-door", testDoorScriptSource)
	if err != nil {
		t.Fatalf("CreateDoorScript: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a real assigned ID")
	}
	if len(created.CompiledSO) < 4 || string(created.CompiledSO[0:4]) != "\x7fELF" {
		t.Errorf("compiled_so doesn't look like a real ELF shared object, len=%d", len(created.CompiledSO))
	}

	got, err := store.GetDoorScript(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDoorScript: %v", err)
	}
	if got.Name != "hallway-door" || got.ParenaSource != testDoorScriptSource {
		t.Errorf("unexpected door script: name=%q source-matches=%v", got.Name, got.ParenaSource == testDoorScriptSource)
	}

	list, err := store.ListDoorScripts(ctx)
	if err != nil {
		t.Fatalf("ListDoorScripts: %v", err)
	}
	if len(list) != 1 || list[0].Name != "hallway-door" {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestDoorScriptStore_CreateRejectsBadSource(t *testing.T) {
	db := newDoorScriptTestDB(t)
	store := &DoorScriptStore{DB: db}
	tests := []struct {
		name string
		src  string
	}{
		{"missing door-tick", "(module doorscript)\n(import math)\n(defn other-fn [(x : F64)] : F64 x)\n"},
		{"bad import", "(module doorscript)\n(import sdl2)\n(defn door-tick [(a : F64) (b : F64)] : F64 0.0)\n"},
		{"target escape hatch", "(module doorscript)\n(import math)\n#target c\n(defn door-tick [(a : F64) (b : F64)] : F64 0.0)\n"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.CreateDoorScript(context.Background(), "x-"+tt.name, tt.src); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestDoorScriptStore_RegenerateKeepsOldRowOnFailure(t *testing.T) {
	db := newDoorScriptTestDB(t)
	store := &DoorScriptStore{DB: db}
	ctx := context.Background()

	orig, err := store.CreateDoorScript(ctx, "door1", testDoorScriptSource)
	if err != nil {
		t.Fatalf("CreateDoorScript: %v", err)
	}

	// A bad edit must fail without touching the existing, working row.
	if _, err := store.RegenerateDoorScript(ctx, orig.ID, "(module doorscript)\nnot valid parena"); err == nil {
		t.Fatal("expected regenerate to fail on bad source")
	}
	stillThere, err := store.GetDoorScript(ctx, orig.ID)
	if err != nil {
		t.Fatalf("GetDoorScript after failed regenerate: %v", err)
	}
	if stillThere.ParenaSource != testDoorScriptSource {
		t.Error("failed regenerate should not have touched the existing row's source")
	}
}

func TestDoorScriptStore_Delete(t *testing.T) {
	db := newDoorScriptTestDB(t)
	store := &DoorScriptStore{DB: db}
	ctx := context.Background()

	created, err := store.CreateDoorScript(ctx, "temp-door", testDoorScriptSource)
	if err != nil {
		t.Fatalf("CreateDoorScript: %v", err)
	}
	if err := store.DeleteDoorScript(ctx, created.ID); err != nil {
		t.Fatalf("DeleteDoorScript: %v", err)
	}
	if _, err := store.GetDoorScript(ctx, created.ID); err == nil {
		t.Error("expected deleted script to be gone")
	}
}
