package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// MonitorsHandler handles check-in monitor routes.
//
// Public (no auth):
//   POST /api/v1/monitors/checkin/:slug  — record a heartbeat check-in
//   GET  /api/v1/monitors/checkin/:slug  — same (allows simple curl/wget probes)
//
// Auth-gated (monitors.read / monitors.write):
//   GET    /api/v1/monitors          — list monitors (monitors.read)
//   POST   /api/v1/monitors          — create monitor (monitors.write)
//   GET    /api/v1/monitors/overdue  — list overdue monitors (monitors.read)
//   DELETE /api/v1/monitors/:id      — delete monitor (monitors.write)
type MonitorsHandler struct {
	Store store.IAMStore
}

func (h *MonitorsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/monitors")
	path = strings.TrimPrefix(path, "/")

	// Public check-in endpoint — no auth required.
	if strings.HasPrefix(path, "checkin/") {
		slug := strings.TrimPrefix(path, "checkin/")
		if r.Method == http.MethodPost || r.Method == http.MethodGet {
			h.checkin(w, r, slug)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// Overdue list — auth checked inside.
	if path == "overdue" {
		if r.Method == http.MethodGet {
			h.listOverdue(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// Single monitor by ID — path may be ":id" or ":id/alerted".
	if path != "" {
		parts := strings.SplitN(path, "/", 2)
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST"})
			return
		}
		if len(parts) == 2 && parts[1] == "alerted" {
			if r.Method == http.MethodPost {
				h.markAlerted(w, r, id)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		switch r.Method {
		case http.MethodDelete:
			h.deleteMonitor(w, r, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	// Collection.
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *MonitorsHandler) checkin(w http.ResponseWriter, r *http.Request, slug string) {
	ctx := r.Context()
	now := time.Now().UTC()

	m, err := h.Store.GetMonitorBySlug(ctx, slug)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	if m == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})
		return
	}
	if err := h.Store.RecordCheckin(ctx, slug, now); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"monitor":    m.Name,
		"checked_in": now.Format(time.RFC3339),
	})
}

func (h *MonitorsHandler) listOverdue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !hasClaimPermission(claims, "monitors.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	monitors, err := h.Store.ListOverdueMonitors(ctx, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	if monitors == nil {
		monitors = []auth.Monitor{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": monitors})
}

func (h *MonitorsHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !hasClaimPermission(claims, "monitors.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	owner := r.URL.Query().Get("owner")
	monitors, err := h.Store.ListMonitors(ctx, owner)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	if monitors == nil {
		monitors = []auth.Monitor{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": monitors})
}

func (h *MonitorsHandler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !hasClaimPermission(claims, "monitors.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}

	var body struct {
		Name              string `json:"name"`
		TimeoutSeconds    int    `json:"timeout_seconds"`
		GraceSeconds      int    `json:"grace_seconds"`
		AlertSlackChannel string `json:"alert_slack_channel"`
		AlertEmail        string `json:"alert_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST"})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":   "BAD_REQUEST",
			"detail": "name required",
		})
		return
	}
	if body.TimeoutSeconds <= 0 {
		body.TimeoutSeconds = 3600
	}
	if body.GraceSeconds < 0 {
		body.GraceSeconds = 60
	}

	slug, err := generateMonitorSlug()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}

	owner := middleware.SubjectFromContext(ctx)
	m := auth.Monitor{
		Name:              body.Name,
		Slug:              slug,
		TimeoutSeconds:    body.TimeoutSeconds,
		GraceSeconds:      body.GraceSeconds,
		Owner:             owner,
		AlertSlackChannel: body.AlertSlackChannel,
		AlertEmail:        body.AlertEmail,
	}
	id, err := h.Store.CreateMonitor(ctx, m)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	m.ID = id
	m.Status = "unknown"
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	writeJSON(w, http.StatusCreated, map[string]any{"monitor": m})
}

// markAlerted records that an alert has been sent for a monitor (prevents duplicate alerts).
func (h *MonitorsHandler) markAlerted(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !hasClaimPermission(claims, "monitors.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	if err := h.Store.MarkMonitorAlerted(ctx, id, time.Now().UTC()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *MonitorsHandler) deleteMonitor(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !hasClaimPermission(claims, "monitors.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	if err := h.Store.DeleteMonitor(ctx, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func generateMonitorSlug() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
