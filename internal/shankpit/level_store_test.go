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
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			name                 TEXT NOT NULL,
			width                REAL NOT NULL DEFAULT 100,
			height               REAL NOT NULL DEFAULT 50,
			depth                REAL NOT NULL DEFAULT 100,
			ground_plane_enabled BOOLEAN NOT NULL DEFAULT 1,
			ground_plane_squares INTEGER NOT NULL DEFAULT 2,
			enclosed BOOLEAN NOT NULL DEFAULT 0,
			walls_json TEXT NOT NULL DEFAULT '[]',
			objects_json TEXT NOT NULL DEFAULT '[]',
			spawners_json TEXT NOT NULL DEFAULT '[]',
			doors_json TEXT NOT NULL DEFAULT '[]',
			nav_nodes_json TEXT NOT NULL DEFAULT '[]',
			characters_json TEXT NOT NULL DEFAULT '[]',
			level_exits_json TEXT NOT NULL DEFAULT '[]',
			next_level_id INTEGER,
			is_story_start BOOLEAN NOT NULL DEFAULT 0,
			is_default_queue BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_levels: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_shankpit_levels_name ON shankpit_levels(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE shankpit_widgets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			walls_json TEXT NOT NULL DEFAULT '[]',
			doors_json TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		t.Fatalf("create shankpit_widgets: %v", err)
	}
	widgets := &shankpit.WidgetStore{DB: db}
	return &shankpit.LevelStore{DB: db, Widgets: widgets}
}

func aCube() shankpit.Wall {
	return shankpit.Wall{ID: 1, X: 0, Y: 0, Z: 0, SX: 4, SY: 4, SZ: 4, R: 0.5, G: 0.5, B: 0.5, Friction: 0.8}
}

func TestCreateLevel_EmptyWallsAllowed(t *testing.T) {
	s := newTestStore(t)
	lvl, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create with zero walls should succeed (S459-01 before S459-04): %v", err)
	}
	if len(lvl.Walls) != 0 {
		t.Fatalf("expected zero walls, got %d", len(lvl.Walls))
	}
}

func TestCreateLevel_RejectsInvalidName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

func TestCreateLevel_RejectsNonPositiveWallSize(t *testing.T) {
	s := newTestStore(t)
	bad := aCube()
	bad.SX = 0
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{bad}, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for a zero-size wall")
	}
}

func TestCreateLevel_RejectsOutOfRangeColor(t *testing.T) {
	s := newTestStore(t)
	bad := aCube()
	bad.R = 1.5
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{bad}, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for an out-of-[0,1] color component")
	}
}

func TestCreateLevel_RejectsOutOfRangeGroundPlaneSquares(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 0, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for ground_plane_squares below the real minimum")
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, shankpit.MaxGroundPlaneSquares+1, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for ground_plane_squares above the real maximum")
	}
}

func TestExport_CarriesGroundPlaneFields(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, false, 7, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if doc.GroundPlaneEnabled != false || doc.GroundPlaneSquares != 7 {
		t.Fatalf("unexpected export doc ground plane fields: %+v", doc)
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
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, walls, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for exceeding MaxWalls")
	}
}

