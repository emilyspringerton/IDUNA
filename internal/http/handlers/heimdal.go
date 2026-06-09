package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// HeimdalHandler handles /api/v1/heimdal routes.
//
//   POST  /api/v1/heimdal/sprints        — submit requirement (heimdal.submit)
//   GET   /api/v1/heimdal/sprints        — list sprints (heimdal.submit or apples.admin)
//   GET   /api/v1/heimdal/sprints/{id}   — get single sprint
//   PATCH /api/v1/heimdal/sprints/{id}   — update status/criteria (heimdal.process — Emily Prime)
type HeimdalHandler struct {
	Store store.IAMStore
}

func (h *HeimdalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/heimdal/sprints" && r.Method == http.MethodPost:
		h.submit(w, r)
	case r.URL.Path == "/api/v1/heimdal/sprints" && r.Method == http.MethodGet:
		h.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/heimdal/sprints/"):
		idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/heimdal/sprints/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "invalid id"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.get(w, r, id)
		case http.MethodPatch:
			h.patch(w, r, id)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// POST /api/v1/heimdal/sprints
// Body: { "requirement": "raw product requirement text" }
func (h *HeimdalHandler) submit(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "heimdal.submit") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "heimdal.submit permission required",
		})
		return
	}

	var body struct {
		Requirement string `json:"requirement"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "invalid JSON"})
		return
	}
	if strings.TrimSpace(body.Requirement) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "requirement required"})
		return
	}

	agentName := middleware.SubjectFromContext(r.Context())
	id, err := h.Store.CreateSprintItem(r.Context(), auth.SprintItem{
		AgentName:   agentName,
		Requirement: body.Requirement,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to create sprint"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"agent_name": agentName,
		"status":     "pending",
	})
}

// GET /api/v1/heimdal/sprints?status=&limit=
func (h *HeimdalHandler) list(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "heimdal.submit") &&
		!hasClaimPermission(claims, "heimdal.process") &&
		!hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "heimdal.submit permission required",
		})
		return
	}

	q := r.URL.Query()
	status := q.Get("status")
	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	agentName := middleware.SubjectFromContext(r.Context())
	if hasClaimPermission(claims, "apples.admin") || hasClaimPermission(claims, "heimdal.process") {
		if name := q.Get("agent_name"); name != "" {
			agentName = name
		} else {
			agentName = "" // admins and emily-prime see all
		}
	}

	items, err := h.Store.ListSprintItems(r.Context(), agentName, status, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to list sprints"})
		return
	}
	summaries := make([]map[string]any, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, sprintSummary(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": summaries})
}

// GET /api/v1/heimdal/sprints/{id}
func (h *HeimdalHandler) get(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "heimdal.submit") &&
		!hasClaimPermission(claims, "heimdal.process") &&
		!hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "heimdal.submit permission required",
		})
		return
	}
	item, err := h.Store.GetSprintItem(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to get sprint"})
		return
	}
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "sprint not found"})
		return
	}
	agentName := middleware.SubjectFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.admin") && !hasClaimPermission(claims, "heimdal.process") && item.AgentName != agentName {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN", "message": "not your sprint"})
		return
	}
	writeJSON(w, http.StatusOK, sprintDetail(*item))
}

// PATCH /api/v1/heimdal/sprints/{id}
// Body: { "criteria_json": "...", "roadmap_id": "...", "status": "queued", "apple_id": 123 }
// Called by Emily Prime after translating the requirement into RSI criteria.
func (h *HeimdalHandler) patch(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "heimdal.process") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "heimdal.process permission required",
		})
		return
	}
	var body struct {
		CriteriaJSON string `json:"criteria_json"`
		RoadmapID    string `json:"roadmap_id"`
		Status       string `json:"status"`
		AppleID      int64  `json:"apple_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "invalid JSON"})
		return
	}
	if body.Status == "" {
		body.Status = "queued"
	}
	if body.CriteriaJSON == "" {
		body.CriteriaJSON = "[]"
	}
	if err := h.Store.UpdateSprintItem(r.Context(), id, body.CriteriaJSON, body.RoadmapID, body.Status, body.AppleID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to update sprint"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
}

func sprintSummary(item auth.SprintItem) map[string]any {
	req := item.Requirement
	if len(req) > 200 {
		req = req[:197] + "..."
	}
	return map[string]any{
		"id":                  item.ID,
		"agent_name":          item.AgentName,
		"requirement_preview": req,
		"roadmap_id":          item.RoadmapID,
		"status":              item.Status,
		"apple_id":            item.AppleID,
		"created_at":          item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"updated_at":          item.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func sprintDetail(item auth.SprintItem) map[string]any {
	m := sprintSummary(item)
	m["requirement"] = item.Requirement
	var criteria any
	if err := json.Unmarshal([]byte(item.CriteriaJSON), &criteria); err == nil {
		m["criteria"] = criteria
	} else {
		m["criteria"] = []any{}
	}
	return m
}
