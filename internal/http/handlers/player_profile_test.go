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

func makeProfileTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Minimal schema for profile tests.
	_, err = db.Exec(`
CREATE TABLE players (
    player_id    TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL DEFAULT '',
    provider_sub TEXT NOT NULL DEFAULT '',
    email        TEXT,
    kills        INTEGER NOT NULL DEFAULT 0,
    deaths       INTEGER NOT NULL DEFAULT 0,
    sessions     INTEGER NOT NULL DEFAULT 0,
    registered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE characters (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    character_id TEXT NOT NULL UNIQUE,
    player_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    scene_id     INTEGER NOT NULL DEFAULT 0,
    job_main     TEXT NOT NULL DEFAULT 'WAR',
    job_sub      TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedProfilePlayer(t *testing.T, db *sql.DB, playerID, displayName string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO players (player_id, display_name, provider, provider_sub) VALUES (?,?,?,?)`,
		playerID, displayName, "iduna_local", "sub-"+playerID,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlayerProfileNotFound(t *testing.T) {
	db := makeProfileTestDB(t)
	defer db.Close()
	h := &handlers.PlayerProfileHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/nobody/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestPlayerProfileMethodNotAllowed(t *testing.T) {
	db := makeProfileTestDB(t)
	defer db.Close()
	h := &handlers.PlayerProfileHandler{DB: db}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/foo/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", rr.Code)
	}
}

func TestPlayerProfileFound(t *testing.T) {
	db := makeProfileTestDB(t)
	defer db.Close()
	seedProfilePlayer(t, db, "player-001", "Emily")

	h := &handlers.PlayerProfileHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/emily/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		DisplayName string `json:"display_name"`
		Job         string `json:"job"`
		ApplesCount int    `json:"apples_count"`
		LastScene   int    `json:"last_scene"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayName != "Emily" {
		t.Errorf("display_name: want Emily, got %s", resp.DisplayName)
	}
	if resp.ApplesCount != 0 {
		t.Errorf("apples_count: want 0 (no store), got %d", resp.ApplesCount)
	}
}

func TestPlayerProfileWithCharacter(t *testing.T) {
	db := makeProfileTestDB(t)
	defer db.Close()
	seedProfilePlayer(t, db, "player-002", "Dragon")
	_, err := db.Exec(`INSERT INTO characters (character_id, player_id, name, scene_id, job_main, job_sub) VALUES ('char-1', 'player-002', 'Dragon', 5, 'DRG', 'WHM')`)
	if err != nil {
		t.Fatal(err)
	}

	h := &handlers.PlayerProfileHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/dragon/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Job       string `json:"job"`
		LastScene int    `json:"last_scene"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Job != "DRG/WHM" {
		t.Errorf("job: want DRG/WHM, got %s", resp.Job)
	}
	if resp.LastScene != 5 {
		t.Errorf("last_scene: want 5, got %d", resp.LastScene)
	}
}

func TestPlayerProfileNoDBServiceUnavailable(t *testing.T) {
	h := &handlers.PlayerProfileHandler{DB: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/anyone/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rr.Code)
	}
}

func TestPlayerProfileFameZeroDefault(t *testing.T) {
	db := makeProfileTestDB(t)
	defer db.Close()
	seedProfilePlayer(t, db, "player-003", "Goblin")

	h := &handlers.PlayerProfileHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/goblin/profile", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var resp struct {
		Fame struct {
			Frequency   int `json:"Frequency"`
			Bloc        int `json:"Bloc"`
			Procurement int `json:"Procurement"`
		} `json:"fame"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Fame.Frequency != 0 || resp.Fame.Bloc != 0 || resp.Fame.Procurement != 0 {
		t.Errorf("fame should be zero when no fame table: %+v", resp.Fame)
	}
}
