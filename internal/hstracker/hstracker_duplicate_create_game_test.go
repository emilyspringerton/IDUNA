package hstracker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// realFixtureLines reads a real captured Power.log excerpt from testdata and splits it into the
// []string shape Runner.Run expects (one line per element, no trailing \r\n -- matching exactly
// what the real PowerShell uplink sends after its own TrimEnd('\r')).
func realFixtureLines(name string) []string {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())
	var lines []string
	for _, ln := range strings.Split(string(b), "\n") {
		lines = append(lines, strings.TrimRight(ln, "\r"))
	}
	return lines
}

// Regression (2026-10-12, root-caused live against the founder's own real game): a real Hearthstone
// build logs every CREATE_GAME twice -- once under GameState.DebugPrintPower(), once under
// PowerTaskList.DebugPrintPower(), the same instant, the same event. fold-text used to treat EITHER
// sighting as "a brand new game," so the harmless PowerTaskList twin -- arriving a couple hundred
// lines after its own GameState original, well after :started was already set -- triggered the
// abort-as-complete=2 branch and fractured the match into two records a few seconds in. The live
// page only ever sees the LAST record (--live's trailing "game still in progress" one), which is the
// SECOND half of that fracture -- and the second half never saw the first half's FULL_ENTITY
// declarations for the player's OWN deck (every card id, its zone0=DECK). Those entities can never
// resolve a card identity again for the rest of the match (nothing re-declares them), so the live
// tracker's "left"/"drawn" computation -- which needs an entity's zone0 to be DECK to count it as
// ours -- silently sees nothing of the player's own hand for the rest of the game. The opponent's
// cards kept working throughout, because they get a fresh SHOW_ENTITY every time one is actually
// played, which happens well after the fracture point too -- exactly the reported symptom
// ("opponent revealed works, my own drawn cards never do").
//
// testdata/real_duplicate_create_game.log is a verbatim, unedited capture of the founder's own
// Power.log around exactly this: CREATE_GAME (GameState), the full deck/hero setup, the opening
// mulligan draw for both players (entities 21/15/32 are the founder's own hand, card0 RLK_708 /
// CORE_CATA_007 / RLK_708, each zone0 DECK), then the duplicate CREATE_GAME (PowerTaskList) a few
// hundred lines later, then more real play continuing past it. No synthetic lines were added or
// reordered -- this is the real, known-bad input, checked in so a regression here is caught against
// the actual shape of the bug, not a hand-rolled approximation of it.
var _ = Describe("the live tracker, fed a real Power.log with a duplicate CREATE_GAME", func() {
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

		lines := realFixtureLines("real_duplicate_create_game.log")
		var err error
		recs, err = runner.Run(context.Background(), lines)
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not fracture the match into two records", func() {
		Expect(recs).To(HaveLen(1), "a duplicate CREATE_GAME must not be treated as a second game")
	})

	It("keeps the game marked as still in progress, not aborted", func() {
		Expect(recs[len(recs)-1].Complete).To(Equal(0), "complete=2 means fold-text treated the duplicate as an abort")
	})

	It("resolves the player's own class and identity correctly", func() {
		Expect(recs[len(recs)-1].Me).To(Equal(1))
	})

	It("still carries the player's own cards that were declared BEFORE the duplicate line", func() {
		byID := map[int]Entity{}
		for _, e := range recs[len(recs)-1].Entities {
			byID[e.ID] = e
		}

		Expect(byID).To(HaveKey(21))
		Expect(byID[21].Player).To(Equal(1))
		Expect(byID[21].Card).To(Equal("RLK_708"))
		Expect(byID[21].Zone0).To(Equal("DECK"), "a missing zone0 is exactly how the live tracker loses the player's own hand")

		Expect(byID).To(HaveKey(15))
		Expect(byID[15].Card).To(Equal("CORE_CATA_007"))
		Expect(byID[15].Zone0).To(Equal("DECK"))

		Expect(byID).To(HaveKey(32))
		Expect(byID[32].Card).To(Equal("RLK_708"))
		Expect(byID[32].Zone0).To(Equal("DECK"))
	})

	It("records the DECK -> HAND draw transition for those same cards in the timeline", func() {
		drawn := map[int]bool{}
		for _, raw := range recs[len(recs)-1].Timeline {
			var ev []interface{}
			Expect(json.Unmarshal(raw, &ev)).To(Succeed())
			if len(ev) < 6 {
				continue
			}
			kind, _ := ev[0].(string)
			if kind != "z" {
				continue
			}
			id := int(ev[2].(float64))
			zone, _ := ev[5].(string)
			if zone == "HAND" {
				drawn[id] = true
			}
		}
		Expect(drawn).To(HaveKey(21))
		Expect(drawn).To(HaveKey(15))
		Expect(drawn).To(HaveKey(32))
	})
})