// TestFaceDragEditing_ReshapesCubeWithoutMovingOppositeFace verifies the exact real mechanism
// S459-05 needs: dragging a wall's +X face outward means changing SX and shifting the center's X
// by half the delta -- the -X face (at X - SX/2) must land exactly where it started, or the "drag
// one face, the rest of the box stays put" contract is broken.
func TestFaceDragEditing_ReshapesCubeWithoutMovingOppositeFace(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{aCube()}, nil, nil, nil, nil, nil, nil, nil)
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

	updated, err := s.UpdateLevel(context.Background(), created.ID, created.Width, created.Height, created.Depth, true, 2, []shankpit.Wall{edited}, nil, nil, nil, nil, nil, nil, nil)
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
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{aCube(), aCube()}, nil, nil, nil, nil, nil, nil, nil); err != nil {
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
	created, err := s.CreateLevel(context.Background(), "Old Name", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
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
	created, err := s.CreateLevel(context.Background(), "Original", 100, 50, 100, true, 2, []shankpit.Wall{aCube()}, nil, nil, nil, nil, nil, nil, nil)
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
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
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

func TestSetDefaultQueueLevel_ExactlyOneDefault(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateLevel(context.Background(), "B", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := s.SetDefaultQueueLevel(context.Background(), a.ID); err != nil {
		t.Fatalf("set default a: %v", err)
	}
	aAfter, _ := s.GetLevel(context.Background(), a.ID)
	if !aAfter.IsDefaultQueue {
		t.Fatal("expected level A to be the default queue level")
	}
	if _, err := s.SetDefaultQueueLevel(context.Background(), b.ID); err != nil {
		t.Fatalf("set default b: %v", err)
	}
	aAfter2, _ := s.GetLevel(context.Background(), a.ID)
	bAfter, _ := s.GetLevel(context.Background(), b.ID)
	if aAfter2.IsDefaultQueue {
		t.Fatal("expected level A to no longer be the default queue level after B was set")
	}
	if !bAfter.IsDefaultQueue {
		t.Fatal("expected level B to be the default queue level")
	}
	list, err := s.ListLevels(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defaultCount := 0
	for _, l := range list {
		if l.IsDefaultQueue {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		t.Fatalf("expected exactly 1 default queue level in list, got %d", defaultCount)
	}
}

// TestSetStoryStartLevel_ExactlyOneStoryStart mirrors TestSetDefaultQueueLevel_ExactlyOneDefault
// exactly (S473) -- same real "exactly one at a time" enforcement shape.
func TestSetStoryStartLevel_ExactlyOneStoryStart(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateLevel(context.Background(), "B", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := s.SetStoryStartLevel(context.Background(), a.ID); err != nil {
		t.Fatalf("set story start a: %v", err)
	}
	aAfter, _ := s.GetLevel(context.Background(), a.ID)
	if !aAfter.IsStoryStart {
		t.Fatal("expected level A to be the story start level")
	}
	if _, err := s.SetStoryStartLevel(context.Background(), b.ID); err != nil {
		t.Fatalf("set story start b: %v", err)
	}
	aAfter2, _ := s.GetLevel(context.Background(), a.ID)
	bAfter, _ := s.GetLevel(context.Background(), b.ID)
	if aAfter2.IsStoryStart {
		t.Fatal("expected level A to no longer be the story start level after B was set")
	}
	if !bAfter.IsStoryStart {
		t.Fatal("expected level B to be the story start level")
	}
	list, err := s.ListLevels(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	storyStartCount := 0
	for _, l := range list {
		if l.IsStoryStart {
			storyStartCount++
		}
	}
	if storyStartCount != 1 {
		t.Fatalf("expected exactly 1 story start level in list, got %d", storyStartCount)
	}
}

// TestSetEnclosed_TogglesIndependentlyPerLevel (S493, founder real-time: "theres not much
// difference between having lights on and not having lights - its still basically illuminated in
// this totally enclosed level") -- unlike SetStoryStartLevel/SetDefaultQueueLevel above, this is
// NOT an exclusive "only one at a time" flag: any number of levels can each independently be
// enclosed or not, and toggling one must never affect another.
func TestSetEnclosed_TogglesIndependentlyPerLevel(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateLevel(context.Background(), "B", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if a.Enclosed || b.Enclosed {
		t.Fatal("expected both new levels to default to enclosed=false")
	}
	if _, err := s.SetEnclosed(context.Background(), a.ID, true); err != nil {
		t.Fatalf("set enclosed a: %v", err)
	}
	aAfter, _ := s.GetLevel(context.Background(), a.ID)
	bAfter, _ := s.GetLevel(context.Background(), b.ID)
	if !aAfter.Enclosed {
		t.Fatal("expected level A to be enclosed")
	}
	if bAfter.Enclosed {
		t.Fatal("expected level B to be UNAFFECTED by A's own enclosed flag (not an exclusive flag)")
	}
	if _, err := s.SetEnclosed(context.Background(), a.ID, false); err != nil {
		t.Fatalf("unset enclosed a: %v", err)
	}
	aAfter2, _ := s.GetLevel(context.Background(), a.ID)
	if aAfter2.Enclosed {
		t.Fatal("expected level A's own enclosed flag to turn back off")
	}
	exported, err := s.Export(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exported.Enclosed {
		t.Fatal("expected Export to carry the real enclosed=false value through")
	}
}

// TestValidateLevelExits_Bounds checks structural bounds (S473) -- same real convention every
// other validator in this file already follows.
func TestValidateLevelExits_Bounds(t *testing.T) {
	s := newTestStore(t)
	tooMany := make([]shankpit.LevelExit, shankpit.MaxLevelExits+1)
	for i := range tooMany {
		tooMany[i] = shankpit.LevelExit{ID: i, X: float64(i), Radius: 5}
	}
	if _, err := s.CreateLevel(context.Background(), "Too Many Exits", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, tooMany, nil); err == nil {
		t.Fatal("expected error for too many level exits")
	}
	badRadius := []shankpit.LevelExit{{ID: 1, X: 0, Y: 0, Z: 0, Radius: 0}}
	if _, err := s.CreateLevel(context.Background(), "Bad Radius", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, badRadius, nil); err == nil {
		t.Fatal("expected error for non-positive level exit radius")
	}
}

// TestNextLevelID_RejectsSelfReferenceAndNonexistent (S473) -- a level naming itself, or a
// nonexistent level, as its own next_level_id is rejected at save time, same "caught immediately"
// discipline validateNavNodes/validateCharacters already established for their own cross-refs.
func TestNextLevelID_RejectsSelfReferenceAndNonexistent(t *testing.T) {
	s := newTestStore(t)
	bogus := int64(999999)
	if _, err := s.CreateLevel(context.Background(), "Bad Next", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, &bogus); err == nil {
		t.Fatal("expected error for next_level_id referencing a nonexistent level")
	}
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := s.UpdateLevel(context.Background(), a.ID, 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, &a.ID); err == nil {
		t.Fatal("expected error for a level referencing itself as its own next_level_id")
	}
}

// TestNextLevelID_RealChainSurvivesRoundTrip (S473) -- a level's own next_level_id, once set to a
// real, existing level, round-trips through GetLevel and reaches Export unchanged.
func TestNextLevelID_RealChainSurvivesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateLevel(context.Background(), "B", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, &a.ID)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if b.NextLevelID == nil || *b.NextLevelID != a.ID {
		t.Fatalf("expected B's next_level_id to be A's id (%d), got %v", a.ID, b.NextLevelID)
	}
	bAfter, err := s.GetLevel(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	if bAfter.NextLevelID == nil || *bAfter.NextLevelID != a.ID {
		t.Fatalf("expected B's next_level_id to survive round-trip, got %v", bAfter.NextLevelID)
	}
	exported, err := s.Export(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exported.NextLevelID == nil || *exported.NextLevelID != a.ID {
		t.Fatalf("expected exported next_level_id to be A's id, got %v", exported.NextLevelID)
	}
}

// TestExport_CarriesLevelExits (S473) -- a level exit round-trips through Export field-for-field,
// same real "no cross-reference to resolve" simplicity TestExport_CharactersRoundTripWithNoIDTranslation
// already established for Character.
func TestExport_CarriesLevelExits(t *testing.T) {
	s := newTestStore(t)
	exits := []shankpit.LevelExit{{ID: 1, X: 10, Y: 0, Z: 20, Radius: 6}}
	created, err := s.CreateLevel(context.Background(), "Exit Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, exits, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	exported, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported.LevelExits) != 1 {
		t.Fatalf("expected 1 exported level exit, got %d", len(exported.LevelExits))
	}
	got := exported.LevelExits[0]
	if got.X != 10 || got.Y != 0 || got.Z != 20 || got.Radius != 6 {
		t.Fatalf("exported level exit fields mismatch: %+v", got)
	}
}

// TestExport_CarriesDrexit -- DREXIT (S524, founder real-time: "can we spawn a door that is also
// an exit via the widget system... call it a DREXIT"). There is no distinct server-side "drexit"
// type -- NOCK's own toggleDrexit composes the two existing primitives client-side: a Door on the
// selected wall plus a LevelExit centered on that same wall's x/y/z. This exercises exactly that
// shape end to end (create, export) and confirms both rows survive the round trip together, same
// as every other Door/LevelExit combination already does individually.
func TestExport_CarriesDrexit(t *testing.T) {
	s := newTestStore(t)
	wall := aCube()
	doors := []shankpit.Door{{ID: 1, WallID: wall.ID, ScriptID: 0}}
	exits := []shankpit.LevelExit{{ID: 1, X: wall.X, Y: wall.Y, Z: wall.Z, Radius: 4}}
	created, err := s.CreateLevel(context.Background(), "Drexit Level", 100, 50, 100, true, 2, []shankpit.Wall{wall}, nil, nil, doors, nil, nil, exits, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	exported, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported.Doors) != 1 || len(exported.LevelExits) != 1 {
		t.Fatalf("expected 1 door + 1 level exit, got %d doors, %d exits", len(exported.Doors), len(exported.LevelExits))
	}
	exit := exported.LevelExits[0]
	if exit.X != wall.X || exit.Y != wall.Y || exit.Z != wall.Z {
		t.Fatalf("drexit exit should sit at the door's own wall position: got %+v, want wall %+v", exit, wall)
	}
}

// TestExport_CarriesLevelExitTargetSpawnerID (S491, founder real-time -- GTA-style building
// interiors: "how can i specify which spawner the exit leads to for the seamless experience of
// exiting the building") -- TargetSpawnerID round-trips through Export same as every other
// LevelExit field, and the real default (0, "no specific target") is preserved when absent.
func TestExport_CarriesLevelExitTargetSpawnerID(t *testing.T) {
	s := newTestStore(t)
	exits := []shankpit.LevelExit{
		{ID: 1, X: 10, Y: 0, Z: 20, Radius: 6, TargetSpawnerID: 9},
		{ID: 2, X: -10, Y: 0, Z: -20, Radius: 6}, // no target -- real, honest default
	}
	created, err := s.CreateLevel(context.Background(), "Exit Level 2", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, exits, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	exported, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported.LevelExits) != 2 {
		t.Fatalf("expected 2 exported level exits, got %d", len(exported.LevelExits))
	}
	if exported.LevelExits[0].TargetSpawnerID != 9 {
		t.Fatalf("expected exit 0's target_spawner_id to round-trip as 9, got %d", exported.LevelExits[0].TargetSpawnerID)
	}
	if exported.LevelExits[1].TargetSpawnerID != 0 {
		t.Fatalf("expected exit 1's target_spawner_id to default to 0 (no specific target), got %d", exported.LevelExits[1].TargetSpawnerID)
	}
}

func TestExport_MatchesNativeWallShape(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{aCube()}, nil, nil, nil, nil, nil, nil, nil)
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

// TestExport_FlattensObjectAtOffset verifies the core S459-15 mechanism: a level referenced as an
// object contributes its own walls, translated by the object's own placement position.
func TestExport_FlattensObjectAtOffset(t *testing.T) {
	s := newTestStore(t)
	child, err := s.CreateLevel(context.Background(), "Child", 100, 50, 100, true, 2, []shankpit.Wall{aCube()}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: child.ID, X: 100, Y: 0, Z: 200, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Walls) != 1 {
		t.Fatalf("expected 1 flattened wall, got %d", len(doc.Walls))
	}
	if doc.Walls[0].X != 100 || doc.Walls[0].Z != 200 {
		t.Fatalf("expected the child's wall translated by the object's own offset (100,_,200), got (%g,_,%g)", doc.Walls[0].X, doc.Walls[0].Z)
	}
}

// TestExport_ComposedObjectCarriesMaterial -- S479, real, found-live bug (founder real-time: "the
// embeded levels dont respect materials at least visually"): flattenObjects' own Wall{} literal
// never carried the child wall's own Material field through, so a composed object's walls always
// silently fell back to the default material regardless of what they were actually authored with.
func TestExport_ComposedObjectCarriesMaterial(t *testing.T) {
	s := newTestStore(t)
	childWall := aCube()
	childWall.Material = "metal"
	child, err := s.CreateLevel(context.Background(), "Child", 100, 50, 100, true, 2, []shankpit.Wall{childWall}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: child.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Walls) != 1 {
		t.Fatalf("expected 1 flattened wall, got %d", len(doc.Walls))
	}
	if doc.Walls[0].Material != "metal" {
		t.Fatalf("expected the composed wall to carry the child's own material %q through, got %q", "metal", doc.Walls[0].Material)
	}
}

// TestExport_ComposedObjectCarriesDoor -- S479 follow-up, founder real-time: "we need an actual
// object builder building a door as a level doesnt make any sense i need an actual door that is a
// door." Root cause: a door could previously ONLY attach to a level's own ROOT walls, never a
// wall contributed by a nested composed object -- so a level authored as a reusable "door room"
// and placed as an Object anywhere (the exact same way every other reusable structure already
// works) could never carry a working door. Verifies a door on a child level's own wall survives
// composition into the parent's export with the correct, real final box_index.
func TestExport_ComposedObjectCarriesDoor(t *testing.T) {
	s := newTestStore(t)
	doorWall := aCube()
	doorWall.ID = 5 // deliberately non-sequential, matching TestExport_ResolvesDoorBoxIndexAndScriptURL's own real-position proof
	child, err := s.CreateLevel(context.Background(), "Door Room", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube(), doorWall}, nil, nil,
		[]shankpit.Door{{WallID: 5, ScriptID: 0}}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Parent also has its own root wall, so the composed door's real final box_index (2) must
	// account for it -- not just the child's own internal position (1).
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube()},
		[]shankpit.LevelObject{{RefLevelID: child.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Walls) != 3 {
		t.Fatalf("expected 3 flattened walls (1 root + 2 from the child), got %d", len(doc.Walls))
	}
	if len(doc.Doors) != 1 {
		t.Fatalf("expected the child's own door to survive composition, got %d doors: %+v", len(doc.Doors), doc.Doors)
	}
	if doc.Doors[0].BoxIndex != 2 {
		t.Fatalf("expected box_index 2 (root wall at 0, child's first wall at 1, door wall at 2), got %d", doc.Doors[0].BoxIndex)
	}
	if doc.Doors[0].ScriptURL != "" {
		t.Fatalf("expected empty script_url for a no-script door, got %q", doc.Doors[0].ScriptURL)
	}
}

// TestExport_RotatesObjectBy90 verifies a 90-degree object rotation swaps the child's own X/Z
// footprint correctly -- the exact "mirror the base for a 2-base fortress vs fortress map" use
// case (founder real-time).
func TestExport_RotatesObjectBy90(t *testing.T) {
	s := newTestStore(t)
	child, err := s.CreateLevel(context.Background(), "Child", 100, 50, 100, true, 2,
		[]shankpit.Wall{{ID: 1, X: 10, Y: 0, Z: 0, SX: 8, SY: 4, SZ: 2, R: 1, G: 1, B: 1}}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: child.ID, X: 0, Y: 0, Z: 0, RotY: 90}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	got := doc.Walls[0]
	// A 90-degree Y turn sends local (x=10,z=0) -> (x=0,z=10), and swaps sx/sz (8,2) -> (2,8).
	if got.X != 0 || got.Z != 10 || got.SX != 2 || got.SZ != 8 {
		t.Fatalf("unexpected 90-degree rotation result: %+v", got)
	}
}

// TestExport_RejectsDirectSelfReference verifies validateObjects catches the trivial 1-hop cycle
// (a level referencing itself) at SAVE time, before it ever reaches Export's own recursion guard.
func TestExport_RejectsDirectSelfReference(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = s.UpdateLevel(context.Background(), created.ID, created.Width, created.Height, created.Depth, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: created.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a level object directly referencing its own parent level")
	}
}

// TestExport_RejectsIndirectCycle verifies Export's own recursion-time cycle guard catches a
// 2-hop cycle (A references B, B references A) that validateObjects' own single-level check
// can't see at save time.
func TestExport_RejectsIndirectCycle(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateLevel(context.Background(), "A", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateLevel(context.Background(), "B", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: a.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := s.UpdateLevel(context.Background(), a.ID, 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: b.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("update a to reference b: %v", err)
	}
	if _, err := s.Export(context.Background(), a.ID); err == nil {
		t.Fatal("expected a cycle error exporting a level whose object graph cycles back to itself")
	}
}

// TestCreateLevel_RejectsDoorWithUnknownWallID verifies validateDoors catches a door referencing
// a wall_id that isn't one of the level's own root walls, at save time -- same real "caught
// immediately" discipline TestExport_RejectsDirectSelfReference already established for objects.
func TestCreateLevel_RejectsDoorWithUnknownWallID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube()}, nil, nil, []shankpit.Door{{WallID: 999, ScriptID: 1}}, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for a door referencing an unknown wall_id")
	}
}

// TestCreateLevel_RejectsTooManyDoors verifies the real MaxDoors bound, mirroring SHANKPIT's own
// native LEVEL_BOXES_MAX_DOORS cap.
func TestCreateLevel_RejectsTooManyDoors(t *testing.T) {
	s := newTestStore(t)
	walls := make([]shankpit.Wall, shankpit.MaxDoors+1)
	doors := make([]shankpit.Door, shankpit.MaxDoors+1)
	for i := range walls {
		w := aCube()
		w.ID = i + 1
		walls[i] = w
		doors[i] = shankpit.Door{WallID: w.ID, ScriptID: 1}
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, walls, nil, nil, doors, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for exceeding MaxDoors")
	}
}

// TestExport_ResolvesDoorBoxIndexAndScriptURL verifies the real, load-bearing mechanism
// doorsForExport implements: a door's own wall_id resolves to that wall's 0-based POSITION in
// the exported walls[] array (not the wall_id itself, and not the door's own id) -- the exact
// shape SHANKPIT's native level_boxes.h door parser expects -- and script_id resolves to the real
// absolute nock-door-scripts download URL.
func TestExport_ResolvesDoorBoxIndexAndScriptURL(t *testing.T) {
	s := newTestStore(t)
	secondWall := aCube()
	secondWall.ID = 7 // deliberately non-sequential, to prove box_index is a real POSITION, not the raw wall_id
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube(), secondWall}, nil, nil,
		[]shankpit.Door{{WallID: 7, ScriptID: 42}}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Doors) != 1 {
		t.Fatalf("expected 1 exported door, got %d: %+v", len(doc.Doors), doc.Doors)
	}
	if doc.Doors[0].BoxIndex != 1 {
		t.Fatalf("expected box_index 1 (second wall's real position), got %d", doc.Doors[0].BoxIndex)
	}
	wantURL := shankpit.NockDoorScriptDownloadBaseURL + "/42/download"
	if doc.Doors[0].ScriptURL != wantURL {
		t.Fatalf("expected script_url %q, got %q", wantURL, doc.Doors[0].ScriptURL)
	}
}

// TestExport_DoorWithNoScriptLeavesScriptURLEmpty is the real regression test for the bug found
// live 2026-09-17 (founder: "i dont know why this never moved forward i kept asking for doors
// please make doors actually work"): a door with no ScriptID attached used to ALWAYS emit a real
// script_url pointing at script id 0 (".../nock-door-scripts/0/download"), a real 404 every time
// -- so a door placed without writing+attaching a custom PARENA script silently never worked, on
// every platform, with zero visible feedback. ScriptID 0 must now leave ScriptURL empty, which is
// what tells the native loader to use its own real, working builtin proximity-open default
// instead of attempting a doomed fetch.
func TestExport_DoorWithNoScriptLeavesScriptURLEmpty(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube()}, nil, nil,
		[]shankpit.Door{{WallID: 1, ScriptID: 0}}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Doors) != 1 {
		t.Fatalf("expected 1 exported door, got %d: %+v", len(doc.Doors), doc.Doors)
	}
	if doc.Doors[0].ScriptURL != "" {
		t.Fatalf("expected empty script_url for a door with no script attached, got %q", doc.Doors[0].ScriptURL)
	}
}

// TestExport_SkipsDoorWithDeletedWall verifies doorsForExport's own real, honest degrade: a door
// whose wall was deleted after the door was attached (only possible for pre-validateDoors data,
// but Export must still never crash on it) is silently skipped, not an Export-time error.
func TestExport_SkipsDoorWithDeletedWall(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube()}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Directly inject a stale door referencing a wall_id that was never (or no longer) valid --
	// simulating data saved before validateDoors existed, bypassing today's own save-time check.
	if _, err := s.DB.ExecContext(context.Background(),
		`UPDATE shankpit_levels SET doors_json = ? WHERE id = ?`, `[{"id":1,"wall_id":999,"script_id":1}]`, created.ID); err != nil {
		t.Fatalf("inject stale door: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export should not fail on a stale door reference: %v", err)
	}
	if len(doc.Doors) != 0 {
		t.Fatalf("expected the stale door to be silently skipped, got %+v", doc.Doors)
	}
}

// TestCreateLevel_RejectsNavNodeWithUnknownNeighborID verifies validateNavNodes catches a node
// whose own neighbor_ids references a node that isn't part of this level, at save time -- same
// real "caught immediately" discipline TestCreateLevel_RejectsDoorWithUnknownWallID established.
func TestCreateLevel_RejectsNavNodeWithUnknownNeighborID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil,
		[]shankpit.NavNode{{ID: 1, X: 0, Y: 0, Z: 0, NeighborIDs: []int{999}}}, nil, nil, nil); err == nil {
		t.Fatal("expected an error for a nav node referencing an unknown neighbor_id")
	}
}

// TestCreateLevel_RejectsTooManyNavNodes verifies the real MaxNavNodes bound, mirroring
// SHANKPIT's own native AI_NAV_MAX_NODES cap.
func TestCreateLevel_RejectsTooManyNavNodes(t *testing.T) {
	s := newTestStore(t)
	nodes := make([]shankpit.NavNode, shankpit.MaxNavNodes+1)
	for i := range nodes {
		nodes[i] = shankpit.NavNode{ID: i + 1, X: float64(i), Y: 0, Z: 0}
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nodes, nil, nil, nil); err == nil {
		t.Fatal("expected an error for exceeding MaxNavNodes")
	}
}

// TestCreateLevel_RejectsTooManyNeighbors verifies the real MaxNavNeighbors per-node bound,
// mirroring SHANKPIT's own native AI_NAV_MAX_NEIGHBORS cap.
func TestCreateLevel_RejectsTooManyNeighbors(t *testing.T) {
	s := newTestStore(t)
	other := shankpit.NavNode{ID: 2, X: 1, Y: 0, Z: 0}
	tooMany := shankpit.NavNode{ID: 1, X: 0, Y: 0, Z: 0, NeighborIDs: []int{2, 2, 2, 2, 2}}
	// NeighborIDs having duplicates is irrelevant to this check -- what matters is len() >
	// MaxNavNeighbors(4), which 5 entries triggers regardless of content.
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil,
		[]shankpit.NavNode{tooMany, other}, nil, nil, nil); err == nil {
		t.Fatal("expected an error for exceeding MaxNavNeighbors on one node")
	}
}

// TestExport_ResolvesNavNodeNeighborsToPositions verifies the real, load-bearing mechanism
// navNodesForExport implements: a node's own neighbor_ids resolve to real 0-based POSITIONS in
// the exported nav_nodes[] array (not the raw ids), the exact shape SHANKPIT's native
// level_boxes.h nav-node parser expects -- same real convention doorsForExport already
// established for box_index.
func TestExport_ResolvesNavNodeNeighborsToPositions(t *testing.T) {
	s := newTestStore(t)
	// Deliberately non-sequential/out-of-order ids to prove positions are real array order, not
	// derived from id value or declaration order.
	nodeA := shankpit.NavNode{ID: 50, X: 0, Y: 0, Z: 0, NeighborIDs: []int{7}}
	nodeB := shankpit.NavNode{ID: 7, X: 5, Y: 0, Z: 0, IsCover: true, CoverDirX: -1, CoverDirZ: 0, NeighborIDs: []int{50}}
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil,
		[]shankpit.NavNode{nodeA, nodeB}, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.NavNodes) != 2 {
		t.Fatalf("expected 2 exported nav nodes, got %d: %+v", len(doc.NavNodes), doc.NavNodes)
	}
	// nodeA is position 0, its neighbor (id 7 = nodeB) must resolve to position 1.
	if len(doc.NavNodes[0].Neighbors) != 1 || doc.NavNodes[0].Neighbors[0] != 1 {
		t.Fatalf("expected node 0's neighbor to resolve to position 1, got %+v", doc.NavNodes[0])
	}
	// nodeB is position 1, its neighbor (id 50 = nodeA) must resolve to position 0.
	if len(doc.NavNodes[1].Neighbors) != 1 || doc.NavNodes[1].Neighbors[0] != 0 {
		t.Fatalf("expected node 1's neighbor to resolve to position 0, got %+v", doc.NavNodes[1])
	}
	if !doc.NavNodes[1].IsCover || doc.NavNodes[1].CoverDirX != -1 {
		t.Fatalf("expected node 1's cover fields to round-trip, got %+v", doc.NavNodes[1])
	}
}

