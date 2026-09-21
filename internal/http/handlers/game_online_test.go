package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/brawlpit"
	"iduna/internal/games"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// Uses the REAL migrations (through the real mysql->sqlite translation), so this also proves
// 202609181800/202609181810 apply cleanly.
func newGameDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../../migrations/truestore"); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

func testGames() map[string]games.Config {
	m := map[string]games.Config{}
	for k, v := range games.Registry {
		m[k] = v
	}
	// A second game purely to prove cross-game refusal + "a fourth game is a config row".
	m["othergame"] = games.Config{Slug: "othergame", PlayPerm: "othergame.play", BotPerm: "othergame.bot.play",
		MatchWritePerm: "othergame.match.write", CheckpointsWritePerm: "othergame.checkpoints.write"}
	return m
}

type gameEnv struct {
	h    *handlers.GameOnlineHandler
	keys *authjwt.Keys
	db   *sql.DB
}

func newGameEnv(t *testing.T) *gameEnv {
	t.Helper()
	keys, err := authjwt.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	db := newGameDB(t)
	return &gameEnv{h: &handlers.GameOnlineHandler{DB: db, Keys: keys, Games: testGames()}, keys: keys, db: db}
}

func (e *gameEnv) agentToken(t *testing.T, perms ...string) string {
	t.Helper()
	tok, err := authjwt.Sign(e.keys, map[string]any{"sub": "agent-x", "agent_name": "X", "permissions": perms,
		"aud": "farthq-ecosystem", "exp": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *gameEnv) do(method, path, token string, body any) (int, map[string]any, []byte) {
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
	e.h.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Body.Bytes()
}

func (e *gameEnv) register(t *testing.T, game, name string) (pid, secret, token string) {
	t.Helper()
	code, m, raw := e.do("POST", "/api/v1/games/"+game+"/guest-register", "", map[string]string{"display_name": name})
	if code != 201 {
		t.Fatalf("register %s: %d %s", name, code, raw)
	}
	return m["player_id"].(string), m["guest_secret"].(string), m["token"].(string)
}

func TestGuestFlow_RegisterLoginVerifyMatchStats(t *testing.T) {
	e := newGameEnv(t)
	pid, secret, tok := e.register(t, "deadweight", "Ada")
	if len(secret) != 64 {
		t.Errorf("secret len %d", len(secret))
	}

	// the secret must not be stored in the clear
	var hash string
	if err := e.db.QueryRow(`SELECT secret_hash FROM game_guest_credentials WHERE player_id=?`, pid).Scan(&hash); err != nil || hash == secret || len(hash) != 64 {
		t.Errorf("secret not hashed: %q %v", hash, err)
	}
	var game, provider string
	_ = e.db.QueryRow(`SELECT game, provider FROM players WHERE player_id=?`, pid).Scan(&game, &provider)
	if game != "deadweight" || provider != "guest" {
		t.Errorf("player row scope wrong: %s %s", game, provider)
	}

	// login: right secret ok, wrong secret and unknown player identical 401
	code, m, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": secret})
	if code != 200 || m["token"] == "" || m["display_name"] != "Ada" {
		t.Fatalf("login: %d %v", code, m)
	}
	c1, b1, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": strings.Repeat("0", 64)})
	c2, b2, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": "no-such", "guest_secret": secret})
	if c1 != 401 || c2 != 401 || b1["error"] != b2["error"] {
		t.Errorf("bad login not uniform: %d/%d %v %v", c1, c2, b1, b2)
	}

	// verify (human)
	code, m, _ = e.do("POST", "/api/v1/games/deadweight/verify", tok, nil)
	if code != 200 || m["kind"] != "human" || m["player_id"] != pid || m["game"] != "deadweight" {
		t.Fatalf("verify: %d %v", code, m)
	}
	// verify rejects garbage / missing
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", "not.a.jwt", nil); c != 401 {
		t.Errorf("garbage token: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", "", nil); c != 401 {
		t.Errorf("missing token: %d", c)
	}

	// bot verify: needs name, yields stable player_id
	botTok := e.agentToken(t, "deadweight.bot.play")
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", botTok, nil); c != 400 {
		t.Errorf("bot without name should 400, got %d", c)
	}
	_, b, _ := e.do("POST", "/api/v1/games/deadweight/verify", botTok, map[string]string{"name": "Ripper"})
	_, b2x, _ := e.do("POST", "/api/v1/games/deadweight/verify", botTok, map[string]string{"name": "Ripper"})
	if b["kind"] != "bot" || b["player_id"] == "" || b["player_id"] != b2x["player_id"] {
		t.Fatalf("bot identity not stable: %v %v", b, b2x)
	}
	botPID := b["player_id"].(string)
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", botTok, map[string]string{"name": "bad name!"}); c != 400 {
		t.Errorf("bad bot name: %d", c)
	}

	// match-result: guests and bots may not write; the server agent may
	res := map[string]any{"match_id": 7, "seed": 99, "seat0_player_id": pid, "seat1_player_id": botPID, "winner": 0, "rounds": 5, "reason": "hull", "mode": 0}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/match-result", tok, res); c != 403 {
		t.Errorf("guest must not report results: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/match-result", botTok, res); c != 403 {
		t.Errorf("bot must not report results: %d", c)
	}
	srv := e.agentToken(t, "deadweight.match.write")
	code, m, raw := e.do("POST", "/api/v1/games/deadweight/match-result", srv, res)
	if code != 200 || m["duplicate"] != false {
		t.Fatalf("match-result: %d %s", code, raw)
	}
	s0 := m["seat0"].(map[string]any)
	s1 := m["seat1"].(map[string]any)
	if math.Abs(s0["rating"].(float64)-1516) > 1e-9 || math.Abs(s1["rating"].(float64)-1484) > 1e-9 ||
		s0["wins"].(float64) != 1 || s1["losses"].(float64) != 1 {
		t.Errorf("elo/stats wrong: %v %v", s0, s1)
	}
	// idempotent replay
	_, m, _ = e.do("POST", "/api/v1/games/deadweight/match-result", srv, res)
	if m["duplicate"] != true || m["seat0"].(map[string]any)["matches"].(float64) != 1 {
		t.Errorf("replay changed state: %v", m)
	}
	// draw between same pair with new id
	res["match_id"], res["winner"] = 8, 2
	_, m, _ = e.do("POST", "/api/v1/games/deadweight/match-result", srv, res)
	if m["seat0"].(map[string]any)["draws"].(float64) != 1 {
		t.Errorf("draw not recorded: %v", m)
	}
	// unknown player / same seat rejected
	bad := map[string]any{"match_id": 9, "seed": 1, "seat0_player_id": pid, "seat1_player_id": "ghost", "winner": 0, "rounds": 1, "reason": "hull"}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/match-result", srv, bad); c != 400 {
		t.Errorf("unknown player: %d", c)
	}

	// stats read-back + leaderboard
	code, m, _ = e.do("GET", "/api/v1/games/deadweight/players/"+pid+"/stats", "", nil)
	if code != 200 || m["kind"] != "human" || m["matches"].(float64) != 2 || m["wins"].(float64) != 1 {
		t.Errorf("stats: %d %v", code, m)
	}
	_, _, raw = e.do("GET", "/api/v1/games/deadweight/leaderboard?limit=5", "", nil)
	var lb []map[string]any
	if err := json.Unmarshal(raw, &lb); err != nil || len(lb) != 2 || lb[0]["kind"] == nil {
		t.Errorf("leaderboard: %s", raw)
	}
	// a never-played guest reads as default rating, not 404
	pid2, _, _ := e.register(t, "deadweight", "Bo")
	_, m, _ = e.do("GET", "/api/v1/games/deadweight/players/"+pid2+"/stats", "", nil)
	if m["rating"].(float64) != 1500 || m["matches"].(float64) != 0 {
		t.Errorf("fresh stats: %v", m)
	}
}

