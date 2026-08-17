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
