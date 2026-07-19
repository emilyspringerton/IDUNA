// dis.go proxies read-only posture/ad-mode data from the DIS (Digital
// Immune System) collector to any EINHORN_INDUSTRIAL surface IDUNA already
// serves — first consumer beyond the original WordPress/EDIS plugin is
// okemily.com's static site + blog. The collector itself is generic (a
// small Go HTTP service reading nginx access logs, see
// EDIS/cmd/dis/main.go) and this box's nginx uses one shared access log
// across every vhost, so the already-running collector instance already
// sees okemily.com's traffic — no second collector needed, this is purely
// the consumption side for a product that isn't PHP/WordPress.
//
// Same fail-open posture as the WordPress plugin's fetch functions: an
// unreachable collector means "healthy" / "svg" (full ads), never an error
// that blocks the page.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// DISHandler is a thin, public, CORS-scoped read-only proxy to the DIS
// collector. It holds no state of its own — every response is a live
// passthrough (or a fail-open default).
type DISHandler struct {
	CollectorURL string // e.g. http://127.0.0.1:9099
	AllowOrigin  []string
	client       *http.Client
}

func (h *DISHandler) httpClient() *http.Client {
	if h.client == nil {
		h.client = &http.Client{Timeout: 2 * time.Second}
	}
	return h.client
}

func (h *DISHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/dis/health", h.proxy("/dis/health", `{"state":"healthy","ad_mode":"svg","hostile_ratio":0,"updated":""}`))
	mux.HandleFunc("GET /api/v1/dis/admode", h.proxy("/dis/admode", "svg"))
	mux.HandleFunc("OPTIONS /api/v1/dis/health", h.preflight)
	mux.HandleFunc("OPTIONS /api/v1/dis/admode", h.preflight)
}

func (h *DISHandler) corsOrigin(r *http.Request) string {
	origin := r.Header.Get("Origin")
	for _, allowed := range h.AllowOrigin {
		if origin == allowed {
			return origin
		}
	}
	return ""
}

func (h *DISHandler) preflight(w http.ResponseWriter, r *http.Request) {
	if origin := h.corsOrigin(r); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	}
	w.WriteHeader(http.StatusNoContent)
}

// proxy returns a handler that GETs collectorPath from the DIS collector
// and streams the body straight through. On any failure (collector down,
// timeout, non-200), it writes fallback instead of an error — fail open,
// never block the page on a posture-service hiccup.
func (h *DISHandler) proxy(collectorPath, fallback string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := h.corsOrigin(r); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Cache-Control", "no-store")

		resp, err := h.httpClient().Get(h.CollectorURL + collectorPath)
		if err != nil {
			writeFallback(w, collectorPath, fallback)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			writeFallback(w, collectorPath, fallback)
			return
		}
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		io.Copy(w, io.LimitReader(resp.Body, 8*1024)) //nolint:errcheck
	}
}

func writeFallback(w http.ResponseWriter, collectorPath, fallback string) {
	if collectorPath == "/dis/health" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(json.RawMessage(fallback)) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(fallback)) //nolint:errcheck
}