func TestGuest_GameScopeIsolation(t *testing.T) {
	e := newGameEnv(t)
	_, secretO, tokO := e.register(t, "othergame", "Zed")
	pidD, _, tokD := e.register(t, "deadweight", "Ada")

	// a token minted for game A is refused by game B, both directions
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", tokO, nil); c != 403 {
		t.Errorf("othergame token accepted by deadweight: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/othergame/verify", tokD, nil); c != 403 {
		t.Errorf("deadweight token accepted by othergame: %d", c)
	}
	// credentials for game A do not log in on game B
	var pidO string
	_ = e.db.QueryRow(`SELECT player_id FROM players WHERE display_name='Zed'`).Scan(&pidO)
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pidO, "guest_secret": secretO}); c != 401 {
		t.Errorf("cross-game login: %d", c)
	}
	// stats of another game's player are invisible
	if c, _, _ := e.do("GET", "/api/v1/games/othergame/players/"+pidD+"/stats", "", nil); c != 404 {
		t.Errorf("cross-game stats: %d", c)
	}
	// a non-game token (google/local user, other agent) is refused
	local, _ := authjwt.Sign(e.keys, map[string]any{"sub": "local:1", "permissions": []string{"iduna.me.read"}, "exp": time.Now().Add(time.Hour).Unix()})
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", local, nil); c != 403 {
		t.Errorf("local token: %d", c)
	}
	// an expired guest token is refused
	old, _ := authjwt.Sign(e.keys, map[string]any{"player_id": pidD, "game": "deadweight", "permissions": []string{"deadweight.play"}, "exp": time.Now().Add(-time.Minute).Unix()})
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/verify", old, nil); c != 401 {
		t.Errorf("expired token: %d", c)
	}
	// unknown game
	if c, _, _ := e.do("POST", "/api/v1/games/nope/guest-register", "", map[string]string{"display_name": "x"}); c != 404 {
		t.Errorf("unknown game: %d", c)
	}
	// guest token carries exactly one permission
	claims, _ := authjwt.Verify(e.keys, tokD)
	perms := claims["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "deadweight.play" {
		t.Errorf("guest permissions: %v", perms)
	}
}

