// cmd/nock_gen_tyler_levels — one-shot loader that registers TYLER VALHANNA's two real levels
// (episodes/vh01_valhanna_coldopen.md, S536) directly into IDUNA's live SQLite-backed NOCK level
// registry (internal/shankpit.LevelStore), the same DB shankpit_levels.go's own HTTP handlers and
// the /admin/nock web editor read -- matching SHANKPIT/CLAUDE.md's own standing instruction
// ("Level Registry Doubles as Living Documentation": verify a level-editor feature by creating a
// REAL level in the live registry, not a throwaway local JSON file). Throwaway, not wired into
// cmd/nock's own command dispatch.
//
// CONSTRUCT is created first so its real DB id exists before ICELAND authors nextLevelID against
// it -- CreateLevel's own validateNextLevelID rejects a forward reference to an id that doesn't
// exist yet, checked directly in level_store.go before writing this.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"iduna/internal/shankpit"
	"iduna/internal/store"
)

const (
	aiRoleStoryAlly = 3 // AI_ROLE_STORY_ALLY, story_ai.h
	aiKitStan       = 2 // AI_KIT_STAN, story_ai.h AIKit enum
	aiKitMike       = 3 // AI_KIT_MIKE
)

func openDB() *sql.DB {
	dbPath := os.Getenv("NOCK_DB_PATH")
	if dbPath == "" {
		dbPath = "/home/fatbaby/IDUNA/var/iduna.db"
	}
	migrationsDir := os.Getenv("NOCK_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "/home/fatbaby/IDUNA/migrations/truestore"
	}
	db, err := store.OpenSQLite(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	if err := store.RunSQLiteMigrations(db, migrationsDir); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	return db
}

func main() {
	db := openDB()
	ls := &shankpit.LevelStore{DB: db}
	ctx := context.Background()

	// CONSTRUCT — the default blank NOCK level (floor only, per NOCK's own "new level" template)
	// plus one added prop: the mecha cube. Where the Duck (third-person, Leela kit) spawns after
	// VH01's cold open ends.
	constructWalls := []shankpit.Wall{
		{X: 0, Y: 1, Z: 0, SX: 2, SY: 2, SZ: 2, R: 0.10, G: 0.62, B: 0.55, Friction: 1.0, Material: "mecha_cube_lo_source"},
	}
	constructSpawners := []shankpit.Spawner{
		{X: 4, Y: 1, Z: 0, Yaw: 180, Team: -1},
	}
	construct, err := ls.CreateLevel(ctx, "CONSTRUCT", 60, 10, 60, true, 20,
		constructWalls, nil, constructSpawners, nil, nil, nil, nil, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create CONSTRUCT:", err)
		os.Exit(1)
	}
	fmt.Printf("created level id=%d name=%s\n", construct.ID, construct.Name)

	// TYLER_VALHANNA_ICELAND_1986 — VH01's cold open. Server room, all racks empty (thin
	// "rack" walls with no equipment color), one ECS-brand monitor prop, spawn only, no
	// authored LevelExit at all -- "spawn but no exit" is the literal absence of any exit
	// entry, not a locked/disabled one. The scripted-sequence coordinator
	// (packages/simulation/tyler_coldopen.c) calls story_force_level_transition() directly when
	// Tyler's final beat fires, reusing g_story_next_level_id (set below) rather than requiring
	// a player to walk into a trigger volume a non-colliding wisp couldn't reliably reach anyway.
	nextID := construct.ID
	icelandWalls := []shankpit.Wall{
		// server room shell
		{X: -10, Y: 2, Z: 0, SX: 1, SY: 4, SZ: 24, R: 0.46, G: 0.51, B: 0.48, Friction: 1.0, Material: "institutional_tile"},
		{X: 10, Y: 2, Z: 0, SX: 1, SY: 4, SZ: 24, R: 0.46, G: 0.51, B: 0.48, Friction: 1.0, Material: "institutional_tile"},
		{X: 0, Y: 2, Z: -12, SX: 20, SY: 4, SZ: 1, R: 0.46, G: 0.51, B: 0.48, Friction: 1.0, Material: "institutional_tile"},
		{X: 0, Y: 2, Z: 12, SX: 20, SY: 4, SZ: 1, R: 0.46, G: 0.51, B: 0.48, Friction: 1.0, Material: "institutional_tile"},
		// empty server racks -- thin, dark, unlit boxes; "all the servers are gone" (the rack
		// shells remain, nothing populates them)
		{X: -6, Y: 1.5, Z: -6, SX: 1.5, SY: 3, SZ: 1, R: 0.12, G: 0.12, B: 0.13, Friction: 1.0, Material: "empty_rack"},
		{X: -6, Y: 1.5, Z: -2, SX: 1.5, SY: 3, SZ: 1, R: 0.12, G: 0.12, B: 0.13, Friction: 1.0, Material: "empty_rack"},
		{X: -6, Y: 1.5, Z: 2, SX: 1.5, SY: 3, SZ: 1, R: 0.12, G: 0.12, B: 0.13, Friction: 1.0, Material: "empty_rack"},
		{X: -6, Y: 1.5, Z: 6, SX: 1.5, SY: 3, SZ: 1, R: 0.12, G: 0.12, B: 0.13, Friction: 1.0, Material: "empty_rack"},
		// the printer (Tyler stands here) -- a low box, distinct warm color
		{X: 4, Y: 0.5, Z: -4, SX: 1.2, SY: 1.0, SZ: 0.8, R: 0.58, G: 0.55, B: 0.30, Friction: 1.0, Material: "printer"},
		// the ECS terminal/monitor prop -- box named "ecs_screen" so the client-side subtitle
		// renderer (apps/lobby/src/main.c) can find its world position by name convention (no
		// "screen" LevelInteractable kind exists yet, Story System Northstar Part 2 -- this is
		// the honest v0 stand-in: a plain colored box carrying the real generated
		// ecs_screen_glow procedural texture's own dominant phosphor-green as its flat color).
		{X: 6, Y: 1.2, Z: -6, SX: 0.8, SY: 0.6, SZ: 0.15, R: 0.08, G: 0.85, B: 0.28, Friction: 1.0, Material: "ecs_screen_glow", Name: "ecs_screen"},
		// the button Tyler presses at the end (see tyler_coldopen.c's final beat)
		{X: 4, Y: 0.9, Z: -3.3, SX: 0.15, SY: 0.15, SZ: 0.15, R: 0.85, G: 0.15, B: 0.15, Friction: 1.0, Material: "button", Name: "tyler_exit_button"},
	}
	icelandSpawners := []shankpit.Spawner{
		{X: 0, Y: 1, Z: 4, Yaw: 180, Team: -1}, // wisp spawn
	}
	icelandCharacters := []shankpit.Character{
		{Role: aiRoleStoryAlly, Kit: aiKitStan, X: -1, Y: 1, Z: 4}, // Tyler
		{Role: aiRoleStoryAlly, Kit: aiKitMike, X: 1, Y: 1, Z: 4},  // Hana
	}
	iceland, err := ls.CreateLevel(ctx, "TYLER_VALHANNA_ICELAND_1986", 24, 10, 24, true, 12,
		icelandWalls, nil, icelandSpawners, nil, nil, icelandCharacters, nil, &nextID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create TYLER_VALHANNA_ICELAND_1986:", err)
		os.Exit(1)
	}
	fmt.Printf("created level id=%d name=%s next_level_id=%d\n", iceland.ID, iceland.Name, nextID)
}
