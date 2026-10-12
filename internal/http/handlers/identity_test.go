package handlers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	googleverify "iduna/internal/auth/google"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/store"

	"database/sql"
)

const idRedirect = "https://wotan.okemily.com/account/"

// idEnv is the clubs + Back Office test env plus the identity OAuth handler with its network seams swapped for
// an in-memory provider (authorization code -> user).
type idEnv struct {
	*adminClubsEnv
	oauth   *handlers.IdentityOAuthHandler
	hsh     *handlers.HSHandler
	players *handlers.PlayerEmailAuthHandler
	users   map[string]*handlers.OAuthUser // code -> provider user
}

func newIDEnv(t *testing.T) *idEnv {
	t.Helper()
	a := newAdminClubsEnv(t)
	e := &idEnv{adminClubsEnv: a, users: map[string]*handlers.OAuthUser{}}
	e.hsh = a.hs.(*handlers.HSHandler)
	e.players = &handlers.PlayerEmailAuthHandler{DB: a.db, Keys: a.keys, Issuer: "test"}
	e.oauth = &handlers.IdentityOAuthHandler{
		DB: a.db, Keys: a.keys, Players: e.players, AllowedHosts: []string{"wotan.okemily.com", "localhost"},
		BnetClientID: "bid", BnetClientSecret: "bsec", BnetRedirectURI: "https://iam.example/api/v1/auth/sso/battlenet/callback",
		TwitchClientID: "tid", TwitchClientSecret: "tsec", TwitchRedirectURI: "https://iam.example/api/v1/auth/sso/twitch/callback",
		BnetExchange:   func(_ context.Context, code, _ string) (string, error) { return code, nil },
		BnetUserInfo:   e.userinfo,
		TwitchExchange: func(_ context.Context, code, _ string) (string, error) { return code, nil },
		TwitchUserInfo: e.userinfo,
	}
	e.hsh.OAuth = e.oauth
	return e
}

func (e *idEnv) userinfo(_ context.Context, tok string) (*handlers.OAuthUser, error) {
	if u, ok := e.users[tok]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("unknown code %q", tok)
}

func (e *idEnv) bearer(sub string) string { return makePlayerToken(e.t, e.keys, sub) }

// hsReq calls the HS handler. tok is a raw bearer token (may be "").
func (e *idEnv) hsReq(tok, method, path, body string) (int, map[string]any, *httptest.ResponseRecorder) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1/hs/"+path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.hsh.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w
}

func (e *idEnv) hsAs(sub, method, path, body string) (int, map[string]any) {
	c, o, _ := e.hsReq(e.bearer(sub), method, path, body)
	return c, o
}

func (e *idEnv) mustHS(want int, sub, method, path, body string) map[string]any {
	e.t.Helper()
	c, o := e.hsAs(sub, method, path, body)
	if c != want {
		e.t.Fatalf("%s %s as %s: %d want %d: %v", method, path, sub, c, want, o)
	}
	return o
}

// flow runs start -> (provider) -> callback for one provider. startPath includes the query. tok is the
// session bearer for link mode. state "" uses the real one. Returns the callback recorder.
func (e *idEnv) flow(prov, startPath, tok, code, forceState string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, startPath, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	if strings.HasPrefix(startPath, "/api/v1/hs/") {
		e.hsh.ServeHTTP(w, req)
	} else {
		e.oauth.ServeHTTP(w, req)
	}
	state, authURL := "", ""
	switch w.Code {
	case http.StatusOK:
		var j map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &j)
		authURL, _ = j["authorize_url"].(string)
	case http.StatusFound:
		authURL = w.Header().Get("Location")
	default:
		e.t.Fatalf("start %s: %d %s", startPath, w.Code, w.Body.String())
	}
	u, err := url.Parse(authURL)
	if err != nil {
		e.t.Fatal(err)
	}
	state = u.Query().Get("state")
	if forceState != "" {
		state = forceState
	}
	return e.callback(prov, code, state, w.Result().Cookies())
}

func (e *idEnv) callback(prov, code, state string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/"+prov+"/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.oauth.ServeHTTP(w, req)
	return w
}

