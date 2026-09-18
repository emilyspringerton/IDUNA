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
