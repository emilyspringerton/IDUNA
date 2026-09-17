// Package shankpit implements the real, SQLite-backed SHANKPIT NOCK level editor store (v0,
// EMILY/BACKLOG.md SECTION 459, founder real-time: "so v0 it and start working dont worry about
// the current levels lets just go full level select brawlpit repo exact model for now").
//
// A real, standalone-row CRUD store, mirroring internal/brawlpit.LevelStore's own established
// shape field-for-field (list/create/get/update/rename/clone/delete/export, one row per level, no
// single "master" with derived views) -- kept in its own package rather than folded into
// internal/nock itself, same design principle that already keeps NOCK un-coupled from any one
// specific game's concepts, and kept separate from internal/brawlpit since this is a different
// game's own domain object, not a variant of BRAWLPIT's 2D Platform2D.
//
// Wall is the EXACT real shape SHANKPIT/packages/map/map.h's own `Wall` struct already defines --
// center x/y/z, FULL extents sx/sy/sz (packages/map/map.c's own collision code computes bounds as
// center +/- size/2, confirmed by reading that file directly, not assumed), r/g/b as a real
// OpenGL-convention [0,1] color (every existing glColor3f/glColor4f call in this monorepo's own
// SHANKPIT client uses that same range), plus friction. See
// migrations/truestore/202609140001_shankpit_levels.sql for the real schema.
package shankpit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

// Wall mirrors SHANKPIT's own real Wall struct field-for-field (json tags match map.h's own real
// field names exactly, not Go convention, so a level exported from here is byte-for-byte what
// GameMap's own native loader already expects once wired up).
type Wall struct {
	ID       int     `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	SX       float64 `json:"sx"`
	SY       float64 `json:"sy"`
	SZ       float64 `json:"sz"`
	R        float64 `json:"r"`
	G        float64 `json:"g"`
	B        float64 `json:"b"`
	Friction float64 `json:"friction"`
	// Material (S459-16, founder real-time: "i would get a lot of value from being able to change
	// their texture - i think it makes sense to abstract into material first" / "the default
	// material is brick"). A plain name resolved against shankpit_materials at export time (see
	// materialsForExport) -- empty means DefaultMaterialName ("brick"), matching every pre-
	// S459-16 level's own real, existing walls_json rows (which have no "material" key at all)
	// without needing a migration to backfill them.
	Material string `json:"material,omitempty"`
}

// LevelObject is a level placed as a child object inside another level -- the "map" primitive,
// founder real-time: "i have this level 2222 - already i want to use it as an object - the whole
// level ... a map is a composition of levels" then, same session: "really a level and a map is
// the same thing - its like a smart document in photoshop where you have like a photoshop doc in
// a photoshop doc." There is no separate Map type -- any level can hold objects, each one a
// reference to another level. RotY is snapped to one of {0, 90, 180, 270} (founder: "snap rotate
// 90 degree turns is good for now"). PlaneVisible/PlaneSolid are real, stored, forward-compatible
// per-instance overrides for the referenced level's own ground plane, but Export's own
// flattenObjects does not act on them yet (both default false, matching "DEFAULTS TO OFF") -- see
// that function's own doc comment for the real, honest, not-yet-built reason why.
type LevelObject struct {
	ID           int     `json:"id"`
	RefLevelID   int64   `json:"ref_level_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Z            float64 `json:"z"`
	RotY         int     `json:"rot_y"`
	PlaneVisible bool    `json:"plane_visible"`
	PlaneSolid   bool    `json:"plane_solid"`
}

// Spawner is a real, author-placed spawn point (S459-58, founder real-time: "add spawners to
// nock so we can add spawners for ffa" / "actual make them team based but fall back to ffa" /
// "call it red team and blue team"). Team uses SHANKPIT's own real, live TDMB_RED_TEAM=0 /
// TDMB_BLUE_TEAM=1 convention (packages/simulation/local_game.h, confirmed by grep, not assumed)
// so a spawner exported from here needs no translation on the native side; TeamFFA (-1) is the
// real "no team / any team" sentinel -- an FFA-tagged spawner is used for FFA matches and as the
// real fallback when a team match has no spawner for the player's own team. Yaw is stored in
// degrees (matching LevelObject's own RotY convention of whole-number degrees, not radians) so
// the level editor's UI can show a plain, human number.
type Spawner struct {
	ID   int     `json:"id"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
	Yaw  float64 `json:"yaw"`
	Team int     `json:"team"` // -1 = FFA/any team, 0 = Red Team, 1 = Blue Team
}

const (
	SpawnerTeamFFA  = -1
	SpawnerTeamRed  = 0
	SpawnerTeamBlue = 1
)

// Door is a real, author-attached scriptable door (Story System Phase 1, SHANKPIT/docs/
// STORY_SYSTEM_NORTHSTAR.md Part 2 -- founder real-time, 2026-09-17: "how do i put doors in my
// levels?"). WallID references a Wall.ID from THIS SAME level's own root Walls array -- never a
// wall contributed by a nested composed object, matching flattenObjects' own already-established
// "only the root level's own X reaches the native client" scope limit for ground planes.
// ScriptID references a nock_door_scripts row (internal/nock's own DoorScript repository,
// S459-81/82) -- resolved to that repository's real public download URL at Export time (see
// doorsForExport), never stored here directly, so a script can be edited/regenerated in place
// without needing every level that uses it to be re-saved.
type Door struct {
	ID       int   `json:"id"`
	WallID   int   `json:"wall_id"`
	ScriptID int64 `json:"script_id"`
}

// MaxDoors mirrors SHANKPIT's own real LEVEL_BOXES_MAX_DOORS (packages/world/level_boxes.h) --
// kept in exact sync so a level saved here can never exceed what the native LevelDoor array can
// actually hold, same real reason MaxWalls/MaxSpawners exist.
const MaxDoors = 16

// validateDoors checks structural bounds and that every door's own wall_id is real -- a door
// referencing a wall that was since deleted (or never existed) is rejected at save time here
// rather than silently dropped later at Export, matching validateObjects' own "self-reference
// caught immediately" discipline (deeper/derived issues, like a since-deleted script_id, are a
// real, separate, deliberately deferred check -- see doorsForExport's own doc comment).
func validateDoors(doors []Door, walls []Wall) error {
	if len(doors) > MaxDoors {
		return fmt.Errorf("shankpit: too many doors (%d, max %d -- SHANKPIT's own native LevelDoor array can't hold more)", len(doors), MaxDoors)
	}
	wallIDs := make(map[int]bool, len(walls))
	for _, w := range walls {
		wallIDs[w.ID] = true
	}
	for i, d := range doors {
		if !wallIDs[d.WallID] {
			return fmt.Errorf("shankpit: door %d references wall_id %d, which is not one of this level's own root walls", i, d.WallID)
		}
	}
	return nil
}

// NavNode is a real, author-placed waypoint/cover node (S461-01/S464 -- founder real-time: "we
// are going to need a waypoint system in the levels and maps northstar it" / "continue filling
// in the gaps in our level editor"). Mirrors SHANKPIT's own real AINavNode field-for-field
// (packages/simulation/ai_nav.h): x/y/z, IsCover + a CoverDir unit vector (the direction FROM
// the node AWAY FROM the obstacle providing cover -- see ai_nav.h's own doc comment for the real
// dot-product test this feeds). NeighborIDs references OTHER NavNode.ID values within the SAME
// level -- resolved into real 0-based array positions only at Export time (navNodesForExport),
// same real "author-facing id vs. native array position" split Door's own WallID/box_index
// already established.
type NavNode struct {
	ID          int     `json:"id"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Z           float64 `json:"z"`
	IsCover     bool    `json:"is_cover"`
	CoverDirX   float64 `json:"cover_dir_x"`
	CoverDirZ   float64 `json:"cover_dir_z"`
	NeighborIDs []int   `json:"neighbor_ids"`
}

