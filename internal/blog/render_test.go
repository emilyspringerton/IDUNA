package blog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderer_RenderPost_EscapesHTML(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}

	post := Post{
		Slug: "test-post", Title: "Test <script>", Author: "Claude Code",
		Body:        "Paragraph one.\n\n<script>alert(1)</script> paragraph two.",
		PublishedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC),
	}
	if err := r.RenderPost(post); err != nil {
		t.Fatalf("RenderPost: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-post", "index.html"))
	if err != nil {
		t.Fatalf("read rendered file: %v", err)
	}
	html := string(data)

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("body script tag was not escaped — XSS risk")
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("expected escaped script tag in rendered body")
	}
	if !strings.Contains(html, "Paragraph one.") {
		t.Fatal("expected first paragraph in output")
	}
	if !strings.Contains(html, "July 18, 2026") {
		t.Fatal("expected formatted publish date")
	}
}

func TestRenderer_RenderPost_SplitsParagraphs(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	post := Post{Slug: "paras", Title: "T", Author: "A", Body: "One.\n\nTwo.\n\nThree."}
	if err := r.RenderPost(post); err != nil {
		t.Fatalf("RenderPost: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "paras", "index.html"))
	html := string(data)
	for _, want := range []string{"<p>One.</p>", "<p>Two.</p>", "<p>Three.</p>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, html)
		}
	}
}

func TestRenderer_RenderIndex_ListsAllPosts(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{OutputDir: dir}
	posts := []Post{
		{Slug: "a", Title: "Post A", Author: "x", PublishedAt: time.Now()},
		{Slug: "b", Title: "Post B", Author: "y", PublishedAt: time.Now()},
	}
	if err := r.RenderIndex(posts); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	html := string(data)
	if !strings.Contains(html, "Post A") || !strings.Contains(html, "Post B") {
		t.Fatalf("expected both posts in index, got:\n%s", html)
	}
	if !strings.Contains(html, "/blog/a/") || !strings.Contains(html, "/blog/b/") {
		t.Fatal("expected links to both post pages")
	}
}
