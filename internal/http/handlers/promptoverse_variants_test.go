package handlers_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/promptoverse"
)

func TestPromptOVerseHandler_AddVariant_Succeeds(t *testing.T) {
	outDir := t.TempDir()
	store, err := promptoverse.Open(filepath.Join(t.TempDir(), "promptoverse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Create(promptoverse.Node{
		Slug: "lil-wayne-paper-craft", Label: "paper-craft", Subject: "Lil Wayne", Kind: "surreal",
		EZPrompt: "paper-craft Lil Wayne", ExpandedPrompt: "grey hoodie", ImageFile: "lil-wayne-paper-craft.png",
	}); err != nil {
		t.Fatal(err)
	}

	h := &handlers.PromptOVerseHandler{Store: store, Renderer: &promptoverse.Renderer{OutputDir: outDir, Store: store}}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, http.NotFoundHandler(), http.HandlerFunc(h.AddVariant))

	body, _ := json.Marshal(map[string]string{
		"ez_prompt":       "paper-craft Lil Wayne red hoodie",
		"expanded_prompt": "red hoodie",
		"image_base64":    base64.StdEncoding.EncodeToString([]byte("fake png bytes")),
		"note":            "red hoodie instead of grey",
	})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/nodes/lil-wayne-paper-craft/variants", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	variants, err := store.ListVariants("lil-wayne-paper-craft")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 1 || variants[0].Note != "red hoodie instead of grey" {
		t.Fatalf("unexpected variants: %+v", variants)
	}

	// Original node must be untouched.
	original, err := store.GetBySlug("lil-wayne-paper-craft")
	if err != nil {
		t.Fatal(err)
	}
	if original.ExpandedPrompt != "grey hoodie" {
		t.Errorf("expected the original node's prompt to survive unchanged, got %q", original.ExpandedPrompt)
	}

	// The variant image file must exist on disk under the SAME slug dir.
	imgPath := filepath.Join(outDir, "lil-wayne-paper-craft", variants[0].ImageFile)
	if _, err := os.Stat(imgPath); err != nil {
		t.Errorf("expected variant image at %s: %v", imgPath, err)
	}
}

func TestPromptOVerseHandler_AddVariant_UnknownSlugReturns404(t *testing.T) {
	outDir := t.TempDir()
	store, err := promptoverse.Open(filepath.Join(t.TempDir(), "promptoverse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	h := &handlers.PromptOVerseHandler{Store: store, Renderer: &promptoverse.Renderer{OutputDir: outDir, Store: store}}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, http.NotFoundHandler(), http.HandlerFunc(h.AddVariant))

	body, _ := json.Marshal(map[string]string{
		"ez_prompt": "p", "expanded_prompt": "p", "image_base64": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/nodes/does-not-exist/variants", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown slug, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPromptOVerseHandler_AddVariant_MissingFieldsReturns400(t *testing.T) {
	outDir := t.TempDir()
	store, err := promptoverse.Open(filepath.Join(t.TempDir(), "promptoverse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Create(promptoverse.Node{Slug: "n1", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}

	h := &handlers.PromptOVerseHandler{Store: store, Renderer: &promptoverse.Renderer{OutputDir: outDir, Store: store}}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, http.NotFoundHandler(), http.HandlerFunc(h.AddVariant))

	body, _ := json.Marshal(map[string]string{"ez_prompt": "p"})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/nodes/n1/variants", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d: %s", rec.Code, rec.Body.String())
	}
}
