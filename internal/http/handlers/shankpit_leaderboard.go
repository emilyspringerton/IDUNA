package handlers

// shankpit_leaderboard.go -- WOTAN S550 (EMILY/BACKLOG.md SECTION 550, founder real-time,
// 2026-09-25: "add shankpit to WOTAN ... if you have an iduna account you have a shankpit
// account ... for now we need basic shankpit match tracking"). Public, read-only basic
// kill/death/session leaderboard sourced directly from the players table's existing
// kills/deaths/sessions columns (migrations/truestore/202606200001_players.sql) -- the same
// columns players.go's handleSessionEnd already writes to on every real SHANKPIT match. No new
// table, no new account type: "IDUNA account = SHANKPIT account" is already true today, this is
// just the first WOTAN-facing read of it. Sibling to match_replay.go (DEADWEIGHT's own public
// WOTAN leaderboard read) -- same public/no-auth/rate-limited posture, much simpler shape (one
// query, no ndjson log, no replay subprocess).
//
//	GET /api/v1/shankpit/leaderboard?limit=

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"iduna/internal/http/middleware"
)

// ShankpitLeaderboardHandler serves the route above.
type ShankpitLeaderboardHandler struct {
	DB      *sql.DB
	Limiter *middleware.IPRateLimiter
}

type shankpitLeaderboardEntry struct {
	DisplayName string  `json:"display_name"`
	Kills       int64   `json:"kills"`
	Deaths      int64   `json:"deaths"`
	KDRatio     float64 `json:"kd_ratio"`
	Sessions    int64   `json:"sessions"`
}

func (h *ShankpitLeaderboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow(clientIP(r)) {
		mmoWriteError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	entries, err := h.leaderboard(r.Context(), limit)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=15")
	writeJSON(w, http.StatusOK, map[string]any{"leaderboard": entries})
}

func (h *ShankpitLeaderboardHandler) leaderboard(ctx context.Context, limit int) ([]shankpitLeaderboardEntry, error) {
	rows, err := h.DB.QueryContext(ctx,
		`SELECT display_name, kills, deaths, sessions FROM players
		 WHERE sessions > 0 ORDER BY kills DESC, deaths ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []shankpitLeaderboardEntry{}
	for rows.Next() {
		var e shankpitLeaderboardEntry
		if err := rows.Scan(&e.DisplayName, &e.Kills, &e.Deaths, &e.Sessions); err != nil {
			return nil, err
		}
		if e.Deaths > 0 {
			e.KDRatio = float64(e.Kills) / float64(e.Deaths)
		} else {
			e.KDRatio = float64(e.Kills)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
