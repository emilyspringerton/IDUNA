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

// IntelligenceHandler handles /api/v1/intelligence routes.
//
//   POST /api/v1/intelligence/observe          — submit image for analysis (intelligence.observe)
//   GET  /api/v1/intelligence/observations     — list observations for caller (intelligence.read)
//   GET  /api/v1/intelligence/observations/{id} — get single observation (intelligence.read)
//   PATCH /api/v1/intelligence/observations/{id} — update analysis/status (intelligence.observe)
type IntelligenceHandler struct {
	Store store.IAMStore
}

func (h *IntelligenceHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/v1/intelligence/observe", h)
	mux.Handle("/api/v1/intelligence/observations", h)
	mux.Handle("/api/v1/intelligence/observations/", h)
}

func (h *IntelligenceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/intelligence/observe" && r.Method == http.MethodPost:
		h.observe(w, r)
	case r.URL.Path == "/api/v1/intelligence/observations" && r.Method == http.MethodGet:
		h.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/intelligence/observations/"):
		idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/intelligence/observations/")
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

// POST /api/v1/intelligence/observe
// Body: { "image_data": "<base64>", "media_type": "image/jpeg", "prompt": "optional context" }
func (h *IntelligenceHandler) observe(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "intelligence.observe") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "intelligence.observe permission required",
		})
		return
	}

	var body struct {
		ImageData string `json:"image_data"`
		MediaType string `json:"media_type"`
		Prompt    string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "invalid JSON"})
		return
	}
	if body.ImageData == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "image_data required"})
		return
	}
	if body.MediaType == "" {
		body.MediaType = "image/jpeg"
	}

	agentName := middleware.SubjectFromContext(r.Context())
	obs := auth.CameraObservation{
		AgentName: agentName,
		ImageData: body.ImageData,
		MediaType: body.MediaType,
		Prompt:    body.Prompt,
	}
	id, err := h.Store.CreateCameraObservation(r.Context(), obs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to store observation"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"agent_name": agentName,
		"status":     "pending",
	})
}

// GET /api/v1/intelligence/observations?status=&limit=
func (h *IntelligenceHandler) list(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "intelligence.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "intelligence.read permission required",
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
	// Agents can only read their own observations unless they have apples.admin
	agentName := middleware.SubjectFromContext(r.Context())
	if hasClaimPermission(claims, "apples.admin") {
		if name := q.Get("agent_name"); name != "" {
			agentName = name
		}
	}

	observations, err := h.Store.ListCameraObservations(r.Context(), agentName, status, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to list observations"})
		return
	}
	items := make([]map[string]any, 0, len(observations))
	for _, obs := range observations {
		items = append(items, observationSummary(obs))
	}
	writeJSON(w, http.StatusOK, map[string]any{"observations": items})
}

// GET /api/v1/intelligence/observations/{id}
func (h *IntelligenceHandler) get(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "intelligence.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "intelligence.read permission required",
		})
		return
	}
	obs, err := h.Store.GetCameraObservation(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to get observation"})
		return
	}
	if obs == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "observation not found"})
		return
	}
	if !hasClaimPermission(claims, "apples.admin") && obs.AgentName != middleware.SubjectFromContext(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN", "message": "not your observation"})
		return
	}
	writeJSON(w, http.StatusOK, observationDetail(*obs))
}

// PATCH /api/v1/intelligence/observations/{id}
// Body: { "analysis": "...", "apple_id": 123, "status": "done" }
// Only Emily Prime (intelligence.observe) can call this.
func (h *IntelligenceHandler) patch(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "intelligence.observe") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "intelligence.observe permission required",
		})
		return
	}
	var body struct {
		Analysis string `json:"analysis"`
		AppleID  int64  `json:"apple_id"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "invalid JSON"})
		return
	}
	if body.Status == "" {
		body.Status = "done"
	}
	if err := h.Store.UpdateCameraObservation(r.Context(), id, body.Analysis, body.AppleID, body.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to update observation"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": body.Status})
}

func observationSummary(obs auth.CameraObservation) map[string]any {
	m := map[string]any{
		"id":         obs.ID,
		"agent_name": obs.AgentName,
		"media_type": obs.MediaType,
		"prompt":     obs.Prompt,
		"status":     obs.Status,
		"created_at": obs.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"apple_id":   obs.AppleID,
	}
	if obs.ProcessedAt != nil {
		m["processed_at"] = obs.ProcessedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	// Truncate analysis in list view
	if len(obs.Analysis) > 200 {
		m["analysis_preview"] = obs.Analysis[:197] + "..."
	} else {
		m["analysis_preview"] = obs.Analysis
	}
	return m
}

func observationDetail(obs auth.CameraObservation) map[string]any {
	m := observationSummary(obs)
	m["analysis"] = obs.Analysis
	return m
}
