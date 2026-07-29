package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func newTestHeroStatsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Mirrors migrations/truestore/202607290001_redgarden_hero_stats.sql exactly.
	_, err = db.Exec(`CREATE TABLE redgarden_hero_stats (
		hero_id        INTEGER  PRIMARY KEY,
		wins           INTEGER  NOT NULL DEFAULT 0,
		losses         INTEGER  NOT NULL DEFAULT 0,
		matches_played INTEGER  NOT NULL DEFAULT 0,
		last_played_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create redgarden_hero_stats table: %v", err)
	}
	return db
}

func redgardenHeroResultHandlerWithAuth(keys *jwt.Keys, db *sql.DB) http.Handler {
	h := &handlers.RedgardenHeroResultHandler{DB: db}
	return middleware.RequireAuth(keys)(middleware.RequirePermission("redgarden.match.write")(h))
}

func postHeroResult(t *testing.T, h http.Handler, token string, heroID int, result string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"hero_id": heroID, "result": result})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/hero-result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRedgardenHeroResult_FirstWinCreatesRow(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestHeroStatsDB(t)
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenHeroResultHandlerWithAuth(keys, db)

	rec := postHeroResult(t, h, token, 5, "win")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Wins          int `json:"wins"`
		Losses        int `json:"losses"`
		MatchesPlayed int `json:"matches_played"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Wins != 1 || resp.Losses != 0 || resp.MatchesPlayed != 1 {
		t.Errorf("got wins=%d losses=%d matches=%d, want 1/0/1", resp.Wins, resp.Losses, resp.MatchesPlayed)
	}
}

func TestRedgardenHeroResult_AccumulatesAcrossMultipleMatches(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestHeroStatsDB(t)
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenHeroResultHandlerWithAuth(keys, db)

	postHeroResult(t, h, token, 7, "win")
	postHeroResult(t, h, token, 7, "win")
	rec := postHeroResult(t, h, token, 7, "loss")

	var resp struct {
		Wins          int `json:"wins"`
		Losses        int `json:"losses"`
		MatchesPlayed int `json:"matches_played"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Wins != 2 || resp.Losses != 1 || resp.MatchesPlayed != 3 {
		t.Errorf("got wins=%d losses=%d matches=%d, want 2/1/3 (accumulated across 3 separate POSTs, not overwritten)",
			resp.Wins, resp.Losses, resp.MatchesPlayed)
	}
}

func TestRedgardenHeroResult_RejectsInvalidResult(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestHeroStatsDB(t)
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenHeroResultHandlerWithAuth(keys, db)

	rec := postHeroResult(t, h, token, 5, "tie")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid result value", rec.Code)
	}
}

func TestRedgardenHeroResult_RejectsOutOfRangeHeroID(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestHeroStatsDB(t)
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenHeroResultHandlerWithAuth(keys, db)

	rec := postHeroResult(t, h, token, -1, "win")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a negative hero_id", rec.Code)
	}
	rec = postHeroResult(t, h, token, 999, "win")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a hero_id past the generous ceiling", rec.Code)
	}
}

func TestRedgardenHeroLeaderboard_SortsByWinRateDescending(t *testing.T) {
	db := newTestHeroStatsDB(t)
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	resultH := redgardenHeroResultHandlerWithAuth(keys, db)

	// Hero 1: 1W/1L (50%). Hero 2: 3W/0L (100%, but fewer total games than hero 1's raw count
	// doesn't matter here -- win rate is the sort key). Hero 3: 1W/3L (25%).
	postHeroResult(t, resultH, token, 1, "win")
	postHeroResult(t, resultH, token, 1, "loss")
	postHeroResult(t, resultH, token, 2, "win")
	postHeroResult(t, resultH, token, 2, "win")
	postHeroResult(t, resultH, token, 2, "win")
	postHeroResult(t, resultH, token, 3, "win")
	postHeroResult(t, resultH, token, 3, "loss")
	postHeroResult(t, resultH, token, 3, "loss")
	postHeroResult(t, resultH, token, 3, "loss")

	leaderboardH := &handlers.RedgardenHeroLeaderboardHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/hero-leaderboard", nil)
	rec := httptest.NewRecorder()
	leaderboardH.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Heroes []struct {
			HeroID  int     `json:"hero_id"`
			WinRate float64 `json:"win_rate"`
		} `json:"heroes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Heroes) != 3 {
		t.Fatalf("got %d heroes, want 3", len(resp.Heroes))
	}
	if resp.Heroes[0].HeroID != 2 || resp.Heroes[1].HeroID != 1 || resp.Heroes[2].HeroID != 3 {
		t.Errorf("hero order = %v, want [2 (100%%), 1 (50%%), 3 (25%%)] -- win rate descending",
			[]int{resp.Heroes[0].HeroID, resp.Heroes[1].HeroID, resp.Heroes[2].HeroID})
	}
	if resp.Heroes[0].WinRate != 1.0 {
		t.Errorf("hero 2's win_rate = %v, want 1.0 (3W/0L)", resp.Heroes[0].WinRate)
	}
}

func TestRedgardenHeroLeaderboard_MinGamesFiltersLowSampleHeroes(t *testing.T) {
	db := newTestHeroStatsDB(t)
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	resultH := redgardenHeroResultHandlerWithAuth(keys, db)

	postHeroResult(t, resultH, token, 9, "win") // only 1 game -- a real, if noisy, 100% win rate
	postHeroResult(t, resultH, token, 10, "win")
	postHeroResult(t, resultH, token, 10, "win")

	leaderboardH := &handlers.RedgardenHeroLeaderboardHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/hero-leaderboard?min-games=2", nil)
	rec := httptest.NewRecorder()
	leaderboardH.ServeHTTP(rec, req)

	var resp struct {
		Heroes []struct {
			HeroID int `json:"hero_id"`
		} `json:"heroes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Heroes) != 1 || resp.Heroes[0].HeroID != 10 {
		t.Fatalf("min-games=2 should exclude hero 9 (only 1 game) -- got %+v", resp.Heroes)
	}
}
