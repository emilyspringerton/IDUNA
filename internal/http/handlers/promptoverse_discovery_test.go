package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryHandler_CombinesAllFourSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "var", "promptoverse-hardcoded-styles.json"),
		`[{"label":"claymation","kind":"surreal","rare":false},{"label":"ice cream novelty","kind":"surreal","rare":true}]`)
	writeFile(t, filepath.Join(root, "var", "promptoverse-discovered-styles.json"),
		`[{"label":"gladiator","kind":"historical","discovered_for_subject":"princess","discovered_at":"2026-08-17T00:00:00Z","rare":false}]`)
	writeFile(t, filepath.Join(root, "var", "promptoverse-candidate-tags.json"),
		`[{"label":"vernacular","seed":"a, b, c","harvested_at":"2026-08-17T00:00:00Z","promoted":true}]`)
	writeFile(t, filepath.Join(root, "var", "promptoverse-content-blocked.jsonl"),
		`{"subject":"Rapunzel","style_label":"anime","reason":"IMAGE_PROHIBITED_CONTENT","message":"blocked","blocked_at":"2026-08-17T23:45:43Z"}`+"\n")

	h := &DiscoveryHandler{EmilyRoot: root}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/promptoverse/discovery", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Styles      []discoveryStyleView      `json:"styles"`
		Candidates  []discoveryCandidateView  `json:"candidates"`
		DeadLetters []discoveryDeadLetterView `json:"dead_letters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(out.Styles) != 3 {
		t.Fatalf("expected 3 styles (2 hardcoded + 1 discovered), got %d: %+v", len(out.Styles), out.Styles)
	}
	var foundRare, foundDiscovered bool
	for _, s := range out.Styles {
		if s.Label == "ice cream novelty" && s.Rare {
			foundRare = true
		}
		if s.Label == "gladiator" && s.Discovered && s.DiscoveredFor == "princess" {
			foundDiscovered = true
		}
	}
	if !foundRare {
		t.Error("expected the hardcoded rare style to carry Rare=true")
	}
	if !foundDiscovered {
		t.Error("expected the discovered style to carry Discovered=true and its DiscoveredFor")
	}

	if len(out.Candidates) != 1 || !out.Candidates[0].Promoted {
		t.Errorf("expected 1 promoted candidate, got %+v", out.Candidates)
	}

	if len(out.DeadLetters) != 1 || out.DeadLetters[0].Subject != "Rapunzel" {
		t.Errorf("expected 1 dead-letter entry for Rapunzel, got %+v", out.DeadLetters)
	}
}

func TestDiscoveryHandler_MissingFilesReturnEmptySections(t *testing.T) {
	root := t.TempDir() // nothing written at all
	h := &DiscoveryHandler{EmilyRoot: root}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/promptoverse/discovery", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even with no source files, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"styles", "candidates", "dead_letters"} {
		arr, ok := out[key].([]any)
		if !ok {
			t.Errorf("expected %q to be an array, got %T", key, out[key])
			continue
		}
		if len(arr) != 0 {
			t.Errorf("expected %q to be empty, got %v", key, arr)
		}
	}
}

func TestDiscoveryHandler_MalformedFileDoesNotBreakOtherSections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "var", "promptoverse-hardcoded-styles.json"), `not valid json`)
	writeFile(t, filepath.Join(root, "var", "promptoverse-candidate-tags.json"),
		`[{"label":"vernacular","seed":"a","harvested_at":"2026-08-17T00:00:00Z"}]`)

	h := &DiscoveryHandler{EmilyRoot: root}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/promptoverse/discovery", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 despite one malformed file, got %d", rec.Code)
	}
	var out struct {
		Candidates []discoveryCandidateView `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 1 {
		t.Errorf("expected the candidates section to still load despite the malformed styles file, got %+v", out.Candidates)
	}
}