// MaxNavNodes/MaxNavNeighbors mirror SHANKPIT's own real AI_NAV_MAX_NODES/AI_NAV_MAX_NEIGHBORS
// (packages/simulation/ai_nav.h) -- kept in exact sync so a level saved here can never exceed
// what the native AINavGraph can actually hold.
const MaxNavNodes = 32
const MaxNavNeighbors = 4

// validateNavNodes checks structural bounds and that every node's own neighbor_ids resolve to
// real nodes within this same level -- a node referencing an unknown/deleted neighbor is
// rejected at save time here, same real "caught immediately" discipline validateDoors already
// established, rather than silently dropped later at Export.
func validateNavNodes(nodes []NavNode) error {
	if len(nodes) > MaxNavNodes {
		return fmt.Errorf("shankpit: too many nav nodes (%d, max %d -- SHANKPIT's own native AINavGraph can't hold more)", len(nodes), MaxNavNodes)
	}
	ids := make(map[int]bool, len(nodes))
	for _, n := range nodes {
		ids[n.ID] = true
	}
	for i, n := range nodes {
		if len(n.NeighborIDs) > MaxNavNeighbors {
			return fmt.Errorf("shankpit: nav node %d has too many neighbors (%d, max %d)", i, len(n.NeighborIDs), MaxNavNeighbors)
		}
		for _, nid := range n.NeighborIDs {
			if !ids[nid] {
				return fmt.Errorf("shankpit: nav node %d references neighbor_id %d, which is not one of this level's own nav nodes", i, nid)
			}
		}
	}
	return nil
}

// Character is a real, author-placed story_ai NPC (S467, STORY_SYSTEM_NORTHSTAR.md Phase 2's own
// "character" kind -- founder real-time: "continue filling in the gaps in our level editor
// scriptable env characters etc"). Role is the same integer AIRole value SHANKPIT's own
// story_ai_spawn_enemy already takes (packages/simulation/story_ai.h) -- this store has no
// simulation-layer dependency, same real "flat data, validated by the consumer" boundary
// Wall/Door/NavNode already hold themselves to. Unlike Door/NavNode, a character has no
// cross-reference to validate at all -- the simplest of the four scriptable object kinds shipped
// so far.
type Character struct {
	ID   int     `json:"id"`
	Role int     `json:"role"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
}

// MaxCharacters mirrors SHANKPIT's own real STORY_AI_MAX (packages/simulation/story_ai.h).
const MaxCharacters = 16

// Real AIRole values, hand-kept in sync with SHANKPIT's own C enum
// (packages/simulation/story_ai.h) -- no shared header exists across this Go/C boundary, same
// established convention SHANKPIT_GRID_CELL_SIZE/LEVEL_BOXES_MAX_NAV_NEIGHBORS already use
// elsewhere in this same file/repo pair.
const (
	AIRoleRiftHound         = 0
	AIRoleShamblerTrooper   = 1
	AIRoleGoreBrute         = 2
	AIRoleStoryAlly         = 3
	AIRoleGuard             = 4
	AIRoleStormCaller       = 5
	AIRoleBombardier        = 6
	AIRoleRelentlessPursuer = 7
	AIRoleTerritorialBeast  = 8
	AIRoleBlindStalker      = 9
)

// validateCharacters checks structural bounds and that every character's own role is a real,
// known AIRole value -- an unknown role is rejected at save time here rather than silently
// skipped later at spawn time (SHANKPIT's own server_apply_custom_level does its own defensive
// range check too, matching "an author's own stale/bad reference mustn't break the level," but
// catching it here gives the level author real, immediate feedback instead of a silent no-op
// enemy).
func validateCharacters(characters []Character) error {
	if len(characters) > MaxCharacters {
		return fmt.Errorf("shankpit: too many characters (%d, max %d -- SHANKPIT's own native STORY_AI_MAX can't hold more)", len(characters), MaxCharacters)
	}
	for i, c := range characters {
		if c.Role < AIRoleRiftHound || c.Role > AIRoleBlindStalker {
			return fmt.Errorf("shankpit: character %d has unknown role %d", i, c.Role)
		}
	}
	return nil
}

// LevelExit is a real, author-placed trigger volume (S473, STORY_LEVEL_SEQUENCING_NORTHSTAR.md
// Phase 1 -- founder real-time: "we need the loading points or whatever the opposite of the
// spawners is") -- a player entering it server-side transitions to this LEVEL's own real
// NextLevelID (v0 is a chain, not a per-exit destination: every exit volume in a level leads to
// the same next level, see the NORTHSTAR doc for why a general graph is deliberately deferred).
// Same real "no cross-reference to resolve" simplicity Character already established -- Radius is
// the only field beyond a plain position.
type LevelExit struct {
	ID     int     `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
}

// MaxLevelExits mirrors SHANKPIT's own real LEVEL_BOXES_MAX_LEVEL_EXITS (packages/world/
// level_boxes.h) -- a real, sane v0 cap, matching MaxCharacters/MaxNavNodes's own reasoning (a
// level needs a handful of real exit points, not hundreds).
const MaxLevelExits = 8

// validateLevelExits checks structural bounds and that every exit's own radius is a real,
// positive number -- a zero/negative radius would never trigger (or would trigger everywhere,
// for a negative stored value squared downstream), caught here rather than silently authored.
func validateLevelExits(exits []LevelExit) error {
	if len(exits) > MaxLevelExits {
		return fmt.Errorf("shankpit: too many level exits (%d, max %d)", len(exits), MaxLevelExits)
	}
	for i, e := range exits {
		if e.Radius <= 0 {
			return fmt.Errorf("shankpit: level exit %d has non-positive radius %v", i, e.Radius)
		}
	}
	return nil
}

// MaxSpawners bounds how many spawn points one level may hold -- a real, sane v0 cap, mirroring
// MaxLevelObjects's own reasoning (a level needs a handful of spawns per team, not hundreds).
const MaxSpawners = 64

func validateSpawners(spawners []Spawner) error {
	if len(spawners) > MaxSpawners {
		return fmt.Errorf("shankpit: too many spawners (%d, max %d)", len(spawners), MaxSpawners)
	}
	for i, sp := range spawners {
		if sp.Team != SpawnerTeamFFA && sp.Team != SpawnerTeamRed && sp.Team != SpawnerTeamBlue {
			return fmt.Errorf("shankpit: spawner %d has invalid team %d (must be -1 FFA, 0 Red Team, or 1 Blue Team)", i, sp.Team)
		}
	}
	return nil
}

