package shankpit_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

func newTestStore(t *testing.T) *shankpit.LevelStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE shankpit_levels (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       TEXT NOT NULL,
			width      REAL NOT NULL DEFAULT 100,
			height     REAL NOT NULL DEFAULT 50,
			depth      REAL NOT NULL DEFAULT 100,
			walls_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_levels: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_shankpit_levels_name ON shankpit_levels(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &shankpit.LevelStore{DB: db}
}

func aCube() shankpit.Wall {
	return shankpit.Wall{ID: 1, X: 0, Y: 0, Z: 0, SX: 4, SY: 4, SZ: 4, R: 0.5, G: 0.5, B: 0.5, Friction: 0.8}
}

func TestCreateLevel_EmptyWallsAllowed(t *testing.T) {
	s := newTestStore(t)
	lvl, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, nil)
	if err != nil {
		t.Fatalf("create with zero walls should succeed (S459-01 before S459-04): %v", err)
	}
	if len(lvl.Walls) != 0 {
		t.Fatalf("expected zero walls, got %d", len(lvl.Walls))
	}
}

func TestCreateLevel_RejectsInvalidName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "", 100, 50, 100, nil); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestCreateLevel_RejectsNonPositiveWallSize(t *testing.T) {
	s := newTestStore(t)
	bad := aCube()
	bad.SX = 0
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, []shankpit.Wall{bad}); err == nil {
		t.Fatal("expected an error for a zero-size wall")
	}
}

func TestCreateLevel_RejectsOutOfRangeColor(t *testing.T) {
	s := newTestStore(t)
	bad := aCube()
	bad.R = 1.5
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, []shankpit.Wall{bad}); err == nil {
		t.Fatal("expected an error for an out-of-[0,1] color component")
	}
}

func TestCreateLevel_RejectsTooManyWalls(t *testing.T) {
	s := newTestStore(t)
	walls := make([]shankpit.Wall, shankpit.MaxWalls+1)
	for i := range walls {
		w := aCube()
		w.ID = i
		walls[i] = w
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, walls); err == nil {
		t.Fatal("expected an error for exceeding MaxWalls")
	}
}

// TestFaceDragEditing_ReshapesCubeWithoutMovingOppositeFace verifies the exact real mechanism
// S459-05 needs: dragging a wall's +X face outward means changing SX and shifting the center's X
// by half the delta -- the -X face (at X - SX/2) must land exactly where it started, or the "drag
// one face, the rest of the box stays put" contract is broken.
func TestFaceDragEditing_ReshapesCubeWithoutMovingOppositeFace(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, []shankpit.Wall{aCube()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	original := created.Walls[0]
	originalMinX := original.X - original.SX/2

	// Drag the +X face from x=2 (0 + 4/2) out to x=10, turning a 4-unit cube into an 8x4x4
	// shipping-container-shaped box along X -- the founder's own concrete v0 bar.
	newMaxX := 10.0
	newSX := newMaxX - originalMinX
	newCenterX := originalMinX + newSX/2
	edited := original
	edited.X = newCenterX
	edited.SX = newSX

	updated, err := s.UpdateLevel(context.Background(), created.ID, created.Width, created.Height, created.Depth, []shankpit.Wall{edited})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got := updated.Walls[0]
	gotMinX := got.X - got.SX/2
	gotMaxX := got.X + got.SX/2
	if gotMinX != originalMinX {
		t.Fatalf("dragging the +X face moved the -X face too: got min_x=%g, want %g (unchanged)", gotMinX, originalMinX)
	}
	if gotMaxX != newMaxX {
		t.Fatalf("dragged +X face landed at %g, want %g", gotMaxX, newMaxX)
	}
}

func TestListLevels_ReturnsWallCountNotFullWalls(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, []shankpit.Wall{aCube(), aCube()}); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := s.ListLevels(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].WallCount != 2 {
		t.Fatalf("unexpected list result: %+v", list)
	}
}

func TestRenameLevel(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Old Name", 100, 50, 100, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	renamed, err := s.RenameLevel(context.Background(), created.ID, "New Name")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "New Name" {
		t.Fatalf("expected renamed level, got %+v", renamed)
	}
}

func TestCloneLevel_CopiesWalls(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Original", 100, 50, 100, []shankpit.Wall{aCube()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	clone, err := s.CloneLevel(context.Background(), created.ID, "Clone")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if clone.ID == created.ID || len(clone.Walls) != 1 {
		t.Fatalf("unexpected clone: %+v", clone)
	}
}

func TestDeleteLevel(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteLevel(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetLevel(context.Background(), created.ID); err == nil {
		t.Fatal("expected level to be gone after delete")
	}
}

func TestExport_MatchesNativeWallShape(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, []shankpit.Wall{aCube()})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if doc.Version != 1 || len(doc.Walls) != 1 || doc.Walls[0].SX != 4 {
		t.Fatalf("unexpected export doc: %+v", doc)
	}
}
