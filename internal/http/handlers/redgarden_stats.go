package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

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

// RedgardenHeroResultHandler writes REDGARDEN hero-level match outcomes to
// redgarden_hero_stats (202607290001 migration) -- a cross-player aggregate
// ("how often does anyone playing this hero win"), separate from
// player_game_stats above, which is keyed by player_id, not hero_id. Same
// trust model as RedgardenGameResultHandler: the request body is trusted
// with no server-side verification beyond the permission check, same
// redgarden.match.write permission (not a new, separate one -- both are
// "the game server reporting its own authoritative match outcome," just
// two different aggregates over the same underlying fact).
//
//	POST /api/v1/redgarden/hero-result   (requires redgarden.match.write)
//	  body: {"hero_id": 5, "result": "win"|"loss"}
//	  -> {"updated": true, "wins": N, "losses": N, "matches_played": N}
type RedgardenHeroResultHandler struct {
	DB *sql.DB
}

func (h *RedgardenHeroResultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		HeroID int    `json:"hero_id"`
		Result string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// 0..63 is a generous ceiling against packages/simulation/arena_game.h's real
	// ARENA_HERO_COUNT (28 as of this writing) -- not imported directly (this is a different
	// repo/language with no C header access, same "duplicated by hand" reasoning
	// apps/arena_bot/src/main.c's own ARENA_HERO_COUNT copy already documents for itself), a
	// deliberately loose bound so a future roster growth doesn't need an IDUNA redeploy just to
	// keep accepting valid hero_ids.
	if body.HeroID < 0 || body.HeroID > 63 {
		http.Error(w, "hero_id out of range", http.StatusBadRequest)
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
		INSERT INTO redgarden_hero_stats (hero_id, wins, losses, matches_played, last_played_at)
		VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(hero_id) DO UPDATE SET
			wins = wins + excluded.wins,
			losses = losses + excluded.losses,
			matches_played = matches_played + 1,
			last_played_at = CURRENT_TIMESTAMP
	`, body.HeroID, win, loss)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var wins, losses, matches int
	err = h.DB.QueryRowContext(r.Context(),
		`SELECT wins, losses, matches_played FROM redgarden_hero_stats WHERE hero_id = ?`,
		body.HeroID,
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

// RedgardenHeroLeaderboardHandler reads every hero's aggregate stats, sorted by win rate --
// the "which heroes are strongest" surface the founder asked for directly, meant for the public
// okemily.com page (wotan.okemily.com), same public-no-permission trust level as
// RedgardenLeaderboardHandler above. Win rate is computed here, not stored, so it's always
// consistent with the current wins/losses rather than a second value that could drift out of
// sync with them.
//
//	GET /api/v1/redgarden/hero-leaderboard?min-games=N
type RedgardenHeroLeaderboardHandler struct {
	DB *sql.DB
}

func (h *RedgardenHeroLeaderboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	minGames := 0
	if g := r.URL.Query().Get("min-games"); g != "" {
		if n, err := parsePositiveInt(g); err == nil {
			minGames = n
		}
	}
	if h.DB == nil {
		http.Error(w, "stats not available", http.StatusServiceUnavailable)
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT hero_id, wins, losses, matches_played
		FROM redgarden_hero_stats
		WHERE matches_played >= ?
		ORDER BY (CAST(wins AS REAL) / matches_played) DESC, matches_played DESC
	`, minGames)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type entry struct {
		HeroID        int     `json:"hero_id"`
		Wins          int     `json:"wins"`
		Losses        int     `json:"losses"`
		MatchesPlayed int     `json:"matches_played"`
		WinRate       float64 `json:"win_rate"`
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.HeroID, &e.Wins, &e.Losses, &e.MatchesPlayed); err != nil {
			http.Error(w, "db scan error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if e.MatchesPlayed > 0 {
			e.WinRate = float64(e.Wins) / float64(e.MatchesPlayed)
		}
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"game": "redgarden", "heroes": entries})
}

// Live match state (2026-07-30, founder: "i want to watch the match on my phone web view").
// Deliberately NOT database-backed, unlike every other REDGARDEN stat above -- this is a single
// piece of ephemeral "what's happening right now" state, not a durable per-hero/per-player
// aggregate, so an in-memory holder (mutex-protected, one process-wide value) is the honestly
// correct shape rather than churning SQLite with a write every few seconds for data nobody needs
// to keep. Only one REDGARDEN match runs at a time under the current bot-pool architecture (a
// single 20-slot lobby), so "the latest reported match" is unambiguous -- a genuinely concurrent-
// matches future would need this to become keyed by match/port, not scoped here.
var redgardenLiveMatch struct {
	mu        sync.Mutex
	payload   json.RawMessage
	updatedAt time.Time
}

// redgardenLiveMatchStaleAfter: if arena_server hasn't reported in this long, treat it as "no
// live match" rather than serving a frozen, increasingly-wrong snapshot of a match that already
// ended (or a server that crashed) -- a stale-but-present payload would otherwise look identical
// to a real live one to anything polling this endpoint.
const redgardenLiveMatchStaleAfter = 30 * time.Second

// RedgardenLiveMatchHandler receives a periodic (every few seconds, from apps/arena_server's own
// main tick loop while ARENA_PHASE_LIVE) snapshot of the current match -- phase, resources,
// nodes, towers, and per-hero HP/K/D/Flow -- and holds only the most recent one. Same trust model
// as every other REDGARDEN write handler in this file: the body is trusted with no server-side
// shape validation beyond "is it valid JSON," gated on the same redgarden.match.write permission
// game-result/hero-result already require, since this is the same authoritative game server
// reporting its own state, just a third aggregate over it.
//
//	POST /api/v1/redgarden/live-match   (requires redgarden.match.write)
//	  body: arbitrary JSON object (arena_server's own live-state shape) -> {"stored": true}
type RedgardenLiveMatchHandler struct{}

func (h *RedgardenLiveMatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	redgardenLiveMatch.mu.Lock()
	redgardenLiveMatch.payload = json.RawMessage(body)
	redgardenLiveMatch.updatedAt = time.Now()
	redgardenLiveMatch.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"stored": true})
}

// RedgardenLiveMatchGetHandler serves the most recently reported live-match snapshot -- the
// actual spectator-dashboard surface, meant for a simple polling web page (okemily.com). Public,
// same trust level as the other REDGARDEN leaderboard reads above -- there's nothing here a
// spectator shouldn't already see by watching the match itself.
//
//	GET /api/v1/redgarden/live-match  -> {"live": false} or {"live": true, "updated_at": ..., "match": {...}}
type RedgardenLiveMatchGetHandler struct{}

func (h *RedgardenLiveMatchGetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	redgardenLiveMatch.mu.Lock()
	payload := redgardenLiveMatch.payload
	updatedAt := redgardenLiveMatch.updatedAt
	redgardenLiveMatch.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if payload == nil || time.Since(updatedAt) > redgardenLiveMatchStaleAfter {
		_ = json.NewEncoder(w).Encode(map[string]any{"live": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"live":       true,
		"updated_at": updatedAt.UTC().Format(time.RFC3339),
		"match":      payload,
	})
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
