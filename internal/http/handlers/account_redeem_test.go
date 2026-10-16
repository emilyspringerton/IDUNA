package handlers_test

// Account-level redeem (account_redeem.go): the routes WOTAN's "Redeem a code" page and account
// overview call with the generic SSO token. The point of every test here is the same one the founder
// asked for -- a code redeemed on WOTAN does its whole job (founder flag, 9999 tickets) on the player
// the DEADWEIGHT client signs in as, and the answer says what it did.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"iduna/internal/http/handlers"
)

// doAccount is e.do for the account-level routes (ServeAccount, mounted at /api/v1/redeem and
// /api/v1/me/entitlements in main.go).
func (e *gameEnv) doAccount(method, path, token string, body any) (int, map[string]any, []byte) {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeAccount(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.Bytes()
}

// ssoIdentity registers a generic IDUNA account the way WOTAN's SSO login page does (no game) and
// returns its player_id plus the generic SSO JWT -- exactly what a signed-in WOTAN visitor holds.
func (e *gameEnv) ssoIdentity(t *testing.T, email string) (pid, token string) {
	t.Helper()
	emailAuth := &handlers.PlayerEmailAuthHandler{DB: e.db, Keys: e.keys, Issuer: "test"}
	resp := doEmailAuth(emailAuth, "/api/v1/auth/email/register", `{"email":"`+email+`","password":"correcthorsebattery"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("SSO register %s: %d %s", email, resp.Code, resp.Body.String())
	}
	var b struct {
		Token    string `json:"token"`
		PlayerID string `json:"player_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b.PlayerID, b.Token
}

func (e *gameEnv) ticketsOf(t *testing.T, game, pid string) (tickets float64, founder bool) {
	t.Helper()
	code, m, raw := e.do("GET", "/api/v1/games/"+game+"/players/"+pid+"/tickets", "", nil)
	if code != 200 {
		t.Fatalf("tickets read: %d %s", code, raw)
	}
	f, _ := m["founder"].(bool)
	return m["tickets"].(float64), f
}

func (e *gameEnv) codeUsedBy(t *testing.T, code, game string) string {
	t.Helper()
	var used *string
	if err := e.db.QueryRow(`SELECT used_by_player_id FROM game_claim_codes WHERE code = ? AND game = ?`, code, game).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used == nil {
		return ""
	}
	return *used
}

// The headline case: a guest saved their progress with IDUNA (guest-link-sso), then redeems a founder
// code on WOTAN with the SSO token. It must land on the GUEST's player -- the one the game client
// signs in as -- and the answer must carry everything the page needs to say what happened.
func TestAccountRedeem_SSOTokenFoundsTheLinkedGuestAndSaysWhatItDid(t *testing.T) {
	e := newGameEnv(t)
	pid, _, guestTok := e.register(t, "deadweight", "Gary")
	ssoPID, ssoTok := e.ssoIdentity(t, "gary@example.com")
	if code, _, raw := e.do("POST", "/api/v1/games/deadweight/guest-link-sso", guestTok, map[string]string{"sso_token": ssoTok}); code != 200 {
		t.Fatalf("guest-link-sso: %d %s", code, raw)
	}
	const code = "FNDR1-ABCDE-FGHJK-LMNPQ-RSTUV"
	seedClaimCode(t, e.db, code, "deadweight", 0, 1)
	before, wasFounder := e.ticketsOf(t, "deadweight", pid)
	if wasFounder {
		t.Fatal("test setup: a fresh guest is not a founder")
	}

	// Pasted lower-case with stray whitespace, the way people paste.
	c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "  fndr1-abcde-fghjk-lmnpq-rstuv\n"})
	if c != 200 {
		t.Fatalf("redeem: %d %s", c, raw)
	}
	if m["ok"] != true || m["game"] != "deadweight" || m["game_name"] != "DEADWEIGHT" || m["code"] != code {
		t.Errorf("identity of the redemption wrong: %v", m)
	}
	if m["founder"] != true || m["was_founder"] != false || m["tickets_granted"].(float64) != 0 {
		t.Errorf("founder facts wrong: %v", m)
	}
	if m["tickets"].(float64) != 9999 || m["tickets_before"].(float64) != before {
		t.Errorf("balance facts wrong (want %v -> 9999): %v", before, m)
	}
	if m["player_id"] != pid || m["display_name"] != "Gary" {
		t.Errorf("must land on the linked guest %s/Gary, got %v/%v (the bare SSO identity %s is the wrong target)", pid, m["player_id"], m["display_name"], ssoPID)
	}
	if got := e.codeUsedBy(t, code, "deadweight"); got != pid {
		t.Errorf("code used_by = %q, want the guest %q", got, pid)
	}

	// The game side sees it: the guest's own token reads 9999 and the founder flag.
	if tk, f := e.ticketsOf(t, "deadweight", pid); tk != 9999 || !f {
		t.Errorf("game side after redeem: tickets=%v founder=%v", tk, f)
	}

	// And the overview shows the same story.
	c, ent, raw := e.doAccount("GET", "/api/v1/me/entitlements", ssoTok, nil)
	if c != 200 {
		t.Fatalf("entitlements: %d %s", c, raw)
	}
	gl, _ := ent["games"].([]any)
	if len(gl) != 1 {
		t.Fatalf("want exactly the one game account, got %v", ent["games"])
	}
	g := gl[0].(map[string]any)
	if g["game"] != "deadweight" || g["founder"] != true || g["tickets"].(float64) != 9999 || g["player_id"] != pid || g["display_name"] != "Gary" {
		t.Errorf("games[0] = %v", g)
	}
	rl, _ := ent["redemptions"].([]any)
	if len(rl) != 1 {
		t.Fatalf("want one redemption, got %v", ent["redemptions"])
	}
	r0 := rl[0].(map[string]any)
	if r0["code"] != code || r0["game_name"] != "DEADWEIGHT" || r0["founder"] != true {
		t.Errorf("redemptions[0] = %v", r0)
	}
	if ts, _ := r0["redeemed_at"].(string); ts == "" {
		t.Errorf("redeemed_at missing: %v", r0)
	} else if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("redeemed_at %q is not RFC 3339 (the browser's Date.parse needs it): %v", ts, err)
	}

	// Spent is spent.
	if c, m, _ := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": code}); c != 400 || m["code"] != "code_unavailable" {
		t.Errorf("second redeem: %d %v", c, m)
	}
}