func fragmentOf(t *testing.T, w *httptest.ResponseRecorder) url.Values {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatalf("want redirect, got %d: %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(u.Fragment)
	return q
}

func (e *idEnv) pidOf(prov, sub string) string {
	var p string
	_ = e.db.QueryRow(`SELECT player_id FROM player_identities WHERE provider=? AND provider_sub=? AND status='active'`, prov, sub).Scan(&p)
	return p
}

// bnetLogin signs a Battle.net user in (creating the player on first use) and returns the player id.
func (e *idEnv) bnetLogin(code, sub, tag string) string {
	e.t.Helper()
	e.users[code] = &handlers.OAuthUser{Sub: sub, Display: tag}
	w := e.flow("battlenet", "/api/v1/auth/sso/battlenet/start?redirect_uri="+url.QueryEscape(idRedirect), "", code, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "iduna_sso_session") {
		e.t.Fatalf("bnet login: %d %s", w.Code, w.Body.String())
	}
	return e.pidOf("battlenet", sub)
}

func (e *idEnv) linkBnet(pid, code, sub, tag string) url.Values {
	e.t.Helper()
	e.users[code] = &handlers.OAuthUser{Sub: sub, Display: tag}
	w := e.flow("battlenet", "/api/v1/auth/sso/battlenet/start?mode=link&redirect_uri="+url.QueryEscape(idRedirect), e.bearer(pid), code, "")
	return fragmentOf(e.t, w)
}

// ---- provider config / login ---------------------------------------------------------------------------------

func TestIdentity_ProviderDisabledUnlessConfigured(t *testing.T) {
	e := newIDEnv(t)
	off := &handlers.IdentityOAuthHandler{DB: e.db, Keys: e.keys, AllowedHosts: []string{"wotan.okemily.com"}}
	for _, path := range []string{
		"/api/v1/auth/sso/battlenet/start?redirect_uri=" + url.QueryEscape(idRedirect),
		"/api/v1/auth/sso/battlenet/callback?code=x&state=y",
		"/api/v1/auth/sso/twitch/callback?code=x&state=y",
	} {
		w := httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 404 || out["code"] != "provider_not_configured" {
			t.Errorf("%s: %d %v", path, w.Code, out)
		}
	}
	// the hs link-start route too, and the list advertises it
	e.hsh.OAuth = off
	e.mustHS(404, "p1", "GET", "me/identities/twitch/start?redirect_uri="+url.QueryEscape(idRedirect), "")
	l := e.mustHS(200, "p1", "GET", "me/identities", "")
	if l["providers"].(map[string]any)["battlenet"].(map[string]any)["configured"] != false {
		t.Fatalf("providers: %v", l["providers"])
	}
}

func TestIdentity_BattlenetLoginCreatesPlayerAndResolvesSamePlayer(t *testing.T) {
	e := newIDEnv(t)
	// start: openid scope only, state set, redirect allowlisted
	w := httptest.NewRecorder()
	e.oauth.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/battlenet/start?redirect_uri="+url.QueryEscape(idRedirect), nil))
	if w.Code != 302 {
		t.Fatalf("start: %d", w.Code)
	}
	u, _ := url.Parse(w.Header().Get("Location"))
	if u.Host != "oauth.battle.net" || u.Query().Get("scope") != "openid" || u.Query().Get("state") == "" || u.Query().Get("client_id") != "bid" {
		t.Fatalf("authorize url: %s", u)
	}
	bad := httptest.NewRecorder()
	e.oauth.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/battlenet/start?redirect_uri="+url.QueryEscape("https://evil.example/x"), nil))
	if bad.Code != 400 {
		t.Fatalf("evil redirect: %d", bad.Code)
	}

	pid := e.bnetLogin("c1", "bn-100", "Alice#1234")
	if pid == "" {
		t.Fatal("no player created")
	}
	var prov, name string
	_ = e.db.QueryRow(`SELECT provider, display_name FROM players WHERE player_id=?`, pid).Scan(&prov, &name)
	if prov != "battlenet" || name != "Alice" {
		t.Fatalf("player: %s %s", prov, name)
	}
	// a second login resolves to the SAME player, no duplicate
	if again := e.bnetLogin("c2", "bn-100", "Alice#1234"); again != pid {
		t.Fatalf("second login: %s vs %s", again, pid)
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&n)
	if n != 1 {
		t.Fatalf("players: %d", n)
	}
	// a changed BattleTag is refreshed on login (mutable display data), the sub stays the anchor
	e.bnetLogin("c3", "bn-100", "Alicia#4321")
	var disp string
	_ = e.db.QueryRow(`SELECT display FROM player_identities WHERE provider='battlenet' AND provider_sub='bn-100'`).Scan(&disp)
	if disp != "Alicia#4321" {
		t.Fatalf("display not refreshed: %s", disp)
	}
	// disabled players cannot sign in
	_, _ = e.db.Exec(`UPDATE players SET disabled_at='2026-01-01T00:00:00Z' WHERE player_id=?`, pid)
	e.users["c4"] = &handlers.OAuthUser{Sub: "bn-100", Display: "Alicia#4321"}
	if w := e.flow("battlenet", "/api/v1/auth/sso/battlenet/start?redirect_uri="+url.QueryEscape(idRedirect), "", "c4", ""); w.Code != 403 {
		t.Fatalf("disabled: %d", w.Code)
	}
}

// ---- linking ----------------------------------------------------------------------------------------------------

func TestIdentity_LinkModeAndIdentityTaken(t *testing.T) {
	e := newIDEnv(t)
	p1 := e.bnetLogin("c1", "bn-1", "Alice#1111")
	p2 := e.bnetLogin("c2", "bn-2", "Bob#2222")

	// link mode needs a session
	w := httptest.NewRecorder()
	e.oauth.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/battlenet/start?mode=link&redirect_uri="+url.QueryEscape(idRedirect), nil))
	if w.Code != 401 {
		t.Fatalf("anonymous link start: %d", w.Code)
	}

	// p2 tries to link p1's Battle.net account: identity_taken, and nothing in the response names p1
	f := e.linkBnet(p2, "c3", "bn-1", "Alice#1111")
	if f.Get("link_error") != "identity_taken" || f.Get("linked") != "" {
		t.Fatalf("fragment: %v", f)
	}
	for k, vs := range f {
		for _, v := range vs {
			if strings.Contains(v, p1) || strings.Contains(strings.ToLower(v), "alice") {
				t.Fatalf("identity_taken leaks owner info in %s=%s", k, v)
			}
		}
	}
	if got := e.pidOf("battlenet", "bn-1"); got != p1 {
		t.Fatalf("ownership changed: %s", got)
	}
	// operators see it in the dispute queue
	if b := e.admin(http.MethodGet, "/admin/identities", nil).Body.String(); !strings.Contains(b, "identity_taken") || !strings.Contains(b, p1) {
		t.Errorf("dispute queue missing: %s", b)
	}

	// a free Battle.net account links (smurfs: N-to-1), re-linking refreshes instead of duplicating
	f = e.linkBnet(p2, "c4", "bn-3", "BobSmurf#3333")
	if f.Get("linked") != "battlenet" || f.Get("identity_id") == "" {
		t.Fatalf("link: %v", f)
	}
	e.linkBnet(p2, "c5", "bn-3", "BobSmurf#3434")
	var n int
	var disp string
	_ = e.db.QueryRow(`SELECT COUNT(*), MAX(display) FROM player_identities WHERE player_id=? AND provider='battlenet' AND status='active'`, p2).Scan(&n, &disp)
	if n != 2 {
		t.Fatalf("p2 battlenet identities: %d", n)
	}
	if got := e.pidOf("battlenet", "bn-3"); got != p2 {
		t.Fatalf("bn-3 owner: %s", got)
	}
	// both smurfs sign in as the same player
	if got := e.bnetLogin("c6", "bn-3", "BobSmurf#3434"); got != p2 {
		t.Fatalf("smurf login resolved to %s want %s", got, p2)
	}
	// a link-only access_token navigation (no header) is accepted too and redirects straight to the provider
	e.users["c7"] = &handlers.OAuthUser{Sub: "bn-4", Display: "Bob#4"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/battlenet/start?mode=link&redirect_uri="+url.QueryEscape(idRedirect)+"&access_token="+url.QueryEscape(e.bearer(p2)), nil)
	rec := httptest.NewRecorder()
	e.oauth.ServeHTTP(rec, req)
	if rec.Code != 302 || !strings.Contains(rec.Header().Get("Location"), "oauth.battle.net") {
		t.Fatalf("access_token start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestIdentity_StateAndCookieBoundToSession(t *testing.T) {
	e := newIDEnv(t)
	p1 := e.bnetLogin("c1", "bn-1", "Alice#1111")
	p2 := e.bnetLogin("c2", "bn-2", "Bob#2222")
	e.users["x"] = &handlers.OAuthUser{Sub: "bn-9", Display: "Mallory#9"}

	start := func(pid string) (*httptest.ResponseRecorder, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/battlenet/start?mode=link&redirect_uri="+url.QueryEscape(idRedirect), nil)
		req.Header.Set("Authorization", "Bearer "+e.bearer(pid))
		w := httptest.NewRecorder()
		e.oauth.ServeHTTP(w, req)
		var j map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &j)
		u, _ := url.Parse(j["authorize_url"].(string))
		return w, u.Query().Get("state")
	}
	w1, s1 := start(p1)
	_, s2 := start(p2)

	if r := e.callback("battlenet", "x", "forged", w1.Result().Cookies()); r.Code != 400 {
		t.Errorf("forged state: %d", r.Code)
	}
	// p1's cookie with p2's state (a state replayed into another session) fails
	if r := e.callback("battlenet", "x", s2, w1.Result().Cookies()); r.Code != 400 {
		t.Errorf("state from another flow: %d", r.Code)
	}
	if r := e.callback("battlenet", "x", s1, nil); r.Code != 400 {
		t.Errorf("no cookie: %d", r.Code)
	}
	// a cookie signed by a different key is worthless
	otherKeys, _ := jwt.GenerateKeys()
	forged, _ := jwt.Sign(otherKeys, map[string]any{"aud": "iduna-oauth-state", "prov": "battlenet", "mode": "link", "nonce": "n", "pid": p1,
		"redir": idRedirect, "exp": time.Now().Add(time.Minute).Unix()})
	fc := &http.Cookie{Name: "iduna_oauth_battlenet", Value: forged}
	if r := e.callback("battlenet", "x", "n", []*http.Cookie{fc}); r.Code != 400 {
		t.Errorf("forged cookie: %d", r.Code)
	}
	// nothing was linked by any of the above
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM player_identities WHERE provider_sub='bn-9'`).Scan(&n)
	if n != 0 {
		t.Fatalf("a rejected callback linked an identity")
	}
	// the genuine pair works exactly once in the genuine browser, and the flow cookie is cleared after
	ok := e.callback("battlenet", "x", s1, w1.Result().Cookies())
	if fragmentOf(t, ok).Get("linked") != "battlenet" || e.pidOf("battlenet", "bn-9") != p1 {
		t.Fatalf("genuine callback failed")
	}
	// a flow-state cookie is not a bearer identity anywhere
	for _, c := range w1.Result().Cookies() {
		if c.Name == "iduna_oauth_battlenet" {
			if code, _, _ := e.hsReq(c.Value, "GET", "me/identities", ""); code != 401 {
				t.Errorf("state cookie accepted as bearer: %d", code)
			}
		}
	}
}

func TestIdentity_TwitchLinkOnlyNoScopesOnePerPlayer(t *testing.T) {
	e := newIDEnv(t)
	p1 := e.bnetLogin("c1", "bn-1", "Alice#1111")
	p2 := e.bnetLogin("c2", "bn-2", "Bob#2222")
	e.users["t1"] = &handlers.OAuthUser{Sub: "tw-1", Display: "alice_tv", Extra: map[string]any{"broadcaster_type": "affiliate"}}
	e.users["t2"] = &handlers.OAuthUser{Sub: "tw-2", Display: "alice_tv2"}
	start := "/api/v1/hs/me/identities/twitch/start?redirect_uri=" + url.QueryEscape(idRedirect)

	// authorize URL carries no scope
	req := httptest.NewRequest(http.MethodGet, start, nil)
	req.Header.Set("Authorization", "Bearer "+e.bearer(p1))
	w := httptest.NewRecorder()
	e.hsh.ServeHTTP(w, req)
	var j map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &j)
	au, _ := url.Parse(j["authorize_url"].(string))
	if w.Code != 200 || au.Host != "id.twitch.tv" || au.Query().Get("scope") != "" {
		t.Fatalf("twitch start: %d %v", w.Code, j)
	}
	// anonymous start refused
	if c, _, _ := e.hsReq("", "GET", "me/identities/twitch/start?redirect_uri="+url.QueryEscape(idRedirect), ""); c != 401 {
		t.Fatalf("anonymous twitch start: %d", c)
	}
	// twitch is never a login door
	lw := httptest.NewRecorder()
	e.oauth.ServeHTTP(lw, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/twitch/start?redirect_uri="+url.QueryEscape(idRedirect), nil))
	if lw.Code != 404 {
		t.Fatalf("twitch login start: %d", lw.Code)
	}

	f := fragmentOf(t, e.flow("twitch", start, e.bearer(p1), "t1", ""))
	if f.Get("linked") != "twitch" {
		t.Fatalf("twitch link: %v", f)
	}
	// second twitch for the same player is refused
	f = fragmentOf(t, e.flow("twitch", start, e.bearer(p1), "t2", ""))
	if f.Get("link_error") != "twitch_already_linked" {
		t.Fatalf("second twitch: %v", f)
	}
	// someone else's twitch is identity_taken
	f = fragmentOf(t, e.flow("twitch", start, e.bearer(p2), "t1", ""))
	if f.Get("link_error") != "identity_taken" {
		t.Fatalf("taken twitch: %v", f)
	}
	var extra string
	_ = e.db.QueryRow(`SELECT extra_json FROM player_identities WHERE provider='twitch' AND player_id=?`, p1).Scan(&extra)
	if !strings.Contains(extra, "affiliate") {
		t.Fatalf("extra_json: %s", extra)
	}
}

// ---- identities API / guards -----------------------------------------------------------------------------------

func TestIdentity_ListUnlinkGuards(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")
	other := e.bnetLogin("c2", "bn-2", "Bob#2222")
	l := e.mustHS(200, p, "GET", "me/identities", "")
	items := l["identities"].([]any)
	if len(items) != 1 || l["battlenet_linked"] != true || l["login_doors"] != float64(1) {
		t.Fatalf("list: %v", l)
	}
	bn := items[0].(map[string]any)
	id := fmt.Sprint(int64(bn["id"].(float64)))
	if bn["can_unlink"] != false || bn["unlink_blocked_by"] != "last_login_door" {
		t.Fatalf("last door should be unremovable: %v", bn)
	}
	c, out := e.hsAs(p, "DELETE", "me/identities/"+id, "")
	if c != 409 || out["code"] != "last_login_door" {
		t.Fatalf("unlink last door: %d %v", c, out)
	}
	// a second login door (google) frees the Battle.net one, until there is an active upload key
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display, verified) VALUES (?, 'google','g-1','a@example.com',1)`, p)
	key := e.mustHS(201, p, "POST", "me/upload-keys", `{"label":"home pc"}`)
	c, out = e.hsAs(p, "DELETE", "me/identities/"+id, "")
	if c != 409 || out["code"] != "has_active_keys" {
		t.Fatalf("unlink with active key: %d %v", c, out)
	}
	e.mustHS(200, p, "DELETE", "me/upload-keys/"+fmt.Sprint(int64(key["id"].(float64))), "")
	// somebody else's identity id is just "not found"
	var otherID int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE player_id=? AND provider='battlenet'`, other).Scan(&otherID)
	if c, _ := e.hsAs(p, "DELETE", fmt.Sprintf("me/identities/%d", otherID), ""); c != 404 {
		t.Fatalf("unlink foreign identity: %d", c)
	}
	e.mustHS(200, p, "DELETE", "me/identities/"+id, "")
	if e.pidOf("battlenet", "bn-1") != "" {
		t.Fatal("identity still active")
	}
	// the sign-in door is really closed: that Battle.net account now creates a brand-new player, not p
	again := e.bnetLogin("c3", "bn-1", "Alice#1111")
	if again == p || again == "" {
		t.Fatalf("unlinked identity still resolves to old player: %s", again)
	}
	// twitch can always be unlinked
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display) VALUES (?, 'twitch','tw-1','alice_tv')`, p)
	var twID int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE provider='twitch' AND player_id=?`, p).Scan(&twID)
	e.mustHS(200, p, "DELETE", fmt.Sprintf("me/identities/%d", twID), "")
}

func TestIdentity_PrefsDefaultOffAndPersist(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")
	g := e.mustHS(200, p, "GET", "me/identities/prefs", "")
	if g["show_twitch_name"] != false || g["show_battletag"] != false {
		t.Fatalf("defaults: %v", g)
	}
	e.mustHS(200, p, "PUT", "me/identities/prefs", `{"show_twitch_name":true}`)
	g = e.mustHS(200, p, "GET", "me/identities/prefs", "")
	if g["show_twitch_name"] != true || g["show_battletag"] != false {
		t.Fatalf("after put: %v", g)
	}
	l := e.mustHS(200, p, "GET", "me/identities", "")
	if l["prefs"].(map[string]any)["show_twitch_name"] != true {
		t.Fatalf("list prefs: %v", l["prefs"])
	}
	// the prefs write must not clobber the existing auto_publish setting
	e.mustHS(200, p, "PUT", "settings", `{"auto_publish":true}`)
	e.mustHS(200, p, "PUT", "me/identities/prefs", `{"show_battletag":true}`)
	if s := e.mustHS(200, p, "GET", "settings", ""); s["auto_publish"] != true {
		t.Fatalf("settings clobbered: %v", s)
	}
	e.mustHS(401, "", "GET", "me/identities", "")
}

func TestIdentity_GoogleLinkAndCrossProviderResolution(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")
	claims := &googleverify.GoogleClaims{Sub: "g-77", Email: "alice@example.com", EmailVerified: true, Name: "Alice"}
	gh := &handlers.SSOGoogleHandler{
		GoogleClientID: "cid", GoogleClientSecret: "sec",
		RedirectURIs: map[string]string{"example.com": "https://iam.example/api/v1/auth/sso/google/callback"}, DefaultHost: "example.com",
		AllowedHosts: []string{"wotan.okemily.com"}, DB: e.db, Players: e.players,
		ExchangeCode: func(context.Context, string, string) (string, error) { return "idtok", nil },
		VerifyToken:  func(context.Context, string, string) (*googleverify.GoogleClaims, error) { return claims, nil },
	}
	run := func(link bool, tok string) *httptest.ResponseRecorder {
		path := "/api/v1/auth/sso/google/start?redirect_uri=" + url.QueryEscape(idRedirect)
		if link {
			path += "&mode=link"
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		gh.ServeHTTP(w, req)
		var authURL string
		if w.Code == 200 {
			var j map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &j)
			authURL, _ = j["authorize_url"].(string)
		} else if w.Code == 302 {
			authURL = w.Header().Get("Location")
		} else {
			return w
		}
		u, _ := url.Parse(authURL)
		cb := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/callback?code=abc&state="+url.QueryEscape(u.Query().Get("state")), nil)
		for _, c := range w.Result().Cookies() {
			cb.AddCookie(c)
		}
		cw := httptest.NewRecorder()
		gh.ServeHTTP(cw, cb)
		return cw
	}
	if w := run(true, ""); w.Code != 401 {
		t.Fatalf("anonymous google link: %d", w.Code)
	}
	f := fragmentOf(t, run(true, e.bearer(p)))
	if f.Get("linked") != "google" {
		t.Fatalf("google link: %v", f)
	}
	if got := e.pidOf("google", "g-77"); got != p {
		t.Fatalf("google identity owner: %s", got)
	}
	var before int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&before)
	// Signing in with Google now resolves to the Battle.net-created player: same player_id, no new row
	w := run(false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), p) {
		t.Fatalf("google login: %d %s", w.Code, w.Body.String())
	}
	var after int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&after)
	if before != after {
		t.Fatalf("google login created a player (%d -> %d)", before, after)
	}
	// a Google account owned by someone else is identity_taken
	q := e.bnetLogin("c2", "bn-2", "Bob#2222")
	if f := fragmentOf(t, run(true, e.bearer(q))); f.Get("link_error") != "identity_taken" {
		t.Fatalf("google taken: %v", f)
	}
	// unlinking Google closes that door: the legacy path must not silently re-open it
	var gid int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE provider='google' AND provider_sub='g-77'`).Scan(&gid)
	e.mustHS(200, p, "DELETE", fmt.Sprintf("me/identities/%d", gid), "")
	if w := run(false, ""); w.Code != 403 {
		t.Fatalf("login via unlinked google: %d", w.Code)
	}
}

