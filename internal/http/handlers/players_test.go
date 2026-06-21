package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
)

func newTestPlayersDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE players (
		player_id    TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		provider     TEXT NOT NULL DEFAULT '',
		provider_sub TEXT NOT NULL DEFAULT '',
		email        TEXT,
		kills        INTEGER NOT NULL DEFAULT 0,
		deaths       INTEGER NOT NULL DEFAULT 0,
		sessions     INTEGER NOT NULL DEFAULT 0,
		last_seen    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		registered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (provider, provider_sub)
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func seedPlayer(t *testing.T, db *sql.DB, id, name string, kills, deaths, sessions int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO players (player_id, display_name, provider, provider_sub, kills, deaths, sessions)
		 VALUES (?,?,?,?,?,?,?)`,
		id, name, "test", id, kills, deaths, sessions,
	)
	if err != nil {
		t.Fatalf("seed player %s: %v", id, err)
	}
}

func TestPlayersListEmpty(t *testing.T) {
	db := newTestPlayersDB(t)
	defer db.Close()
	h := &handlers.PlayersHandler{DB: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result []any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty list, got %d items", len(result))
	}
}

func TestPlayersListSorted(t *testing.T) {
	db := newTestPlayersDB(t)
	defer db.Close()
	seedPlayer(t, db, "p1", "Alice", 10, 5, 2)
	seedPlayer(t, db, "p2", "Bob", 30, 20, 5)
	seedPlayer(t, db, "p3", "Charlie", 5, 1, 1)

	h := &handlers.PlayersHandler{DB: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players?sort=kills&limit=3", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 players, got %d", len(result))
	}
	// Bob should be first (30 kills)
	if result[0]["player_id"] != "p2" {
		t.Errorf("expected p2 (Bob) first, got %v", result[0]["player_id"])
	}
	// KD ratio included
	kd := result[1]["kd_ratio"].(float64)
	if fmt.Sprintf("%.1f", kd) != "2.0" {
		t.Errorf("Alice kd_ratio = %.2f, want 2.0", kd)
	}
}

func TestPlayersListLimit(t *testing.T) {
	db := newTestPlayersDB(t)
	defer db.Close()
	for i := 0; i < 10; i++ {
		seedPlayer(t, db, fmt.Sprintf("p%d", i), fmt.Sprintf("Player%d", i), i, 0, 1)
	}
	h := &handlers.PlayersHandler{DB: db}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players?limit=3", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var result []any
	json.NewDecoder(rec.Body).Decode(&result)
	if len(result) != 3 {
		t.Errorf("limit=3: got %d results, want 3", len(result))
	}
}
