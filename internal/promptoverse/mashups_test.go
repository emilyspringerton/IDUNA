package promptoverse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMashupCrossLinks_ComponentPointsToCompound(t *testing.T) {
	judgments := []mashupJudgment{
		{Subject: "Fractal Raccoon", Provider: "gemini", IsCompositionalMashup: true, Components: []string{"Fractal", "Raccoon"}},
	}
	links := mashupCrossLinks(judgments)
	if !contains(links["Fractal"], "Fractal Raccoon") {
		t.Errorf("expected Fractal -> Fractal Raccoon, got %v", links["Fractal"])
	}
	if !contains(links["Raccoon"], "Fractal Raccoon") {
		t.Errorf("expected Raccoon -> Fractal Raccoon, got %v", links["Raccoon"])
	}
	// The compound itself gets no self-referential or reverse entry from
	// this relation alone.
	if len(links["Fractal Raccoon"]) != 0 {
		t.Errorf("did not expect Fractal Raccoon to point anywhere from a components-only judgment, got %v", links["Fractal Raccoon"])
	}
}

func TestMashupCrossLinks_NonCompositionalMashupIsIgnored(t *testing.T) {
	// Regression for the exact case that killed lexical matching: a
	// judgment can list a subject with no real components ("tuxedo duck"
	// is not compositional even though "tuxedo" and "duck" both exist).
	judgments := []mashupJudgment{
		{Subject: "tuxedo duck", Provider: "gemini", IsCompositionalMashup: false, Components: []string{"tuxedo", "duck"}},
	}
	links := mashupCrossLinks(judgments)
	if len(links) != 0 {
		t.Errorf("expected no cross-links from a non-compositional judgment even with Components set, got %v", links)
	}
}

func TestMashupCrossLinks_ParaphraseEquivalenceIsSymmetric(t *testing.T) {
	judgments := []mashupJudgment{
		{Subject: "tuxedo duck", Provider: "gemini", ParaphraseEquivalents: []string{"a duck wearing a tuxedo"}},
	}
	links := mashupCrossLinks(judgments)
	if !contains(links["tuxedo duck"], "a duck wearing a tuxedo") {
		t.Errorf("expected tuxedo duck -> a duck wearing a tuxedo, got %v", links["tuxedo duck"])
	}
	if !contains(links["a duck wearing a tuxedo"], "tuxedo duck") {
		t.Errorf("expected the reverse link too, got %v", links["a duck wearing a tuxedo"])
	}
}

func TestMashupCrossLinks_DedupesAcrossProviders(t *testing.T) {
	judgments := []mashupJudgment{
		{Subject: "Fractal Raccoon", Provider: "gemini", IsCompositionalMashup: true, Components: []string{"Fractal"}},
		{Subject: "Fractal Raccoon", Provider: "claude", IsCompositionalMashup: true, Components: []string{"Fractal"}},
	}
	links := mashupCrossLinks(judgments)
	if len(links["Fractal"]) != 1 {
		t.Errorf("expected exactly one deduped entry across providers, got %v", links["Fractal"])
	}
}

