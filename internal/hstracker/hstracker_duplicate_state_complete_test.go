package hstracker

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression (2026-10-12, caught live during the founder's own session, moments after the previous
// duplicate-CREATE_GAME fix had already shipped and was confirmed working for several games): exactly
// like CREATE_GAME, a real Hearthstone build logs STATE=COMPLETE twice -- once under
// GameState.DebugPrintPower(), once under PowerTaskList.DebugPrintPower(), same instant -- and the
// PowerTaskList copy is itself preceded by its own short re-statement of a few end-of-game tags
// (PLAYSTATE, NUM_TURNS_IN_HAND, ...). host.c's fold_buffer, right after the GameState copy correctly
// ends and emits the real game, folds whatever text remains in that same read into a FRESH GameState
// (:started 0) -- and before this fix, handle-line fed that trailing re-statement straight to
// handle-payload regardless of :started, so its own STATE=COMPLETE (fold-text's stop-on-complete check
// does not care whether a game ever genuinely started) printed a SECOND, near-empty phantom record (0
// entities, :me unresolved) immediately after every single real game. The live page always reads the
// LAST record (hs_live.go's `rec = &recs[len(recs)-1]`), so it showed this phantom -- full deck, nothing
// drawn, "Game found; waiting for your opening hand to identify you" -- the instant any game completed,
// exactly the message caught live on a real poll of /live/state right as a long game ended.
//
// testdata/real_duplicate_state_complete.log is a real, verbatim two-part excerpt of the founder's own
// Power.log for that exact game (same match as real_duplicate_create_game.log, a later point in it):
// the first 900 lines are CREATE_GAME through the opening mulligan and a few real turn-1 plays
// (entities resolve, player 1 draws 4 real cards), verbatim and contiguous; the last 60 lines are the
// real end of that same game -- both STATE=COMPLETE copies and the real trailing PowerTaskList
// re-statement -- also verbatim and contiguous. The ~15,000 lines of ordinary mid-game play between
// those two windows were cut only to keep the fixture small (the existing real_excerpt.log /
// real_duplicate_create_game.log fixtures are both ~40-60KB trimmed excerpts of real games for the same
// reason); nothing within either kept window was reordered, edited, or synthesized. The cut has no
// bearing on the bug: completion is driven entirely by the STATE=COMPLETE lines, not by turn count.
var _ = Describe("the live tracker, fed a real Power.log with a duplicate STATE=COMPLETE", func() {
	var runner *Runner
	var recs []Record

	BeforeEach(func() {
		parena := os.Getenv("NOCK_PARENA_BIN")
		if parena == "" {
			parena = "/home/garybifrost/PARENA/parena"
		}
		if _, err := os.Stat(parena); err != nil {
			Skip("parena compiler not available")
		}
		os.Setenv("NOCK_PARENA_BIN", parena)
		os.Setenv("NOCK_PARENA_RUNTIME_DIR", "../nock/parena_runtime")
		runner = &Runner{WorkDir: GinkgoT().TempDir()}

		lines := realFixtureLines("real_duplicate_state_complete.log")
		var err error
		recs, err = runner.Run(context.Background(), lines)
		Expect(err).NotTo(HaveOccurred())
	})

	It("emits exactly one record for the whole game, not a phantom second one", func() {
		Expect(recs).To(HaveLen(1), "a duplicated STATE=COMPLETE must not spawn a second, empty record")
	})

	It("keeps the real game's own result and resolved identity on that one record", func() {
		rec := recs[len(recs)-1]
		Expect(rec.Complete).To(Equal(1))
		Expect(rec.Me).To(Equal(1), "0 here is exactly how the live page loses the player's identity and shows the 'waiting for your opening hand' note")
		Expect(rec.Player1).To(Equal("zzzvvi#1404"))
	})

	It("still carries the player's own real entities and draws, not an empty table", func() {
		Expect(recs[len(recs)-1].Entities).NotTo(BeEmpty(), "0 entities is exactly the shape of the phantom trailing record this regression guards against")
		var ownDraws int
		for _, e := range recs[len(recs)-1].Entities {
			if e.Player == 1 && e.Zone0 == "DECK" {
				ownDraws++
			}
		}
		Expect(ownDraws).To(BeNumerically(">", 0))
	})
})