func TestGuest_NameValidationAndRateLimit(t *testing.T) {
	e := newGameEnv(t)
	for _, bad := range []string{"", "   ", strings.Repeat("x", 17), "a\x00b"} {
		if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-register", "", map[string]string{"display_name": bad}); c != 400 {
			t.Errorf("name %q: %d", bad, c)
		}
	}
	e.h.Limiter = middleware.NewIPRateLimiter(3)
	got429 := false
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest("POST", "/api/v1/games/deadweight/guest-register", strings.NewReader(`{"display_name":"r`+strconv.Itoa(i)+`"}`))
		req.RemoteAddr = "10.0.0.9:1234"
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code == 429 {
			got429 = true
		}
	}
	if !got429 {
		t.Error("rate limit never tripped")
	}
}

// ---- game-scoped checkpoint registry ----

func TestGameCheckpoints_ScopedRegistryAndBrawlpitUnchanged(t *testing.T) {
	keys, _ := authjwt.GenerateKeys()
	db := newGameDB(t)
	dwDir, bpDir := t.TempDir(), t.TempDir()
	cfgs := testGames()
	dw := cfgs["deadweight"]
	dw.CheckpointBlobDir = dwDir
	cfgs["deadweight"] = dw
	router := &handlers.GameCheckpointsRouter{DB: db, Keys: keys, Games: cfgs}
	bp := &handlers.BrawlpitCheckpointsHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: bpDir}}

	agent := func(perm string) string {
		tk, _ := authjwt.Sign(keys, map[string]any{"sub": "a", "permissions": []string{perm}, "exp": time.Now().Add(time.Hour).Unix()})
		return tk
	}
	up := func(h http.Handler, path, token, role string) (int, map[string]any) {
		body, ct := multipartUploadBody(t, role, "1", "1500", "box", "c.zip", []byte("ckpt-bytes-"+role))
		req := httptest.NewRequest("POST", path, body)
		req.Header.Set("Content-Type", ct)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	get := func(h http.Handler, path string) (int, []byte) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code, rec.Body.Bytes()
	}

	// writes need the game's permission
	if c, _ := up(router, "/api/v1/game-checkpoints/deadweight", "", "main"); c != 401 {
		t.Errorf("anon upload: %d", c)
	}
	if c, _ := up(router, "/api/v1/game-checkpoints/deadweight", agent("brawlpit.checkpoints.write"), "main"); c != 403 {
		t.Errorf("wrong-game permission upload: %d", c)
	}
	code, cp := up(router, "/api/v1/game-checkpoints/deadweight", agent("deadweight.checkpoints.write"), "main_exploiter")
	if code != 201 || cp["role"] != "main_exploiter" {
		t.Fatalf("deadweight upload: %d %v", code, cp)
	}
	dwID := strconv.Itoa(int(cp["id"].(float64)))

	// public read: list/download work, scoped
	c, raw := get(router, "/api/v1/game-checkpoints/deadweight?role=main_exploiter")
	var list []map[string]any
	_ = json.Unmarshal(raw, &list)
	if c != 200 || len(list) != 1 {
		t.Fatalf("deadweight list: %d %s", c, raw)
	}
	if c, raw = get(router, "/api/v1/game-checkpoints/deadweight/"+dwID+"/download"); c != 200 || string(raw) != "ckpt-bytes-main_exploiter" {
		t.Errorf("download: %d %q", c, raw)
	}

	// brawlpit route (historic, unchanged): upload works with its own permission model upstream; here the
	// handler directly. Must not see deadweight rows and vice-versa.
	code, bcp := up(bp, "/api/v1/brawlpit-checkpoints", "", "main")
	if code != 201 {
		t.Fatalf("brawlpit upload regression: %d %v", code, bcp)
	}
	bpID := strconv.Itoa(int(bcp["id"].(float64)))
	c, raw = get(bp, "/api/v1/brawlpit-checkpoints")
	list = nil
	_ = json.Unmarshal(raw, &list)
	if c != 200 || len(list) != 1 || list[0]["role"] != "main" {
		t.Errorf("brawlpit list leaked or lost rows: %s", raw)
	}
	if c, _ = get(bp, "/api/v1/brawlpit-checkpoints/"+dwID+"/download"); c != 404 {
		t.Errorf("brawlpit can read a deadweight checkpoint: %d", c)
	}
	if c, _ = get(router, "/api/v1/game-checkpoints/deadweight/"+bpID+"/download"); c != 404 {
		t.Errorf("deadweight can read a brawlpit checkpoint: %d", c)
	}
	var g string
	_ = db.QueryRow(`SELECT game FROM brawlpit_rl_checkpoints WHERE id=?`, bpID).Scan(&g)
	if g != "brawlpit" {
		t.Errorf("brawlpit row game = %q", g)
	}

	// match-result on the game registry needs the write permission and is game-scoped
	_, cp2 := up(router, "/api/v1/game-checkpoints/deadweight", agent("deadweight.checkpoints.write"), "main")
	id2 := int64(cp2["id"].(float64))
	mr := func(token string, a, b int64) int {
		bd, _ := json.Marshal(map[string]any{"a_id": a, "b_id": b, "score_a": 1.0})
		req := httptest.NewRequest("POST", "/api/v1/game-checkpoints/deadweight/match-result", bytes.NewReader(bd))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	dwInt, _ := strconv.ParseInt(dwID, 10, 64)
	bpInt, _ := strconv.ParseInt(bpID, 10, 64)
	if c := mr("", dwInt, id2); c != 401 {
		t.Errorf("anon match-result: %d", c)
	}
	if c := mr(agent("deadweight.checkpoints.write"), dwInt, id2); c != 200 {
		t.Errorf("match-result: %d", c)
	}
	if c := mr(agent("deadweight.checkpoints.write"), dwInt, bpInt); c != 400 {
		t.Errorf("cross-game match-result should be refused: %d", c)
	}
	// unknown game
	if c, _ := get(router, "/api/v1/game-checkpoints/nope"); c != 404 {
		t.Errorf("unknown game registry: %d", c)
	}
}

