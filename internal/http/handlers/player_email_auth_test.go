package handlers_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
)

func newTestEmailAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE players (
		player_id     TEXT PRIMARY KEY,
		display_name  TEXT NOT NULL DEFAULT '',
		provider      TEXT NOT NULL DEFAULT '',
		provider_sub  TEXT NOT NULL DEFAULT '',
		email         TEXT,
		last_seen     DATETIME,
		registered_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		disabled_at   DATETIME
	)`); err != nil {
		t.Fatalf("create players: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE player_credentials (
		player_id     TEXT PRIMARY KEY,
		email         TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create player_credentials: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE characters (
		character_id TEXT PRIMARY KEY,
		player_id    TEXT NOT NULL,
		name         TEXT NOT NULL UNIQUE,
		job_main     TEXT NOT NULL DEFAULT 'WAR',
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create characters: %v", err)
	}
	return db
}

func newTestKeys(t *testing.T) *jwt.Keys {
	t.Helper()
	k, err := jwt.GenerateKeys()
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	return k
}

func doEmailAuth(h *handlers.PlayerEmailAuthHandler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestEmailLogin_DisabledAccountRejected covers the first Game Master tool
// (2026-08-05, founder: "a way to disable accounts") -- players.disabled_at
// must actually block login, not just look disabled in the Back Office UI.
func TestEmailLogin_DisabledAccountRejected(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	h := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}

	regBody := `{"email":"gmtest@example.com","password":"correcthorsebattery","character_name":"GMTestChar"}`
	if w := doEmailAuth(h, "/api/v1/auth/email/register", regBody); w.Code != http.StatusOK {
		t.Fatalf("register: status=%d body=%s", w.Code, w.Body.String())
	}

	loginBody := `{"email":"gmtest@example.com","password":"correcthorsebattery"}`
	if w := doEmailAuth(h, "/api/v1/auth/email/login", loginBody); w.Code != http.StatusOK {
		t.Fatalf("login before disable should succeed: status=%d body=%s", w.Code, w.Body.String())
	}

	if _, err := db.Exec(`UPDATE players SET disabled_at=? WHERE player_id IN (SELECT player_id FROM player_credentials WHERE email=?)`,
		"2026-08-05T00:00:00Z", "gmtest@example.com"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	w := doEmailAuth(h, "/api/v1/auth/email/login", loginBody)
	if w.Code != http.StatusForbidden {
		t.Fatalf("login after disable should be rejected with 403, got status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "disabled") {
		t.Errorf("rejection message should mention the account is disabled, got: %s", w.Body.String())
	}

	if _, err := db.Exec(`UPDATE players SET disabled_at=NULL WHERE player_id IN (SELECT player_id FROM player_credentials WHERE email=?)`,
		"gmtest@example.com"); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if w := doEmailAuth(h, "/api/v1/auth/email/login", loginBody); w.Code != http.StatusOK {
		t.Fatalf("login after re-enable should succeed: status=%d body=%s", w.Code, w.Body.String())
	}
}