func TestLoadMashupJudgments_MissingFileReturnsNilNotError(t *testing.T) {
	got := loadMashupJudgments(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if got != nil {
		t.Errorf("expected nil for a missing file, got %v", got)
	}
}

func TestRenderer_SubjectPage_ShowsMashupsSection(t *testing.T) {
	outDir := t.TempDir()
	emilyRoot := t.TempDir()
	writeJudgments(t, filepath.Join(emilyRoot, "var", "promptoverse-mashup-judgments.json"), []mashupJudgment{
		{Subject: "Fractal", Provider: "gemini", IsCompositionalMashup: false},
		{Subject: "Fractal Raccoon", Provider: "gemini", IsCompositionalMashup: true, Components: []string{"Fractal", "Raccoon"}},
	})
	r := &Renderer{OutputDir: outDir, EmilyRoot: emilyRoot}

	nodes := []Node{
		{Slug: "fractal-1", Label: "style a", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "fractal-2", Label: "style b", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		// "Fractal Raccoon" itself has only 1 node -- no page of its own,
		// so it must NOT appear as a dead link on Fractal's page.
		{Slug: "fractal-raccoon-1", Label: "style c", Subject: "Fractal Raccoon", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "c.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "subject", "fractal", "index.html"))
	if err != nil {
		t.Fatalf("read Fractal subject page: %v", err)
	}
	html := string(data)
	if strings.Contains(html, "Fractal Raccoon") {
		t.Errorf("did not expect a dead link to Fractal Raccoon (no page, <2 nodes): %s", html)
	}
	if strings.Contains(html, `<section class="mashups">`) {
		t.Errorf("expected no Mashups section when the only related subject has no page: %s", html)
	}
}

func TestRenderer_SubjectPage_NominationWidget_NoClientIDShowsUnavailable(t *testing.T) {
	outDir := t.TempDir()
	r := &Renderer{OutputDir: outDir, EmilyRoot: t.TempDir()}
	nodes := []Node{
		{Slug: "fractal-1", Label: "a", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "fractal-2", Label: "b", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "subject", "fractal", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, "not yet available") {
		t.Errorf("expected the 'not yet available' sign-in fallback with no GoogleClientID: %s", html)
	}
	if strings.Contains(html, "accounts.google.com/gsi/client") {
		t.Errorf("did not expect the GSI script tag when GoogleClientID is empty: %s", html)
	}
}

func TestRenderer_SubjectPage_NominationWidget_WithClientIDShowsSignIn(t *testing.T) {
	outDir := t.TempDir()
	r := &Renderer{OutputDir: outDir, EmilyRoot: t.TempDir(), GoogleClientID: "test-client-id.apps.googleusercontent.com"}
	nodes := []Node{
		{Slug: "fractal-1", Label: "a", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "fractal-2", Label: "b", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		{Slug: "raccoon-1", Label: "a", Subject: "Raccoon", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "raccoon-2", Label: "b", Subject: "Raccoon", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "subject", "fractal", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, "accounts.google.com/gsi/client") {
		t.Errorf("expected the GSI script tag when GoogleClientID is set: %s", html)
	}
	if !strings.Contains(html, `data-google-client-id="test-client-id.apps.googleusercontent.com"`) {
		t.Errorf("expected the client id embedded in the widget's data attribute: %s", html)
	}
	if !strings.Contains(html, `<option value="Raccoon">`) {
		t.Errorf("expected Raccoon (the other subject with a page) in the autocomplete datalist: %s", html)
	}
	if strings.Contains(html, `<option value="Fractal">`) {
		t.Errorf("did not expect Fractal's own page listed as a mashup partner for itself: %s", html)
	}
}

func TestRenderer_SubjectPage_LinksRealCompoundMashup(t *testing.T) {
	outDir := t.TempDir()
	emilyRoot := t.TempDir()
	writeJudgments(t, filepath.Join(emilyRoot, "var", "promptoverse-mashup-judgments.json"), []mashupJudgment{
		{Subject: "Fractal Raccoon", Provider: "gemini", IsCompositionalMashup: true, Components: []string{"Fractal", "Raccoon"}},
	})
	r := &Renderer{OutputDir: outDir, EmilyRoot: emilyRoot}

	nodes := []Node{
		{Slug: "fractal-1", Label: "style a", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "fractal-2", Label: "style b", Subject: "Fractal", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		{Slug: "fr-1", Label: "style c", Subject: "Fractal Raccoon", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "c.png", PublishedAt: time.Now()},
		{Slug: "fr-2", Label: "style d", Subject: "Fractal Raccoon", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "d.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "subject", "fractal", "index.html"))
	if err != nil {
		t.Fatalf("read Fractal subject page: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, `href="/prompt-o-verse/subject/fractal-raccoon/"`) {
		t.Errorf("expected Fractal's page to link to Fractal Raccoon (both have pages now): %s", html)
	}
	if !strings.Contains(html, "Mashups featuring Fractal") {
		t.Errorf("expected a Mashups heading on Fractal's page: %s", html)
	}
}

func TestRenderer_StylePage_ShowsMashupsSection(t *testing.T) {
	outDir := t.TempDir()
	emilyRoot := t.TempDir()
	writeJudgments(t, filepath.Join(emilyRoot, "var", "promptoverse-style-mashup-judgments.json"), []mashupJudgment{
		{Subject: "candy fractal", Provider: "gemini", IsCompositionalMashup: true, Components: []string{"candy", "fractal"}},
	})
	r := &Renderer{OutputDir: outDir, EmilyRoot: emilyRoot}

	nodes := []Node{
		{Slug: "n1", Label: "candy", Subject: "s1", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "n2", Label: "candy fractal", Subject: "s2", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "style", "candy", "index.html"))
	if err != nil {
		t.Fatalf("read candy style page: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, `href="/prompt-o-verse/style/candy-fractal/"`) {
		t.Errorf("expected candy's style page to link to candy fractal: %s", html)
	}
}

func writeJudgments(t *testing.T, path string, judgments []mashupJudgment) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(judgments)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