// MaxLevelObjects bounds how many object children one level may hold -- a real, sane v0 cap
// (mirrors MaxWalls's own reasoning), not unbounded.
const MaxLevelObjects = 50

// MaxLevelObjectDepth bounds recursive flatten nesting (levels containing objects that are
// levels containing objects...) -- founder: "its a bit fractal we can let it go further down or
// up." A real, finite safety bound, not infinite -- combined with the cycle check in
// flattenObjects, this is what keeps a level that (directly or indirectly) references itself from
// hanging or crashing Export.
const MaxLevelObjectDepth = 6

var validRotY = map[int]bool{0: true, 90: true, 180: true, 270: true}

func validateObjects(selfID int64, objs []LevelObject) error {
	if len(objs) > MaxLevelObjects {
		return fmt.Errorf("shankpit: too many objects (%d, max %d)", len(objs), MaxLevelObjects)
	}
	for i, o := range objs {
		if !validRotY[o.RotY] {
			return fmt.Errorf("shankpit: object %d has invalid rot_y %d (must be 0, 90, 180, or 270)", i, o.RotY)
		}
		if o.RefLevelID == selfID {
			return fmt.Errorf("shankpit: object %d references its own parent level directly -- not allowed (deeper cycles are caught at export time)", i)
		}
	}
	return nil
}

// GridCellSize is the real, fixed, constant world-unit size of one ground-plane grid square --
// founder, direct: "the squares are always the same size" / "so the units needs to be the number
// of squares in the grid." The plane's own real editable field is a SQUARE COUNT
// (GroundPlaneSquares), not a raw world-unit length -- the actual world-unit footprint is always
// GroundPlaneSquares * GridCellSize.
//
// REAL, FOUND, LIVE value, not invented: SHANKPIT's own native client (apps/lobby/src/main.c)
// already has a real, working "Matrix floor" grid + a magenta-glow footstep trail effect
// (draw_grid/update_and_draw_trails) at a fixed real cell size, `#define GRID_SIZE 50.0f` --
// founder, direct: "shankpit has it built in that the grid lights up when you touch it... it
// would be great if we integrated with that." This constant matches that exactly (not 1.0) so a
// level authored here lines up, square-for-square, with the real in-game glowing-trail floor
// instead of introducing a second, mismatched grid convention. Shared, by convention (not by
// import -- this is a Go/C/TS boundary with no shared schema to generate from), with the
// identical constant in ShankpitLevelEditor.tsx and packages/world/level_boxes.h -- kept in sync
// by hand.
const GridCellSize = 50.0

// Level is one row of the shankpit_levels table.
type Level struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Depth  float64 `json:"depth"`
	// GroundPlaneEnabled/GroundPlaneSquares (founder real-time: "i want there to be a plane by
	// default that the player collides with - the checkerboard in the level editor - that
	// should constitute the plane for that level... configurable in terms of size... turn on
	// able and off able per level") -- a real, first-class, per-level, persisted property, not a
	// Wall and not a hardcoded engine default. GroundPlaneSquares is a real square COUNT (see
	// GridCellSize's own doc comment for why), not a raw length.
	GroundPlaneEnabled bool          `json:"ground_plane_enabled"`
	GroundPlaneSquares int           `json:"ground_plane_squares"`
	Walls              []Wall        `json:"walls"`
	Objects            []LevelObject `json:"objects"`
	// Spawners (S459-58) -- real, author-placed spawn points, team-tagged with FFA fallback. See
	// Spawner's own doc comment for the real team convention.
	Spawners []Spawner `json:"spawners"`
	// Doors -- real, author-attached scriptable doors. See Door's own doc comment.
	Doors []Door `json:"doors"`
	// NavNodes -- real, author-placed waypoint/cover nodes. See NavNode's own doc comment.
	NavNodes []NavNode `json:"nav_nodes"`
	// Characters -- real, author-placed story_ai NPCs. See Character's own doc comment.
	Characters []Character `json:"characters"`
	// LevelExits -- real, author-placed exit trigger volumes. See LevelExit's own doc comment.
	LevelExits []LevelExit `json:"level_exits"`
	// NextLevelID (S473, STORY_LEVEL_SEQUENCING_NORTHSTAR.md Phase 1) -- nullable: no value is a
	// real, honest "end of the story" or "not part of a chain" state, not an error. v0 is a real
	// CHAIN (one next level per level), not a general branching graph -- see the NORTHSTAR doc for
	// why. Validated as "must reference a real, existing level" at save time (validateNextLevelID,
	// a DB-backed check, not a pure structural one like the other validators in this file).
	NextLevelID *int64 `json:"next_level_id"`
	// IsStoryStart (S473) -- exactly one level may hold this at a time, same real
	// "exactly one, enforced in Go, no SQL partial-unique-index" shape IsDefaultQueue already
	// established below. MODE_STORY's own match init discovers this through the ordinary,
	// already-public level LIST endpoint, same discovery mechanism IsDefaultQueue already uses --
	// see SetStoryStartLevel's own doc comment for the real enforcement.
	IsStoryStart bool `json:"is_story_start"`
	// IsDefaultQueue (S459-41, founder real-time: "need to add an option to shankpit levels to
	// set a level as default for queue") -- exactly one level may be the real, global QUEUE
	// default at a time, same real shape shankpit_sprays.IsDefault already established. The
	// native SHANKPIT game server/client both discover this through the ordinary, already-public
	// level LIST endpoint (no name-lookup, no new endpoint) -- see SetDefaultQueueLevel's own doc
	// comment for the real enforcement.
	IsDefaultQueue bool   `json:"is_default_queue"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// ExportDoc is the real, native-loader-facing shape (SHANKPIT's own real physics.h `Box`/
// `phys_set_custom_level` contract -- see packages/world/level_boxes.h's own doc comment for the
// real, found-live correction on which native format this actually targets) -- narrower than
// Level (no id/timestamps), matching internal/brawlpit's own ExportDoc precedent exactly.
type ExportDoc struct {
	Version            int     `json:"version"`
	Name               string  `json:"name"`
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	Depth              float64 `json:"depth"`
	GroundPlaneEnabled bool    `json:"ground_plane_enabled"`
	GroundPlaneSquares int     `json:"ground_plane_squares"`
	Walls              []Wall  `json:"walls"`
	// Spawners (S459-58) -- real, author-placed spawn points, exported unchanged (no per-object
	// flattening/transform is applied, matching the real, honest, not-yet-built limit already
	// documented on flattenObjects: only the root level's own real fields reach the native
	// client). See Spawner's own doc comment for the real team convention.
	Spawners []Spawner `json:"spawners,omitempty"`
	// Doors -- the real, native-loader-facing shape SHANKPIT/packages/world/level_boxes.h's own
	// door parser expects exactly ({box_index, script_url}, script_path deliberately never set
	// here -- every door authored through NOCK goes via the real, downloadable script repository,
	// script_path stays a local-dev-only fallback on the native side). See doorsForExport.
	Doors []DoorExport `json:"doors,omitempty"`
	// NavNodes -- the real, native-loader-facing shape SHANKPIT/packages/world/level_boxes.h's
	// own nav-node parser expects exactly (x/y/z, is_cover, cover_dir_x/z, neighbors as 0-based
	// array POSITIONS -- not any node's own persisted id). See navNodesForExport.
	NavNodes []NavNodeExport `json:"nav_nodes,omitempty"`
	// Characters -- the real, native-loader-facing shape SHANKPIT's own new LevelCharacter parser
	// expects exactly (role/x/y/z). Only meaningful when the exported level loads in MODE_STORY/
	// MODE_STORY_CAVE on the dedicated server -- server_apply_custom_level's own real game-mode
	// gate decides whether these actually spawn, not this field's mere presence.
	Characters []CharacterExport `json:"characters,omitempty"`
	// LevelExits -- the real, native-loader-facing shape SHANKPIT's own new LevelExit parser
	// expects exactly (x/y/z/radius). See levelExitsForExport.
	LevelExits []LevelExitExport `json:"level_exits,omitempty"`
	// NextLevelID (S473) -- carried through unchanged from the source Level (no cross-reference to
	// resolve -- unlike Door/NavNode, a level id IS the real, final identifier the native loader
	// needs, not an author-facing id requiring position translation). Omitted entirely (not a
	// zeroed 0) when nil, so the native loader's own "0 = none" sentinel (see level_boxes.h's own
	// doc comment) is never confused with a real level id -- omitempty on a *int64 that's nil
	// serializes to nothing, matching this file's own "absent key is a real, honest empty state"
	// convention every other omitempty field here already uses.
	NextLevelID *int64 `json:"next_level_id,omitempty"`
	// Materials (S459-16) -- every real, currently-defined material's own shading parameters,
	// embedded directly so the native loader gets everything it needs from ONE fetch (no second
	// round-trip to a separate materials endpoint just to render a level). See
	// materialsForExport's own doc comment for exactly what "TextureURL" means here.
	Materials []MaterialExport `json:"materials,omitempty"`
}

// MaterialExport is the real, native-loader-facing shape of a material -- narrower than Material
// (no id/timestamps), matching every other ExportDoc field's own "narrower than the DB row"
// convention. TextureURL is a real, absolute URL into NOCK's own texture image endpoint when the
// material has a texture_id set, but see this package's own doc comment: the native client does
// not fetch/decode it yet (no image codec) -- it is real, present data for the day that lands,
// not dead weight kept "just in case."
// DoorExport is the real, native-loader-facing shape of one door -- narrower than Door (no id),
// matching every other ExportDoc field's own "narrower than the DB row" convention. BoxIndex is
// this door's wall's own 0-based position in the exported walls[] array (see doorsForExport's own
// doc comment for exactly how that position is derived), NOT the door's own WallID or the wall's
// own persisted Wall.ID -- level_boxes.h's door parser indexes directly into the boxes it just
// parsed from walls[], in order.
type DoorExport struct {
	BoxIndex  int    `json:"box_index"`
	ScriptURL string `json:"script_url"`
}

// NavNodeExport is the real, native-loader-facing shape of one nav node -- narrower than NavNode
// (no id), matching every other ExportDoc field's own "narrower than the DB row" convention.
// Neighbors are 0-based POSITIONS in the exported nav_nodes[] array (see navNodesForExport's own
// doc comment), NOT any node's own persisted NeighborIDs -- level_boxes.h's nav-node parser
// indexes directly into the nodes it just parsed, in order, same real convention DoorExport's
// own BoxIndex already established for walls.
type NavNodeExport struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Z         float64 `json:"z"`
	IsCover   bool    `json:"is_cover"`
	CoverDirX float64 `json:"cover_dir_x"`
	CoverDirZ float64 `json:"cover_dir_z"`
	Neighbors []int   `json:"neighbors"`
}

// CharacterExport is the real, native-loader-facing shape of one character -- narrower than
// Character (no id), matching every other ExportDoc field's own "narrower than the DB row"
// convention. Unlike DoorExport/NavNodeExport, no position/id resolution happens here -- a
// character has no cross-reference to translate, it's exported as authored.
type CharacterExport struct {
	Role int     `json:"role"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
}

