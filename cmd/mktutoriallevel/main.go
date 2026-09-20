// One-off seed tool: creates TUTORIAL_DOOR in the live shankpit_levels registry, going through
// the exact same validated shankpit.LevelStore.CreateLevel path the /admin/nock HTTP handler
// calls (so this level is exactly as valid as one a real designer would save) -- run directly
// against var/iduna.db since no registered agent currently holds iduna.admin to authenticate
// through the HTTP API. Not meant to be committed; delete after running.
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

	materials := &shankpit.MaterialStore{DB: db}
	widgets := &shankpit.WidgetStore{DB: db}
	store := &shankpit.LevelStore{DB: db, Materials: materials, Widgets: widgets}

	ctx := context.Background()

	// Floor: a generous slab both rooms sit on. Top surface at y = -2 + 4/2 = 0.
	floor := shankpit.Wall{ID: 1, X: 0, Y: -2, Z: 0, SX: 60, SY: 4, SZ: 40, R: 0.35, G: 0.35, B: 0.35, Material: "concrete"}

	// Dividing wall, split into two segments either side of an 8-unit-wide door gap at z=-4..4,
	// plus a header segment above the door so there's no open gap up to the wall's own full
	// height. All three sit on the floor (bottom at y=0).
	wallNorth := shankpit.Wall{ID: 2, X: 0, Y: 8, Z: -12, SX: 2, SY: 16, SZ: 16, R: 0.55, G: 0.55, B: 0.6, Material: "concrete"}
	wallSouth := shankpit.Wall{ID: 3, X: 0, Y: 8, Z: 12, SX: 2, SY: 16, SZ: 16, R: 0.55, G: 0.55, B: 0.6, Material: "concrete"}
	header := shankpit.Wall{ID: 4, X: 0, Y: 12, Z: 0, SX: 2, SY: 8, SZ: 8, R: 0.55, G: 0.55, B: 0.6, Material: "concrete"}

	// The door itself: fills the gap exactly (x=0, z=-4..4, y=0..8), sized >=2 wide / >=7 tall
	// per the real, confirmed native requirement (SHANKPIT's door detection needs a wall at least
	// 2 units in its narrow axis and at least 7 units tall). Given its own distinct "wood"
	// material + warm color so it's visually obvious which block is the door, separate from the
	// concrete walls around it -- this is deliberate: a designer opening this level in NOCK should
	// be able to tell which wall IS the door just by looking at it, no guessing required.
	doorWall := shankpit.Wall{ID: 5, X: 0, Y: 4, Z: 0, SX: 2, SY: 8, SZ: 8, R: 0.55, G: 0.35, B: 0.15, Material: "wood"}

	walls := []shankpit.Wall{floor, wallNorth, wallSouth, header, doorWall}

	// ScriptID: 0 -- the real "no script" sentinel (S480a fix, now correctly offered in NOCK's own
	// door dropdown). No PARENA script needed: SHANKPIT's native door code already opens/closes
	// this wall on pure proximity (8-unit open / 12-unit close hysteresis) whenever ScriptID==0.
	doors := []shankpit.Door{{ID: 1, WallID: 5, ScriptID: 0}}

	// One spawner in Room A (west side, z=-12) so a player entering this level starts on the side
	// they have to walk THROUGH the door to leave -- the whole point of the tutorial.
	spawners := []shankpit.Spawner{{ID: 1, X: -14, Y: 1, Z: -12, Yaw: 90, Team: shankpit.SpawnerTeamFFA}}

	lvl, err := store.CreateLevel(ctx, "TUTORIAL_DOOR", 60, 30, 40, true, 24,
		walls, nil, spawners, doors, nil, nil, nil, nil)
	if err != nil {
		log.Fatalf("create level: %v", err)
	}
	fmt.Printf("created TUTORIAL_DOOR id=%d\n", lvl.ID)

	doc, err := store.Export(ctx, lvl.ID)
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Printf("export ok: %d walls, %d doors, %d spawners\n", len(doc.Walls), len(doc.Doors), len(doc.Spawners))
}