// An identity that has never touched a game is claimed for the code's game on first use (the same rule
// sso-exchange applies, see claimGamePlayer) -- and signing in to the game afterwards must resolve to
// the very same player, or the code would have gone to an account the game never shows.
func TestAccountRedeem_UnscopedIdentityIsClaimedAndSignInResolvesTheSamePlayer(t *testing.T) {
	e := newGameEnv(t)
	pid, ssoTok := e.ssoIdentity(t, "fresh@example.com")
	seedClaimCode(t, e.db, "FRSH1-ABCDE-FGHJK-LMNPQ-RSTUV", "deadweight", 0, 1)

	c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "FRSH1-ABCDE-FGHJK-LMNPQ-RSTUV"})
	if c != 200 || m["player_id"] != pid || m["tickets"].(float64) != 9999 || m["founder"] != true {
		t.Fatalf("redeem on a fresh identity: %d %s", c, raw)
	}
	var game string
	if err := e.db.QueryRow(`SELECT game FROM players WHERE player_id = ?`, pid).Scan(&game); err != nil || game != "deadweight" {
		t.Fatalf("identity should now be scoped to deadweight, got %q (%v)", game, err)
	}
	c, ex, raw := e.do("POST", "/api/v1/games/deadweight/sso-exchange", ssoTok, nil)
	if c != 200 || ex["player_id"] != pid || ex["tickets"].(float64) != 9999 {
		t.Fatalf("sso-exchange after the redeem must resolve to the same player with 9999 tickets: %d %s", c, raw)
	}
}