// --- Steam auth + ticket ledger (S507) ---------------------------------------------------

func steamGames() map[string]games.Config {
	m := testGames()
	dw := m["deadweight"]
	dw.SteamAppID = "480" // test app id, no real Steam credentials involved
	dw.TicketsWritePerm = "deadweight.tickets.write"
	m["deadweight"] = dw
	return m
}

func newSteamEnv(t *testing.T) *gameEnv {
	t.Helper()
	keys, err := authjwt.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	db := newGameDB(t)
	return &gameEnv{h: &handlers.GameOnlineHandler{DB: db, Keys: keys, Games: steamGames()}, keys: keys, db: db}
}

func TestSteamLogin_NotConfiguredWithoutAppID(t *testing.T) {
	e := newGameEnv(t) // testGames() leaves deadweight.SteamAppID empty
	code, _, _ := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "aabbcc"})
	if code != 404 {
		t.Fatalf("expected 404 with no SteamAppID configured, got %d", code)
	}
}

func TestSteamLogin_NotConfiguredWithoutAPIKey(t *testing.T) {
	t.Setenv("STEAM_WEB_API_KEY", "")
	e := newSteamEnv(t)
	code, _, _ := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "aabbcc"})
	if code != 501 {
		t.Fatalf("expected 501 with no STEAM_WEB_API_KEY, got %d", code)
	}
}

func TestSteamLogin_ShadowProvisionsAndGrantsOneTicket(t *testing.T) {
	t.Setenv("STEAM_WEB_API_KEY", "fake-test-key")
	e := newSteamEnv(t)

	orig := handlers.SteamAuthenticateFn
	t.Cleanup(func() { handlers.SteamAuthenticateFn = orig })
	handlers.SteamAuthenticateFn = func(apiKey, appID, ticketHex string) (*handlers.SteamAuthResult, error) {
		if apiKey != "fake-test-key" || appID != "480" {
			t.Errorf("unexpected steam call: key=%s appid=%s ticket=%s", apiKey, appID, ticketHex)
		}
		return &handlers.SteamAuthResult{SteamID: "76561198000000123"}, nil
	}

	code, m, raw := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "aabbcc"})
	if code != 200 {
		t.Fatalf("steam-login: %d %s", code, raw)
	}
	pid, _ := m["player_id"].(string)
	if pid == "" || m["token"] == "" || m["is_new"] != true {
		t.Fatalf("unexpected first-login response: %v", m)
	}
	if m["tickets"].(float64) != 1 {
		t.Errorf("expected 1 starter ticket, got %v", m["tickets"])
	}
	var provider, providerSub string
	_ = e.db.QueryRow(`SELECT provider, provider_sub FROM players WHERE player_id=?`, pid).Scan(&provider, &providerSub)
	if provider != "steam" || providerSub != "76561198000000123" {
		t.Errorf("shadow account not provisioned correctly: provider=%s sub=%s", provider, providerSub)
	}

	// Re-login with the same SteamID64 must NOT grant a second ticket and must return the same player_id.
	code2, m2, _ := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "ddeeff"})
	if code2 != 200 || m2["player_id"] != pid || m2["is_new"] != false || m2["tickets"].(float64) != 1 {
		t.Fatalf("re-login should reuse the shadow account with no extra ticket: %d %v", code2, m2)
	}
}

