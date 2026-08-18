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
	if strings.Count(html, "Renaissance oil painting</a><span") != 1 {
		t.Errorf("expected exactly one 'Renaissance oil painting' category heading, got %d occurrences", strings.Count(html, "Renaissance oil painting</a><span"))
	}
	if !strings.Contains(html, "2 variants") {
		t.Errorf("expected the Renaissance category to show a 2-variant count: %s", html)
	}
	for _, want := range []string{
		"/prompt-o-verse/renaissance-masterchief/", "/prompt-o-verse/renaissance-baseball/", "/prompt-o-verse/lego-baseball/",
		"Master Chief (Halo)", "baseball card",
		`<a href="/prompt-o-verse/style/renaissance-oil-painting/">Renaissance oil painting</a>`,
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

func TestRenderer_RenderAll_LinksSubjectWithTwoOrMoreLeaves(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "duck-tux-claymation", Label: "claymation", Subject: "a duck wearing a tuxedo", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "duck-tux-lego", Label: "LEGO minifigure", Subject: "a duck wearing a tuxedo", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		{Slug: "solo-subject", Label: "watercolor sketchbook", Subject: "a lonely teapot", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "c.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// A Subject with 2 leaves: both leaf pages must link to the subject page.
	wantLink := "/prompt-o-verse/subject/a-duck-wearing-a-tuxedo/"
	for _, slug := range []string{"duck-tux-claymation", "duck-tux-lego"} {
		data, err := os.ReadFile(filepath.Join(dir, slug, "index.html"))
		if err != nil {
			t.Fatalf("read %s: %v", slug, err)
		}
		html := string(data)
		if !strings.Contains(html, `href="`+wantLink+`"`) {
			t.Errorf("%s: expected a link to %s, got: %s", slug, wantLink, html)
		}
	}

	// A Subject with only 1 leaf: no link, plain text.
	data, err := os.ReadFile(filepath.Join(dir, "solo-subject", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if strings.Contains(html, "/prompt-o-verse/subject/a-lonely-teapot/") {
		t.Errorf("solo-subject: expected NO subject link for a single-leaf subject, got: %s", html)
	}
	if !strings.Contains(html, "Applied to: a lonely teapot</p>") {
		t.Errorf("solo-subject: expected plain (unlinked) subject text, got: %s", html)
	}

	// The subject page itself must exist and list both duck-tuxedo leaves.
	subjData, err := os.ReadFile(filepath.Join(dir, "subject", "a-duck-wearing-a-tuxedo", "index.html"))
	if err != nil {
		t.Fatalf("read subject page: %v", err)
	}
	subjHTML := string(subjData)
	for _, want := range []string{"/prompt-o-verse/duck-tux-claymation/", "/prompt-o-verse/duck-tux-lego/", "2 styles", "a duck wearing a tuxedo"} {
		if !strings.Contains(subjHTML, want) {
			t.Errorf("subject page missing %q: %s", want, subjHTML)
		}
	}

	// Regression: the subject page lives one directory deeper
	// (subject/<slug>/) than leaf pages (<slug>/), so a bare "<slug>/<img>"
	// src (correct on the top-level index) resolves to the wrong place here
	// -- it needs the full /prompt-o-verse/ prefix like the <a href>s use.
	for _, wantImg := range []string{
		`src="/prompt-o-verse/duck-tux-claymation/a.png"`,
		`src="/prompt-o-verse/duck-tux-lego/b.png"`,
	} {
		if !strings.Contains(subjHTML, wantImg) {
			t.Errorf("subject page image src broken, expected %q: %s", wantImg, subjHTML)
		}
	}

	// No subject page should exist for a single-leaf subject.
	if _, err := os.Stat(filepath.Join(dir, "subject", "a-lonely-teapot", "index.html")); !os.IsNotExist(err) {
		t.Error("expected no subject page for a single-leaf subject")
	}
}

func TestRenderer_RenderAll_ReRendersOlderSiblingWhenSecondLeafArrives(t *testing.T) {
	// Regression for the exact scenario the founder named: publishing a
	// SECOND leaf under a Subject must add a link to the FIRST leaf's
	// already-existing page too, not just the new one.
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}

	first := Node{Slug: "first-leaf", Label: "stained glass", Subject: "a red panda", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()}
	if err := r.RenderAll([]Node{first}); err != nil {
		t.Fatalf("RenderAll (first leaf only): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "first-leaf", "index.html"))
	if strings.Contains(string(data), "/prompt-o-verse/subject/a-red-panda/") {
		t.Fatal("did not expect a subject link with only 1 leaf published")
	}

	second := Node{Slug: "second-leaf", Label: "claymation", Subject: "a red panda", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()}
	if err := r.RenderAll([]Node{first, second}); err != nil {
		t.Fatalf("RenderAll (both leaves): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "first-leaf", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/prompt-o-verse/subject/a-red-panda/") {
		t.Error("expected the FIRST leaf's page to gain a subject link once a second leaf was published")
	}
}

func TestRenderer_RenderAll_StylePageAlwaysExistsEvenForOneLeaf(t *testing.T) {
	// Founder: "we have no way to go from node up a level like im on the
	// lego baseball card but theres no way for me to go to the lego page to
	// show all those nodes." Unlike Subject pages, Style pages have no
	// >=2 threshold -- Label is required on every node.
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "lego-baseball", Label: "LEGO minifigure", Subject: "baseball card", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
		{Slug: "lego-duck", Label: "LEGO minifigure", Subject: "a duck", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "b.png", PublishedAt: time.Now()},
		{Slug: "solo-style", Label: "watercolor sketchbook", Subject: "a lonely teapot", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "c.png", PublishedAt: time.Now()},
	}
	if err := r.RenderAll(nodes); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Every leaf's <h1> must link up to its style page, regardless of count.
	for _, tc := range []struct{ slug, wantLink string }{
		{"lego-baseball", "/prompt-o-verse/style/lego-minifigure/"},
		{"solo-style", "/prompt-o-verse/style/watercolor-sketchbook/"},
	} {
		data, err := os.ReadFile(filepath.Join(dir, tc.slug, "index.html"))
		if err != nil {
			t.Fatalf("read %s: %v", tc.slug, err)
		}
		if !strings.Contains(string(data), `<h1><a href="`+tc.wantLink+`">`) {
			t.Errorf("%s: expected h1 to link to %s, got: %s", tc.slug, tc.wantLink, data)
		}
	}

	// The style page for a 2-leaf Label lists both, with real leaf links.
	lego, err := os.ReadFile(filepath.Join(dir, "style", "lego-minifigure", "index.html"))
	if err != nil {
		t.Fatalf("read style page: %v", err)
	}
	legoHTML := string(lego)
	for _, want := range []string{"/prompt-o-verse/lego-baseball/", "/prompt-o-verse/lego-duck/", "2 nodes", "LEGO minifigure"} {
		if !strings.Contains(legoHTML, want) {
			t.Errorf("style page missing %q: %s", want, legoHTML)
		}
	}

	// The style page for a Label with only 1 leaf must still exist (no threshold).
	solo, err := os.ReadFile(filepath.Join(dir, "style", "watercolor-sketchbook", "index.html"))
	if err != nil {
		t.Fatalf("expected a style page even for a single-leaf style: %v", err)
	}
	if !strings.Contains(string(solo), `id="leaf-gallery-count">1 node</span> use this style`) {
		t.Errorf("expected singular '1 node' for a single-leaf style: %s", solo)
	}
}

func TestRenderer_RenderIndex_CategoryHeadingLinksToStylePage(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	nodes := []Node{
		{Slug: "solo", Label: "stained glass", Subject: "baseball card", Kind: "historical", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png", PublishedAt: time.Now()},
	}
	if err := r.RenderIndex(nodes); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `<a href="/prompt-o-verse/style/stained-glass/">stained glass</a>`) {
		t.Errorf("expected the index category heading to link to the style page: %s", data)
	}
}

func TestRenderer_UsesThumbnailAndOptimizedFilesWhenPresent(t *testing.T) {
	// Founder: "our webapp loads the optimized or the thumbnail optimized
	// versions if they are available and falls back to full size if they
	// arent." cmd/promptoverse-thumbnails writes these alongside the
	// original -- the renderer must notice them at render time.
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	n := Node{
		Slug: "duck-claymation", Label: "claymation", Subject: "a duck", Kind: "surreal",
		EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "duck-claymation.png", PublishedAt: time.Now(),
	}

	// Simulate the thumbnail tool having already run for this node.
	nodeDir := filepath.Join(dir, n.Slug)
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, ThumbFileName(n.Slug)), []byte("thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, OptimizedFileName(n.Slug)), []byte("optimized"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.RenderAll([]Node{n}); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	leaf, err := os.ReadFile(filepath.Join(nodeDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leaf), `src="`+OptimizedFileName(n.Slug)+`"`) {
		t.Errorf("expected the leaf page's hero image to use the optimized file, got: %s", leaf)
	}

	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), n.Slug+"/"+ThumbFileName(n.Slug)) {
		t.Errorf("expected the index gallery card to use the thumbnail file, got: %s", index)
	}
}

func TestRenderer_FallsBackToOriginalWhenNoGeneratedFilesExist(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	n := Node{
		Slug: "duck-claymation", Label: "claymation", Subject: "a duck", Kind: "surreal",
		EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "duck-claymation.png", PublishedAt: time.Now(),
	}
	if err := r.RenderAll([]Node{n}); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	leaf, err := os.ReadFile(filepath.Join(dir, n.Slug, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(leaf), `src="duck-claymation.png"`) {
		t.Errorf("expected the leaf page to fall back to the original image, got: %s", leaf)
	}

	index, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), n.Slug+"/duck-claymation.png") {
		t.Errorf("expected the index gallery card to fall back to the original image, got: %s", index)
	}
}
