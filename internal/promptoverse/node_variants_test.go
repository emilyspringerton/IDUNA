package promptoverse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderNodePage_ShowsVariantsWithMetadata(t *testing.T) {
	// Founder, real-time: "we need to keep both and i think for seo
	// reasons we should condense the forced feature leaf nodes onto the
	// same html page" / "showing all metadata for both".
	outDir := t.TempDir()
	store := newTestStore(t)
	n := Node{
		Slug: "lil-wayne-paper-craft", Label: "paper-craft", Subject: "Lil Wayne", Kind: "surreal",
		EZPrompt: "paper-craft Lil Wayne", ExpandedPrompt: "Lil Wayne in a grey hoodie, paper-craft style",
		ImageFile: "lil-wayne-paper-craft.png", PublishedAt: time.Now(),
	}
	if _, err := store.Create(n); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddVariant(n.Slug, "lil-wayne-paper-craft-variant-2.png",
		"paper-craft Lil Wayne red hoodie", "Lil Wayne in a red hoodie, paper-craft style", "red hoodie instead of grey"); err != nil {
		t.Fatal(err)
	}

	r := &Renderer{OutputDir: outDir, Store: store}
	if err := r.RenderNode(n); err != nil {
		t.Fatalf("RenderNode: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, n.Slug, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)

	for _, want := range []string{
		"Lil Wayne in a grey hoodie, paper-craft style", // original, unchanged
		"red hoodie instead of grey",                    // variant note
		"Lil Wayne in a red hoodie, paper-craft style",  // variant expanded prompt
		"lil-wayne-paper-craft-variant-2.png",           // variant image
		"Other variants of this generation",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q:\n%s", want, html)
		}
	}
}

func TestRenderNodePage_NoVariantsShowsNoVariantsSection(t *testing.T) {
	outDir := t.TempDir()
	store := newTestStore(t)
	n := Node{Slug: "solo", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()}
	if _, err := store.Create(n); err != nil {
		t.Fatal(err)
	}
	r := &Renderer{OutputDir: outDir, Store: store}
	if err := r.RenderNode(n); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, n.Slug, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `<section class="variants">`) {
		t.Errorf("did not expect a variants section with no variants: %s", data)
	}
}

func TestRenderNodePage_NilStoreSkipsVariantLookup(t *testing.T) {
	// Renderer.Store is optional -- must not panic when unset.
	outDir := t.TempDir()
	n := Node{Slug: "solo", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()}
	r := &Renderer{OutputDir: outDir}
	if err := r.RenderNode(n); err != nil {
		t.Fatalf("RenderNode with nil Store: %v", err)
	}
}

func TestAddVariant_Succeeds(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "lil-wayne-paper-craft", Label: "paper-craft", Subject: "Lil Wayne", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddVariant("lil-wayne-paper-craft", "lil-wayne-paper-craft-variant-2.png", "paper-craft Lil Wayne red hoodie", "Lil Wayne in a red hoodie, paper-craft style", "red hoodie instead of grey")
	if err != nil {
		t.Fatalf("AddVariant: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected a positive id, got %d", id)
	}

	variants, err := s.ListVariants("lil-wayne-paper-craft")
	if err != nil {
		t.Fatalf("ListVariants: %v", err)
	}
	if len(variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(variants))
	}
	v := variants[0]
	if v.NodeSlug != "lil-wayne-paper-craft" || v.Note != "red hoodie instead of grey" {
		t.Errorf("unexpected variant: %+v", v)
	}
}

func TestAddVariant_UnknownSlugFails(t *testing.T) {
	s := newTestStore(t)
	_, err := s.AddVariant("does-not-exist", "x.png", "p", "p", "note")
	if err == nil {
		t.Fatal("expected an error adding a variant to a nonexistent node")
	}
}

func TestAddVariant_OriginalNodeUnchanged(t *testing.T) {
	// Founder, real-time: "we need to keep both" -- adding a variant must
	// NEVER touch the original node's own row.
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "n1", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "original ez", ExpandedPrompt: "original expanded", ImageFile: "orig.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVariant("n1", "n1-variant-2.png", "new ez", "new expanded", "a tweak"); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetBySlug("n1")
	if err != nil {
		t.Fatal(err)
	}
	if n.EZPrompt != "original ez" || n.ExpandedPrompt != "original expanded" || n.ImageFile != "orig.png" {
		t.Errorf("original node was mutated by AddVariant: %+v", n)
	}
}

func TestListVariants_MultipleOrderedByCreatedAt(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "n1", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVariant("n1", "n1-variant-2.png", "p", "e1", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddVariant("n1", "n1-variant-3.png", "p", "e2", "second"); err != nil {
		t.Fatal(err)
	}
	variants, err := s.ListVariants("n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 || variants[0].Note != "first" || variants[1].Note != "second" {
		t.Errorf("unexpected variant order: %+v", variants)
	}
}

func TestListVariants_NoneReturnsEmptyNotError(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "n1", Label: "l", Subject: "s", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}
	variants, err := s.ListVariants("n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 0 {
		t.Errorf("expected no variants, got %d", len(variants))
	}
}
