package handlers_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
)

func newShankpitLeaderboardDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE players (
			player_id    TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			kills        INTEGER NOT NULL DEFAULT 0,
			deaths       INTEGER NOT NULL DEFAULT 0,
			sessions     INTEGER NOT NULL DEFAULT 0
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// TestShankpitLeaderboard_OrdersByKillsAndComputesKD -- basic sanity: two players with real
// SHANKPIT sessions rank by kills descending, and K/D is computed correctly including the
// deaths=0 edge case (real, not a divide-by-zero panic).
func TestShankpitLeaderboard_OrdersByKillsAndComputesKD(t *testing.T) {
	db := newShankpitLeaderboardDB(t)
	_, err := db.Exec(`INSERT INTO players (player_id, display_name, kills, deaths, sessions) VALUES
		('p1', 'Alice', 10, 5, 3),
		('p2', 'Bob', 20, 0, 1),
		('p3', 'NeverPlayed', 0, 0, 0)`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := &handlers.ShankpitLeaderboardHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shankpit/leaderboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Leaderboard []struct {
			DisplayName string  `json:"display_name"`
			Kills       int64   `json:"kills"`
			Deaths      int64   `json:"deaths"`
			KDRatio     float64 `json:"kd_ratio"`
			Sessions    int64   `json:"sessions"`
		} `json:"leaderboard"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v, body = %s", err, rec.Body.String())
	}
	if len(out.Leaderboard) != 2 {
		t.Fatalf("len = %d, want 2 (NeverPlayed with 0 sessions excluded), body = %s", len(out.Leaderboard), rec.Body.String())
	}
	if out.Leaderboard[0].DisplayName != "Bob" {
		t.Fatalf("first = %s, want Bob (20 kills > Alice's 10)", out.Leaderboard[0].DisplayName)
	}
	if out.Leaderboard[0].KDRatio != 20 {
		t.Fatalf("Bob's K/D = %f, want 20 (deaths=0 edge case)", out.Leaderboard[0].KDRatio)
	}
	if out.Leaderboard[1].KDRatio != 2 {
		t.Fatalf("Alice's K/D = %f, want 2 (10/5)", out.Leaderboard[1].KDRatio)
	}
}

// TestShankpitLeaderboard_RejectsNonGet -- public read-only endpoint, no write verb.
func TestShankpitLeaderboard_RejectsNonGet(t *testing.T) {
	db := newShankpitLeaderboardDB(t)
	h := &handlers.ShankpitLeaderboardHandler{DB: db}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/leaderboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
