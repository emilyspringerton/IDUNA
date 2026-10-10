package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// RedgardenPlayerHandler is the public per-player profile read WOTAN's REDGARDEN tab renders
// (founder real-time, 2026-10-10: "move all of the REDGARDEN stats to wotan, make it nice like
// the hearthstone decks, keep the profiles different for HS vs REDGARDEN"). Same public,
// no-permission trust level as RedgardenLeaderboardHandler: a display name and win/loss counts,
// nothing private.
//
//	GET /api/v1/redgarden/players/{player_id}
//	  -> {"player_id","display_name","wins","losses","matches","win_rate",
//	      "heroes":[{"hero_id","wins","losses","matches"}],
//	      "recent":[{"hero_id","result","played_at"}]}
//	  -> 404 if the player has never played a REDGARDEN match
type RedgardenPlayerHandler struct {
	DB *sql.DB
}

func (h *RedgardenPlayerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/redgarden/players/"), "/")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "player id must be a UUID", http.StatusBadRequest)
		return
	}
	if h.DB == nil {
		http.Error(w, "stats not available", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()

	var name string
	var wins, losses, matches int
	err := h.DB.QueryRowContext(ctx, `
		SELECT p.display_name, COALESCE(SUM(s.wins),0), COALESCE(SUM(s.losses),0), COALESCE(SUM(s.matches_played),0)
		FROM players p
		JOIN player_game_stats s ON s.player_id = p.player_id
		WHERE p.player_id = ? AND s.game IN ('redgarden', 'redgarden-arena')
		GROUP BY p.player_id, p.display_name`, id).Scan(&name, &wins, &losses, &matches)
	if err == sql.ErrNoRows {
		http.Error(w, "no REDGARDEN record for this player", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	type heroRow struct {
		HeroID  int `json:"hero_id"`
		Wins    int `json:"wins"`
		Losses  int `json:"losses"`
		Matches int `json:"matches"`
	}
	heroes := []heroRow{}
	hrows, err := h.DB.QueryContext(ctx, `
		SELECT hero_id,
		       SUM(CASE WHEN result='win' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN result='loss' THEN 1 ELSE 0 END),
		       COUNT(*)
		FROM redgarden_player_matches
		WHERE player_id = ? AND hero_id >= 0
		GROUP BY hero_id ORDER BY COUNT(*) DESC, hero_id`, id)
	if err == nil {
		defer hrows.Close()
		for hrows.Next() {
			var hr heroRow
			if hrows.Scan(&hr.HeroID, &hr.Wins, &hr.Losses, &hr.Matches) == nil {
				heroes = append(heroes, hr)
			}
		}
	}

	type recentRow struct {
		HeroID   int    `json:"hero_id"`
		Result   string `json:"result"`
		PlayedAt string `json:"played_at"`
	}
	recent := []recentRow{}
	rrows, err := h.DB.QueryContext(ctx, `
		SELECT hero_id, result, played_at FROM redgarden_player_matches
		WHERE player_id = ? ORDER BY id DESC LIMIT 20`, id)
	if err == nil {
		defer rrows.Close()
		for rrows.Next() {
			var rr recentRow
			if rrows.Scan(&rr.HeroID, &rr.Result, &rr.PlayedAt) == nil {
				recent = append(recent, rr)
			}
		}
	}

	rate := 0.0
	if matches > 0 {
		rate = float64(wins) / float64(matches)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"player_id": id, "display_name": name,
		"wins": wins, "losses": losses, "matches": matches, "win_rate": rate,
		"heroes": heroes, "recent": recent,
	})
}
