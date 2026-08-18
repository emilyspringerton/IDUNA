// mashups.go — reads the LLM-judgment mashup/hybrid verdicts `emily
// promptoverse mashups` writes to EMILY_ROOT/var (internal/mashupjudge in
// emily.cli) and turns them into cross-links on subject/style pages.
//
// A first implementation attempt at this feature was pure lexical
// word-matching and was abandoned mid-build once tested against real
// counterexamples ("tuxedo duck" is plausibly just a real duck breed,
// not a mashup of "tuxedo" and "duck"; "tuxedo duck" and "a duck wearing
// a tuxedo" are the same subject despite sharing almost no words, while
// "tuxedo duck" and "duck tuxedo" are not the same despite sharing every
// word). See EMILY/docs/NORTHSTAR_PROMPT_O_VERSE.md §9 for the full
// history. This package only ever reads pre-computed judgments -- it has
// no string-matching fallback logic of its own, deliberately, so it
// can't silently regress back to the abandoned approach.
package promptoverse

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// emilyRootDefault mirrors handlers.emilyRootDefault -- duplicated rather
// than imported since internal/http/handlers imports this package, not
// the other way around, and it's a two-line function.
func emilyRootDefault() string {
	if v := os.Getenv("EMILY_ROOT"); v != "" {
		return v
	}
	return "/home/fatbaby/EMILY"
}

type mashupJudgment struct {
	Subject               string   `json:"subject"`
	Provider              string   `json:"provider"`
	IsCompositionalMashup bool     `json:"is_compositional_mashup"`
	Components            []string `json:"components"`
	ParaphraseEquivalents []string `json:"paraphrase_equivalents"`
}

// loadMashupJudgments reads a JSON array file written by `emily
// promptoverse mashups`. Missing or malformed files are not fatal --
// read fresh on every render, same as DiscoveryHandler's files, and the
// feature just shows no cross-links yet if the judgment job hasn't run.
func loadMashupJudgments(path string) []mashupJudgment {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []mashupJudgment
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&out); err != nil {
		return nil
	}
	return out
}

// mashupCrossLinks turns a flat judgment list into label -> [related
// labels], unioned across every provider (only one, gemini, has real
// data as of this writing, but a future claude/A-B run should widen
// coverage, not need a rendering change). Two relations, both symmetric
// at display time even though the underlying judgment is directional:
//   - component -> compound: if X is judged a real component of Y, Y
//     shows up on X's page ("this subject appears in these mashups").
//   - paraphrase equivalence: if X lists Y as meaning the same thing,
//     both directions link, since "same concept" has no natural owner.
func mashupCrossLinks(judgments []mashupJudgment) map[string][]string {
	links := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	add := func(a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		if seen[a] == nil {
			seen[a] = make(map[string]bool)
		}
		if seen[a][b] {
			return
		}
		seen[a][b] = true
		links[a] = append(links[a], b)
	}
	for _, j := range judgments {
		if j.IsCompositionalMashup {
			for _, c := range j.Components {
				add(c, j.Subject)
			}
		}
		for _, p := range j.ParaphraseEquivalents {
			add(j.Subject, p)
			add(p, j.Subject)
		}
	}
	return links
}

func (r *Renderer) subjectMashupCrossLinks() map[string][]string {
	path := filepath.Join(r.emilyRoot(), "var", "promptoverse-mashup-judgments.json")
	return mashupCrossLinks(loadMashupJudgments(path))
}

func (r *Renderer) styleMashupCrossLinks() map[string][]string {
	path := filepath.Join(r.emilyRoot(), "var", "promptoverse-style-mashup-judgments.json")
	return mashupCrossLinks(loadMashupJudgments(path))
}