// TestExport_SkipsNavNodeNeighborWithDeletedNode verifies navNodesForExport's own real, honest
// degrade: a neighbor_id that no longer resolves (simulating data saved before validateNavNodes
// existed) is silently dropped from that node's own neighbor list, not an Export-time error.
func TestExport_SkipsNavNodeNeighborWithDeletedNode(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.DB.ExecContext(context.Background(),
		`UPDATE shankpit_levels SET nav_nodes_json = ? WHERE id = ?`,
		`[{"id":1,"x":0,"y":0,"z":0,"neighbor_ids":[999]}]`, created.ID); err != nil {
		t.Fatalf("inject stale nav node: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export should not fail on a stale neighbor reference: %v", err)
	}
	if len(doc.NavNodes) != 1 {
		t.Fatalf("expected the node itself to still export, got %+v", doc.NavNodes)
	}
	if len(doc.NavNodes[0].Neighbors) != 0 {
		t.Fatalf("expected the stale neighbor reference to be silently dropped, got %+v", doc.NavNodes[0])
	}
}

// TestCreateLevel_RejectsUnknownCharacterRole verifies validateCharacters catches a role outside
// the real, known AIRole range at save time -- same real "caught immediately" discipline every
// other scriptable object kind's own validation already established.
func TestCreateLevel_RejectsUnknownCharacterRole(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil,
		[]shankpit.Character{{ID: 1, Role: 999, X: 0, Y: 0, Z: 0}}, nil, nil); err == nil {
		t.Fatal("expected an error for an unknown character role")
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil,
		[]shankpit.Character{{ID: 1, Role: -1, X: 0, Y: 0, Z: 0}}, nil, nil); err == nil {
		t.Fatal("expected an error for a negative character role")
	}
}

