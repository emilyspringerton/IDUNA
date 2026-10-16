package shankpit_test

import (
	"context"
	"testing"

	"iduna/internal/shankpit"
)

func TestCubeStyles_OverlayAndScopes(t *testing.T) {
	ls := newTestStore(t)
	if _, err := ls.DB.Exec(`CREATE TABLE shankpit_cube_styles (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, level_id INTEGER,
		material TEXT NOT NULL DEFAULT 'brick', r REAL NOT NULL DEFAULT 0.6, g REAL NOT NULL DEFAULT 0.6, b REAL NOT NULL DEFAULT 0.6,
		friction REAL NOT NULL DEFAULT 0.3, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE UNIQUE INDEX i ON shankpit_cube_styles (COALESCE(level_id, 0), name)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ss := &shankpit.StyleStore{DB: ls.DB}
	g, err := ss.Create(ctx, shankpit.CubeStyle{Name: "Wall", Material: "concrete", R: 0.5, G: 0.5, B: 0.5, Friction: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	walls := []shankpit.Wall{
		{ID: 1, SX: 1, SY: 1, SZ: 1, R: 0.1, Material: "brick", StyleID: g.ID},
		{ID: 2, SX: 1, SY: 1, SZ: 1, R: 0.1, Material: "brick"},
	}
	lvl, err := ls.CreateLevel(ctx, "StyleLvl", 100, 50, 100, true, 2, walls, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lvl.Walls[0].Material != "concrete" || lvl.Walls[0].R != 0.5 || lvl.Walls[0].Friction != 0.4 {
		t.Fatalf("styled wall not overlaid: %+v", lvl.Walls[0])
	}
	if lvl.Walls[1].Material != "brick" || lvl.Walls[1].R != 0.1 {
		t.Fatalf("unstyled wall changed: %+v", lvl.Walls[1])
	}
	// editing the style restyles the cube with no level save
	g.Material, g.R = "wood", 0.9
	if _, err := ss.Update(ctx, g.ID, *g); err != nil {
		t.Fatal(err)
	}
	lvl, _ = ls.GetLevel(ctx, lvl.ID)
	if lvl.Walls[0].Material != "wood" || lvl.Walls[0].R != 0.9 {
		t.Fatalf("style edit not propagated: %+v", lvl.Walls[0])
	}
	// same name allowed in a different scope, rejected in the same one
	if _, err := ss.Create(ctx, shankpit.CubeStyle{Name: "Wall", LevelID: &lvl.ID, Material: "brick", Friction: 0.3}); err != nil {
		t.Fatalf("local same-name: %v", err)
	}
	if _, err := ss.Create(ctx, shankpit.CubeStyle{Name: "Wall", Material: "brick", Friction: 0.3}); err == nil {
		t.Fatal("duplicate global name accepted")
	}
	if l, _ := ss.List(ctx, lvl.ID); len(l) != 2 {
		t.Fatalf("list = %d, want 2", len(l))
	}
	if l, _ := ss.List(ctx, 0); len(l) != 1 {
		t.Fatalf("global-only list = %d, want 1", len(l))
	}
	// deleting detaches: the cube keeps its last values
	if err := ss.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	lvl, _ = ls.GetLevel(ctx, lvl.ID)
	if lvl.Walls[0].Material == "" {
		t.Fatal("wall lost material after style delete")
	}
}
