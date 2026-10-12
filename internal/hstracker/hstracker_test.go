package hstracker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	parena := os.Getenv("NOCK_PARENA_BIN")
	if parena == "" {
		parena = "/home/garybifrost/PARENA/parena"
	}
	if _, err := os.Stat(parena); err != nil {
		t.Skip("parena compiler not available")
	}
	t.Setenv("NOCK_PARENA_BIN", parena)
	t.Setenv("NOCK_PARENA_RUNTIME_DIR", "../nock/parena_runtime")
	return &Runner{WorkDir: filepath.Join(t.TempDir(), "trk")}
}

func entityByID(rec Record, id int) (Entity, bool) {
	for _, e := range rec.Entities {
		if e.ID == id {
			return e, true
		}
	}
	return Entity{}, false
}

func pw(s string) string { return "D 12:00:00.0000000 GameState.DebugPrintPower() - " + s }

// Regression (2026-10-11 founder report, still broken after the hero-transform/card0 fix):
// CHANGE_ENTITY -- the real Hearthstone event for a card that transforms IN PLACE (a hero change
// like Dragon Soul, Shattered's own reforge, Faceless-copy-style effects, etc.) -- had no handler
// in handle-payload at all. Every other header type (FULL_ENTITY, SHOW_ENTITY) repoints `cur` to
// the entity it describes so the indented tag= lines that immediately follow land on the right
// entity; CHANGE_ENTITY silently fell through to plain `st`, leaving `cur` stuck on whatever
// entity was last seen. The transforming entity's own new CONTROLLER/ZONE tags then got attributed
// to that STALE entity instead -- in this repro, entity 50 (genuinely mine, player=1, created
// first) is still `cur` when entity 60's CHANGE_ENTITY block arrives and re-declares ITS
// controller as player 2; before the fix that value landed on entity 50, silently flipping an
// already-correct card to "the opponent's" -- exactly the live-tracker symptom reported ("my cards
// show up under the opponent") for Dragon Soul/Aspect Essence-family cards.
func TestChangeEntityRepointsCurrentEntity(t *testing.T) {
	r := testRunner(t)
	lines := []string{
		pw("CREATE_GAME"),
		pw("    FULL_ENTITY - Creating ID=50 CardID=MINE_1"),
		pw("        tag=ZONE value=PLAY"),
		pw("        tag=CONTROLLER value=1"),
		// entity 60 was already known (e.g. revealed earlier) and now transforms in place --
		// CHANGE_ENTITY re-declares its full current tag set, same shape as SHOW_ENTITY.
		pw("    CHANGE_ENTITY - Updating Entity=60 CardID=TRANSFORMED"),
		pw("        tag=ZONE value=PLAY"),
		pw("        tag=CONTROLLER value=2"),
		pw("TAG_CHANGE Entity=GameEntity tag=STATE value=COMPLETE"),
	}
	recs, err := r.Run(context.Background(), lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	rec := recs[0]
	e50, ok := entityByID(rec, 50)
	if !ok || e50.Player != 1 {
		t.Fatalf("entity 50 (mine) must stay player=1, got %+v (ok=%v)", e50, ok)
	}
	e60, ok := entityByID(rec, 60)
	if !ok || e60.Player != 2 || e60.Card != "TRANSFORMED" {
		t.Fatalf("entity 60's transform must resolve to player=2 card=TRANSFORMED, got %+v (ok=%v)", e60, ok)
	}
}

// Regression (found live-debugging the founder's report above): on a real, current Hearthstone
// client build, CREATE_GAME -- and whole nested TRIGGER/POWER sub-blocks of a multi-stage spell
// chain (verified: Dragon Soul, Shattered's own Aspect Essence attack sequence) -- can be logged
// EXCLUSIVELY under PowerTaskList.DebugPrintPower(), with NO GameState.DebugPrintPower() copy at
// all, contradicting this package's own long-standing assumption ("PowerTaskList lines are exact
// duplicates -- verified on a real log", true for the older capture in tests/fixtures but not for
// the client build that produced the founder's actual game). Before this fix, a game whose
// CREATE_GAME arrived this way never set `started`, so --live's "game still in progress" trailer
// record never fired: the live tracker page looked permanently stuck on "waiting for the game to
// start" for the whole match, only ever producing a real record once STATE=COMPLETE (itself a
// TAG_CHANGE, independently GameState-logged) ended it -- which is also exactly why testing only
// against FINISHED games, as every prior fix attempt did, could never catch this.
func TestLiveTrailerFiresWhenCreateGameIsOnlyUnderPowerTaskList(t *testing.T) {
	r := testRunner(t)
	ptpw := func(s string) string { return "D 12:00:00.0000000 PowerTaskList.DebugPrintPower() - " + s }
	lines := []string{
		ptpw("CREATE_GAME"), // no GameState duplicate -- the real-world case
		ptpw("    FULL_ENTITY - Creating ID=50 CardID=MINE_1"),
		ptpw("        tag=ZONE value=PLAY"),
		ptpw("        tag=CONTROLLER value=1"),
		pw("TAG_CHANGE Entity=GameEntity tag=TURN value=1 "),
		// the match is still running: no STATE=COMPLETE line at all.
	}
	recs, err := r.Run(context.Background(), lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want the in-progress trailer record, got %d records", len(recs))
	}
	rec := recs[0]
	if rec.Complete != 0 {
		t.Fatalf("game must read as still in progress, got complete=%d", rec.Complete)
	}
	if _, ok := entityByID(rec, 50); !ok {
		t.Fatalf("entity created under PowerTaskList-only CREATE_GAME must still be tracked: %+v", rec.Entities)
	}
}
