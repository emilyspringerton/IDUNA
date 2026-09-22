package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/games"
	"iduna/internal/http/handlers"
)

// TestGamesRegistry_HasPlayOnlyIdentityForBigOAndBrawlpit -- kanban card 123214231 ("big_o
// account creation interface") and SHANKPIT_OS_NORTHSTAR.md item 4 ("BRAWLPIT has no player-
// facing IDUNA identity integration at all"): both must exist with a real, distinct play
// permission, matching internal/games.Config's own doc comment ("one row here... not a new
// handler").
func TestGamesRegistry_HasPlayOnlyIdentityForBigOAndBrawlpit(t *testing.T) {
	for game, wantPerm := range map[string]string{"big_o": "big_o.play", "brawlpit": "brawlpit.play"} {
		cfg, ok := games.Registry[game]
		if !ok {
			t.Fatalf("expected %q to be registered in games.Registry", game)
		}
		if cfg.PlayPerm != wantPerm {
			t.Fatalf("%s: unexpected PlayPerm: %q", game, cfg.PlayPerm)
		}
	}
	// Real, deliberate isolation check: brawlpit.checkpoints.write (an existing, separate,
	// M2M-only permission -- 202609131400_brawlpit_rl_checkpoints.sql) must not have been
	// touched or reused as the new player-facing permission.
	if games.Registry["brawlpit"].PlayPerm == "brawlpit.checkpoints.write" {
		t.Fatal("brawlpit's new player PlayPerm must not collide with the existing M2M checkpoints.write permission")
	}
}

// TestGuestRegisterLoginUpgrade_RealEndToEnd -- exercises the generic guest-account API against
// each game's REAL games.Registry entry (not a synthetic testGames() row): create an account,
// log back in with the returned credentials, and link an email -- the exact flow each game's own
// /play/{game} page drives.
func TestGuestRegisterLoginUpgrade_RealEndToEnd(t *testing.T) {
	for _, game := range []string{"big_o", "brawlpit"} {
		t.Run(game, func(t *testing.T) {
			e := newGameEnv(t)

			pid, secret, token := e.register(t, game, "Watcher")
			if pid == "" || secret == "" || token == "" {
				t.Fatalf("expected real player_id/guest_secret/token from registration")
			}

			code, m, raw := e.do("POST", "/api/v1/games/"+game+"/guest-login", "", map[string]string{
				"player_id": pid, "guest_secret": secret,
			})
			if code != http.StatusOK {
				t.Fatalf("guest-login: %d %s", code, raw)
			}
			if m["player_id"] != pid {
				t.Fatalf("login returned a different player_id: %+v", m)
			}

			code, _, raw = e.do("POST", "/api/v1/games/"+game+"/guest-upgrade", token, map[string]string{
				"email": "watcher-" + game + "@example.com", "password": "correcthorsebatterystaple",
			})
			if code != http.StatusOK {
				t.Fatalf("guest-upgrade: %d %s", code, raw)
			}
		})
	}
}

func TestGameSignupPage_ServesRealHTMLPerGame(t *testing.T) {
	for _, tc := range []struct{ game, title, path string }{
		{"big_o", "BIG_O", "/play/big_o"},
		{"brawlpit", "BRAWLPIT", "/play/brawlpit"},
	} {
		t.Run(tc.game, func(t *testing.T) {
			h := &handlers.GameSignupPageHandler{Game: tc.game, Title: tc.title, Tagline: "Account creation"}
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "guest-register") || !strings.Contains(body, "guest-login") || !strings.Contains(body, "guest-upgrade") {
				t.Fatal("page does not wire up all three real API calls")
			}
			if !strings.Contains(body, `GAME = "`+tc.game+`"`) {
				t.Fatalf("page's API base does not target the right game (%s)", tc.game)
			}
			if !strings.Contains(body, tc.title) {
				t.Fatalf("page does not render its own Title (%s)", tc.title)
			}
		})
	}
}

// TestGameSignupPage_TwoGamesDoNotShareLocalStorageKey -- each page's STORAGE_KEY must be
// game-namespaced, or a browser that's played two of these games would clobber one game's saved
// player id with the other's on shared localStorage.
func TestGameSignupPage_TwoGamesDoNotShareLocalStorageKey(t *testing.T) {
	bigO := httptest.NewRecorder()
	(&handlers.GameSignupPageHandler{Game: "big_o", Title: "BIG_O"}).ServeHTTP(bigO, httptest.NewRequest(http.MethodGet, "/play/big_o", nil))
	brawlpit := httptest.NewRecorder()
	(&handlers.GameSignupPageHandler{Game: "brawlpit", Title: "BRAWLPIT"}).ServeHTTP(brawlpit, httptest.NewRequest(http.MethodGet, "/play/brawlpit", nil))

	if !strings.Contains(bigO.Body.String(), "GAME = \"big_o\"") {
		t.Fatal("big_o page does not set its own GAME constant correctly")
	}
	if !strings.Contains(brawlpit.Body.String(), "GAME = \"brawlpit\"") {
		t.Fatal("brawlpit page does not set its own GAME constant correctly")
	}
}
