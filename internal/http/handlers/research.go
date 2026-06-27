// research.go — S137-03 research cache endpoints
//
// GET  /api/v1/research/cache?q=<hash>  — check cache by query hash
// POST /api/v1/research/cache           — store a result
// DELETE /api/v1/research/cache/:hash   — expire an entry
//
// Cache TTL: 48 hours (configurable via expires_at in POST body).
// Auth: requires valid JWT.

package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ResearchHandler struct {
	DB *sql.DB
}

func (h *ResearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/research")

	switch {
	case path == "/cache" && r.Method == http.MethodGet:
		h.getCacheEntry(w, r)
	case path == "/cache" && r.Method == http.MethodPost:
		h.putCacheEntry(w, r)
	case strings.HasPrefix(path, "/cache/") && r.Method == http.MethodDelete:
		hash := strings.TrimPrefix(path, "/cache/")
		h.deleteCacheEntry(w, r, hash)
	default:
		http.NotFound(w, r)
	}
}

func (h *ResearchHandler) getCacheEntry(w http.ResponseWriter, r *http.Request) {
	qHash := r.URL.Query().Get("q")
	if qHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "q required"})
		return
	}

	type cacheRow struct {
		QueryHash  string `json:"query_hash"`
		QueryText  string `json:"query_text"`
		ResultJSON string `json:"result_json"`
		SourceURLs string `json:"source_urls"`
		SourcedAt  string `json:"sourced_at"`
		ExpiresAt  string `json:"expires_at"`
	}
	var row cacheRow
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT query_hash, query_text, result_json, source_urls, sourced_at, expires_at
		 FROM research_cache WHERE query_hash = ? AND expires_at > CURRENT_TIMESTAMP`,
		qHash).Scan(&row.QueryHash, &row.QueryText, &row.ResultJSON, &row.SourceURLs, &row.SourcedAt, &row.ExpiresAt)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cached": true, "entry": row})
}

func (h *ResearchHandler) putCacheEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QueryHash  string `json:"query_hash"`
		QueryText  string `json:"query_text"`
		ResultJSON string `json:"result_json"`
		SourceURLs string `json:"source_urls"`
		TTLHours   int    `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "invalid JSON"})
		return
	}
	if body.QueryHash == "" || body.ResultJSON == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "query_hash and result_json required"})
		return
	}
	ttl := 48
	if body.TTLHours > 0 && body.TTLHours <= 720 {
		ttl = body.TTLHours
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttl) * time.Hour).Format(time.RFC3339)

	_, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO research_cache (query_hash, query_text, result_json, source_urls, expires_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(query_hash) DO UPDATE SET result_json = excluded.result_json,
		   source_urls = excluded.source_urls, sourced_at = CURRENT_TIMESTAMP, expires_at = excluded.expires_at`,
		body.QueryHash, body.QueryText, body.ResultJSON, body.SourceURLs, expiresAt,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "expires_at": expiresAt})
}

func (h *ResearchHandler) deleteCacheEntry(w http.ResponseWriter, r *http.Request, hash string) {
	_, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM research_cache WHERE query_hash = ?`, hash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "detail": fmt.Sprintf("%v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