// LevelExitExport is the real, native-loader-facing shape of one exit trigger volume -- narrower
// than LevelExit (no id), matching CharacterExport's own "no cross-reference, plain field-for-
// field map" precedent exactly.
type LevelExitExport struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
}

type MaterialExport struct {
	Name       string  `json:"name"`
	ShaderName string  `json:"shader_name"`
	Specular   float64 `json:"specular"`
	Shininess  float64 `json:"shininess"`
	TextureURL string  `json:"texture_url,omitempty"`
}

var validLevelName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]{0,63}$`)

// ValidateName rejects an empty or over-long/oddly-charactered level name -- same real bound
// internal/brawlpit.ValidateName already uses (a level name is a real, display-facing title, not
// a filesystem path component).
func ValidateName(name string) error {
	if !validLevelName.MatchString(name) {
		return fmt.Errorf("shankpit: invalid level name %q (must match %s)", name, validLevelName.String())
	}
	return nil
}

// MaxWalls mirrors SHANKPIT/packages/map/map.h's own real `Wall walls[100]` fixed-array capacity
// -- kept in exact sync so a level saved here can never exceed what the native GameMap can
// actually hold, same real reason internal/brawlpit.MaxPlatforms exists.
const MaxWalls = 100

// MinGroundPlaneSquares/MaxGroundPlaneSquares bound the real, editable square-count field --
// a real, sane range (1 = a single 1x1 square, 2000 = a 2000x2000-unit plane, comfortably larger
// than any real level authored here yet) rather than an unbounded integer.
const MinGroundPlaneSquares = 1
const MaxGroundPlaneSquares = 2000

func validateGroundPlane(squares int) error {
	if squares < MinGroundPlaneSquares || squares > MaxGroundPlaneSquares {
		return fmt.Errorf("shankpit: ground_plane_squares must be in [%d, %d], got %d", MinGroundPlaneSquares, MaxGroundPlaneSquares, squares)
	}
	return nil
}

func validateWalls(walls []Wall) error {
	if len(walls) > MaxWalls {
		return fmt.Errorf("shankpit: too many walls (%d, max %d -- SHANKPIT's own native GameMap can't hold more)", len(walls), MaxWalls)
	}
	for i, w := range walls {
		if w.SX <= 0 || w.SY <= 0 || w.SZ <= 0 {
			return fmt.Errorf("shankpit: wall %d has non-positive size (%g x %g x %g)", i, w.SX, w.SY, w.SZ)
		}
		for _, c := range []struct {
			name string
			v    float64
		}{{"r", w.R}, {"g", w.G}, {"b", w.B}} {
			if c.v < 0 || c.v > 1 {
				return fmt.Errorf("shankpit: wall %d has out-of-range %s %g (must be in [0, 1], matching this codebase's own real OpenGL color convention)", i, c.name, c.v)
			}
		}
	}
	return nil
}

// LevelStore is the real SQLite-backed CRUD layer.
type LevelStore struct {
	DB *sql.DB
	// Materials resolves each exported wall's own Material name into real shading parameters
	// (S459-16) -- nil is a real, valid, "materials feature not wired up" state (e.g. an older
	// caller/test that doesn't need it): Export falls back to embedding no Materials array at all
	// rather than panicking, and the native client's own DefaultMaterialName fallback covers it.
	Materials *MaterialStore
}

// CreateLevel inserts a new, real, independent level row. A level may start with zero walls (v0's
// own real "create a level, then add a cube" flow, S459-01 before S459-04) -- unlike BRAWLPIT's
// own CreateLevel, an empty wall list is not an error here, since there is no equivalent real
// native-loader requirement forcing "at least one platform" the way BRAWLPIT's 2D format does.
func (s *LevelStore) CreateLevel(ctx context.Context, name string, width, height, depth float64, groundPlaneEnabled bool, groundPlaneSquares int, walls []Wall, objects []LevelObject, spawners []Spawner, doors []Door, navNodes []NavNode, characters []Character, levelExits []LevelExit, nextLevelID *int64) (*Level, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := validateGroundPlane(groundPlaneSquares); err != nil {
		return nil, err
	}
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if err := validateObjects(0, objects); err != nil {
		return nil, err
	}
	if err := validateSpawners(spawners); err != nil {
		return nil, err
	}
	if err := validateDoors(doors, walls); err != nil {
		return nil, err
	}
	if err := validateNavNodes(navNodes); err != nil {
		return nil, err
	}
	if err := validateCharacters(characters); err != nil {
		return nil, err
	}
	if err := validateLevelExits(levelExits); err != nil {
		return nil, err
	}
	if err := s.validateNextLevelID(ctx, 0, nextLevelID); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	if objects == nil {
		objects = []LevelObject{}
	}
	if spawners == nil {
		spawners = []Spawner{}
	}
	if doors == nil {
		doors = []Door{}
	}
	if navNodes == nil {
		navNodes = []NavNode{}
	}
	if characters == nil {
		characters = []Character{}
	}
	if levelExits == nil {
		levelExits = []LevelExit{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal walls: %w", err)
	}
	objectsJSON, err := json.Marshal(objects)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal objects: %w", err)
	}
	spawnersJSON, err := json.Marshal(spawners)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal spawners: %w", err)
	}
	doorsJSON, err := json.Marshal(doors)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal doors: %w", err)
	}
	navNodesJSON, err := json.Marshal(navNodes)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal nav nodes: %w", err)
	}
	charactersJSON, err := json.Marshal(characters)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal characters: %w", err)
	}
	levelExitsJSON, err := json.Marshal(levelExits)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal level exits: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO shankpit_levels (name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json, objects_json, spawners_json, doors_json, nav_nodes_json, characters_json, level_exits_json, next_level_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, width, height, depth, groundPlaneEnabled, groundPlaneSquares, string(wallsJSON), string(objectsJSON), string(spawnersJSON), string(doorsJSON), string(navNodesJSON), string(charactersJSON), string(levelExitsJSON), nextLevelID)
	if err != nil {
		return nil, fmt.Errorf("shankpit: create level: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("shankpit: create level: %w", err)
	}
	return s.GetLevel(ctx, id)
}

// validateNextLevelID checks that nextLevelID, if set, references a real, existing level -- a DB-
// backed check (unlike this file's other, purely structural validators) since "does this id
// exist" can only be answered against the store itself. selfID (0 for a not-yet-created level, via
// CreateLevel) rejects a level naming itself as its own next level -- a real, trivial one-node
// cycle, same class of guard validateObjects already applies to LevelObject.RefLevelID.
func (s *LevelStore) validateNextLevelID(ctx context.Context, selfID int64, nextLevelID *int64) error {
	if nextLevelID == nil {
		return nil
	}
	if *nextLevelID == selfID {
		return fmt.Errorf("shankpit: level cannot reference itself as its own next_level_id")
	}
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM shankpit_levels WHERE id = ?`, *nextLevelID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("shankpit: next_level_id %d does not reference a real level", *nextLevelID)
		}
		return fmt.Errorf("shankpit: validate next_level_id: %w", err)
	}
	return nil
}