// A game player token (the native client's, a guest's) is accepted too -- for its own game only.
func TestAccountRedeem_GameTokenWorksForItsOwnGameOnly(t *testing.T) {
	e := newGameEnv(t)
	pid, _, tok := e.register(t, "deadweight", "Ada")
	_, _, otherTok := e.register(t, "othergame", "Zed")
	seedClaimCode(t, e.db, "GTOK1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 7, 0)
	seedClaimCode(t, e.db, "GTOK2-ABCDE-FGHJK-LMNPQ-RSTU2", "deadweight", 7, 0)

	if c, m, _ := e.doAccount("POST", "/api/v1/redeem", otherTok, map[string]string{"code": "GTOK1-ABCDE-FGHJK-LMNPQ-RSTU1"}); c != 403 || m["code"] != "wrong_game_token" {
		t.Fatalf("a token for another game must be refused, got %d %v", c, m)
	}
	if got := e.codeUsedBy(t, "GTOK1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight"); got != "" {
		t.Fatalf("a refused redeem must not spend the code (used_by=%q)", got)
	}
	before, _ := e.ticketsOf(t, "deadweight", pid)
	c, m, raw := e.doAccount("POST", "/api/v1/redeem", tok, map[string]string{"code": "GTOK1-ABCDE-FGHJK-LMNPQ-RSTU1"})
	if c != 200 || m["player_id"] != pid || m["tickets_granted"].(float64) != 7 || m["founder"] != false || m["tickets"].(float64) != before+7 {
		t.Fatalf("own-game token redeem: %d %s", c, raw)
	}
}

// A second founder code on an account that already is one is spent but changes nothing -- the answer
// must say so (was_founder true, balance unchanged) so the page does not shout "Founder unlocked".
func TestAccountRedeem_SecondFounderCodeReportsItChangedNothing(t *testing.T) {
	e := newGameEnv(t)
	_, ssoTok := e.ssoIdentity(t, "twice@example.com")
	seedClaimCode(t, e.db, "TWCE1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 0, 1)
	seedClaimCode(t, e.db, "TWCE2-ABCDE-FGHJK-LMNPQ-RSTU2", "deadweight", 0, 1)
	if c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "TWCE1-ABCDE-FGHJK-LMNPQ-RSTU1"}); c != 200 || m["was_founder"] != false {
		t.Fatalf("first: %d %s", c, raw)
	}
	c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "TWCE2-ABCDE-FGHJK-LMNPQ-RSTU2"})
	if c != 200 || m["was_founder"] != true || m["founder"] != true || m["tickets_before"].(float64) != 9999 || m["tickets"].(float64) != 9999 {
		t.Fatalf("second founder code: %d %s", c, raw)
	}
}

// A plain ticket pack adds to the balance and does not make anyone a founder.
func TestAccountRedeem_TicketPackAddsToTheBalanceWithoutFounder(t *testing.T) {
	e := newGameEnv(t)
	pid, ssoTok := e.ssoIdentity(t, "packs@example.com")
	seedClaimCode(t, e.db, "PACK1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 10, 0)
	c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "PACK1-ABCDE-FGHJK-LMNPQ-RSTU1"})
	if c != 200 {
		t.Fatalf("redeem: %d %s", c, raw)
	}
	if m["tickets_granted"].(float64) != 10 || m["founder"] != false || m["tickets"].(float64) != m["tickets_before"].(float64)+10 {
		t.Fatalf("ticket pack facts wrong: %v", m)
	}
	if _, f := e.ticketsOf(t, "deadweight", pid); f {
		t.Fatal("a ticket pack must not set the founder flag")
	}
}

