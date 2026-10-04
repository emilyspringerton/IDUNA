// One-off seed tool: creates TUTORIAL_RAMP in the live shankpit_levels registry via the same
// validated LevelStore.CreateLevel path /admin/nock uses (no agent holds iduna.admin for the HTTP
// API; same approach as cmd/mktutoriallevel). A worked example of the Ramp checkbox and the
// Rot X/Y/Z fields for designers. Safe to re-run? No -- names are unique, so it errors if it exists.
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
	store := &shankpit.LevelStore{DB: db, Materials: &shankpit.MaterialStore{DB: db}, Widgets: &shankpit.WidgetStore{DB: db}}

	// Floor top surface at y=0.
	floor := shankpit.Wall{ID: 1, Name: "floor", X: 0, Y: -2, Z: 0, SX: 60, SY: 4, SZ: 40, R: 0.35, G: 0.35, B: 0.35, Material: "concrete"}
	// RAMP: a plain cube with Ramp=true. The slope rises toward the cube's local +Z (rot 0 = world
	// +Z). 12 wide, 6 tall, 16 deep: climbs from y=0 at z=-16 to y=6 at z=0.
	ramp := shankpit.Wall{ID: 2, Name: "ramp (Ramp checked, rises toward +Z)", X: -15, Y: 3, Z: -8, SX: 12, SY: 6, SZ: 16, R: 0.8, G: 0.55, B: 0.2, Material: "concrete", Ramp: true}
	// Platform the ramp leads onto: top at y=6, flush with the ramp's top edge at z=0.
	platform := shankpit.Wall{ID: 3, Name: "platform (plain cube)", X: -15, Y: 3, Z: 6, SX: 12, SY: 6, SZ: 12, R: 0.55, G: 0.55, B: 0.6, Material: "concrete"}
	// Second ramp turned 90 degrees about Y (Rot Y = 90): local +Z becomes world +X, so it climbs
	// toward +X. Shows that ramp direction is set with rotation, not a separate field.
	ramp90 := shankpit.Wall{ID: 4, Name: "ramp turned (Rot Y 90, rises toward +X)", X: 6, Y: 2, Z: 12, SX: 10, SY: 4, SZ: 10, R: 0.8, G: 0.55, B: 0.2, Material: "concrete", Ramp: true, RotY: 90}
	// Rotated cube: Rot Y = 45. Collides as a real diamond, not its axis-aligned box.
	diamond := shankpit.Wall{ID: 5, Name: "cube rotated 45 deg (Rot Y 45)", X: 12, Y: 4, Z: -10, SX: 8, SY: 8, SZ: 8, R: 0.3, G: 0.5, B: 0.8, Material: "concrete", RotY: 45}
	// Tilted plank: Rot Z = -20 gives a walkable slope (surfaces under ~60 degrees carry the player).
	plank := shankpit.Wall{ID: 6, Name: "tilted plank (Rot Z -20, walkable)", X: 24, Y: 2.2, Z: 0, SX: 12, SY: 1, SZ: 6, R: 0.6, G: 0.4, B: 0.25, Material: "wood", RotZ: -20}

	walls := []shankpit.Wall{floor, ramp, platform, ramp90, diamond, plank}
	spawners := []shankpit.Spawner{{ID: 1, X: -15, Y: 1, Z: -17, Yaw: 0, Team: shankpit.SpawnerTeamFFA}}

	lvl, err := store.CreateLevel(context.Background(), "TUTORIAL_RAMP", 60, 30, 40, true, 24,
		walls, nil, spawners, nil, nil, nil, nil, nil)
	if err != nil {
		log.Fatalf("create level: %v", err)
	}
	doc, err := store.Export(context.Background(), lvl.ID)
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Printf("created TUTORIAL_RAMP id=%d, export %d walls\n", lvl.ID, len(doc.Walls))
}
