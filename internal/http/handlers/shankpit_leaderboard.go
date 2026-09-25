package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// ShankpitLeaderboardHandler reads the top SHANKPIT players by kills — the
// WOTAN-leaderboard-for-SHANKPIT surface, same pattern as
// RedgardenLeaderboardHandler (redgarden_stats.go). Unlike REDGARDEN,
// SHANKPIT match results already land on the shared `players` table
// (kills/deaths/sessions, written by handleSessionEnd in players.go under
// the shankpit.match.write permission) rather than the genre-agnostic
// player_game_stats table — this handler reads that existing column set
// directly instead of introducing a second, parallel aggregate.
//
// Public (no permission required), same trust level as GET
// /api/v1/players/{id}'s public profile read and the REDGARDEN leaderboard.
//
//	GET /api/v1/shankpit/leaderboard?limit=N
type ShankpitLeaderboardHandler struct {
	DB *sql.DB
}

func (h *ShankpitLeaderboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := parsePositiveInt(l); err == nil && n > 0 {
			limit = min(n, 200)
		}
	}
	if h.DB == nil {
		http.Error(w, "stats not available", http.StatusServiceUnavailable)
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT player_id, display_name, kills, deaths, sessions
		FROM players
		WHERE sessions > 0
		ORDER BY kills DESC, sessions DESC
		LIMIT ?
	`, limit)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type entry struct {
		PlayerID    string  `json:"player_id"`
		DisplayName string  `json:"display_name"`
		Kills       int     `json:"kills"`
		Deaths      int     `json:"deaths"`
		Sessions    int     `json:"sessions"`
		KDRatio     float64 `json:"kd_ratio"`
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.PlayerID, &e.DisplayName, &e.Kills, &e.Deaths, &e.Sessions); err != nil {
			http.Error(w, "db scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if e.Deaths > 0 {
			e.KDRatio = float64(e.Kills) / float64(e.Deaths)
		} else {
			e.KDRatio = float64(e.Kills)
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"game": "shankpit", "leaderboard": entries})
}
