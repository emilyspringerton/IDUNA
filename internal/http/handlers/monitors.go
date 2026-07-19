package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// monitorSlugRe matches a caller-supplied slug for get-or-create semantics
// in create() below — same shape convention as blog.go's slugRe.
var monitorSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MonitorsHandler handles check-in monitor routes.
//
// Public (no auth):
//   POST /api/v1/monitors/checkin/:slug  — record a heartbeat check-in
//   GET  /api/v1/monitors/checkin/:slug  — same (allows simple curl/wget probes)
//
// Granular RBAC permissions:
//   monitors.read   — GET /monitors (list) + GET /monitors/:id
//   monitors.create — POST /monitors (create)
//   monitors.delete — DELETE /monitors/:id
//   monitors.alert  — GET /monitors/overdue + POST /monitors/:id/alerted + POST /monitors/:id/recover
//   monitors.admin  — all of the above
//   monitors.write  — backward-compat alias: implies create+delete+alert
//
// Monitor kinds:
//   heartbeat — default; alert if no check-in within timeout+grace
//   cron      — same alerting; signals a scheduled job (timeout_seconds = expected interval)
//   deadman   — zero-tolerance; grace_seconds ignored; alert immediately after timeout
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

	// Overdue list.
	if path == "overdue" {
		if r.Method == http.MethodGet {
			h.listOverdue(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// Single monitor by ID — path may be ":id", ":id/alerted", or ":id/recover".
	if path != "" {
		parts := strings.SplitN(path, "/", 2)
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST"})
			return
		}
		if len(parts) == 2 {
			switch parts[1] {
			case "alerted":
				if r.Method == http.MethodPost {
					h.markAlerted(w, r, id)
				} else {
					http.NotFound(w, r)
				}
			case "recover":
				if r.Method == http.MethodPost {
					h.recover(w, r, id)
				} else {
					http.NotFound(w, r)
				}
			default:
				http.NotFound(w, r)
			}
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.getMonitor(w, r, id)
		case http.MethodPatch:
			h.updateMonitor(w, r, id)
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

// monitorPerm returns true if the claims carry the named permission OR monitors.admin
// OR monitors.write (backward-compat alias for create+delete+alert).
func monitorPerm(claims map[string]any, perm string) bool {
	if hasClaimPermission(claims, "monitors.admin") {
		return true
	}
	if hasClaimPermission(claims, perm) {
		return true
	}
	// monitors.write is a backward-compat alias that implies create+delete+alert.
	if perm == "monitors.create" || perm == "monitors.delete" || perm == "monitors.alert" {
		return hasClaimPermission(claims, "monitors.write")
	}
	return false
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
		"kind":       m.Kind,
		"checked_in": now.Format(time.RFC3339),
	})
}

func (h *MonitorsHandler) listOverdue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.alert") && !hasClaimPermission(claims, "monitors.read") {
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
	if !monitorPerm(claims, "monitors.read") {
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

func (h *MonitorsHandler) getMonitor(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	m, err := h.Store.GetMonitorByID(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	if m == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitor": m})
}

func (h *MonitorsHandler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.create") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}

	var body struct {
		Name              string `json:"name"`
		Slug              string `json:"slug"`
		Kind              string `json:"kind"`
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "name required"})
		return
	}
	if body.Kind == "" {
		body.Kind = "heartbeat"
	}
	if body.Kind != "heartbeat" && body.Kind != "cron" && body.Kind != "deadman" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":   "BAD_REQUEST",
			"detail": "kind must be heartbeat, cron, or deadman",
		})
		return
	}
	if body.TimeoutSeconds <= 0 {
		body.TimeoutSeconds = 3600
	}
	if body.GraceSeconds < 0 {
		body.GraceSeconds = 60
	}

	// A caller-supplied slug means "get or create" — return the existing
	// monitor if one already has this slug, instead of creating a
	// duplicate. Without this, EnsureCronMonitor-style callers (post the
	// same slug on every process startup, expecting idempotency) silently
	// accumulated a new monitor with a random slug on every restart, while
	// checkins posted to the slug they actually asked for 404'd forever
	// because no monitor ever had it.
	slug := strings.ToLower(strings.TrimSpace(body.Slug))
	if slug != "" {
		if !monitorSlugRe.MatchString(slug) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code": "BAD_REQUEST", "detail": "slug must be lowercase letters/numbers/hyphens",
			})
			return
		}
		existing, err := h.Store.GetMonitorBySlug(ctx, slug)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
			return
		}
		if existing != nil {
			writeJSON(w, http.StatusOK, map[string]any{"monitor": existing})
			return
		}
	} else {
		var err error
		slug, err = generateMonitorSlug()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
			return
		}
	}

	owner := middleware.SubjectFromContext(ctx)
	m := auth.Monitor{
		Name:              body.Name,
		Slug:              slug,
		Kind:              body.Kind,
		TimeoutSeconds:    body.TimeoutSeconds,
		GraceSeconds:      body.GraceSeconds,
		Owner:             owner,
		AlertSlackChannel: body.AlertSlackChannel,
		AlertEmail:        body.AlertEmail,
	}
	id, err := h.Store.CreateMonitor(ctx, m)
	if err != nil {
		// Race: another request created a monitor with this slug between
		// our lookup and this insert (the DB's UNIQUE constraint on slug
		// is the backstop). Re-fetch and return it rather than a bare 500.
		if slug != "" {
			if existing, gerr := h.Store.GetMonitorBySlug(ctx, slug); gerr == nil && existing != nil {
				writeJSON(w, http.StatusOK, map[string]any{"monitor": existing})
				return
			}
		}
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

func (h *MonitorsHandler) updateMonitor(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.create") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}

	existing, err := h.Store.GetMonitorByID(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})
		return
	}

	var body struct {
		Name              *string `json:"name"`
		Kind              *string `json:"kind"`
		TimeoutSeconds    *int    `json:"timeout_seconds"`
		GraceSeconds      *int    `json:"grace_seconds"`
		AlertSlackChannel *string `json:"alert_slack_channel"`
		AlertEmail        *string `json:"alert_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST"})
		return
	}

	if body.Name != nil && *body.Name != "" {
		existing.Name = *body.Name
	}
	if body.Kind != nil {
		if *body.Kind != "heartbeat" && *body.Kind != "cron" && *body.Kind != "deadman" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code":   "BAD_REQUEST",
				"detail": "kind must be heartbeat, cron, or deadman",
			})
			return
		}
		existing.Kind = *body.Kind
	}
	if body.TimeoutSeconds != nil && *body.TimeoutSeconds > 0 {
		existing.TimeoutSeconds = *body.TimeoutSeconds
	}
	if body.GraceSeconds != nil && *body.GraceSeconds >= 0 {
		existing.GraceSeconds = *body.GraceSeconds
	}
	if body.AlertSlackChannel != nil {
		existing.AlertSlackChannel = *body.AlertSlackChannel
	}
	if body.AlertEmail != nil {
		existing.AlertEmail = *body.AlertEmail
	}

	if err := h.Store.UpdateMonitor(ctx, *existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitor": existing})
}

// markAlerted records that an alert has been sent (prevents duplicate alerts).
func (h *MonitorsHandler) markAlerted(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.alert") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	if err := h.Store.MarkMonitorAlerted(ctx, id, time.Now().UTC()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// recover manually clears alerted_at and sets status=healthy without a check-in.
// Used when a service recovers via a mechanism other than the check-in URL.
func (h *MonitorsHandler) recover(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.alert") {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "FORBIDDEN"})
		return
	}
	if err := h.Store.RecoverMonitor(ctx, id, time.Now().UTC()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recovered": true})
}

func (h *MonitorsHandler) deleteMonitor(w http.ResponseWriter, r *http.Request, id int64) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if !monitorPerm(claims, "monitors.delete") {
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
