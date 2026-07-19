package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"iduna/internal/blog"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// BlogHandler serves okemily.com's blog. Posting (programmatic or manual —
// both go through the same authenticated endpoint) requires the blog.write
// permission; reading is public. Every successful create immediately
// re-renders that post + the index page to static HTML — publishing is
// live the moment the request returns, no separate build step.
type BlogHandler struct {
	Store    *blog.Store
	Renderer *blog.Renderer
}

// RegisterRoutes wires the handler's routes. createProtected should be
// h.Create wrapped with middleware.RequireAuth(keys) + a blog.write
// permission check — constructed in main.go where the JWKS keys are
// available.
func (h *BlogHandler) RegisterRoutes(mux *http.ServeMux, createProtected http.Handler) {
	mux.Handle("POST /api/v1/blog/posts", createProtected)
	mux.HandleFunc("GET /api/v1/blog/posts", h.list)
	mux.HandleFunc("GET /api/v1/blog/posts/{slug}", h.get)
}

type createPostRequest struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Body   string `json:"body"`
	AdLine string `json:"ad_line"` // optional; falls back to a default STINKIES line if empty
	AdCTA  string `json:"ad_cta"`  // optional; falls back to a default CTA if empty
	AdHref string `json:"ad_href"` // optional; falls back to /stinkies.html if empty
}

// Create handles POST /api/v1/blog/posts — exported so main.go can wrap it
// with permission middleware (RequirePermission needs to sit between auth
// and this handler, same shape as every other protected IDUNA route).
func (h *BlogHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	req.Title = strings.TrimSpace(req.Title)
	req.Author = strings.TrimSpace(req.Author)
	req.Body = strings.TrimSpace(req.Body)

	if req.Slug == "" || !slugRe.MatchString(req.Slug) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slug must be lowercase letters/numbers/hyphens, e.g. 'my-first-post'"})
		return
	}
	if req.Title == "" || req.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title and body are required"})
		return
	}
	if req.Author == "" {
		req.Author = "EINHORN_INDUSTRIAL"
	}

	post := blog.Post{
		Slug:        req.Slug,
		Title:       req.Title,
		Author:      req.Author,
		Body:        req.Body,
		AdLine:      strings.TrimSpace(req.AdLine),
		AdCTA:       strings.TrimSpace(req.AdCTA),
		AdHref:      strings.TrimSpace(req.AdHref),
		PublishedAt: time.Now().UTC(),
	}
	id, err := h.Store.Create(post)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "slug already exists or could not be saved: " + err.Error()})
		return
	}
	post.ID = id

	if err := h.Renderer.RenderPost(post); err != nil {
		log.Printf("[blog] render post %q failed: %v", post.Slug, err)
	}
	posts, err := h.Store.List()
	if err != nil {
		log.Printf("[blog] list posts for index render failed: %v", err)
	} else if err := h.Renderer.RenderIndex(posts); err != nil {
		log.Printf("[blog] render index failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "published",
		"slug":   post.Slug,
		"url":    "https://okemily.com/blog/" + post.Slug + "/",
	})
}

func (h *BlogHandler) list(w http.ResponseWriter, r *http.Request) {
	posts, err := h.Store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	out := make([]map[string]any, len(posts))
	for i, p := range posts {
		out[i] = map[string]any{
			"slug": p.Slug, "title": p.Title, "author": p.Author,
			"published_at": p.PublishedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"posts": out})
}

func (h *BlogHandler) get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	p, err := h.Store.GetBySlug(slug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "post not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": p.Slug, "title": p.Title, "author": p.Author,
		"body": p.Body, "published_at": p.PublishedAt,
	})
}
