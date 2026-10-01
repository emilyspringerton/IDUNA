// Seed tool: creates TUTORIAL_BRICK in the live shankpit_levels registry -- the living-documentation
// example level for SHANKPIT's destructible brick (founder real-time, 2026-10-01: "i want brick to
// be destructable"; SHANKPIT/CLAUDE.md: verification levels live in the registry as examples
// designers can open). Goes through the same validated shankpit.LevelStore.CreateLevel path the
// /admin/nock HTTP handler uses. Idempotent: does nothing if TUTORIAL_BRICK already exists.
// Run from the IDUNA root: go run ./cmd/mktutorialbrick
//
// What a designer should take from it: every box whose material is "brick" (the default material) is
// destructible -- long thin wall, thick wall, tower, single-cell crates -- EXCEPT thin horizontal slabs
// (floors/platform decks: height <= 12 and both horizontal extents >= 20), door boxes, and anything
// that is not "brick" (the metal pillar, the concrete floor below).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

func main() {
	db, err := sql.Open("sqlite", "var/iduna.db?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var existing int
	if err := db.QueryRow("select count(*) from shankpit_levels where name = ?", "TUTORIAL_BRICK").Scan(&existing); err != nil {
		log.Fatalf("check existing: %v", err)
	}
	if existing > 0 {
		fmt.Println("TUTORIAL_BRICK already exists -- nothing to do")
		return
	}

	materials := &shankpit.MaterialStore{DB: db}
	widgets := &shankpit.WidgetStore{DB: db}
	store := &shankpit.LevelStore{DB: db, Materials: materials, Widgets: widgets}
	ctx := context.Background()

	brick := func(id int, x, y, z, sx, sy, sz float64) shankpit.Wall {
		return shankpit.Wall{ID: id, X: x, Y: y, Z: z, SX: sx, SY: sy, SZ: sz, R: 0.62, G: 0.36, B: 0.28, Material: "brick"}
	}
	walls := []shankpit.Wall{
		// Floor: concrete, so it is NOT destructible (and it is a slab anyway). Top surface at y = 0.
		{ID: 1, X: 0, Y: -2, Z: 0, SX: 140, SY: 4, SZ: 140, R: 0.35, G: 0.35, B: 0.35, Material: "concrete"},
		// A long, thin brick wall (one cell thick): shoot a hole straight through it.
		brick(2, 0, 8, -30, 50, 16, 4),
		// A thick brick wall (several cells deep): it takes sustained fire, or a rocket, to breach.
		brick(3, -40, 8, 10, 6, 16, 30),
		// A brick tower: a rocket carves a crater out of it.
		brick(4, 40, 12, 0, 16, 24, 16),
		// Single-cell brick crates: one cell each, they shatter whole.
		brick(5, -8, 2, 22, 4, 4, 4),
		brick(6, 0, 2, 22, 4, 4, 4),
		brick(7, 8, 2, 22, 4, 4, 4),
		// A metal pillar: NOT brick, so it is indestructible -- the contrast case.
		{ID: 8, X: 0, Y: 8, Z: 0, SX: 4, SY: 16, SZ: 4, R: 0.5, G: 0.5, B: 0.55, Material: "metal"},
	}
	// Spawn south of everything, facing north (yaw 0 faces -z) toward the long wall and the crates.
	spawners := []shankpit.Spawner{{ID: 1, X: 0, Y: 1, Z: 50, Yaw: 0, Team: shankpit.SpawnerTeamFFA}}

	lvl, err := store.CreateLevel(ctx, "TUTORIAL_BRICK", 140, 30, 140, true, 24,
		walls, nil, spawners, nil, nil, nil, nil, nil)
	if err != nil {
		log.Fatalf("create level: %v", err)
	}
	fmt.Printf("created TUTORIAL_BRICK id=%d\n", lvl.ID)
	doc, err := store.Export(ctx, lvl.ID)
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Printf("export ok: %d walls, %d spawners\n", len(doc.Walls), len(doc.Spawners))
}
