// blog-rerender re-renders every existing blog post + the index to static
// HTML using the current template. One-off tool for template changes that
// need to reach already-published posts, not just future ones (normal
// publishing already re-renders on every POST) -- see BACKLOG.md, the
// STINKIES footer-ad addition, 2026-07-19.
package main

import (
	"flag"
	"log"
	"os"

	"iduna/internal/blog"
)

func main() {
	dbPath := flag.String("db", "./var/blog.db", "blog SQLite db path")
	outDir := flag.String("out", "/var/www/okemily/blog", "rendered output dir")
	flag.Parse()

	store, err := blog.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	posts, err := store.List()
	if err != nil {
		log.Fatalf("list posts: %v", err)
	}

	r := &blog.Renderer{OutputDir: *outDir}
	for _, p := range posts {
		full, err := store.GetBySlug(p.Slug)
		if err != nil {
			log.Printf("WARNING: get %s: %v", p.Slug, err)
			continue
		}
		if err := r.RenderPost(full); err != nil {
			log.Printf("WARNING: render %s: %v", p.Slug, err)
			continue
		}
		log.Printf("rendered %s", p.Slug)
	}
	if err := r.RenderIndex(posts); err != nil {
		log.Fatalf("render index: %v", err)
	}
	log.Printf("done: %d posts + index rendered to %s", len(posts), *outDir)
	os.Exit(0)
}