func TestSteamLogin_BannedAccountRejected(t *testing.T) {
	t.Setenv("STEAM_WEB_API_KEY", "fake-test-key")
	e := newSteamEnv(t)
	orig := handlers.SteamAuthenticateFn
	t.Cleanup(func() { handlers.SteamAuthenticateFn = orig })
	handlers.SteamAuthenticateFn = func(apiKey, appID, ticketHex string) (*handlers.SteamAuthResult, error) {
		return &handlers.SteamAuthResult{SteamID: "76561198000000999", VACBanned: true}, nil
	}
	code, _, _ := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "aabbcc"})
	if code != 403 {
		t.Fatalf("expected 403 for VAC banned account, got %d", code)
	}
}

func TestTicketsConsume_AtomicAndGated(t *testing.T) {
	t.Setenv("STEAM_WEB_API_KEY", "fake-test-key")
	e := newSteamEnv(t)
	orig := handlers.SteamAuthenticateFn
	t.Cleanup(func() { handlers.SteamAuthenticateFn = orig })
	handlers.SteamAuthenticateFn = func(apiKey, appID, ticketHex string) (*handlers.SteamAuthResult, error) {
		return &handlers.SteamAuthResult{SteamID: "76561198000000456"}, nil
	}
	_, m, _ := e.do("POST", "/api/v1/games/deadweight/steam-login", "", map[string]string{"ticket": "aabbcc"})
	pid := m["player_id"].(string)

	// unauthenticated / wrong permission both refused
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/tickets/consume", "", map[string]string{"player_id": pid}); c != 401 {
		t.Errorf("anon consume: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/tickets/consume", e.agentToken(t, "deadweight.match.write"), map[string]string{"player_id": pid}); c != 403 {
		t.Errorf("wrong-perm consume: %d", c)
	}

	serverTok := e.agentToken(t, "deadweight.tickets.write")
	code, m2, _ := e.do("POST", "/api/v1/games/deadweight/tickets/consume", serverTok, map[string]string{"player_id": pid})
	if code != 200 || m2["tickets"].(float64) != 0 {
		t.Fatalf("first consume should succeed and leave 0: %d %v", code, m2)
	}
	code, m3, _ := e.do("POST", "/api/v1/games/deadweight/tickets/consume", serverTok, map[string]string{"player_id": pid})
	if code != http.StatusPaymentRequired {
		t.Fatalf("second consume with 0 tickets should 402, got %d %v", code, m3)
	}

	code, m4, _ := e.do("GET", "/api/v1/games/deadweight/players/"+pid+"/tickets", "", nil)
	if code != 200 || m4["tickets"].(float64) != 0 {
		t.Fatalf("tickets read: %d %v", code, m4)
	}
}

// --- Redeem codes + daily freebie (S508) --------------------------------------------------

