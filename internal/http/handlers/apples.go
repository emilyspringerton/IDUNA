package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// ApplesHandler handles /api/v1/apples routes.
// POST /api/v1/apples                  requires apples.write
// GET  /api/v1/apples                  requires apples.read
// GET  /api/v1/apples/{id}             requires apples.read
// GET  /api/v1/apples/stats/daily-tokens?days=7  requires apples.read
type ApplesHandler struct {
	Store store.IAMStore
}

func (h *ApplesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip /api/v1/apples prefix and check for sub-paths.
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/apples")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodPost:
			h.create(w, r)
		case http.MethodGet:
			h.list(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if path == "stats/daily-tokens" {
		if r.Method == http.MethodGet {
			h.dailyTokenStats(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// path is the id segment
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "invalid apple id",
		})
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.get(w, r, id)
}

// POST /api/v1/apples
func (h *ApplesHandler) create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.write permission required",
		})
		return
	}

	agentID := middleware.SubjectFromContext(r.Context())
	if agentID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":    "UNAUTHORIZED",
			"message": "missing subject",
		})
		return
	}

	var body struct {
		SourceRepo string          `json:"source_repo"`
		RunID      string          `json:"run_id"`
		AppleType  string          `json:"apple_type"`
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		Metadata   json.RawMessage `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "invalid JSON body",
		})
		return
	}
	if body.SourceRepo == "" || body.RunID == "" || body.AppleType == "" || body.Title == "" || body.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "source_repo, run_id, apple_type, title, and body are required",
		})
		return
	}

	apple := auth.AppleRecord{
		AgentID:    agentID,
		SourceRepo: body.SourceRepo,
		RunID:      body.RunID,
		AppleType:  body.AppleType,
		Title:      body.Title,
		Body:       body.Body,
		Metadata:   []byte(body.Metadata),
	}
	id, err := h.Store.AppendApple(r.Context(), apple)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to store apple",
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// GET /api/v1/apples
func (h *ApplesHandler) list(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sourceRepo := q.Get("source_repo")
	appleType := q.Get("apple_type")
	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	apples, err := h.Store.ListApples(r.Context(), agentID, sourceRepo, appleType, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to list apples",
		})
		return
	}

	type appleListItem struct {
		ID         int64  `json:"id"`
		AgentID    string `json:"agent_id"`
		SourceRepo string `json:"source_repo"`
		RunID      string `json:"run_id"`
		AppleType  string `json:"apple_type"`
		Title      string `json:"title"`
		RecordedAt string `json:"recorded_at"`
	}
	items := make([]appleListItem, 0, len(apples))
	for _, a := range apples {
		items = append(items, appleListItem{
			ID:         a.ID,
			AgentID:    a.AgentID,
			SourceRepo: a.SourceRepo,
			RunID:      a.RunID,
			AppleType:  a.AppleType,
			Title:      a.Title,
			RecordedAt: a.RecordedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apples": items})
}

// GET /api/v1/apples/{id}
func (h *ApplesHandler) get(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	apple, err := h.Store.GetApple(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"code":    "NOT_FOUND",
			"message": "apple not found",
		})
		return
	}

	var meta any
	if len(apple.Metadata) > 0 && string(apple.Metadata) != "null" {
		_ = json.Unmarshal(apple.Metadata, &meta)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          apple.ID,
		"agent_id":    apple.AgentID,
		"source_repo": apple.SourceRepo,
		"run_id":      apple.RunID,
		"apple_type":  apple.AppleType,
		"title":       apple.Title,
		"body":        apple.Body,
		"metadata":    meta,
		"recorded_at": apple.RecordedAt.UTC().Format(time.RFC3339Nano),
	})
}

// GET /api/v1/apples/stats/daily-tokens?days=7
// Returns daily token usage aggregated from Apple metadata for sparkline display.
// Response: {"days": 7, "stats": [{"date":"2026-06-14","tokens":12345}, ...]}
func (h *ApplesHandler) dailyTokenStats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}

	stats, err := h.Store.DailyTokenStats(r.Context(), days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to aggregate token stats",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"days":  days,
		"stats": stats,
	})
}

// hasClaimPermission checks the "permissions" claim in the JWT for a specific permission.
// This duplicates the logic in middleware but allows the handler to check multiple
// permissions without middleware wrapping each route individually.
func hasClaimPermission(claims map[string]any, perm string) bool {
	if claims == nil {
		return false
	}
	perms, ok := claims["permissions"]
	if !ok {
		return false
	}
	switch v := perms.(type) {
	case []any:
		for _, p := range v {
			if s, ok := p.(string); ok && s == perm {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == perm {
				return true
			}
		}
	}
	return false
}