func TestIdentity_LocalAccountsAppearLazily(t *testing.T) {
	e := newIDEnv(t)
	_, _ = e.db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES ('loc-1','Loc','iduna_local','uid-1','loc@example.com')`)
	l := e.mustHS(200, "loc-1", "GET", "me/identities", "")
	items := l["identities"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["provider"] != "iduna_local" || l["battlenet_linked"] != false {
		t.Fatalf("local identity: %v", l)
	}
}

// ---- migration backfill ----------------------------------------------------------------------------------------

func TestIdentity_MigrationBackfillsPlayersAndGrace(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	src := "../../../migrations/truestore"
	dir := t.TempDir()
	ents, _ := os.ReadDir(src)
	for _, f := range ents {
		if f.IsDir() || strings.HasPrefix(f.Name(), "202610130001") || f.Name() > "202610130001" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(src, f.Name()))
		_ = os.WriteFile(filepath.Join(dir, f.Name()), b, 0o644)
	}
	if err := store.RunSQLiteMigrations(db, dir); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES ('g1','G','google','gs-1','g@example.com')`)
	mustExec(`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES ('l1','L','iduna_local','uid-1','l@example.com')`)
	mustExec(`INSERT INTO players (player_id, display_name, provider, provider_sub) VALUES ('a1','Anon','anonymous','anon-1')`)
	mustExec(`INSERT INTO clubs (id, slug, name, owner_sub) VALUES ('c1','old-club','Old Club','g1')`)
	mustExec(`INSERT INTO hs_games (owner_sub, fingerprint, me, record_json) VALUES ('l1','fp1',1,'{}')`)
	// now the new migration
	b, _ := os.ReadFile(filepath.Join(src, "202610130001_wotan_identity.sql"))
	_ = os.WriteFile(filepath.Join(dir, "202610130001_wotan_identity.sql"), b, 0o644)
	if err := store.RunSQLiteMigrations(db, dir); err != nil {
		t.Fatalf("identity migration: %v", err)
	}
	rows, _ := db.Query(`SELECT player_id, provider, provider_sub, verified FROM player_identities ORDER BY player_id`)
	var got []string
	for rows.Next() {
		var a, b2, c string
		var v int
		_ = rows.Scan(&a, &b2, &c, &v)
		got = append(got, fmt.Sprintf("%s/%s/%s/%d", a, b2, c, v))
	}
	rows.Close()
	if strings.Join(got, ",") != "g1/google/gs-1/1,l1/iduna_local/uid-1/0" {
		t.Fatalf("backfill: %v", got)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM player_identity_grace WHERE player_id IN ('g1','l1') AND grace_until > datetime('now','+29 days')`).Scan(&n)
	if n != 2 {
		t.Fatalf("grace rows: %d", n)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM permissions WHERE name IN ('wotan.identity.link','wotan.identity.unlink','wotan.uploadkey.manage','wotan.identity.admin','wotan.identity.bypass')`).Scan(&n)
	if n != 5 {
		t.Fatalf("permission catalog rows: %d", n)
	}
}