// GetLevel returns the full row, including its real wall list.
func (s *LevelStore) GetLevel(ctx context.Context, id int64) (*Level, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json, objects_json, spawners_json, doors_json, nav_nodes_json, characters_json, level_exits_json, next_level_id, is_story_start, is_default_queue, created_at, updated_at
		 FROM shankpit_levels WHERE id = ?`, id)
	return scanLevel(row)
}

func scanLevel(row *sql.Row) (*Level, error) {
	var l Level
	var wallsJSON, objectsJSON, spawnersJSON, doorsJSON, navNodesJSON, charactersJSON, levelExitsJSON string
	var nextLevelID sql.NullInt64
	if err := row.Scan(&l.ID, &l.Name, &l.Width, &l.Height, &l.Depth, &l.GroundPlaneEnabled, &l.GroundPlaneSquares, &wallsJSON, &objectsJSON, &spawnersJSON, &doorsJSON, &navNodesJSON, &charactersJSON, &levelExitsJSON, &nextLevelID, &l.IsStoryStart, &l.IsDefaultQueue, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("shankpit: level not found")
		}
		return nil, fmt.Errorf("shankpit: get level: %w", err)
	}
	if nextLevelID.Valid {
		l.NextLevelID = &nextLevelID.Int64
	}
	if err := json.Unmarshal([]byte(wallsJSON), &l.Walls); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored walls: %w", err)
	}
	if err := json.Unmarshal([]byte(objectsJSON), &l.Objects); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored objects: %w", err)
	}
	if err := json.Unmarshal([]byte(spawnersJSON), &l.Spawners); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored spawners: %w", err)
	}
	if err := json.Unmarshal([]byte(navNodesJSON), &l.NavNodes); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored nav nodes: %w", err)
	}
	if err := json.Unmarshal([]byte(charactersJSON), &l.Characters); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored characters: %w", err)
	}
	if err := json.Unmarshal([]byte(doorsJSON), &l.Doors); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored doors: %w", err)
	}
	if err := json.Unmarshal([]byte(levelExitsJSON), &l.LevelExits); err != nil {
		return nil, fmt.Errorf("shankpit: decode stored level exits: %w", err)
	}
	return &l, nil
}

// LevelSummary is the real, lightweight shape a level LIST returns -- everything about a Level
// except its own full wall array, matching internal/brawlpit.LevelSummary's own precedent.
type LevelSummary struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	Depth              float64 `json:"depth"`
	GroundPlaneEnabled bool    `json:"ground_plane_enabled"`
	GroundPlaneSquares int     `json:"ground_plane_squares"`
	WallCount          int     `json:"wall_count"`
	ObjectCount        int     `json:"object_count"`
	SpawnerCount       int     `json:"spawner_count"`
	DoorCount          int     `json:"door_count"`
	NavNodeCount       int     `json:"nav_node_count"`
	CharacterCount     int     `json:"character_count"`
	LevelExitCount     int     `json:"level_exit_count"`
	// IsStoryStart (S473) -- same real discovery-via-LIST shape IsDefaultQueue already
	// established. See Level's own doc comment for the full enforcement story.
	IsStoryStart   bool   `json:"is_story_start"`
	IsDefaultQueue bool   `json:"is_default_queue"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// ListLevels returns every level as a real, lightweight summary, newest first -- the real
// level-select registry primitive this section exists to build.
func (s *LevelStore) ListLevels(ctx context.Context) ([]LevelSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, width, height, depth, ground_plane_enabled, ground_plane_squares, walls_json, objects_json, spawners_json, doors_json, nav_nodes_json, characters_json, level_exits_json, is_story_start, is_default_queue, created_at, updated_at
		 FROM shankpit_levels ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list levels: %w", err)
	}
	defer rows.Close()

	out := []LevelSummary{}
	for rows.Next() {
		var sum LevelSummary
		var wallsJSON, objectsJSON, spawnersJSON, doorsJSON, navNodesJSON, charactersJSON, levelExitsJSON string
		if err := rows.Scan(&sum.ID, &sum.Name, &sum.Width, &sum.Height, &sum.Depth, &sum.GroundPlaneEnabled, &sum.GroundPlaneSquares, &wallsJSON, &objectsJSON, &spawnersJSON, &doorsJSON, &navNodesJSON, &charactersJSON, &levelExitsJSON, &sum.IsStoryStart, &sum.IsDefaultQueue, &sum.CreatedAt, &sum.UpdatedAt); err != nil {
			return nil, fmt.Errorf("shankpit: list levels: %w", err)
		}
		var walls []Wall
		if err := json.Unmarshal([]byte(wallsJSON), &walls); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored walls: %w", err)
		}
		var objects []LevelObject
		if err := json.Unmarshal([]byte(objectsJSON), &objects); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored objects: %w", err)
		}
		var spawners []Spawner
		if err := json.Unmarshal([]byte(spawnersJSON), &spawners); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored spawners: %w", err)
		}
		var doors []Door
		if err := json.Unmarshal([]byte(doorsJSON), &doors); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored doors: %w", err)
		}
		var navNodes []NavNode
		if err := json.Unmarshal([]byte(navNodesJSON), &navNodes); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored nav nodes: %w", err)
		}
		var characters []Character
		if err := json.Unmarshal([]byte(charactersJSON), &characters); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored characters: %w", err)
		}
		var levelExits []LevelExit
		if err := json.Unmarshal([]byte(levelExitsJSON), &levelExits); err != nil {
			return nil, fmt.Errorf("shankpit: decode stored level exits: %w", err)
		}
		sum.WallCount = len(walls)
		sum.ObjectCount = len(objects)
		sum.SpawnerCount = len(spawners)
		sum.DoorCount = len(doors)
		sum.NavNodeCount = len(navNodes)
		sum.CharacterCount = len(characters)
		sum.LevelExitCount = len(levelExits)
		out = append(out, sum)
	}
	return out, rows.Err()
}

