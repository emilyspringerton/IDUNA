package promptoverse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderer_RenderNode_WritesSemanticHTML(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	n := Node{
		Slug: "glossy-90s-card", Label: "1990s glossy rookie card", Subject: "baseball card", Kind: "historical",
		EZPrompt:       "1990s glossy baseball rookie card",
		ExpandedPrompt: "A 1990s glossy baseball rookie card portrait photo.",
		ImageFile:      "glossy-90s-card.png",
		Tags:           Tags{"era": "1990s", "shot_type": "portrait"},
		PublishedAt:    time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
	}
	if err := r.RenderNode(n); err != nil {
		t.Fatalf("RenderNode: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "glossy-90s-card", "index.html"))
	if err != nil {
		t.Fatalf("read rendered page: %v", err)
	}
	html := string(data)

	for _, want := range []string{
		"<article>", "<figure", "<figcaption>", "<dl class=\"taxonomy\">",
		"<dt>era</dt><dd>1990s</dd>", "<dt>shot_type</dt><dd>portrait</dd>",
		"1990s glossy baseball rookie card",
		"A 1990s glossy baseball rookie card portrait photo.",
		"1990s glossy rookie card", "Applied to: baseball card",
		"<time datetime=\"2026-08-17\">",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q", want)
		}
	}
}

func TestRenderer_RenderIndex_GroupsByLabel(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "renaissance-masterchief", Label: "Renaissance oil painting", Subject: "Master Chief (Halo)", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		{Slug: "renaissance-baseball", Label: "Renaissance oil painting", Subject: "baseball card", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now().Add(-time.Minute)},
		{Slug: "lego-baseball", Label: "LEGO minifigure", Subject: "baseball card", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "c.png", PublishedAt: time.Now().Add(-2 * time.Minute)},
	}
	if err := r.RenderIndex(nodes); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	html := string(data)

	// Both Renaissance leaves must appear under one category grouping, not two.
	if strings.Count(html, "Renaissance oil painting<span") != 1 {
		t.Errorf("expected exactly one 'Renaissance oil painting' category heading, got %d occurrences", strings.Count(html, "Renaissance oil painting<span"))
	}
	if !strings.Contains(html, "2 variants") {
		t.Errorf("expected the Renaissance category to show a 2-variant count: %s", html)
	}
	for _, want := range []string{
		"/prompt-o-verse/renaissance-masterchief/", "/prompt-o-verse/renaissance-baseball/", "/prompt-o-verse/lego-baseball/",
		"Master Chief (Halo)", "baseball card",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index missing %q", want)
		}
	}
}

func TestRenderer_RenderIndex_SingleVariantSingular(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "solo", Label: "Solo Style", Subject: "baseball card", Kind: "historical", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
	}
	if err := r.RenderIndex(nodes); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "1 variant<") {
		t.Errorf("expected singular '1 variant' for a single-node category: %s", data)
	}
}
