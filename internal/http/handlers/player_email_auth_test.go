package handlers_test

import (
	"database/sql"
	"encoding/json"
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
		disabled_at   DATETIME,
		game          TEXT
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
	if _, err := db.Exec(`CREATE TABLE gfd_registration_settings (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		mode       TEXT NOT NULL DEFAULT 'open',
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("create gfd_registration_settings: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO gfd_registration_settings (id, mode) VALUES (1, 'open')`); err != nil {
		t.Fatalf("seed gfd_registration_settings: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE gfd_waitlist (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		email          TEXT NOT NULL UNIQUE,
		display_name   TEXT NOT NULL DEFAULT '',
		password_hash  TEXT NOT NULL,
		character_name TEXT NOT NULL DEFAULT '',
		character_job  TEXT NOT NULL DEFAULT '',
		requested_at   TEXT NOT NULL DEFAULT (datetime('now')),
		approved_at    TEXT
	)`); err != nil {
		t.Fatalf("create gfd_waitlist: %v", err)
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

// TestEmailRegister_GameScopesTheAccount -- S241-01's real fix, the exact
// founder-named scenario ("make sure that account only for papercraft"): a
// registration with game=papercraft stamps that claim into the issued JWT,
// and it's echoed in the response body too.
func TestEmailRegister_GameScopesTheAccount(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	keys := newTestKeys(t)
	h := &handlers.PlayerEmailAuthHandler{DB: db, Keys: keys, Issuer: "test"}

	regBody := `{"email":"gary@example.com","password":"correcthorsebattery","game":"papercraft"}`
	w := doEmailAuth(h, "/api/v1/auth/email/register", regBody)
	if w.Code != http.StatusOK {
		t.Fatalf("register: status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"game":"papercraft"`) {
		t.Errorf("expected game echoed in register response, got: %s", w.Body.String())
	}

	var reg struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &reg); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	claims, err := jwt.Verify(keys, reg.Token)
	if err != nil {
		t.Fatalf("verify register token: %v", err)
	}
	if claims["game"] != "papercraft" {
		t.Fatalf("register JWT game claim = %v, want papercraft", claims["game"])
	}

	// Login afterward must carry the same stored game claim, not the
	// register-only default -- proves it's read back from the players row,
	// not just threaded through the single register request.
	loginBody := `{"email":"gary@example.com","password":"correcthorsebattery"}`
	w = doEmailAuth(h, "/api/v1/auth/email/login", loginBody)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", w.Code, w.Body.String())
	}
	var login struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	claims, err = jwt.Verify(keys, login.Token)
	if err != nil {
		t.Fatalf("verify login token: %v", err)
	}
	if claims["game"] != "papercraft" {
		t.Fatalf("login JWT game claim = %v, want papercraft", claims["game"])
	}
}

// TestEmailRegister_NoGameLeavesClaimAbsent -- backward compatibility: an
// ordinary registration with no game field must not carry any game claim at
// all (not even an empty string) so every existing ticket handler's
// unscoped check keeps treating it exactly as before this fix existed.
func TestEmailRegister_NoGameLeavesClaimAbsent(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	keys := newTestKeys(t)
	h := &handlers.PlayerEmailAuthHandler{DB: db, Keys: keys, Issuer: "test"}

	regBody := `{"email":"unscoped@example.com","password":"correcthorsebattery"}`
	w := doEmailAuth(h, "/api/v1/auth/email/register", regBody)
	if w.Code != http.StatusOK {
		t.Fatalf("register: status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"game"`) {
		t.Errorf("expected no game field in response for an unscoped registration, got: %s", w.Body.String())
	}

	var reg struct{ Token string }
	if err := json.Unmarshal(w.Body.Bytes(), &reg); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	claims, err := jwt.Verify(keys, reg.Token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if _, present := claims["game"]; present {
		t.Fatalf("expected no game claim at all, got %v", claims["game"])
	}
}
