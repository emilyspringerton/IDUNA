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
		Slug: "glossy-90s-card", Label: "1990s glossy rookie card", Kind: "historical",
		TopLevelPrompt: "A 1990s glossy baseball rookie card portrait photo.",
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
		"A 1990s glossy baseball rookie card portrait photo.",
		"1990s glossy rookie card", "<time datetime=\"2026-08-17\">",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q", want)
		}
	}
}

func TestRenderer_RenderIndex_ListsAllNodes(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "a", Label: "Node A", Kind: "historical", TopLevelPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "b", Label: "Node B", Kind: "surreal", TopLevelPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
	}
	if err := r.RenderIndex(nodes); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, "Node A") || !strings.Contains(html, "Node B") {
		t.Errorf("index missing expected node labels: %s", html)
	}
	if !strings.Contains(html, "/prompt-o-verse/a/") || !strings.Contains(html, "/prompt-o-verse/b/") {
		t.Errorf("index missing expected node links")
	}
}
