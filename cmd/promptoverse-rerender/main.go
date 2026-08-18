// promptoverse-rerender re-renders every existing Prompt-o-verse node + the
// index + subject pages to static HTML using the current template. One-off
// tool for template/schema changes that need to reach already-published
// nodes, not just future ones (normal publishing already re-renders
// everything via RenderAll on every POST) -- see cmd/blog-rerender for the
// precedent this mirrors.
//
// Two known-stale states this fixes as of 2026-08-17:
//  1. Published-date bug: nodes published by the early VS0 MVP scripts baked
//     a zero PublishedAt into their static page before Store.Create's
//     time.Now() default existed; the DB rows are correct now, the HTML
//     wasn't, because pre-RenderAll publishing only ever re-rendered the ONE
//     new node + the index, never already-published siblings.
//  2. New Subject-grouping feature (RenderAll's subject pages / SubjectLink):
//     every already-published node needs a re-render to pick up its "Applied
//     to: <linked subject>" line and the new /subject/<slug>/ pages.
package main

import (
	"flag"
	"log"
	"os"

	"iduna/internal/promptoverse"
)

func main() {
	dbPath := flag.String("db", "./var/promptoverse.db", "promptoverse SQLite db path")
	outDir := flag.String("out", "/var/www/okemily/prompt-o-verse", "rendered output dir")
	flag.Parse()

	store, err := promptoverse.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	nodes, err := store.List()
	if err != nil {
		log.Fatalf("list nodes: %v", err)
	}

	r := &promptoverse.Renderer{OutputDir: *outDir, Store: store}
	if err := r.RenderAll(nodes); err != nil {
		log.Fatalf("render all: %v", err)
	}
	log.Printf("done: %d nodes + index + subject pages rendered to %s", len(nodes), *outDir)
	os.Exit(0)
}
