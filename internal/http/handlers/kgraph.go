// kgraph.go — S138-06 (partial) EINHORN INDEX query endpoint
//
// GET /api/v1/kgraph/query?entity=<name>&predicate=<rel>&limit=<n>
//
// This handler proxies to the PRRJECT_FATBABY kgraph service (KGRAPH_URL env var).
// If KGRAPH_URL is not set or the service is unreachable, returns 503.
// Auth: requires valid JWT.

package handlers

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type KGraphHandler struct{}

func (h *KGraphHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/kgraph")
	if path != "/query" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.proxyQuery(w, r)
}

func (h *KGraphHandler) proxyQuery(w http.ResponseWriter, r *http.Request) {
	upstream := os.Getenv("KGRAPH_URL")
	if upstream == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code":   "SERVICE_UNAVAILABLE",
			"detail": "KGRAPH_URL not configured",
		})
		return
	}

	targetURL, err := url.Parse(strings.TrimRight(upstream, "/") + "/query?" + r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL.String(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	proxyReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code":   "SERVICE_UNAVAILABLE",
			"detail": "kgraph service unreachable",
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