// ---- gates -------------------------------------------------------------------------------------------------------

func TestIdentity_ClubCreateAndKeyMintingGate(t *testing.T) {
	e := newIDEnv(t)
	mkProfile := func(sub string) {
		_, _ = e.db.Exec(`INSERT INTO hs_profiles (sub, handle) VALUES (?,?)`, sub, sub+"_hs") // NOT e.handle: no grace row
	}
	newClub := func(tok, slug string) (int, map[string]any) {
		return e.doTok(tok, "POST", "/api/v1/clubs/", fmt.Sprintf(`{"name":"Club %s","slug":%q,"privacy_tier":1}`, slug, slug))
	}
	mkProfile("fresh")
	// no Battle.net, no grace, no bypass: both gated actions refused, with a machine code and gate detail
	c, out := newClub(e.token("fresh"), "fresh-club")
	if c != 403 || out["code"] != "battlenet_required" {
		t.Fatalf("club create ungated: %d %v", c, out)
	}
	if g, _ := out["gate"].(map[string]any); g == nil || g["open"] != false {
		t.Fatalf("gate detail: %v", out)
	}
	if c, o := e.hsAs("fresh", "POST", "me/upload-keys", `{}`); c != 403 || o["code"] != "battlenet_required" {
		t.Fatalf("key mint ungated: %d %v", c, o)
	}
	// 30-day grace opens it ...
	_, _ = e.db.Exec(`INSERT INTO player_identity_grace (player_id, grace_until, reason) VALUES ('fresh', datetime('now','+10 days'), 't')`)
	if c, o := newClub(e.token("fresh"), "fresh-club"); c != 201 {
		t.Fatalf("club create in grace: %d %v", c, o)
	}
	// ... and closing the window closes it again
	_, _ = e.db.Exec(`UPDATE player_identity_grace SET grace_until=datetime('now','-1 day') WHERE player_id='fresh'`)
	if c, _ := newClub(e.token("fresh"), "fresh-club-2"); c != 403 {
		t.Fatalf("club create after grace: %d", c)
	}
	if c, _ := e.hsAs("fresh", "POST", "me/upload-keys", `{}`); c != 403 {
		t.Fatalf("key mint after grace: %d", c)
	}
	// wotan.identity.bypass: per-account grant (player_permissions) works for key minting and clubs, and so does the claim
	_, _ = e.db.Exec(`INSERT INTO player_permissions (player_id, permission) VALUES ('fresh','wotan.identity.bypass')`)
	if c, o := newClub(e.token("fresh"), "fresh-club-3"); c != 201 {
		t.Fatalf("club create with bypass: %d %v", c, o)
	}
	if c, _ := e.hsAs("fresh", "POST", "me/upload-keys", `{}`); c != 201 {
		t.Fatalf("key mint with bypass: %d", c)
	}
	mkProfile("claimer")
	if c, o := newClub(e.token("claimer", "wotan.identity.bypass"), "claim-club"); c != 201 {
		t.Fatalf("bypass claim: %d %v", c, o)
	}
	// a linked Battle.net identity opens it with no grace and no bypass
	mkProfile("linked")
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display) VALUES ('linked','battlenet','bn-L','L#1')`)
	if c, o := newClub(e.token("linked"), "linked-club"); c != 201 {
		t.Fatalf("club create with battlenet: %d %v", c, o)
	}
	// revoked Battle.net identity no longer counts
	_, _ = e.db.Exec(`UPDATE player_identities SET status='revoked' WHERE provider_sub='bn-L'`)
	if c, _ := newClub(e.token("linked"), "linked-club-2"); c != 403 {
		t.Fatalf("revoked battlenet still opens the gate: %d", c)
	}
	// the handle requirement still comes first and keeps its own code
	if c, o := newClub(e.token("nohandle"), "nh"); c != 409 || o["code"] != "handle_required" {
		t.Fatalf("no handle: %d %v", c, o)
	}
}

func TestIdentity_UploadKeyLifecycle(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")

	k := e.mustHS(201, p, "POST", "me/upload-keys", `{"label":"desktop"}`)
	key, _ := k["key"].(string)
	if !strings.HasPrefix(key, "wt_") || len(key) < 43 || k["prefix"] != key[:9] {
		t.Fatalf("key shape: %v", k)
	}
	sum := sha256.Sum256([]byte(key))
	var hash, prefix string
	if err := e.db.QueryRow(`SELECT key_hash, key_prefix FROM hs_upload_keys WHERE id=?`, int64(k["id"].(float64))).Scan(&hash, &prefix); err != nil {
		t.Fatal(err)
	}
	if hash != hex.EncodeToString(sum[:]) || prefix != key[:9] || strings.Contains(hash, key) {
		t.Fatalf("at rest: hash=%s prefix=%s", hash, prefix)
	}
	// nothing in the table (or the audit log) holds the plaintext
	var leaked int
	_ = e.db.QueryRow(`SELECT (SELECT COUNT(*) FROM hs_upload_keys WHERE key_hash=? OR key_prefix=? OR label=?) +
		(SELECT COUNT(*) FROM identity_audit_log WHERE meta_json LIKE ?)`, key, key, key, "%"+key+"%").Scan(&leaked)
	if leaked != 0 {
		t.Fatalf("plaintext key stored: %d", leaked)
	}
	// shown once: the list never returns it
	l := e.mustHS(200, p, "GET", "me/upload-keys", "")
	raw, _ := json.Marshal(l)
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), hash) {
		t.Fatalf("list leaks key or hash: %s", raw)
	}
	ks := l["keys"].([]any)
	if len(ks) != 1 || ks[0].(map[string]any)["prefix"] != key[:9] || ks[0].(map[string]any)["active"] != true {
		t.Fatalf("list: %v", l)
	}

	// exchange: key -> 12 h hs-live-upload JWT, no bearer needed; wrong/garbage keys are one indistinguishable 401
	c, ex, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": key}))
	if c != 200 || ex["tags_linked"] != float64(1) || ex["expires_in"] != float64(12*3600) {
		t.Fatalf("exchange: %d %v", c, ex)
	}
	tok := ex["token"].(string)
	claims, err := jwt.Verify(e.keys, tok)
	if err != nil || claims["aud"] != "hs-live-upload" || claims["sub"] != p || claims["key_id"] != k["id"] {
		t.Fatalf("exchanged token: %v %v", claims, err)
	}
	for _, bad := range []string{"wt_" + strings.Repeat("A", 43), "nope", ""} {
		if c, o, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": bad})); c != 401 || o["code"] != "invalid_key" {
			t.Fatalf("bad key %q: %d %v", bad, c, o)
		}
	}
	var used string
	_ = e.db.QueryRow(`SELECT COALESCE(last_used_at,'') FROM hs_upload_keys WHERE id=?`, int64(k["id"].(float64))).Scan(&used)
	if used == "" {
		t.Fatal("last_used_at not recorded")
	}
	// the token works for the uplink and is NOT an identity anywhere else
	c, _, _ = e.hsReq(tok, "POST", "live/lines", `{"reset":true,"decks":[],"power":[]}`)
	if c != 200 {
		t.Fatalf("live/lines with exchanged token: %d", c)
	}
	if c, _, _ := e.hsReq(tok, "GET", "me/upload-keys", ""); c != 401 {
		t.Fatalf("upload token minted/read keys: %d", c)
	}
	if c, _, _ := e.hsReq(tok, "POST", "me/upload-keys", `{}`); c != 401 {
		t.Fatalf("upload token minted a key: %d", c)
	}
	if c, _, _ := e.hsReq(tok, "GET", "me/identities", ""); c != 401 {
		t.Fatalf("upload token read identities: %d", c)
	}
	if code, _ := e.doTok(tok, "GET", "/api/v1/clubs/me/clubs", ""); code != 401 {
		t.Fatalf("clubs handler accepted an upload token: %d", code)
	}
	st := e.mustHS(200, p, "GET", "live/state", "")
	if st["connected"] != true {
		t.Fatalf("session should be live: %v", st)
	}

	// revoke: immediate. the key and its still-unexpired JWT are both dead, and the session is tombstoned
	e.mustHS(200, p, "DELETE", fmt.Sprintf("me/upload-keys/%d", int64(k["id"].(float64))), "")
	if c, o, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": key})); c != 401 || o["code"] != "invalid_key" {
		t.Fatalf("revoked key exchanged: %d %v", c, o)
	}
	if c, o, _ := e.hsReq(tok, "POST", "live/lines", `{"reset":false,"decks":[],"power":[]}`); c != 401 || o["code"] != "key_revoked" {
		t.Fatalf("revoked key's JWT still ingests: %d %v", c, o)
	}
	if c, _, _ := e.hsReq(tok, "GET", "live/sync-target", ""); c != 401 {
		t.Fatalf("revoked key's JWT reads sync-target: %d", c)
	}
	if st := e.mustHS(200, p, "GET", "live/state", ""); st["connected"] == true {
		t.Fatalf("session not tombstoned: %v", st)
	}
	l = e.mustHS(200, p, "GET", "me/upload-keys", "")
	if l["keys"].([]any)[0].(map[string]any)["active"] != false {
		t.Fatalf("revoked key still listed active: %v", l)
	}
	// revoking again / someone else's key is "not found"
	e.mustHS(404, p, "DELETE", fmt.Sprintf("me/upload-keys/%d", int64(k["id"].(float64))), "")

	// at most 5 active keys
	for i := 0; i < 5; i++ {
		e.mustHS(201, p, "POST", "me/upload-keys", `{}`)
	}
	if c, o := e.hsAs(p, "POST", "me/upload-keys", `{}`); c != 409 || o["code"] != "key_limit" {
		t.Fatalf("key limit: %d %v", c, o)
	}
}

func TestIdentity_ExchangeRefusedWhenGateClosedAfterwards(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub) VALUES (?, 'google','g-1')`, p)
	key := e.mustHS(201, p, "POST", "me/upload-keys", `{}`)["key"].(string)
	// operator force-unlinks the Battle.net identity: the keys go with it
	var id int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE player_id=? AND provider='battlenet'`, p).Scan(&id)
	w := e.admin(http.MethodPost, "/admin/identities/"+p+"/unlink", url.Values{"identity_id": {fmt.Sprint(id)}, "reason": {"abuse report 12"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("admin unlink: %d %s", w.Code, w.Body.String())
	}
	if c, o, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": key})); c != 401 {
		t.Fatalf("exchange after operator unlink: %d %v", c, o)
	}
	// a key that is still active but whose owner lost the gate is refused with battlenet_required
	_, _ = e.db.Exec(`UPDATE hs_upload_keys SET revoked_at=NULL WHERE player_id=?`, p)
	if c, o, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": key})); c != 403 || o["code"] != "battlenet_required" {
		t.Fatalf("exchange with closed gate: %d %v", c, o)
	}
}

// ---- Back Office -------------------------------------------------------------------------------------------------

func TestIdentity_AdminConsole(t *testing.T) {
	e := newIDEnv(t)
	// auth gate
	if w := e.as("op-9", nil, http.MethodGet, "/admin/identities", nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", w.Code)
	}
	if w := e.as("op-9", []string{"wotan.identity.admin"}, http.MethodGet, "/admin/identities", nil); w.Code != http.StatusForbidden {
		t.Fatalf("wotan.identity.admin alone (not iduna.admin): %d", w.Code)
	}
	p := e.bnetLogin("c1", "bn-1", "Searchable#4242")
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display) VALUES (?, 'google','g-1','s@example.com')`, p)
	key := e.mustHS(201, p, "POST", "me/upload-keys", `{"label":"x"}`)

	// index search by BattleTag / key prefix / player id
	for _, q := range []string{"Searchable", "4242", key["prefix"].(string), p} {
		if b := e.admin(http.MethodGet, "/admin/identities?q="+url.QueryEscape(q), nil).Body.String(); !strings.Contains(b, "/admin/identities/"+p) {
			t.Errorf("search %q did not find the player", q)
		}
	}
	d := e.admin(http.MethodGet, "/admin/identities/"+p, nil)
	if d.Code != 200 || !strings.Contains(d.Body.String(), "Searchable#4242") || !strings.Contains(d.Body.String(), key["prefix"].(string)) {
		t.Fatalf("detail: %d", d.Code)
	}
	if strings.Contains(d.Body.String(), key["key"].(string)) {
		t.Fatal("detail leaks the key")
	}
	if e.admin(http.MethodGet, "/admin/identities/nobody-here", nil).Code != 404 {
		t.Fatal("unknown player should 404")
	}

	var bnID int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE player_id=? AND provider='battlenet'`, p).Scan(&bnID)
	// reason required
	w := e.admin(http.MethodPost, "/admin/identities/"+p+"/revoke-key", url.Values{"key_id": {fmt.Sprint(int64(key["id"].(float64)))}})
	if w.Code != 422 {
		t.Fatalf("no reason: %d", w.Code)
	}
	var active int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM hs_upload_keys WHERE player_id=? AND revoked_at IS NULL`, p).Scan(&active)
	if active != 1 {
		t.Fatal("key revoked without a reason")
	}
	w = e.admin(http.MethodPost, "/admin/identities/"+p+"/revoke-key", url.Values{"key_id": {fmt.Sprint(int64(key["id"].(float64)))}, "reason": {"leaked in a stream"}})
	if w.Code != 303 {
		t.Fatalf("revoke-key: %d", w.Code)
	}
	var aud int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM identity_audit_log WHERE player_id=? AND action='admin.revoke_key' AND platform_admin=1 AND actor_sub='op-1' AND meta_json LIKE '%leaked in a stream%'`, p).Scan(&aud)
	if aud != 1 {
		t.Fatalf("audit rows: %d", aud)
	}
	if c, _, _ := e.hsReq("", "POST", "live/exchange", jstr(map[string]string{"key": key["key"].(string)})); c != 401 {
		t.Fatalf("operator-revoked key still exchanges: %d", c)
	}

	// force-unlink: the Google door first (fine), then the last remaining door needs explicit confirmation
	var gID int64
	_ = e.db.QueryRow(`SELECT id FROM player_identities WHERE player_id=? AND provider='google'`, p).Scan(&gID)
	if w := e.admin(http.MethodPost, "/admin/identities/"+p+"/unlink", url.Values{"identity_id": {fmt.Sprint(gID)}, "reason": {"user asked us"}}); w.Code != 303 {
		t.Fatalf("unlink google: %d", w.Code)
	}
	if w := e.admin(http.MethodPost, "/admin/identities/"+p+"/unlink", url.Values{"identity_id": {fmt.Sprint(bnID)}, "reason": {"user asked us"}}); w.Code != 409 {
		t.Fatalf("last door without confirm: %d", w.Code)
	}
	if w := e.admin(http.MethodPost, "/admin/identities/"+p+"/unlink", url.Values{"identity_id": {fmt.Sprint(bnID)}, "reason": {"user asked us"}, "allow_last_door": {"1"}}); w.Code != 303 {
		t.Fatalf("last door with confirm: %d", w.Code)
	}
	if e.pidOf("battlenet", "bn-1") != "" {
		t.Fatal("identity still active")
	}
}

func TestIdentity_AdminMerge(t *testing.T) {
	e := newIDEnv(t)
	into := e.bnetLogin("c1", "bn-1", "Main#1111")
	from := e.bnetLogin("c2", "bn-2", "Smurf#2222")
	other := "third"
	e.people(into, from, other) // handles + grace so they can own/join clubs
	deck := e.deck(from, "Moved Deck", 0)
	_, _ = e.db.Exec(`INSERT INTO player_permissions (player_id, permission) VALUES (?, 'edge.game.operator')`, from)

	// clubs: from owns "alpha"; both are in "beta" (into: coach, from: manager -> higher role wins)
	alpha := e.mk(from, "alpha-club", 2)
	beta := e.mk(other, "beta-club", 2)
	e.addMember(other, beta, into, "coach")
	e.addMember(other, beta, from, "player")
	_, _ = e.db.Exec(`UPDATE club_memberships SET role='manager' WHERE club_id=? AND sub=?`, beta, from)
	_, _ = e.db.Exec(`DELETE FROM hs_profiles WHERE sub=?`, into) // `into` has no profile, `from` does: it moves over

	if w := e.admin(http.MethodPost, "/admin/identities/"+into+"/merge", url.Values{"from_sub": {from}}); w.Code != 422 {
		t.Fatalf("merge without reason: %d", w.Code)
	}
	if w := e.admin(http.MethodPost, "/admin/identities/"+into+"/merge", url.Values{"from_sub": {into}, "reason": {"same one"}}); w.Code != 422 {
		t.Fatalf("self merge: %d", w.Code)
	}
	w := e.admin(http.MethodPost, "/admin/identities/"+into+"/merge", url.Values{"from_sub": {from}, "reason": {"same human, two accounts"}})
	if w.Code != 303 {
		t.Fatalf("merge: %d %s", w.Code, w.Body.String())
	}
	// both Battle.net identities (smurfs) now belong to `into`; logging in with either gives `into`
	if e.pidOf("battlenet", "bn-2") != into || e.pidOf("battlenet", "bn-1") != into {
		t.Fatal("identities not re-pointed")
	}
	if got := e.bnetLogin("c3", "bn-2", "Smurf#2222"); got != into {
		t.Fatalf("login after merge: %s", got)
	}
	if e.roleOf(beta, into) != "manager" || e.roleOf(beta, from) != "" {
		t.Fatalf("beta roles: into=%q from=%q", e.roleOf(beta, into), e.roleOf(beta, from))
	}
	var owner, deckOwner, perm string
	_ = e.db.QueryRow(`SELECT owner_sub FROM clubs WHERE id=?`, alpha).Scan(&owner)
	_ = e.db.QueryRow(`SELECT owner_sub FROM hs_decks WHERE id=?`, deck).Scan(&deckOwner)
	_ = e.db.QueryRow(`SELECT player_id FROM player_permissions WHERE permission='edge.game.operator'`).Scan(&perm)
	if owner != into || deckOwner != into || perm != into || e.roleOf(alpha, into) != "manager" {
		t.Fatalf("re-point: owner=%s deck=%s perm=%s", owner, deckOwner, perm)
	}
	var h string
	_ = e.db.QueryRow(`SELECT handle FROM hs_profiles WHERE sub=?`, into).Scan(&h)
	if h != from+"_hs" {
		t.Fatalf("profile: %q", h)
	}
	var a1, a2 int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM identity_audit_log WHERE action='admin.merge' AND player_id=?`, into).Scan(&a1)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM identity_audit_log WHERE action='admin.merged_away' AND player_id=?`, from).Scan(&a2)
	if a1 != 1 || a2 != 1 {
		t.Fatalf("merge audit: %d %d", a1, a2)
	}
}

func TestIdentity_AdminMergeRefusals(t *testing.T) {
	e := newIDEnv(t)
	into := e.bnetLogin("c1", "bn-1", "Main#1111")
	from := e.bnetLogin("c2", "bn-2", "Smurf#2222")
	e.people(into, from, "owner", "owner2")
	merge := func() *httptest.ResponseRecorder {
		return e.admin(http.MethodPost, "/admin/identities/"+into+"/merge", url.Values{"from_sub": {from}, "reason": {"operator decision"}})
	}
	// same exclusive club
	club := e.mk("owner", "elite-club", 1)
	e.addMember("owner", club, into, "player")
	e.addMember("owner", club, from, "player")
	_, _ = e.db.Exec(`UPDATE clubs SET is_exclusive=1 WHERE id=?`, club)
	w := merge()
	if w.Code != 409 || !strings.Contains(w.Body.String(), "exclusive club elite-club") {
		t.Fatalf("same exclusive club: %d", w.Code)
	}
	if e.pidOf("battlenet", "bn-2") != from {
		t.Fatal("refused merge changed data")
	}
	// exclusive + another club
	_, _ = e.db.Exec(`DELETE FROM club_memberships WHERE club_id=? AND sub=?`, club, into)
	other := e.mk("owner2", "plain-club", 2)
	_, _ = e.db.Exec(`UPDATE clubs SET is_exclusive=0 WHERE id=?`, club)
	e.addMember("owner2", other, into, "player")
	_, _ = e.db.Exec(`UPDATE clubs SET is_exclusive=1 WHERE id=?`, club)
	if w := merge(); w.Code != 409 {
		t.Fatalf("exclusive plus other: %d", w.Code)
	}
	// resolved: remove the conflict, then it goes through
	_, _ = e.db.Exec(`DELETE FROM club_memberships WHERE club_id=? AND sub=?`, other, into)
	if w := merge(); w.Code != 303 {
		t.Fatalf("after resolving: %d %s", w.Code, w.Body.String())
	}
	// two twitch identities
	a := e.bnetLogin("c3", "bn-3", "A#1")
	b := e.bnetLogin("c4", "bn-4", "B#2")
	_, _ = e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub) VALUES (?, 'twitch','tw-a'), (?, 'twitch','tw-b')`, a, b)
	if w := e.admin(http.MethodPost, "/admin/identities/"+a+"/merge", url.Values{"from_sub": {b}, "reason": {"operator decision"}}); w.Code != 409 {
		t.Fatalf("two twitch: %d", w.Code)
	}
	if w := e.admin(http.MethodPost, "/admin/identities/"+a+"/merge", url.Values{"from_sub": {"ghost"}, "reason": {"operator decision"}}); w.Code != 404 {
		t.Fatalf("unknown from: %d", w.Code)
	}
}