// UpdateLevel replaces a level's own real editable fields in place -- the web editor's own real
// "save" action (dimensions + ground plane + the full wall layout can all change together in one
// save). This is the one endpoint both S459-04 "create a cube" (append a default wall to the
// array, save) and S459-05 "face-drag editing" (adjust an existing wall's center/size, save) both
// go through -- matching BRAWLPIT's own LevelStore precedent exactly: there is no separate "add
// one platform" endpoint there either, the whole array is replaced together.
func (s *LevelStore) UpdateLevel(ctx context.Context, id int64, width, height, depth float64, groundPlaneEnabled bool, groundPlaneSquares int, walls []Wall, objects []LevelObject, spawners []Spawner, doors []Door, navNodes []NavNode, characters []Character, levelExits []LevelExit, nextLevelID *int64) (*Level, error) {
	if err := validateGroundPlane(groundPlaneSquares); err != nil {
		return nil, err
	}
	if err := validateWalls(walls); err != nil {
		return nil, err
	}
	if err := validateObjects(id, objects); err != nil {
		return nil, err
	}
	if err := validateSpawners(spawners); err != nil {
		return nil, err
	}
	if err := validateDoors(doors, walls); err != nil {
		return nil, err
	}
	if err := validateNavNodes(navNodes); err != nil {
		return nil, err
	}
	if err := validateCharacters(characters); err != nil {
		return nil, err
	}
	if err := validateLevelExits(levelExits); err != nil {
		return nil, err
	}
	if err := s.validateNextLevelID(ctx, id, nextLevelID); err != nil {
		return nil, err
	}
	if walls == nil {
		walls = []Wall{}
	}
	if objects == nil {
		objects = []LevelObject{}
	}
	if spawners == nil {
		spawners = []Spawner{}
	}
	if doors == nil {
		doors = []Door{}
	}
	if navNodes == nil {
		navNodes = []NavNode{}
	}
	if characters == nil {
		characters = []Character{}
	}
	if levelExits == nil {
		levelExits = []LevelExit{}
	}
	wallsJSON, err := json.Marshal(walls)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal walls: %w", err)
	}
	objectsJSON, err := json.Marshal(objects)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal objects: %w", err)
	}
	spawnersJSON, err := json.Marshal(spawners)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal spawners: %w", err)
	}
	doorsJSON, err := json.Marshal(doors)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal doors: %w", err)
	}
	navNodesJSON, err := json.Marshal(navNodes)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal nav nodes: %w", err)
	}
	charactersJSON, err := json.Marshal(characters)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal characters: %w", err)
	}
	levelExitsJSON, err := json.Marshal(levelExits)
	if err != nil {
		return nil, fmt.Errorf("shankpit: marshal level exits: %w", err)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_levels SET width = ?, height = ?, depth = ?, ground_plane_enabled = ?, ground_plane_squares = ?, walls_json = ?, objects_json = ?, spawners_json = ?, doors_json = ?, nav_nodes_json = ?, characters_json = ?, level_exits_json = ?, next_level_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		width, height, depth, groundPlaneEnabled, groundPlaneSquares, string(wallsJSON), string(objectsJSON), string(spawnersJSON), string(doorsJSON), string(navNodesJSON), string(charactersJSON), string(levelExitsJSON), nextLevelID, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: update level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// RenameLevel updates a level's own name in place.
func (s *LevelStore) RenameLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE shankpit_levels SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: rename level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	return s.GetLevel(ctx, id)
}

// SetDefaultQueueLevel marks id as the one real, global QUEUE default level, clearing every other
// row's own flag inside one transaction -- founder: "need to add an option to shankpit levels to
// set a level as default for queue." Same real "exactly one default, enforced in Go, no SQL
// partial-unique-index" pattern SetDefaultSpray already established.
func (s *LevelStore) SetDefaultQueueLevel(ctx context.Context, id int64) (*Level, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set default queue level: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE shankpit_levels SET is_default_queue = 0`); err != nil {
		return nil, fmt.Errorf("shankpit: set default queue level: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE shankpit_levels SET is_default_queue = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set default queue level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("shankpit: set default queue level: %w", err)
	}
	return s.GetLevel(ctx, id)
}

// SetStoryStartLevel marks id as the one real, global MODE_STORY entry level, clearing every
// other row's own flag inside one transaction -- same exact "exactly one, enforced in Go" shape
// SetDefaultQueueLevel already established above (S473, STORY_LEVEL_SEQUENCING_NORTHSTAR.md).
func (s *LevelStore) SetStoryStartLevel(ctx context.Context, id int64) (*Level, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set story start level: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE shankpit_levels SET is_story_start = 0`); err != nil {
		return nil, fmt.Errorf("shankpit: set story start level: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE shankpit_levels SET is_story_start = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("shankpit: set story start level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("shankpit: level %d not found", id)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("shankpit: set story start level: %w", err)
	}
	return s.GetLevel(ctx, id)
}

