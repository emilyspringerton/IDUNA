package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"iduna/internal/promptoverse"
)

// PromptOVerseHandler serves the Prompt-o-verse gallery on okemily.com --
// see internal/promptoverse's own doc comment. Posting requires
// promptoverse.write; reading is public. Same "publish = immediate
// re-render" contract as BlogHandler/TylerHandler.
type PromptOVerseHandler struct {
	Store    *promptoverse.Store
	Renderer *promptoverse.Renderer
}

func (h *PromptOVerseHandler) RegisterRoutes(mux *http.ServeMux, createProtected http.Handler) {
	mux.Handle("POST /api/v1/promptoverse/nodes", createProtected)
	mux.HandleFunc("GET /api/v1/promptoverse/nodes", h.list)
	mux.HandleFunc("GET /api/v1/promptoverse/nodes/{slug}", h.get)
}

type createNodeRequest struct {
	Slug           string            `json:"slug"`
	Label          string            `json:"label"`           // style/subcategory, e.g. "Renaissance oil painting" -- gallery groups by this
	Subject        string            `json:"subject"`         // what the style was applied to, e.g. "baseball card", "Master Chief (Halo)"
	Kind           string            `json:"kind"`            // "historical" | "surreal"
	EZPrompt       string            `json:"ez_prompt"`       // short/bare top-top-level prompt
	ExpandedPrompt string            `json:"expanded_prompt"` // the real prompt the image was generated from
	ImageBase64    string            `json:"image_base64"`    // raw PNG bytes, base64-encoded
	Tags           map[string]string `json:"tags"`
}

// Create handles POST /api/v1/promptoverse/nodes -- exported so main.go can
// wrap it with permission middleware, same shape as TylerHandler.Create.
// Unlike Tyler (pure text), this endpoint also writes the decoded image to
// disk before rendering, since the page template references it as a static
// asset rather than inlining it.
func (h *PromptOVerseHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	req.Label = strings.TrimSpace(req.Label)
	req.Subject = strings.TrimSpace(req.Subject)
	req.Kind = strings.TrimSpace(strings.ToLower(req.Kind))
	req.EZPrompt = strings.TrimSpace(req.EZPrompt)
	req.ExpandedPrompt = strings.TrimSpace(req.ExpandedPrompt)

	if req.Slug == "" || !slugRe.MatchString(req.Slug) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slug must be lowercase letters/numbers/hyphens"})
		return
	}
	if req.Label == "" || req.EZPrompt == "" || req.ExpandedPrompt == "" || req.ImageBase64 == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "label, ez_prompt, expanded_prompt, and image_base64 are required"})
		return
	}
	if req.Kind != "historical" && req.Kind != "surreal" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "kind must be 'historical' or 'surreal'"})
		return
	}

	imageFile := req.Slug + ".png"
	n := promptoverse.Node{
		Slug:           req.Slug,
		Label:          req.Label,
		Subject:        req.Subject,
		Kind:           req.Kind,
		EZPrompt:       req.EZPrompt,
		ExpandedPrompt: req.ExpandedPrompt,
		ImageFile:      imageFile,
		Tags:           req.Tags,
	}
	id, err := h.Store.Create(n)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "slug already exists or could not be saved: " + err.Error()})
		return
	}
	n.ID = id

	if err := writeImage(h.Renderer.OutputDir, req.Slug, imageFile, req.ImageBase64); err != nil {
		log.Printf("[promptoverse] write image for %q failed: %v", n.Slug, err)
	}
	if err := h.Renderer.RenderNode(n); err != nil {
		log.Printf("[promptoverse] render node %q failed: %v", n.Slug, err)
	}
	nodes, err := h.Store.List()
	if err != nil {
		log.Printf("[promptoverse] list nodes for index render failed: %v", err)
	} else if err := h.Renderer.RenderIndex(nodes); err != nil {
		log.Printf("[promptoverse] render index failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "published",
		"slug":   n.Slug,
		"url":    "https://okemily.com/prompt-o-verse/" + n.Slug + "/",
	})
}

func writeImage(outputDir, slug, imageFile, b64 string) error {
	dir := filepath.Join(outputDir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decode image_base64: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, imageFile), data, 0o644)
}

func (h *PromptOVerseHandler) list(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.Store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	out := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		out[i] = map[string]any{
			"slug": n.Slug, "label": n.Label, "subject": n.Subject, "kind": n.Kind,
			"ez_prompt": n.EZPrompt, "expanded_prompt": n.ExpandedPrompt,
			"tags": n.Tags, "published_at": n.PublishedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

func (h *PromptOVerseHandler) get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	n, err := h.Store.GetBySlug(slug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": n.Slug, "label": n.Label, "subject": n.Subject, "kind": n.Kind,
		"ez_prompt": n.EZPrompt, "expanded_prompt": n.ExpandedPrompt,
		"image_file": n.ImageFile, "tags": n.Tags, "published_at": n.PublishedAt,
	})
}
