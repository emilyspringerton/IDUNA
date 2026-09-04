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
	"iduna/internal/http/middleware"
)

func newTestUserSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE user_settings (
		user_id       TEXT PRIMARY KEY,
		high_contrast INTEGER NOT NULL DEFAULT 0,
		updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("create user_settings: %v", err)
	}
	return db
}

func settingsRequest(t *testing.T, h http.Handler, token, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/settings/me", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// WOTAN-24412/ACCESSABILITY-14441: real per-user settings, first real setting is high_contrast.

func TestUserSettings_GetDefaultsToFalse_NoRowYet(t *testing.T) {
	db := newTestUserSettingsDB(t)
	defer db.Close()
	keys, _ := jwt.GenerateKeys()
	token := makePlayerToken(t, keys, "user-1")
	h := middleware.RequireAuth(keys)(&handlers.UserSettingsHandler{DB: db})

	w := settingsRequest(t, h, token, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got handlers.UserSettings
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.HighContrast {
		t.Fatal("expected high_contrast to default to false with no row yet")
	}
}

func TestUserSettings_PatchThenGet_RealRoundTrip(t *testing.T) {
	db := newTestUserSettingsDB(t)
	defer db.Close()
	keys, _ := jwt.GenerateKeys()
	token := makePlayerToken(t, keys, "user-2")
	h := middleware.RequireAuth(keys)(&handlers.UserSettingsHandler{DB: db})

	patchW := settingsRequest(t, h, token, http.MethodPatch, `{"high_contrast":true}`)
	if patchW.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", patchW.Code, patchW.Body.String())
	}

	getW := settingsRequest(t, h, token, http.MethodGet, "")
	var got handlers.UserSettings
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.HighContrast {
		t.Fatal("expected high_contrast to be true after a real PATCH+GET round trip")
	}
}

func TestUserSettings_PatchIsPerUser_NotGlobal(t *testing.T) {
	db := newTestUserSettingsDB(t)
	defer db.Close()
	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.UserSettingsHandler{DB: db})

	tokenA := makePlayerToken(t, keys, "user-a")
	tokenB := makePlayerToken(t, keys, "user-b")

	settingsRequest(t, h, tokenA, http.MethodPatch, `{"high_contrast":true}`)

	getB := settingsRequest(t, h, tokenB, http.MethodGet, "")
	var gotB handlers.UserSettings
	json.Unmarshal(getB.Body.Bytes(), &gotB)
	if gotB.HighContrast {
		t.Fatal("expected user-b's own setting to be unaffected by user-a's real PATCH")
	}
}

func TestUserSettings_NoAuth_Rejected(t *testing.T) {
	db := newTestUserSettingsDB(t)
	defer db.Close()
	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.UserSettingsHandler{DB: db})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/me", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("expected an unauthenticated request to be rejected, not served")
	}
}
