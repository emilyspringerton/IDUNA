package handlers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmilyBinPath_EnvOverrideWins(t *testing.T) {
	t.Setenv("EMILY_BIN", "/custom/path/emily")
	if got := emilyBinPath(); got != "/custom/path/emily" {
		t.Errorf("emilyBinPath() = %q, want /custom/path/emily", got)
	}
}

func TestEmilyBinPath_FallsBackToBareNameWhenNothingResolves(t *testing.T) {
	t.Setenv("EMILY_BIN", "")
	t.Setenv("HOME", t.TempDir()) // real dir, but no .local/bin/emily inside it
	if got := emilyBinPath(); got != "emily" {
		t.Errorf("emilyBinPath() = %q, want bare \"emily\" (PATH fallback)", got)
	}
}

// TestFetchSagaGaps_RealBinary is a real integration test against the actual
// `emily` binary and a real (empty) temp repo -- no manifest file, so this
// exercises the exact same "no gaps to report yet" path loadManifest's own
// doc comment names as expected, not a mocked stand-in. Matches this
// codebase's existing precedent (git exec.Command calls in apples.go are
// similarly untested via mocks -- real local binaries are treated as real
// dependencies, not something to fake out).
func TestFetchSagaGaps_RealBinary(t *testing.T) {
	if _, err := os.Stat(emilyBinPath()); err != nil {
		t.Skipf("emily binary not available in this environment: %v", err)
	}
	repo := t.TempDir()
	report := fetchSagaGaps(repo)
	if report.FetchErr != "" {
		t.Fatalf("fetchSagaGaps against an empty repo should not fail: %s", report.FetchErr)
	}
	if report.ManifestEntries != 0 {
		t.Errorf("empty repo should report 0 manifest entries, got %d", report.ManifestEntries)
	}
	wantManifest := filepath.Join(repo, "saga.manifest.yaml")
	if report.ManifestPath != wantManifest {
		t.Errorf("manifest path = %q, want %q", report.ManifestPath, wantManifest)
	}
}

func TestAdminSaga_RendersPage(t *testing.T) {
	if _, err := os.Stat(emilyBinPath()); err != nil {
		t.Skipf("emily binary not available in this environment: %v", err)
	}
	h := &AdminHandler{}
	req := httptest.NewRequest("GET", "/admin/saga", nil)
	w := httptest.NewRecorder()
	h.saga(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Divergence Queue") {
		t.Errorf("page missing expected heading, got: %s", body)
	}
	if !strings.Contains(body, "PRRJECT_FATBABY") {
		t.Errorf("page missing configured repo name, got: %s", body)
	}
}
