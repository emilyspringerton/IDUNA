// promptoverse-thumbnails is a cron-driven, idempotent background job:
// generates a thumbnail and a JPEG-compressed "optimized" version of every
// published Prompt-o-verse node's original PNG, using ImageMagick's
// `convert`. Meant to run on a schedule (systemd timer / cron), not as
// part of the publish path itself.
//
// Founder direction (2026-08-18): "when we first gen go ahead and load the
// whole image and scale it with styles or whatever we are doing (it loads
// slow its fine) ok but at some point a tootally background process
// optimizes ... for now we just need 2 or 3 versions, original, thumbnail,
// optimized (full size with jpg compression or whatever would be
// fastest)... imagemagic goes on a cron and adds thumbnails and optimized
// versions and then our webapp loads the optimized or the thumbnail
// optimized versions if they are available and falls back to full size if
// they arent."
//
// Originals (1024x1024 PNG, ~1.5-1.8MB as generated) are never touched or
// deleted -- they stay the permanent fallback. This tool only ever ADDS
// <slug>-thumb.jpg and <slug>-optimized.jpg alongside them, and skips any
// pair that already exists, so re-running on every cron tick only costs
// work proportional to what's new since the last run.
//
// After processing, re-renders every page (same Renderer.RenderAll used by
// cmd/promptoverse-rerender) so pages generated before a node's thumb/
// optimized files existed pick up the smaller images without a separate
// manual step.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"iduna/internal/promptoverse"
)

func main() {
	dbPath := flag.String("db", "./var/promptoverse.db", "promptoverse SQLite db path")
	outDir := flag.String("out", "/var/www/okemily/prompt-o-verse", "rendered output dir")
	thumbSize := flag.Int("thumb-size", 320, "thumbnail edge length in pixels (square, cropped to fill)")
	thumbQuality := flag.Int("thumb-quality", 82, "JPEG quality for thumbnails, 1-100")
	optimizedQuality := flag.Int("optimized-quality", 85, "JPEG quality for the full-size optimized version, 1-100")
	flag.Parse()

	if _, err := exec.LookPath("convert"); err != nil {
		log.Fatalf("ImageMagick 'convert' not found on PATH: %v", err)
	}

	store, err := promptoverse.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	nodes, err := store.List()
	if err != nil {
		log.Fatalf("list nodes: %v", err)
	}

	generated, alreadyDone, failed, missingOriginal := 0, 0, 0, 0
	for _, n := range nodes {
		dir := filepath.Join(*outDir, n.Slug)
		orig := filepath.Join(dir, n.ImageFile)
		if _, err := os.Stat(orig); err != nil {
			log.Printf("WARNING: %s: original image missing (%s), skipping", n.Slug, orig)
			missingOriginal++
			continue
		}

		thumbPath := filepath.Join(dir, promptoverse.ThumbFileName(n.Slug))
		if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
			if err := runConvert(orig, thumbPath,
				"-resize", fmt.Sprintf("%dx%d^", *thumbSize, *thumbSize),
				"-gravity", "center",
				"-extent", fmt.Sprintf("%dx%d", *thumbSize, *thumbSize),
				"-quality", fmt.Sprintf("%d", *thumbQuality),
				"-strip",
			); err != nil {
				log.Printf("FAILED thumbnail for %s: %v", n.Slug, err)
				failed++
			} else {
				generated++
			}
		} else {
			alreadyDone++
		}

		optPath := filepath.Join(dir, promptoverse.OptimizedFileName(n.Slug))
		if _, err := os.Stat(optPath); os.IsNotExist(err) {
			if err := runConvert(orig, optPath,
				"-quality", fmt.Sprintf("%d", *optimizedQuality),
				"-strip",
			); err != nil {
				log.Printf("FAILED optimized version for %s: %v", n.Slug, err)
				failed++
			} else {
				generated++
			}
		} else {
			alreadyDone++
		}
	}

	log.Printf("done: %d generated, %d already existed, %d failed, %d missing originals (of %d nodes)",
		generated, alreadyDone, failed, missingOriginal, len(nodes))

	if generated == 0 {
		log.Printf("nothing new -- skipping re-render")
		return
	}

	r := &promptoverse.Renderer{OutputDir: *outDir}
	if err := r.RenderAll(nodes); err != nil {
		log.Fatalf("re-render after thumbnail generation: %v", err)
	}
	log.Printf("re-rendered %d nodes + index + subject/style pages", len(nodes))
}

func runConvert(src, dst string, args ...string) error {
	full := append([]string{src}, args...)
	full = append(full, dst)
	cmd := exec.Command("convert", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("convert %s: %w (%s)", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
