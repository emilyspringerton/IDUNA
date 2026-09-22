package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/games"
	"iduna/internal/http/handlers"
)

// TestBigO_RegisteredInGamesRegistry -- kanban card 123214231's own real registry row: big_o
// must exist with a real play permission, matching internal/games.Config's own doc comment
// ("one row here... not a new handler").
func TestBigO_RegisteredInGamesRegistry(t *testing.T) {
	cfg, ok := games.Registry["big_o"]
	if !ok {
		t.Fatal("expected big_o to be registered in games.Registry")
	}
	if cfg.PlayPerm != "big_o.play" {
		t.Fatalf("unexpected PlayPerm: %q", cfg.PlayPerm)
	}
}

// TestBigO_GuestRegisterLoginUpgrade -- real end-to-end exercise of the generic guest-account
// API against the real big_o registry entry (not a synthetic testGames() row): create an
// account, log back in with the returned credentials, and link an email -- the exact flow
// /play/big_o's own page drives.
func TestBigO_GuestRegisterLoginUpgrade(t *testing.T) {
	e := newGameEnv(t)

	pid, secret, token := e.register(t, "big_o", "Watcher")
	if pid == "" || secret == "" || token == "" {
		t.Fatalf("expected real player_id/guest_secret/token from registration")
	}

	code, m, raw := e.do("POST", "/api/v1/games/big_o/guest-login", "", map[string]string{
		"player_id": pid, "guest_secret": secret,
	})
	if code != http.StatusOK {
		t.Fatalf("guest-login: %d %s", code, raw)
	}
	if m["player_id"] != pid {
		t.Fatalf("login returned a different player_id: %+v", m)
	}

	code, _, raw = e.do("POST", "/api/v1/games/big_o/guest-upgrade", token, map[string]string{
		"email": "watcher@example.com", "password": "correcthorsebatterystaple",
	})
	if code != http.StatusOK {
		t.Fatalf("guest-upgrade: %d %s", code, raw)
	}
}

func TestBigOSignupPage_ServesRealHTML(t *testing.T) {
	h := &handlers.BigOSignupPageHandler{}
	req := httptest.NewRequest(http.MethodGet, "/play/big_o", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "guest-register") || !strings.Contains(body, "guest-login") || !strings.Contains(body, "guest-upgrade") {
		t.Fatal("page does not wire up all three real API calls")
	}
}