// CloneLevel makes a real, full, independent copy of an existing level under a new name -- same
// real shape as internal/brawlpit.LevelStore's own CloneLevel. Deliberately does NOT copy
// IsStoryStart/IsDefaultQueue (a clone is never automatically the new default/story-start, same
// real "exactly one, explicit action required" reasoning both those flags' own setters already
// enforce) -- NextLevelID and LevelExits DO copy, matching every other real editable field here.
func (s *LevelStore) CloneLevel(ctx context.Context, id int64, newName string) (*Level, error) {
	src, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateLevel(ctx, newName, src.Width, src.Height, src.Depth, src.GroundPlaneEnabled, src.GroundPlaneSquares, src.Walls, src.Objects, src.Spawners, src.Doors, src.NavNodes, src.Characters, src.LevelExits, src.NextLevelID)
}

// DeleteLevel permanently removes a level row.
func (s *LevelStore) DeleteLevel(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shankpit_levels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("shankpit: delete level: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("shankpit: level %d not found", id)
	}
	return nil
}

// rotateY90 rotates a wall's own (x,z) center and (sx,sz) extents by a 0/90/180/270-degree
// Y-axis turn (founder: "snap rotate 90 degree turns is good for now") -- exact for an
// axis-aligned box at these angles (no trig needed): a 90/270 turn swaps X and Z (negating one),
// and swaps the sx/sz extents along with them since an axis-aligned box's own footprint rotates
// with it. y and sy are never touched -- this is Y-axis-only rotation, matching NOCK's own
// established "constrain Y" convention for objects elsewhere in this system.
func rotateY90(x, z, sx, sz float64, rotY int) (rx, rz, rsx, rsz float64) {
	switch rotY {
	case 90:
		return -z, x, sz, sx
	case 180:
		return -x, -z, sx, sz
	case 270:
		return z, -x, sz, sx
	default:
		return x, z, sx, sz
	}
}

