package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"iduna/internal/tyler"
)

// TylerHandler serves the TYLER reading room on okemily.com -- a dedicated
// reading experience for TYLER episode scripts, separate from the generic
// blog (which only does paragraph-level "poor man's markdown" and can't
// render TYLER's headers/tables/checklists correctly). Posting requires
// tyler.write; reading is public. Same "publish = immediate re-render"
// contract as BlogHandler.
type TylerHandler struct {
	Store    *tyler.Store
	Renderer *tyler.Renderer
}

// RegisterRoutes wires the handler's routes. createProtected should be
// h.Create wrapped with middleware.RequireAuth(keys) + a tyler.write
// permission check, same shape as BlogHandler.RegisterRoutes.
func (h *TylerHandler) RegisterRoutes(mux *http.ServeMux, createProtected http.Handler) {
	mux.Handle("POST /api/v1/tyler/episodes", createProtected)
	mux.HandleFunc("GET /api/v1/tyler/episodes", h.list)
	mux.HandleFunc("GET /api/v1/tyler/episodes/{slug}", h.get)
}

type createEpisodeRequest struct {
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Series     string `json:"series"`      // e.g. "SERIES X"
	EpisodeTag string `json:"episode_tag"` // e.g. "INTERLUDE, UNNUMBERED"
	Build      string `json:"build"`       // e.g. "0133"
	Body       string `json:"body"`
}

// Create handles POST /api/v1/tyler/episodes -- exported so main.go can
// wrap it with permission middleware, same shape as BlogHandler.Create.
func (h *TylerHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createEpisodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	req.Title = strings.TrimSpace(req.Title)
	req.Series = strings.TrimSpace(req.Series)
	req.EpisodeTag = strings.TrimSpace(req.EpisodeTag)
	req.Build = strings.TrimSpace(req.Build)
	req.Body = strings.TrimSpace(req.Body)

	if req.Slug == "" || !slugRe.MatchString(req.Slug) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slug must be lowercase letters/numbers/hyphens, e.g. 'ask-the-frog'"})
		return
	}
	if req.Title == "" || req.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title and body are required"})
		return
	}

	ep := tyler.Episode{
		Slug:        req.Slug,
		Title:       req.Title,
		Series:      req.Series,
		EpisodeTag:  req.EpisodeTag,
		Build:       req.Build,
		Body:        req.Body,
		PublishedAt: time.Now().UTC(),
	}
	id, err := h.Store.Create(ep)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "slug already exists or could not be saved: " + err.Error()})
		return
	}
	ep.ID = id

	if err := h.Renderer.RenderEpisode(ep); err != nil {
		log.Printf("[tyler] render episode %q failed: %v", ep.Slug, err)
	}
	episodes, err := h.Store.List()
	if err != nil {
		log.Printf("[tyler] list episodes for index render failed: %v", err)
	} else if err := h.Renderer.RenderIndex(episodes); err != nil {
		log.Printf("[tyler] render index failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "published",
		"slug":   ep.Slug,
		"url":    "https://okemily.com/tyler/" + ep.Slug + "/",
	})
}

func (h *TylerHandler) list(w http.ResponseWriter, r *http.Request) {
	episodes, err := h.Store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	out := make([]map[string]any, len(episodes))
	for i, e := range episodes {
		out[i] = map[string]any{
			"slug": e.Slug, "title": e.Title, "series": e.Series,
			"episode_tag": e.EpisodeTag, "build": e.Build, "published_at": e.PublishedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": out})
}

func (h *TylerHandler) get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	e, err := h.Store.GetBySlug(slug)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "episode not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": e.Slug, "title": e.Title, "series": e.Series, "episode_tag": e.EpisodeTag,
		"build": e.Build, "body": e.Body, "published_at": e.PublishedAt,
	})
}