// TestCreateLevel_AcceptsWanderingBotRole (S492, founder real-time: "how do i stat to have
// different characters like walking around the city and stuff - even just standing there and
// having their head turn and look at you and they can say something or whatever") -- real
// regression test for a found-live gap: AIRoleWanderingBot (10) is a real, already-working native
// role (AI_ROLE_WANDERING_BOT, packages/simulation/story_ai.h -- patrols, turns to face + waves +
// dances near the player, never combat), but validateCharacters' own range check stopped one
// value short at AIRoleBlindStalker (9), so a level with this role would have been REJECTED
// outright at save time, not just hidden from NOCK's own dropdown (a separate, now also fixed,
// gap in frontend/nock/src/api.ts's AI_ROLE_OPTIONS).
func TestCreateLevel_AcceptsWanderingBotRole(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Wandering Bot Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil,
		[]shankpit.Character{{ID: 1, Role: shankpit.AIRoleWanderingBot, X: 5, Y: 0, Z: 5}}, nil, nil)
	if err != nil {
		t.Fatalf("expected AIRoleWanderingBot (10) to be accepted, got: %v", err)
	}
	exported, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(exported.Characters) != 1 || exported.Characters[0].Role != shankpit.AIRoleWanderingBot {
		t.Fatalf("expected the wandering bot character to round-trip through export, got %+v", exported.Characters)
	}
}