// ---- tokens ------------------------------------------------------------------------------------------------------

func TestIdentity_NarrowTokensStillRefused(t *testing.T) {
	e := newIDEnv(t)
	p := e.bnetLogin("c1", "bn-1", "Alice#1111")
	club := e.mustHS(200, p, "GET", "me/identities", "") // sanity
	_ = club
	mk := func(aud string) string {
		tok, err := jwt.Sign(e.keys, map[string]any{"sub": p, "aud": aud, "iss": "iduna", "exp": time.Now().Add(time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	for _, aud := range []string{"hs-live-upload", "wotan-club", "iduna-oauth-state"} {
		tok := mk(aud)
		for _, path := range []string{"me/identities", "me/upload-keys"} {
			if c, _, _ := e.hsReq(tok, "GET", path, ""); c != 401 {
				t.Errorf("aud %s accepted by hs %s: %d", aud, path, c)
			}
		}
		if c, _, _ := e.hsReq(tok, "POST", "me/upload-keys", `{}`); c != 401 {
			t.Errorf("aud %s minted a key", aud)
		}
		if c, _ := e.doTok(tok, "GET", "/api/v1/clubs/me/clubs", ""); c != 401 {
			t.Errorf("aud %s accepted by clubs: %d", aud, c)
		}
	}
	// hs-live-upload is still good for exactly the two uplink routes
	if c, _, _ := e.hsReq(mk("hs-live-upload"), "POST", "live/lines", `{"reset":true,"decks":[],"power":[]}`); c != 200 {
		t.Errorf("live/lines with a plain upload token: %d", c)
	}
	if c, _, _ := e.hsReq(mk("hs-live-upload"), "GET", "live/sync-target", ""); c != 200 {
		t.Errorf("live/sync-target with an upload token: %d", c)
	}
	// and a club-context token is refused by the sync-target route
	if c, _, _ := e.hsReq(mk("wotan-club"), "GET", "live/sync-target", ""); c != 401 {
		t.Errorf("club token on sync-target: %d", c)
	}
}

// ---- ingest binding (live pipeline) ---------------------------------------------------------------------------------

type ingestHarness struct {
	e    *hsEnv
	dbfs []int
	sub  string
	tok  string
	key  map[string]any
}

// ingest wires a real PARENA live-tracker env for sub and mints an upload key via the real API. tags are the
// BattleTags to link as active Battle.net identities beforehand.
func ingest(t *testing.T, sub string, tags ...string) *ingestHarness {
	t.Helper()
	e, _, dbfs := liveTrackerEnv(t, sub)
	for i, tag := range tags {
		if _, err := e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display) VALUES (?, 'battlenet', ?, ?)`,
			sub, fmt.Sprintf("bn-%s-%d", sub, i), tag); err != nil {
			t.Fatal(err)
		}
	}
	h := &ingestHarness{e: e, dbfs: dbfs, sub: sub}
	if len(tags) > 0 {
		h.key = e.must(201, sub, "POST", "me/upload-keys", `{}`)
		c, ex := e.post("", "live/exchange", jstr(map[string]string{"key": h.key["key"].(string)}))
		if c != 200 {
			t.Fatalf("exchange: %d %v", c, ex)
		}
		h.tok = ex["token"].(string)
	}
	return h
}

// play uploads one finished game whose local player (slot 1) is named name, at the given clock.
func (h *ingestHarness) play(t *testing.T, tok, name, clock string) {
	t.Helper()
	pw := func(s string) string { return "D " + clock + " GameState.DebugPrintPower() - " + s }
	lines := liveGameLines(clock, h.dbfs)
	for i, l := range lines {
		lines[i] = strings.Replace(l, "PlayerName=Alice#1", "PlayerName="+name, 1)
	}
	lines = append(lines, pw("TAG_CHANGE Entity="+name+" tag=PLAYSTATE value=WON "), pw("TAG_CHANGE Entity=GameEntity tag=STATE value=COMPLETE "))
	queue := []string{"I 11:00:00.0000000 Finding Game With Deck:", "I 11:00:00.0000000 ### Deck A", "I 11:00:00.0000000 # Deck ID: 7",
		"I 11:00:00.0000000 " + liveTestCode}
	c, out := h.e.post(tok, "live/lines", jstr(map[string]any{"reset": true, "decks": queue, "power": lines}))
	if c != 200 {
		t.Fatalf("live/lines: %d %v", c, out)
	}
}

func (h *ingestHarness) games() (n int, vis string, tagID sql.NullInt64, unverified int) {
	_ = h.e.db.QueryRow(`SELECT COUNT(*) FROM hs_games WHERE owner_sub=?`, h.sub).Scan(&n)
	_ = h.e.db.QueryRow(`SELECT visibility, battletag_id, unverified_tag FROM hs_games WHERE owner_sub=? ORDER BY id DESC LIMIT 1`, h.sub).Scan(&vis, &tagID, &unverified)
	return
}

func (h *ingestHarness) counters(t *testing.T) (ok, rejected int) {
	t.Helper()
	l := h.e.must(200, h.sub, "GET", "me/upload-keys", "")
	k := l["keys"].([]any)[0].(map[string]any)
	return int(k["uploads_ok"].(float64)), int(k["rejected_tag_mismatch"].(float64))
}

func TestIdentity_IngestAttributesLinkedBattleTag(t *testing.T) {
	h := ingest(t, "ingest-a", "Alice#1")
	h.play(t, h.tok, "alice#1", "12:00:00.0000000") // case-insensitive match
	n, vis, tagID, unv := h.games()
	var want int64
	_ = h.e.db.QueryRow(`SELECT id FROM player_identities WHERE player_id='ingest-a'`).Scan(&want)
	if n != 1 || vis != "personal" || !tagID.Valid || tagID.Int64 != want || unv != 0 {
		t.Fatalf("attributed game: n=%d vis=%s tag=%v unv=%d", n, vis, tagID, unv)
	}
	if ok, rej := h.counters(t); ok != 1 || rej != 0 {
		t.Fatalf("counters ok=%d rej=%d", ok, rej)
	}
}

func TestIdentity_IngestDropsUnlinkedBattleTagAndCounts(t *testing.T) {
	h := ingest(t, "ingest-b", "Zed#9999")
	h.play(t, h.tok, "Alice#1", "12:00:00.0000000") // BattleTag-shaped, not linked: dropped
	if n, _, _, _ := h.games(); n != 0 {
		t.Fatalf("mismatched game stored: %d", n)
	}
	if ok, rej := h.counters(t); ok != 0 || rej != 1 {
		t.Fatalf("counters ok=%d rej=%d", ok, rej)
	}
	// the uploader is not told anything different (attacker sees nothing): the batch is accepted
	// (play() already asserted 200). A second mismatching game bumps the counter again.
	h.play(t, h.tok, "Mallory#4", "12:30:00.0000000")
	if _, rej := h.counters(t); rej != 2 {
		t.Fatalf("rejected counter: %d", rej)
	}
	// and the linked tag still ingests normally
	h.play(t, h.tok, "Zed#9999", "13:00:00.0000000")
	if n, _, _, _ := h.games(); n != 1 {
		t.Fatalf("linked tag game: %d", n)
	}
}

func TestIdentity_IngestGenericNameIsUnverifiedAndPrivate(t *testing.T) {
	h := ingest(t, "ingest-c", "Zed#9999")
	h.play(t, h.tok, "UNKNOWN HUMAN PLAYER", "12:00:00.0000000")
	n, vis, tagID, unv := h.games()
	if n != 1 || vis != "private" || tagID.Valid || unv != 1 {
		t.Fatalf("generic-name game: n=%d vis=%s tag=%v unv=%d", n, vis, tagID, unv)
	}
	// even when the owner syncs to a club, it can never be routed there or re-targeted out of private
	_, _ = h.e.db.Exec(`INSERT INTO clubs (id, slug, name, owner_sub) VALUES ('cc','cc-club','CC','ingest-c')`)
	_, _ = h.e.db.Exec(`INSERT INTO club_memberships (club_id, sub, role) VALUES ('cc','ingest-c','manager')`)
	var gid int64
	_ = h.e.db.QueryRow(`SELECT id FROM hs_games WHERE owner_sub='ingest-c'`).Scan(&gid)
	for _, body := range []string{`{"kind":"club","club_id":"cc"}`, `{"kind":"personal"}`} {
		c, out := h.e.do("ingest-c", "PATCH", fmt.Sprintf("games/%d/target", gid), body)
		if c != 409 || out["code"] != "unverified_tag" {
			t.Fatalf("retarget %s: %d %v", body, c, out)
		}
	}
	if c, _ := h.e.do("ingest-c", "PATCH", fmt.Sprintf("games/%d/target", gid), `{"kind":"none"}`); c != 200 {
		t.Fatalf("retarget none: %d", c)
	}
	// the public aggregate never sees it
	if m := h.e.must(200, "", "GET", "stats/matchups", ""); len(m["matchups"].([]any)) != 0 {
		t.Fatalf("unverified game leaked into the public matchups: %v", m)
	}
}

func TestIdentity_IngestNoLinkedTagFollowsGrace(t *testing.T) {
	// grandfathered (grace) uploader with no linked tag: legacy behavior, nothing is cut off
	h := ingest(t, "ingest-d")
	h.play(t, mustLiveToken(t, h.e, "ingest-d"), "Alice#1", "12:00:00.0000000")
	if n, vis, _, unv := h.games(); n != 1 || vis != "personal" || unv != 0 {
		t.Fatalf("grace uploader: n=%d vis=%s unv=%d", n, vis, unv)
	}
	// grace expired and still nothing linked: a BattleTag-shaped name is not linked -> dropped
	_, _ = h.e.db.Exec(`UPDATE player_identity_grace SET grace_until=datetime('now','-1 day') WHERE player_id='ingest-d'`)
	_, _ = h.e.db.Exec(`DELETE FROM hs_games WHERE owner_sub='ingest-d'`)
	h.play(t, mustLiveToken(t, h.e, "ingest-d"), "Alice#1", "13:00:00.0000000")
	if n, _, _, _ := h.games(); n != 0 {
		t.Fatalf("post-grace unlinked uploader stored a game: %d", n)
	}
}

func mustLiveToken(t *testing.T, e *hsEnv, sub string) string {
	t.Helper()
	return e.must(200, sub, "POST", "live/token", "")["token"].(string)
}

func TestIdentity_FirstStreamedLiveSeamCachedAndOffByDefault(t *testing.T) {
	h := ingest(t, "ingest-e", "Alice#1", "Alicia#1", "Alix#1")
	hh := h.e.h.(*handlers.HSHandler)
	// default (no seam): flag stays 0 even with a Twitch identity linked
	_, _ = h.e.db.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display) VALUES ('ingest-e','twitch','tw-e','alice_tv')`)
	h.play(t, h.tok, "Alice#1", "12:00:00.0000000")
	var f int
	_ = h.e.db.QueryRow(`SELECT first_streamed_live FROM hs_games WHERE owner_sub='ingest-e' ORDER BY id DESC LIMIT 1`).Scan(&f)
	if f != 0 {
		t.Fatalf("flag without a Twitch seam: %d", f)
	}
	calls := 0
	hh.TwitchLive = func(_ context.Context, id string) (bool, error) {
		calls++
		if id != "tw-e" {
			t.Errorf("seam asked about %q", id)
		}
		return true, nil
	}
	h.play(t, h.tok, "Alicia#1", "13:00:00.0000000") // distinct names: distinct records (no fingerprint dedup)
	h.play(t, h.tok, "Alix#1", "14:00:00.0000000")
	_ = h.e.db.QueryRow(`SELECT first_streamed_live FROM hs_games WHERE owner_sub='ingest-e' ORDER BY id DESC LIMIT 1`).Scan(&f)
	if f != 1 || calls != 1 {
		t.Fatalf("flag=%d calls=%d (want 1 and 1: cached for 60s)", f, calls)
	}
}