func seedClaimCode(t *testing.T, db *sql.DB, code, game string, tickets, founder int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO game_claim_codes (code, game, tickets, founder_flag) VALUES (?,?,?,?)`,
		code, game, tickets, founder); err != nil {
		t.Fatal(err)
	}
}

func TestRedeem_GrantsTicketsAndFounderFlagOnce(t *testing.T) {
	e := newGameEnv(t)
	pid, _, tok := e.register(t, "deadweight", "Ada")
	seedClaimCode(t, e.db, "FOUND-1ER1P-ACK00-1AAAA-ABBBB", "deadweight", 10, 1)

	code, m, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "found-1er1p-ack00-1aaaa-abbbb"})
	if code != 200 {
		t.Fatalf("redeem: %d %s", code, raw)
	}
	// register() already tops tickets up to tier_alpha's cap (20), so the balance after
	// redeeming a 10-ticket code is 30, not 10.
	if m["tickets_granted"].(float64) != 10 || m["founder"] != true || m["tickets"].(float64) != 30 {
		t.Fatalf("unexpected redeem response: %v", m)
	}
	var isFounder int
	_ = e.db.QueryRow(`SELECT is_founder FROM players WHERE player_id=?`, pid).Scan(&isFounder)
	if isFounder != 1 {
		t.Errorf("is_founder not set")
	}

	// Same code again must fail -- already used.
	code2, _, _ := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "FOUND-1ER1P-ACK00-1AAAA-ABBBB"})
	if code2 != 400 {
		t.Fatalf("re-redeem should 400, got %d", code2)
	}

	// Unknown code, no auth, wrong game -- all real refusals.
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "NOSUC-HCODE-12XXX-XXXXX-XXXXX"}); c != 400 {
		t.Errorf("unknown code: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/redeem", "", map[string]string{"code": "FOUND-1ER1P-ACK00-1AAAA-ABBBB"}); c != 401 {
		t.Errorf("anon redeem: %d", c)
	}
	botTok := e.agentToken(t, "deadweight.bot.play")
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/redeem", botTok, map[string]string{"code": "FOUND-1ER1P-ACK00-1AAAA-ABBBB"}); c != 403 {
		t.Errorf("agent-token redeem should be refused: %d", c)
	}
}

func TestRedeem_PasteTolerant_DashlessAndWhitespace(t *testing.T) {
	e := newGameEnv(t)
	_, _, tok := e.register(t, "deadweight", "Ada")
	seedClaimCode(t, e.db, "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE", "deadweight", 3, 0)
	seedClaimCode(t, e.db, "FFFFF-GGGGG-HHHHH-JJJJJ-KKKKK", "deadweight", 4, 0)

	// Pasted without dashes -- re-grouped into the real 5x5 code, not rejected.
	if c, m, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "aaaaabbbbbcccccdddddeeeee"}); c != 200 {
		t.Fatalf("dashless paste should be accepted: %d %s", c, raw)
	} else if m["tickets_granted"].(float64) != 3 {
		t.Fatalf("dashless paste: %v", m)
	}
	// Pasted with leading/trailing whitespace (the real, common paste artifact) -- still accepted.
	if c, m, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "  FFFFF-GGGGG-HHHHH-JJJJJ-KKKKK\n"}); c != 200 {
		t.Fatalf("whitespace-wrapped paste should be accepted: %d %s", c, raw)
	} else if m["tickets_granted"].(float64) != 4 {
		t.Fatalf("whitespace paste: %v", m)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "too-short"}); c != 400 {
		t.Errorf("malformed code should 400, got %d", c)
	}
}

func TestRedeem_RefillCodeAddsToExistingBalance(t *testing.T) {
	e := newGameEnv(t)
	_, _, tok := e.register(t, "deadweight", "Ada")
	seedClaimCode(t, e.db, "START-10TIX-XXXXX-YYYYY-ZZZZZ", "deadweight", 10, 0)
	seedClaimCode(t, e.db, "REFIL-L5TIX-01XXX-XXXXW-WWWWW", "deadweight", 5, 0)

	// register() already tops tickets up to tier_alpha's cap (20) -- baseline is 20, not 0.
	_, m1, _ := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "START-10TIX-XXXXX-YYYYY-ZZZZZ"})
	if m1["tickets"].(float64) != 30 {
		t.Fatalf("first redeem: %v", m1)
	}
	_, m2, _ := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "REFIL-L5TIX-01XXX-XXXXW-WWWWW"})
	if m2["tickets"].(float64) != 35 || m2["founder"] != false {
		t.Fatalf("refill should stack onto existing balance: %v", m2)
	}
}

func TestDailyTopUp_GrantedOnRegisterToTierCapNotDoubledOnImmediateRelogin(t *testing.T) {
	e := newGameEnv(t)
	pid, secret, _ := e.register(t, "deadweight", "Ada")

	var tickets int
	var tier string
	_ = e.db.QueryRow(`SELECT tickets, tier FROM game_player_tickets WHERE player_id=? AND game='deadweight'`, pid).Scan(&tickets, &tier)
	if tickets != 20 || tier != "tier_alpha" {
		t.Fatalf("expected tickets topped up to tier_alpha's cap (20) on first register, got tickets=%d tier=%s", tickets, tier)
	}

	// Logging back in moments later must NOT top up again (already at cap, nothing to raise).
	_, m, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": secret})
	if m["tickets"].(float64) != 20 {
		t.Fatalf("immediate re-login should not change the balance: %v", m)
	}

	// Spend some tickets, force the clock back 25h, confirm the next login tops back up to the
	// cap -- and NEVER above it, even though the GREATEST-not-overwrite semantics also mean a
	// balance already above the cap (e.g. from a claim code) is never reduced.
	if _, err := e.db.Exec(`UPDATE game_player_tickets SET tickets = 3, last_ticket_topup_at = datetime('now', '-25 hours') WHERE player_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	_, m2, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": secret})
	if m2["tickets"].(float64) != 20 {
		t.Fatalf("expected the daily top-up to raise a below-cap balance back to 20, got %v", m2)
	}
}