func TestAccountRedeem_Refusals(t *testing.T) {
	e := newGameEnv(t)
	_, ssoTok := e.ssoIdentity(t, "refuse@example.com")
	seedClaimCode(t, e.db, "REFU1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 0, 1)

	cases := []struct {
		name  string
		token string
		body  any
		want  int
		code  string
	}{
		{"no token", "", map[string]string{"code": "REFU1-ABCDE-FGHJK-LMNPQ-RSTU1"}, 401, ""},
		{"agent token", e.agentToken(t, "deadweight.bot.play"), map[string]string{"code": "REFU1-ABCDE-FGHJK-LMNPQ-RSTU1"}, 403, "agent_token"},
		{"malformed code", ssoTok, map[string]string{"code": "too-short"}, 400, "invalid_code_format"},
		{"empty code", ssoTok, map[string]string{"code": ""}, 400, "invalid_code_format"},
		{"unknown code", ssoTok, map[string]string{"code": "NOSUC-HCODE-12XXX-XXXXX-XXXXX"}, 400, "code_unavailable"},
		{"bad json", ssoTok, "not an object", 400, "invalid_json"},
	}
	for _, tc := range cases {
		c, m, raw := e.doAccount("POST", "/api/v1/redeem", tc.token, tc.body)
		if c != tc.want || (tc.code != "" && m["code"] != tc.code) {
			t.Errorf("%s: got %d %s, want %d %q", tc.name, c, raw, tc.want, tc.code)
		}
		if c >= 400 && c != 401 {
			if s, _ := m["error"].(string); s == "" {
				t.Errorf("%s: every refusal needs human prose in \"error\": %s", tc.name, raw)
			}
		}
	}
	if got := e.codeUsedBy(t, "REFU1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight"); got != "" {
		t.Errorf("no refusal may spend the code (used_by=%q)", got)
	}
	if c, _, _ := e.doAccount("GET", "/api/v1/redeem", ssoTok, nil); c != 405 {
		t.Errorf("GET /redeem: %d, want 405", c)
	}
	if c, _, _ := e.doAccount("POST", "/api/v1/me/entitlements", ssoTok, nil); c != 405 {
		t.Errorf("POST /me/entitlements: %d, want 405", c)
	}
	if c, _, _ := e.doAccount("GET", "/api/v1/me/entitlements", "", nil); c != 401 {
		t.Errorf("anonymous entitlements: %d, want 401", c)
	}
}

// An identity already belonging to a different game cannot take a DEADWEIGHT code -- and the refusal
// must leave the code unspent so it can still go to the right account.
func TestAccountRedeem_IdentityOfAnotherGameIsRefusedAndTheCodeSurvives(t *testing.T) {
	e := newGameEnv(t)
	pid, ssoTok := e.ssoIdentity(t, "othergame@example.com")
	if c, _, raw := e.do("POST", "/api/v1/games/othergame/sso-exchange", ssoTok, nil); c != 200 {
		t.Fatalf("claim for the other game: %d %s", c, raw)
	}
	seedClaimCode(t, e.db, "OTHR1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 0, 1)
	c, m, raw := e.doAccount("POST", "/api/v1/redeem", ssoTok, map[string]string{"code": "OTHR1-ABCDE-FGHJK-LMNPQ-RSTU1"})
	if c != 403 || m["code"] != "other_game" {
		t.Fatalf("want 403 other_game, got %d %s", c, raw)
	}
	if got := e.codeUsedBy(t, "OTHR1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight"); got != "" {
		t.Fatalf("the code must survive the refusal, used_by=%q", got)
	}
	var isFounder int
	_ = e.db.QueryRow(`SELECT is_founder FROM players WHERE player_id = ?`, pid).Scan(&isFounder)
	if isFounder != 0 {
		t.Fatal("a refused redeem must not touch the founder flag")
	}
}

// Two identities racing the same code: exactly one wins, the other is told it is gone, and only the
// winner is a founder. (applyClaimCode's single UPDATE ... WHERE used_by_player_id IS NULL is what
// makes this true; this is the test that would catch a read-then-write regression.)
func TestAccountRedeem_ARacedCodeIsSpentExactlyOnce(t *testing.T) {
	e := newGameEnv(t)
	pidA, tokA := e.ssoIdentity(t, "racea@example.com")
	pidB, tokB := e.ssoIdentity(t, "raceb@example.com")
	const code = "RACE1-ABCDE-FGHJK-LMNPQ-RSTU1"
	seedClaimCode(t, e.db, code, "deadweight", 0, 1)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, tok := range []string{tokA, tokB} {
		wg.Add(1)
		go func(i int, tok string) {
			defer wg.Done()
			statuses[i], _, _ = e.doAccount("POST", "/api/v1/redeem", tok, map[string]string{"code": code})
		}(i, tok)
	}
	wg.Wait()
	wins := 0
	for _, s := range statuses {
		if s == 200 {
			wins++
		} else if s != 400 {
			t.Errorf("the loser must get 400 code_unavailable, got %d", s)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one redeem may win, statuses=%v", statuses)
	}
	winner := pidA
	if statuses[1] == 200 {
		winner = pidB
	}
	if got := e.codeUsedBy(t, code, "deadweight"); got != winner {
		t.Errorf("code belongs to %q, want the winner %q", got, winner)
	}
	var founders int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM players WHERE player_id IN (?, ?) AND is_founder = 1`, pidA, pidB).Scan(&founders)
	if founders != 1 {
		t.Errorf("exactly the winner is a founder, got %d founders", founders)
	}
}

// With nothing redeemed and no game account yet, the overview is two empty lists (not null) -- the
// page renders its empty states from that.
func TestAccountEntitlements_EmptyForAnIdentityWithNoGameAccountsAndNoCodes(t *testing.T) {
	e := newGameEnv(t)
	_, ssoTok := e.ssoIdentity(t, "empty@example.com")
	c, m, raw := e.doAccount("GET", "/api/v1/me/entitlements", ssoTok, nil)
	if c != 200 {
		t.Fatalf("entitlements: %d %s", c, raw)
	}
	if g, ok := m["games"].([]any); !ok || len(g) != 0 {
		t.Errorf("games should be an empty array, got %v", m["games"])
	}
	if r, ok := m["redemptions"].([]any); !ok || len(r) != 0 {
		t.Errorf("redemptions should be an empty array, got %v", m["redemptions"])
	}
	// Reading the overview must never claim the identity for a game.
	var game *string
	_ = e.db.QueryRow(`SELECT game FROM players WHERE player_id = ?`, m["player_id"]).Scan(&game)
	if game != nil && *game != "" {
		t.Errorf("a read-only overview claimed the identity for %q", *game)
	}
}

// The per-game route the native client uses keeps its original answer and gains the additive facts.
func TestRedeem_PerGameRouteGainsAdditiveFacts(t *testing.T) {
	e := newGameEnv(t)
	pid, _, tok := e.register(t, "deadweight", "Ada")
	seedClaimCode(t, e.db, "PRGM1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 0, 1)
	before, _ := e.ticketsOf(t, "deadweight", pid)
	code, m, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "PRGM1-ABCDE-FGHJK-LMNPQ-RSTU1"})
	if code != 200 {
		t.Fatalf("redeem: %d %s", code, raw)
	}
	for _, k := range []string{"ok", "tickets_granted", "founder", "tickets", "tier"} {
		if _, ok := m[k]; !ok {
			t.Errorf("legacy key %q missing: %s", k, raw)
		}
	}
	if m["game"] != "deadweight" || m["was_founder"] != false || m["tickets_before"].(float64) != before {
		t.Errorf("additive facts wrong: %s", raw)
	}
}

// The tickets read the web client polls carries the founder flag.
func TestTicketsRead_CarriesTheFounderFlag(t *testing.T) {
	e := newGameEnv(t)
	pid, _, tok := e.register(t, "deadweight", "Ada")
	if _, f := e.ticketsOf(t, "deadweight", pid); f {
		t.Fatal("not a founder yet")
	}
	seedClaimCode(t, e.db, "FLAG1-ABCDE-FGHJK-LMNPQ-RSTU1", "deadweight", 0, 1)
	if c, _, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "FLAG1-ABCDE-FGHJK-LMNPQ-RSTU1"}); c != 200 {
		t.Fatalf("redeem: %d %s", c, raw)
	}
	if tk, f := e.ticketsOf(t, "deadweight", pid); !f || tk != 9999 {
		t.Fatalf("after a founder code: tickets=%v founder=%v", tk, f)
	}
}
