// admin_saga.go — Back Office divergence queue (S143-03, first slice)
//
// HQ-SPEC-DOC-102 §9 build-sequence step 3 asks for "divergence + conflict
// queues in Back Office; aging rules wired to the corpus health gate." This
// is a deliberately modest first slice, not the full item: a read-only
// divergence queue (claim-without-code / code-without-claim, per §4), no
// conflict queue yet (semantic conflict detection is step 5, unbuilt --
// there is nothing to list), and no aging/gate logic (what a "corpus health
// gate" concretely blocks is a real, undecided design question, not
// mechanical to add). Shells out to `emily saga gaps --repo <path> --json`
// (emily.cli 4cc144b) rather than duplicating its restricted-YAML parser
// and README-token surface scan here -- same "one real implementation, not
// two that can drift" reasoning this codebase already applies elsewhere
// (e.g. GFD's NETCODE_CONTRACT_SPEC.md pointer-stub pattern, for docs
// instead of code).
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// sagaRepos is the static list of repos with a saga.manifest.yaml worth
// checking. Intentionally small and hand-maintained for this first slice --
// PRRJECT_FATBABY is the only repo with a manifest yet (S143-02). Add an
// entry here once another repo gets one; no code change needed beyond that.
var sagaRepos = []struct{ Name, Path string }{
	{"PRRJECT_FATBABY", "/home/fatbaby/PRRJECT_FATBABY"},
}

// sagaGapsReport mirrors emily.cli's cmd.SagaGapsReport (JSON field names
// must match exactly -- no shared Go package between the two modules for
// this small a struct, see this file's own doc comment).
type sagaGapsReport struct {
	Repo            string   `json:"repo"`
	ManifestPath    string   `json:"manifest_path"`
	ManifestEntries int      `json:"manifest_entries"`
	Vaporware       []string `json:"vaporware"`
	BrokenRefs      []string `json:"broken_refs"`
	UnknownClaims   []string `json:"unknown_claims"`
	DarkMatter      []string `json:"dark_matter"`
	SurfaceScanErr  string   `json:"surface_scan_error,omitempty"`
	TotalGaps       int      `json:"total_gaps"`
	FetchErr        string   `json:"-"` // this repo's own exec/parse failure, not part of emily's JSON
}

// emilyBinPath resolves the `emily` binary the same way a real operator
// would find it -- EMILY_BIN override first (explicit, wins), then the
// standard `emily.cli/scripts/build.sh` install location under $HOME,
// falling back to bare "emily" (PATH lookup) as a last resort. systemd
// units commonly run with a bare PATH that doesn't include ~/.local/bin,
// so the middle case is the one that actually matters in production.
func emilyBinPath() string {
	if p := os.Getenv("EMILY_BIN"); p != "" {
		return p
	}
	if home := os.Getenv("HOME"); home != "" {
		p := filepath.Join(home, ".local", "bin", "emily")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "emily"
}

// fetchSagaGaps runs `emily saga gaps --repo <path> --json` with a bounded
// timeout (this executes synchronously inside an HTTP handler -- the
// underlying command is pure local file I/O, so 10s is generous, not tight).
func fetchSagaGaps(repoPath string) sagaGapsReport {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, emilyBinPath(), "saga", "gaps", "--repo", repoPath, "--json")
	out, err := cmd.Output()
	// Exit code 1 is a real, documented success case here (emily saga gaps
	// returns 1 when it found gaps, 0 when clean) -- only treat this as a
	// fetch failure if there's no parseable JSON on stdout at all.
	var report sagaGapsReport
	if len(out) > 0 {
		if jsonErr := json.Unmarshal(out, &report); jsonErr == nil {
			return report
		}
	}
	if err != nil {
		return sagaGapsReport{Repo: repoPath, FetchErr: err.Error()}
	}
	return sagaGapsReport{Repo: repoPath, FetchErr: "empty or unparseable output from emily saga gaps"}
}

func (h *AdminHandler) saga(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	reports := make([]sagaGapsReport, 0, len(sagaRepos))
	for _, repo := range sagaRepos {
		rep := fetchSagaGaps(repo.Path)
		if rep.Repo == "" {
			rep.Repo = repo.Path
		}
		reports = append(reports, rep)
	}
	renderHTML(w, adminSagaTmpl, map[string]any{
		"Title":   "SAGA — Divergence Queue",
		"Reports": reports,
	})
}

var adminSagaTmpl = mustParseTmpl("saga", `
{{define "body"}}
<h1>SAGA — Divergence Queue</h1>
<p class="meta">
  HQ-SPEC-DOC-102 §4. Claim-without-code (vaporware debt) and code-without-claim (dark matter),
  per repo. Conflict queue and aging/gate logic not built yet -- see
  <a href="https://github.com/emilyspringerton/EMILY/blob/main/docs/hq-specs/HQ-SPEC-DOC-102-saga-curation-lifecycle.md">HQ-SPEC-DOC-102</a>
  §9 build sequence steps 3/5/6.
</p>
{{range .Reports}}
<div class="section-card">
  <h2>{{.Repo}}{{if .FetchErr}} <span class="badge badge-suspended">fetch failed</span>{{end}}</h2>
  {{if .FetchErr}}
    <p class="empty">{{.FetchErr}}</p>
  {{else}}
    <p class="meta">manifest: {{.ManifestPath}} ({{.ManifestEntries}} entries) — {{.TotalGaps}} total gap(s){{if .SurfaceScanErr}} — surface scan skipped: {{.SurfaceScanErr}}{{end}}</p>

    <h2>Vaporware debt ({{len .Vaporware}})</h2>
    {{if .Vaporware}}<table><tbody>{{range .Vaporware}}<tr><td>{{.}}</td></tr>{{end}}</tbody></table>{{else}}<p class="empty">none</p>{{end}}

    <h2>Broken verification anchors ({{len .BrokenRefs}})</h2>
    {{if .BrokenRefs}}<table><tbody>{{range .BrokenRefs}}<tr><td>{{.}}</td></tr>{{end}}</tbody></table>{{else}}<p class="empty">none</p>{{end}}

    <h2>Unknown claim IDs ({{len .UnknownClaims}})</h2>
    {{if .UnknownClaims}}<table><tbody>{{range .UnknownClaims}}<tr><td>{{.}}</td></tr>{{end}}</tbody></table>{{else}}<p class="empty">none</p>{{end}}

    <h2>Dark matter — real surface, no manifest entry ({{len .DarkMatter}})</h2>
    {{if .DarkMatter}}<table><tbody>{{range .DarkMatter}}<tr><td><code>{{.}}</code></td></tr>{{end}}</tbody></table>{{else}}<p class="empty">none</p>{{end}}
  {{end}}
</div>
{{end}}
{{end}}
`)