func TestDailyTopUp_NeverLowersABalanceAboveTheCap(t *testing.T) {
	e := newGameEnv(t)
	pid, secret, _ := e.register(t, "deadweight", "Ada")
	seedClaimCode(t, e.db, "BIGWI-NFOUN-DXXXX-XVVVV-VVVVV", "deadweight", 50, 0)
	_, mlog, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": secret})
	tok := mlog["token"].(string)
	_, mr, raw := e.do("POST", "/api/v1/games/deadweight/redeem", tok, map[string]string{"code": "BIGWI-NFOUN-DXXXX-XVVVV-VVVVV"})
	if mr["tickets"].(float64) != 70 {
		t.Fatalf("redeem: %v %s", mr, raw)
	}
	if _, err := e.db.Exec(`UPDATE game_player_tickets SET last_ticket_topup_at = datetime('now', '-25 hours') WHERE player_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	_, m2, _ := e.do("POST", "/api/v1/games/deadweight/guest-login", "", map[string]string{"player_id": pid, "guest_secret": secret})
	if m2["tickets"].(float64) != 70 {
		t.Fatalf("daily top-up must never lower a balance already above the tier cap, got %v", m2)
	}
}

// --- Guest -> email upgrade, uncapped draft runs (S508c) --------------------------------------

func TestAccountState_DerivedFromCredentialsNotAColumn(t *testing.T) {
	e := newGameEnv(t)
	_, _, tok := e.register(t, "deadweight", "Ada")

	// Fresh registration: Guest.
	_, mv, _ := e.do("POST", "/api/v1/games/deadweight/verify", tok, nil)
	if mv["account_state"] != "guest" {
		t.Fatalf("fresh guest account should report account_state=guest, got %v", mv)
	}

	// After linking email: Base -- derived live, no stored column to get out of sync.
	_, mu, _ := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", tok,
		map[string]string{"email": "state@example.com", "password": "correcthorsebattery"})
	if mu["account_state"] != "base" {
		t.Fatalf("guest-upgrade response should report account_state=base, got %v", mu)
	}
	newTok := mu["token"].(string)
	_, mv2, _ := e.do("POST", "/api/v1/games/deadweight/verify", newTok, nil)
	if mv2["account_state"] != "base" {
		t.Fatalf("verify after upgrade should report account_state=base, got %v", mv2)
	}
}

func TestGuestUpgrade_SamePlayerIDCarriesOverTicketsAndStats(t *testing.T) {
	e := newGameEnv(t)
	pid, _, tok := e.register(t, "deadweight", "Ada")

	code, m, raw := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", tok,
		map[string]string{"email": "ada@example.com", "password": "correcthorsebattery"})
	if code != 200 {
		t.Fatalf("guest-upgrade: %d %s", code, raw)
	}
	if m["player_id"] != pid {
		t.Fatalf("upgrade must keep the same player_id, got %v want %s", m["player_id"], pid)
	}
	newTok := m["token"].(string)

	// The new token still reads the SAME ticket balance (tier_alpha cap = 20 from register()).
	code2, m2, _ := e.do("GET", "/api/v1/games/deadweight/players/"+pid+"/tickets", "", nil)
	if code2 != 200 || m2["tickets"].(float64) != 20 {
		t.Fatalf("tickets should carry over unchanged: %d %v", code2, m2)
	}

	// email-login now works and returns the same player_id + a usable token.
	code3, m3, _ := e.do("POST", "/api/v1/games/deadweight/email-login", "",
		map[string]string{"email": "ADA@EXAMPLE.COM", "password": "correcthorsebattery"})
	if code3 != 200 || m3["player_id"] != pid {
		t.Fatalf("email-login: %d %v", code3, m3)
	}
	loginTok := m3["token"].(string)
	if code4, mv, _ := e.do("POST", "/api/v1/games/deadweight/verify", loginTok, nil); code4 != 200 || mv["player_id"] != pid {
		t.Fatalf("email-login token should verify as the same player: %d %v", code4, mv)
	}

	// wrong password refused
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/email-login", "", map[string]string{"email": "ada@example.com", "password": "wrongwrongwrong"}); c != 401 {
		t.Errorf("wrong password: %d", c)
	}
	_ = newTok
}

func TestGuestUpgrade_RequiresPlayerTokenAndValidEmail(t *testing.T) {
	e := newGameEnv(t)
	_, _, tok := e.register(t, "deadweight", "Ada")

	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", "", map[string]string{"email": "a@b.com", "password": "longenoughpass"}); c != 401 {
		t.Errorf("anon upgrade: %d", c)
	}
	botTok := e.agentToken(t, "deadweight.bot.play")
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", botTok, map[string]string{"email": "a@b.com", "password": "longenoughpass"}); c != 403 {
		t.Errorf("agent-token upgrade: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", tok, map[string]string{"email": "not-an-email", "password": "longenoughpass"}); c != 400 {
		t.Errorf("invalid email: %d", c)
	}
	if c, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-upgrade", tok, map[string]string{"email": "a@b.com", "password": "short"}); c != 400 {
		t.Errorf("short password: %d", c)
	}
}

func TestSignupRateLimit_ThreePerIPPerDay(t *testing.T) {
	e := newGameEnv(t)
	for i := 0; i < 3; i++ {
		code, _, raw := e.do("POST", "/api/v1/games/deadweight/guest-register", "", map[string]string{"display_name": "P"})
		if code != 201 {
			t.Fatalf("signup %d should succeed: %d %s", i, code, raw)
		}
	}
	code, _, _ := e.do("POST", "/api/v1/games/deadweight/guest-register", "", map[string]string{"display_name": "P4"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("4th signup from the same IP within 24h should be refused, got %d", code)
	}
}

func draftRunStartReq(e *gameEnv, t *testing.T, pid string) (int, map[string]any) {
	t.Helper()
	code, m, _ := e.do("POST", "/api/v1/games/deadweight/draft-run/start", e.agentToken(t, "deadweight.tickets.write"), map[string]string{"player_id": pid})
	return code, m
}

func TestUncappedDraftRun_ConsumesOneTicketThenResumesFree(t *testing.T) {
	e := newGameEnv(t)
	pid, _, _ := e.register(t, "deadweight", "Ada") // 20 tickets (tier_alpha)

	code, m := draftRunStartReq(e, t, pid)
	if code != 200 || m["resumed"] != false || m["ticket_spent"] != true {
		t.Fatalf("first draft-run/start should spend a ticket: %d %v", code, m)
	}
	var tickets int
	_ = e.db.QueryRow(`SELECT tickets FROM game_player_tickets WHERE player_id=?`, pid).Scan(&tickets)
	if tickets != 19 {
		t.Fatalf("expected 19 tickets after starting a run, got %d", tickets)
	}

	// Calling start again while the run is still active must NOT spend a second ticket.
	code2, m2 := draftRunStartReq(e, t, pid)
	if code2 != 200 || m2["resumed"] != true || m2["ticket_spent"] != false {
		t.Fatalf("resuming an active run should not spend a ticket: %d %v", code2, m2)
	}
	_ = e.db.QueryRow(`SELECT tickets FROM game_player_tickets WHERE player_id=?`, pid).Scan(&tickets)
	if tickets != 19 {
		t.Fatalf("resume must not change the ticket balance, got %d", tickets)
	}
}

func TestUncappedDraftRun_InsufficientTicketsRefused(t *testing.T) {
	e := newGameEnv(t)
	pid, _, _ := e.register(t, "deadweight", "Ada")
	if _, err := e.db.Exec(`UPDATE game_player_tickets SET tickets = 0 WHERE player_id=?`, pid); err != nil {
		t.Fatal(err)
	}
	code, _ := draftRunStartReq(e, t, pid)
	if code != http.StatusPaymentRequired {
		t.Fatalf("expected 402 with 0 tickets, got %d", code)
	}
}

func TestUncappedDraftRun_NoWinCapEndsStrictlyAtThreeLosses(t *testing.T) {
	e := newGameEnv(t)
	pid0, _, _ := e.register(t, "deadweight", "Ada")
	pid1, _, _ := e.register(t, "deadweight", "Bob")
	if code, _ := draftRunStartReq(e, t, pid0); code != 200 {
		t.Fatal("start run for pid0")
	}

	serverTok := e.agentToken(t, "deadweight.match.write")
	report := func(matchID int64, winner int) int {
		body := map[string]any{
			"match_id": matchID, "seed": matchID, "seat0_player_id": pid0, "seat1_player_id": pid1,
			"winner": winner, "rounds": 5, "reason": "hull", "mode": 2,
		}
		code, _, _ := e.do("POST", "/api/v1/games/deadweight/match-result", serverTok, body)
		return code
	}

	// pid0 racks up 12 wins (well past any Hearthstone-style 12-win cap) with no cap enforced.
	for i := int64(1); i <= 12; i++ {
		if c := report(i, 0); c != 200 {
			t.Fatalf("match %d report: %d", i, c)
		}
	}
	var wins, losses, active int
	_ = e.db.QueryRow(`SELECT wins, losses, active FROM game_draft_runs WHERE player_id=?`, pid0).Scan(&wins, &losses, &active)
	if wins != 12 || losses != 0 || active != 1 {
		t.Fatalf("expected 12 wins, 0 losses, still active: wins=%d losses=%d active=%d", wins, losses, active)
	}

	// 3 losses in a row end the run and post the final win count to the leaderboard.
	for i := int64(13); i <= 15; i++ {
		if c := report(i, 1); c != 200 { // seat1 (pid1) wins -> pid0 takes a loss
			t.Fatalf("match %d report: %d", i, c)
		}
	}
	_ = e.db.QueryRow(`SELECT wins, losses, active FROM game_draft_runs WHERE player_id=?`, pid0).Scan(&wins, &losses, &active)
	if wins != 0 || losses != 0 || active != 0 {
		t.Fatalf("run should be wiped after 3 losses: wins=%d losses=%d active=%d", wins, losses, active)
	}

	code, _, raw := e.do("GET", "/api/v1/games/deadweight/draft-runs/leaderboard", "", nil)
	if code != 200 {
		t.Fatalf("leaderboard: %d %s", code, raw)
	}
	arr := m2arr(t, raw)
	if len(arr) != 1 || arr[0]["wins"].(float64) != 12 || arr[0]["player_id"] != pid0 {
		t.Fatalf("expected one leaderboard entry with 12 wins for pid0: %v", arr)
	}

	// A fresh run needs a fresh ticket -- the row still exists (active=0) but that's not an
	// active run, so the next start spends a new ticket rather than resuming for free.
	code2, m2r := draftRunStartReq(e, t, pid0)
	if code2 != 200 || m2r["resumed"] != false || m2r["ticket_spent"] != true {
		t.Fatalf("starting a new run after the previous one ended should spend a fresh ticket: %d %v", code2, m2r)
	}
}

func m2arr(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("expected a JSON array: %v (%s)", err, raw)
	}
	return arr
}
