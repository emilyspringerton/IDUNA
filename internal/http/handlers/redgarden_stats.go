package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// RedgardenGameResultHandler writes REDGARDEN match results (win/loss) to
// player_game_stats — the genre-agnostic table REDGARDEN NORTHSTAR §12
// Phase A flagged as the right shape, rather than corrupting shankpit's
// FPS-specific kills/deaths columns on the `players` table.
//
// Same trust model as shankpit.match.write (see players.go's
// shankpitMatchWritePermission comment): this handler trusts its request
// body with no server-side verification beyond the permission check, so
// only an authoritative source of match results (the REDGARDEN-BOTS agent,
// or the game server itself once granted the same permission) may call it.
//
//	POST /api/v1/redgarden/game-result   (requires redgarden.match.write)
//	  body: {"player_id": "<uuid>", "game": "redgarden", "result": "win"|"loss"}
//	  -> {"updated": true, "wins": N, "losses": N, "matches_played": N}
type RedgardenGameResultHandler struct {
	DB *sql.DB
}

func (h *RedgardenGameResultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		PlayerID string `json:"player_id"`
		Game     string `json:"game"`
		Result   string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if _, err := uuid.Parse(body.PlayerID); err != nil {
		http.Error(w, "player_id must be a valid UUID", http.StatusBadRequest)
		return
	}
	if body.Game == "" {
		http.Error(w, "game is required", http.StatusBadRequest)
		return
	}
	var win, loss int
	switch body.Result {
	case "win":
		win = 1
	case "loss":
		loss = 1
	default:
		http.Error(w, `result must be "win" or "loss"`, http.StatusBadRequest)
		return
	}

	if h.DB == nil {
		http.Error(w, "stats not available", http.StatusServiceUnavailable)
		return
	}
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO player_game_stats (player_id, game, wins, losses, matches_played, last_played_at)
		VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(player_id, game) DO UPDATE SET
			wins = wins + excluded.wins,
			losses = losses + excluded.losses,
			matches_played = matches_played + 1,
			last_played_at = CURRENT_TIMESTAMP
	`, body.PlayerID, body.Game, win, loss)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var wins, losses, matches int
	err = h.DB.QueryRowContext(r.Context(),
		`SELECT wins, losses, matches_played FROM player_game_stats WHERE player_id = ? AND game = ?`,
		body.PlayerID, body.Game,
	).Scan(&wins, &losses, &matches)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"updated":        true,
		"wins":           wins,
		"losses":         losses,
		"matches_played": matches,
	})
}

// RedgardenLeaderboardHandler reads the top players for a given game by
// wins — the actual WOTAN-leaderboard-for-REDGARDEN surface. Public
// (no permission required), same as GET /api/v1/players/{id}'s public
// profile read.
//
//	GET /api/v1/redgarden/leaderboard?limit=N
type RedgardenLeaderboardHandler struct {
	DB *sql.DB
}

func (h *RedgardenLeaderboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		SELECT p.player_id, p.display_name, s.wins, s.losses, s.matches_played
		FROM player_game_stats s
		JOIN players p ON p.player_id = s.player_id
		WHERE s.game = 'redgarden'
		ORDER BY s.wins DESC, s.matches_played DESC
		LIMIT ?
	`, limit)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type entry struct {
		PlayerID      string `json:"player_id"`
		DisplayName   string `json:"display_name"`
		Wins          int    `json:"wins"`
		Losses        int    `json:"losses"`
		MatchesPlayed int    `json:"matches_played"`
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.PlayerID, &e.DisplayName, &e.Wins, &e.Losses, &e.MatchesPlayed); err != nil {
			http.Error(w, "db scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"game": "redgarden", "leaderboard": entries})
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, sql.ErrNoRows
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