// TestCreateLevel_RejectsTooManyCharacters verifies the real MaxCharacters bound, mirroring
// SHANKPIT's own native STORY_AI_MAX cap.
func TestCreateLevel_RejectsTooManyCharacters(t *testing.T) {
	s := newTestStore(t)
	characters := make([]shankpit.Character, shankpit.MaxCharacters+1)
	for i := range characters {
		characters[i] = shankpit.Character{ID: i + 1, Role: shankpit.AIRoleGuard, X: float64(i), Y: 0, Z: 0}
	}
	if _, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, characters, nil, nil); err == nil {
		t.Fatal("expected an error for exceeding MaxCharacters")
	}
}

// TestExport_CharactersRoundTripWithNoIDTranslation verifies charactersForExport's own real,
// deliberate simplicity: unlike doors/nav nodes, a character has no cross-reference, so role/x/
// y/z round-trip through export completely unchanged (no position/id resolution needed).
func TestExport_CharactersRoundTripWithNoIDTranslation(t *testing.T) {
	s := newTestStore(t)
	created, err := s.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil,
		[]shankpit.Character{{ID: 42, Role: shankpit.AIRoleTerritorialBeast, X: 10, Y: 8, Z: -10}}, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doc, err := s.Export(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Characters) != 1 {
		t.Fatalf("expected 1 exported character, got %d: %+v", len(doc.Characters), doc.Characters)
	}
	got := doc.Characters[0]
	if got.Role != shankpit.AIRoleTerritorialBeast || got.X != 10 || got.Y != 8 || got.Z != -10 {
		t.Fatalf("expected role/x/y/z to round-trip unchanged, got %+v", got)
	}
}