// flattenObjects recursively resolves a level's own object children (each one a reference to
// another level, founder: "a map is a composition of levels ... its a bit fractal we can let it
// go further down or up") into a flat wall list the native SHANKPIT client already understands
// unchanged -- see this function's own call site in Export for why that matters. Each object's
// own transform (position + 90-degree Y rotation) is applied to every wall the referenced level
// contributes, INCLUDING that level's own further-nested objects (recursion), so an object that
// is itself a composed map works exactly the same as one that's a single flat level -- the real
// "smart document in photoshop where you have like a photoshop doc in a photoshop doc" self-
// similarity the founder asked for directly.
//
// visited is a real cycle guard (a level that directly or indirectly references itself would
// otherwise recurse forever) and depth is a real, finite backstop on top of it -- both return a
// real, honest error rather than hanging or silently truncating.
//
// REAL, HONEST, NOT-YET-BUILT: a referenced level's own ground plane (GroundPlaneEnabled/
// GroundPlaneSquares) is never composed here, regardless of the object's own PlaneVisible/
// PlaneSolid fields -- those are stored (see LevelObject's own doc comment) but not yet acted on.
// physics.h's own SCENE_CUSTOM_LEVEL only supports ONE ground plane for the whole scene today
// (a single enabled/squares pair, not one per placed object at its own offset); composing several
// independently-positioned, independently-toggleable planes is real, scoped future work, not
// something this pass silently fakes. Only the ROOT level's own plane (set on the top-level
// Export call) ever reaches the native client -- every nested object contributes geometry only,
// matching "DEFAULTS TO OFF" for the nested case.
func (s *LevelStore) flattenObjects(ctx context.Context, objects []LevelObject, originX, originY, originZ float64, originRotY int, visited map[int64]bool, depth int) ([]Wall, error) {
	if depth > MaxLevelObjectDepth {
		return nil, fmt.Errorf("shankpit: object nesting too deep (max %d) -- possible runaway composition", MaxLevelObjectDepth)
	}
	var out []Wall
	for _, obj := range objects {
		if visited[obj.RefLevelID] {
			return nil, fmt.Errorf("shankpit: level object cycle detected involving level %d", obj.RefLevelID)
		}
		child, err := s.GetLevel(ctx, obj.RefLevelID)
		if err != nil {
			return nil, fmt.Errorf("shankpit: object references level %d: %w", obj.RefLevelID, err)
		}
		childVisited := make(map[int64]bool, len(visited)+1)
		for k := range visited {
			childVisited[k] = true
		}
		childVisited[obj.RefLevelID] = true

		// Compose the object's own local transform (its position/rotation within ITS parent)
		// with the parent's own already-accumulated world transform, so nesting several levels
		// deep places everything correctly, not just one level down.
		ox, oz, _, _ := rotateY90(obj.X, obj.Z, 0, 0, originRotY)
		worldX := originX + ox
		worldZ := originZ + oz
		worldY := originY + obj.Y
		worldRotY := (originRotY + obj.RotY) % 360

		for _, w := range child.Walls {
			rx, rz, rsx, rsz := rotateY90(w.X, w.Z, w.SX, w.SZ, worldRotY)
			out = append(out, Wall{
				ID: 0, X: worldX + rx, Y: worldY + w.Y, Z: worldZ + rz,
				SX: rsx, SY: w.SY, SZ: rsz,
				R: w.R, G: w.G, B: w.B, Friction: w.Friction,
			})
		}
		nested, err := s.flattenObjects(ctx, child.Objects, worldX, worldY, worldZ, worldRotY, childVisited, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// Export returns the real, native-loader-facing document for a level -- the real end-to-end
// target the native SHANKPIT client actually consumes (apps/lobby + apps/server's own
// packages/world/level_boxes.h loader, S459-07/08/11). A level's own object children (S459-15)
// are recursively flattened into ONE flat wall list right here, server-side, in Go -- the native
// client itself needs zero changes to load a composed "map": it always just sees one flat
// walls[] + one ground-plane pair, the exact same shape as a single, object-free level, matching
// the founder's own explicit "we want them to load the totally same way."
func (s *LevelStore) Export(ctx context.Context, id int64) (*ExportDoc, error) {
	lvl, err := s.GetLevel(ctx, id)
	if err != nil {
		return nil, err
	}
	walls := append([]Wall{}, lvl.Walls...)
	if len(lvl.Objects) > 0 {
		flattened, err := s.flattenObjects(ctx, lvl.Objects, 0, 0, 0, 0, map[int64]bool{id: true}, 1)
		if err != nil {
			return nil, err
		}
		walls = append(walls, flattened...)
	}
	if len(walls) > MaxWalls {
		return nil, fmt.Errorf("shankpit: composed level %d has too many boxes after flattening objects (%d, max %d)", id, len(walls), MaxWalls)
	}
	for i := range walls {
		walls[i].ID = i + 1 // renumbered sequentially -- see flattenObjects' own doc comment on why wall IDs are safe to reassign
	}
	materials, err := s.materialsForExport(ctx)
	if err != nil {
		return nil, err
	}
	return &ExportDoc{
		Version: 1, Name: lvl.Name, Width: lvl.Width, Height: lvl.Height, Depth: lvl.Depth,
		GroundPlaneEnabled: lvl.GroundPlaneEnabled, GroundPlaneSquares: lvl.GroundPlaneSquares,
		Walls: walls, Spawners: lvl.Spawners, Doors: doorsForExport(lvl.Doors, lvl.Walls),
		NavNodes: navNodesForExport(lvl.NavNodes), Characters: charactersForExport(lvl.Characters), Materials: materials,
		LevelExits: levelExitsForExport(lvl.LevelExits), NextLevelID: lvl.NextLevelID,
	}, nil
}

// NockDoorScriptDownloadBaseURL is the real, absolute base every door's own script_url is built
// against -- hardcoded to match SHANKPIT's own native LEVEL_REGISTRY_BASE_URL convention
// (packages/world/level_boxes.h: "https://okemily.com/api/v1/shankpit-levels") rather than a
// runtime-configurable value: story_doors.h's own level_boxes_fetch_url shells out to real curl,
// which needs a real, absolute URL -- a relative path here would silently fail every door fetch.
const NockDoorScriptDownloadBaseURL = "https://okemily.com/api/v1/nock-door-scripts"

// doorsForExport resolves each door's own WallID into its real 0-based position within rootWalls
// -- NOT the door's own WallID and NOT the wall's own persisted Wall.ID, but its position in
// export order, since level_boxes.h's door parser indexes directly into the boxes it just parsed
// from walls[]. rootWalls must be lvl.Walls (pre-flatten, pre-renumber) -- root walls always
// occupy the first len(rootWalls) slots of Export's own final, renumbered walls slice, in the
// exact same relative order, so a position found here is already correct for the final array.
// A door whose wall_id no longer resolves (e.g. the wall was deleted after the door was attached
// -- validateDoors only catches this at save time, not for data saved before this pass existed)
// is silently skipped here rather than erroring Export, same real "an author's own stale
// reference mustn't break the whole level" discipline flattenObjects' own cycle/depth guards
// apply to a different real failure mode. Likewise, a since-deleted script_id is never checked
// here at all -- SHANKPIT's own story_doors.h already degrades gracefully (a real, visible
// stderr line, that one door just never loads) when a script_url 404s, so re-deriving the same
// check here server-side would be real, duplicate work for no additional safety.
func doorsForExport(doors []Door, rootWalls []Wall) []DoorExport {
	pos := make(map[int]int, len(rootWalls))
	for i, w := range rootWalls {
		pos[w.ID] = i
	}
	out := make([]DoorExport, 0, len(doors))
	for _, d := range doors {
		idx, ok := pos[d.WallID]
		if !ok {
			continue
		}
		out = append(out, DoorExport{
			BoxIndex:  idx,
			ScriptURL: fmt.Sprintf("%s/%d/download", NockDoorScriptDownloadBaseURL, d.ScriptID),
		})
	}
	return out
}

// navNodesForExport resolves each node's own NeighborIDs into real 0-based positions within
// nodes -- NOT any node's own persisted id, but its position in export order, matching
// doorsForExport's own established BoxIndex convention exactly. Unlike doors (which resolve
// against rootWalls because a door's own position depends on flattening), nav nodes are never
// flattened -- they're exported directly from the root level's own authored NavNodes, no object-
// composition concept applies to them (a real, deliberate scope limit named in level_boxes.h's
// own LevelNavNode doc comment: nodes are a root-level-only concern, matching Door's own "root
// walls only" limit). A neighbor_id that no longer resolves (e.g. deleted after being linked,
// for data saved before validateNavNodes existed) is silently dropped from that node's own
// neighbor list rather than erroring Export, same real "a stale reference mustn't break the
// level" discipline doorsForExport already established.
func navNodesForExport(nodes []NavNode) []NavNodeExport {
	pos := make(map[int]int, len(nodes))
	for i, n := range nodes {
		pos[n.ID] = i
	}
	out := make([]NavNodeExport, 0, len(nodes))
	for _, n := range nodes {
		neighbors := make([]int, 0, len(n.NeighborIDs))
		for _, nid := range n.NeighborIDs {
			if idx, ok := pos[nid]; ok {
				neighbors = append(neighbors, idx)
			}
		}
		out = append(out, NavNodeExport{
			X: n.X, Y: n.Y, Z: n.Z, IsCover: n.IsCover,
			CoverDirX: n.CoverDirX, CoverDirZ: n.CoverDirZ, Neighbors: neighbors,
		})
	}
	return out
}

// charactersForExport is a plain field-for-field map -- unlike doorsForExport/navNodesForExport,
// a character has no cross-reference to resolve (no wall_id, no neighbor_ids), so there's no
// position-translation step here at all.
func charactersForExport(characters []Character) []CharacterExport {
	out := make([]CharacterExport, 0, len(characters))
	for _, c := range characters {
		out = append(out, CharacterExport{Role: c.Role, X: c.X, Y: c.Y, Z: c.Z})
	}
	return out
}

// levelExitsForExport is a plain field-for-field map -- same real "no cross-reference to resolve"
// simplicity charactersForExport already established.
func levelExitsForExport(exits []LevelExit) []LevelExitExport {
	out := make([]LevelExitExport, 0, len(exits))
	for _, e := range exits {
		out = append(out, LevelExitExport{X: e.X, Y: e.Y, Z: e.Z, Radius: e.Radius})
	}
	return out
}

// materialsForExport embeds every real, currently-defined material (S459-16) directly into the
// export document -- every material, not just the ones this particular level's own walls
// reference, since the real set stays small (a handful, not hundreds) and this way the native
// client always has the FULL real registry from one fetch, matching the founder's own "registries
// for everything" direction, without needing a second round-trip to a separate materials endpoint
// (also real and live, see shankpit_materials.go's own public handler, for a caller that wants
// just the registry on its own). s.Materials == nil (an older test/caller that never wired a
// MaterialStore in) is a real, valid state -- returns an empty slice, not an error; the native
// loader's own DefaultMaterialName fallback covers a level exported without any materials array.
func (s *LevelStore) materialsForExport(ctx context.Context) ([]MaterialExport, error) {
	if s.Materials == nil {
		return nil, nil
	}
	mats, err := s.Materials.ListMaterials(ctx)
	if err != nil {
		return nil, fmt.Errorf("shankpit: list materials for export: %w", err)
	}
	out := make([]MaterialExport, 0, len(mats))
	for _, m := range mats {
		me := MaterialExport{Name: m.Name, ShaderName: m.ShaderName, Specular: m.Specular, Shininess: m.Shininess}
		if m.TextureID != nil {
			me.TextureURL = fmt.Sprintf("/admin/nock/api/textures/%d/image", *m.TextureID)
		}
		out = append(out, me)
	}
	return out, nil
}
