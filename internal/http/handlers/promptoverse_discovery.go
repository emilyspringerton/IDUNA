// internal/http/handlers/promptoverse_discovery.go — GET
// /api/v1/promptoverse/discovery, public/read-only.
//
// Founder direction: "wheres the link to the tag discovery page with the
// candidates for style promotion via the gpt2 discovery pipeline?" then,
// on scope ("interactive promote UI" vs. "read-only styles listing"):
// read-only. Promotion itself still only happens via `emily promptoverse
// promote` on the box -- this just shows what exists and what's been
// harvested and not yet promoted.
//
// Reads four files emily.cli already owns and writes directly from
// EMILY_ROOT/var (same cross-repo "known shared location on the same box"
// pattern apples.go's emilyRootDefault already established, not a new
// convention):
//   - promptoverse-hardcoded-styles.json — the compiled-in registry
//     (promptoverseStyles + promptoverseRareStyles); IDUNA has no other way
//     to see this, it's Go source in a different binary. Refreshed by
//     emily.cli on every `emily promptoverse` invocation.
//   - promptoverse-discovered-styles.json — same file drainQueue/add read
//     from, already has a Rare field.
//   - promptoverse-candidate-tags.json — GPT-2-harvested candidates from
//     `emily promptoverse brainstorm`, with a Promoted flag.
//   - promptoverse-content-blocked.jsonl — the dead-letter dataset of
//     permanently content-policy-blocked (subject, style) attempts
//     (founder: "add a page on iduna to view that data").
//
// Read fresh on every request rather than cached -- these files are small
// (dozens to low hundreds of entries) and change rarely enough that a
// stat+read per request is not worth the complexity of invalidation.
package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type discoveryStyleView struct {
	Label         string    `json:"label"`
	Kind          string    `json:"kind"`
	Rare          bool      `json:"rare"`
	Discovered    bool      `json:"discovered"`
	DiscoveredFor string    `json:"discovered_for,omitempty"`
	DiscoveredAt  time.Time `json:"discovered_at,omitzero"`
}

type discoveryCandidateView struct {
	Label       string    `json:"label"`
	Seed        string    `json:"seed"`
	HarvestedAt time.Time `json:"harvested_at"`
	Promoted    bool      `json:"promoted"`
}

type discoveryDeadLetterView struct {
	Subject    string    `json:"subject"`
	StyleLabel string    `json:"style_label"`
	Reason     string    `json:"reason"`
	Message    string    `json:"message"`
	BlockedAt  time.Time `json:"blocked_at"`
}

// DiscoveryHandler serves the read-only style-registry + candidate-tag
// listing for the Prompt-o-verse discovery page.
type DiscoveryHandler struct {
	EmilyRoot string // defaults to emilyRootDefault() if empty
}

func (h *DiscoveryHandler) root() string {
	if h.EmilyRoot != "" {
		return h.EmilyRoot
	}
	return emilyRootDefault()
}

func (h *DiscoveryHandler) Get(w http.ResponseWriter, r *http.Request) {
	root := h.root()

	var hardcoded []struct {
		Label string `json:"label"`
		Kind  string `json:"kind"`
		Rare  bool   `json:"rare"`
	}
	readJSONFile(filepath.Join(root, "var", "promptoverse-hardcoded-styles.json"), &hardcoded)

	var discovered []struct {
		Label         string    `json:"label"`
		Kind          string    `json:"kind"`
		DiscoveredFor string    `json:"discovered_for_subject"`
		DiscoveredAt  time.Time `json:"discovered_at"`
		Rare          bool      `json:"rare"`
	}
	readJSONFile(filepath.Join(root, "var", "promptoverse-discovered-styles.json"), &discovered)

	var candidates []struct {
		Label       string    `json:"label"`
		Seed        string    `json:"seed"`
		HarvestedAt time.Time `json:"harvested_at"`
		Promoted    bool      `json:"promoted"`
	}
	readJSONFile(filepath.Join(root, "var", "promptoverse-candidate-tags.json"), &candidates)

	styles := make([]discoveryStyleView, 0, len(hardcoded)+len(discovered))
	for _, s := range hardcoded {
		styles = append(styles, discoveryStyleView{Label: s.Label, Kind: s.Kind, Rare: s.Rare})
	}
	for _, s := range discovered {
		styles = append(styles, discoveryStyleView{
			Label: s.Label, Kind: s.Kind, Rare: s.Rare,
			Discovered: true, DiscoveredFor: s.DiscoveredFor, DiscoveredAt: s.DiscoveredAt,
		})
	}

	candidateViews := make([]discoveryCandidateView, 0, len(candidates))
	for _, c := range candidates {
		candidateViews = append(candidateViews, discoveryCandidateView{
			Label: c.Label, Seed: c.Seed, HarvestedAt: c.HarvestedAt, Promoted: c.Promoted,
		})
	}

	deadLetters := readJSONLFile[discoveryDeadLetterView](filepath.Join(root, "var", "promptoverse-content-blocked.jsonl"))

	writeJSON(w, http.StatusOK, map[string]any{
		"styles":       styles,
		"candidates":   candidateViews,
		"dead_letters": deadLetters,
	})
}

// readJSONLFile parses a newline-delimited JSON file (the queue/dead-letter
// file shape), skipping any corrupt line rather than failing the whole
// read -- same "one bad record shouldn't take down the page" posture as
// readJSONFile below. A missing file returns an empty (non-nil) slice.
func readJSONLFile[T any](path string) []T {
	out := make([]T, 0)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// readJSONFile is a best-effort load: a missing or malformed file just
// leaves dest at its zero value (empty slice) rather than failing the
// whole discovery response -- one file being stale/absent shouldn't take
// down the other two sections of the page.
func readJSONFile(path string, dest any) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, dest)
}