// TestExport_ComposedWidgetObject -- S482, founder real-time: "i dont want to make doors be
// levels please - make widget or something they are both objects but widgets just dont show up
// in the levels menu and the geometry of the widget shows up not the geometry of the underlying
// level under the widget - there should be no ground plane and no dimension in the widget - a
// level is a dimension - a widget is just a widget." Verifies a widget's own walls AND doors
// compose into a level's export correctly, the same real way a level-referencing object already
// does (S479 follow-up), via the new RefWidgetID field.
func TestExport_ComposedWidgetObject(t *testing.T) {
	s := newTestStore(t)
	doorWall := aCube()
	doorWall.ID = 9 // deliberately non-sequential, same real-position proof as the level-object door test
	widget, err := s.Widgets.CreateWidget(context.Background(), "Door Widget",
		[]shankpit.Wall{aCube(), doorWall},
		[]shankpit.Door{{WallID: 9, ScriptID: 0}})
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2,
		[]shankpit.Wall{aCube()},
		[]shankpit.LevelObject{{RefWidgetID: widget.ID, X: 20, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Walls) != 3 {
		t.Fatalf("expected 3 flattened walls (1 root + 2 from the widget), got %d", len(doc.Walls))
	}
	// The widget's second wall was placed at local x=0; the object itself is offset by X=20.
	if doc.Walls[1].X != 20 || doc.Walls[2].X != 20 {
		t.Fatalf("expected the widget's own walls translated by the object's own offset (20), got %+v", doc.Walls)
	}
	if len(doc.Doors) != 1 {
		t.Fatalf("expected the widget's own door to survive composition, got %d doors: %+v", len(doc.Doors), doc.Doors)
	}
	if doc.Doors[0].BoxIndex != 2 {
		t.Fatalf("expected box_index 2 (root wall at 0, widget's first wall at 1, door wall at 2), got %d", doc.Doors[0].BoxIndex)
	}
}

// TestExport_ComposedWidgetObjectCarriesMaterial -- S482 follow-up, founder real-time: "ensure
// that embeded levels acurately carry their materials into their parent levels." Widget
// composition already carries Material through (flattenObjects' own widget branch copies it the
// same way the level branch does), but the earlier TestExport_ComposedWidgetObject never actually
// asserted on it -- real, direct verification, not an assumption.
func TestExport_ComposedWidgetObjectCarriesMaterial(t *testing.T) {
	s := newTestStore(t)
	metalWall := aCube()
	metalWall.Material = "metal"
	widget, err := s.Widgets.CreateWidget(context.Background(), "Metal Widget", []shankpit.Wall{metalWall}, nil)
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	parent, err := s.CreateLevel(context.Background(), "Parent", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefWidgetID: widget.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	doc, err := s.Export(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(doc.Walls) != 1 || doc.Walls[0].Material != "metal" {
		t.Fatalf("expected the widget's own material %q to survive composition, got %+v", "metal", doc.Walls)
	}
}

// TestCreateLevel_RejectsObjectWithBothOrNeitherRef verifies validateObjects' own new S482 rule:
// exactly one of ref_level_id/ref_widget_id must be set, never both, never neither.
func TestCreateLevel_RejectsObjectWithBothOrNeitherRef(t *testing.T) {
	s := newTestStore(t)
	widget, err := s.Widgets.CreateWidget(context.Background(), "Some Widget", nil, nil)
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	other, err := s.CreateLevel(context.Background(), "Other Level", 100, 50, 100, true, 2, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create other level: %v", err)
	}
	if _, err := s.CreateLevel(context.Background(), "Neither", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for an object with neither ref_level_id nor ref_widget_id set")
	}
	if _, err := s.CreateLevel(context.Background(), "Both", 100, 50, 100, true, 2, nil,
		[]shankpit.LevelObject{{RefLevelID: other.ID, RefWidgetID: widget.ID, X: 0, Y: 0, Z: 0, RotY: 0}}, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected an error for an object with BOTH ref_level_id and ref_widget_id set")
	}
}
