package handlers

import (
	"net/http"
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
