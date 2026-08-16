package handlers

import (
	"net/http"
	"strconv"
	"time"

	"iduna/internal/statuspage"
)

// StatusPageHandler serves GET /api/v1/status — public, read-only, real
// current status + a live-computed uptime percentage per target. Backing
// data comes from statuspage.Checker's background polling loop (see
// main.go), not synthesized on request.
type StatusPageHandler struct {
	Store   *statuspage.Store
	Targets []statuspage.Target
}

func (h *StatusPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	type targetStatus struct {
		Name        string  `json:"name"`
		Label       string  `json:"label"`
		Up          bool    `json:"up"`
		Checked     bool    `json:"checked"` // false if never checked yet (e.g. right after startup)
		LastChecked string  `json:"last_checked_at,omitempty"`
		Uptime24h   float64 `json:"uptime_24h_percent"`
		Samples24h  int     `json:"uptime_24h_samples"`
	}

	since := time.Now().Add(-24 * time.Hour)
	out := make([]targetStatus, 0, len(h.Targets))
	for _, t := range h.Targets {
		up, found := h.Store.LatestStatus(t.Name)
		pct, samples := h.Store.UptimePercent(t.Name, since)
		ts := targetStatus{
			Name: t.Name, Label: t.Label, Up: up, Checked: found,
			Uptime24h: pct, Samples24h: samples,
		}
		if checkedAt, ok := h.Store.LatestCheckedAt(t.Name); ok {
			ts.LastChecked = checkedAt.Format(time.RFC3339)
		}
		out = append(out, ts)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"services":   out,
		"note":       "Self-reported from the same host running these services — not independent third-party monitoring. If the host itself is down, this page is down with it.",
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// StatusHistoryHandler serves GET /api/v1/status/history?target=<name>&hours=<n> —
// public, read-only raw check history for one target, the incident-timeline
// and latency-graph data source named as still-open in BACKLOG.md S153-11.
// No schema change backs this: statuspage.Store has retained every check
// since day one (see Store.UptimePercent's doc comment); this just exposes
// the same rows directly instead of only a rolled-up percentage.
type StatusHistoryHandler struct {
	Store   *statuspage.Store
	Targets []statuspage.Target
}

const (
	statusHistoryDefaultHours = 24
	statusHistoryMaxHours     = 168 // 7 days — matches the checks table's practical retention horizon; no pruning job exists yet, so this is a request-size guard, not a real data-lifetime limit
	statusHistoryMaxSamples   = 500 // caps response size regardless of interval; 500 samples at the 60s poll interval is ~8.3h, so a 24h/168h request is thinned by the underlying poll cadence, not by this cap, in practice
)

func (h *StatusHistoryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	target := r.URL.Query().Get("target")
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target query param is required"})
		return
	}
	known := false
	label := ""
	for _, t := range h.Targets {
		if t.Name == target {
			known = true
			label = t.Label
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown target"})
		return
	}

	hours := statusHistoryDefaultHours
	if raw := r.URL.Query().Get("hours"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			hours = n
		}
	}
	if hours > statusHistoryMaxHours {
		hours = statusHistoryMaxHours
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	checks, err := h.Store.History(target, since, statusHistoryMaxSamples)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to load history"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"target": target,
		"label":  label,
		"hours":  hours,
		"checks": checks,
	})
}
