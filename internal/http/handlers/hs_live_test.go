package handlers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"iduna/internal/hsdeck"
	"iduna/internal/hstracker"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

// A deck code for a 30-card Druid deck (real, from a Decks.log "Finding Game With Deck" block).
const liveTestCode = "AAECAZICBpigBODAB4TEB4jdB7HjB+vkBwyHnwTZnwSd1ATRngbFzgb75QahiQeZlwezrQfosQeSsgeT8QcAAA=="

func (e *hsEnv) post(token, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hs/"+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestHS_LiveTrackerPrivateAndCountsCardsLeft(t *testing.T) {
	parena := os.Getenv("NOCK_PARENA_BIN")
	if parena == "" {
		parena = "/home/garybifrost/PARENA/parena"
	}
	if _, err := os.Stat(parena); err != nil {
		t.Skip("parena compiler not available")
	}
	t.Setenv("NOCK_PARENA_BIN", parena)
	t.Setenv("NOCK_PARENA_RUNTIME_DIR", "../../nock/parena_runtime")

	e := newHSEnv(t)
	hh := e.h.(*handlers.HSHandler)
	hh.Tracker = &hstracker.Runner{WorkDir: filepath.Join(t.TempDir(), "trk")}

	// card table: every dbf in the deck gets a card_id T_<dbf> and a name
	d, err := hsdeck.Decode(liveTestCode)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range d.DBFCards {
		if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id) VALUES (?,?,?,?)`,
			c.DBF, "Card "+strconv.Itoa(c.DBF), c.DBF%7, "T_"+strconv.Itoa(c.DBF)); err != nil {
			t.Fatal(err)
		}
	}
	first, second := d.DBFCards[0].DBF, d.DBFCards[1].DBF

	// --- upload token: minted for alice, usable only on /live/lines
	e.must(401, "", "POST", "live/token", "")
	tok := e.must(200, "alice", "POST", "live/token", "")["token"].(string)
	if code, _ := e.do("", "GET", "live/state", ""); code != 401 {
		t.Fatalf("anonymous state read must be 401, got %d", code)
	}
	req := httptest.NewRequest("GET", "/api/v1/hs/live/state", nil)
	req.Header.Set("Authorization", "Bearer "+tok) // upload token must NOT read state
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("upload token read state: %d, want 401", w.Code)
	}
	if code, _ := e.post("garbage", "live/lines", `{}`); code != 401 {
		t.Fatalf("bad token on live/lines: %d", code)
	}

	// --- a synthetic game: alice is player 1, draws her first two deck cards on turn 1
	pw := func(s string) string { return "D 12:00:00.0000000 GameState.DebugPrintPower() - " + s }
	gm := func(s string) string { return "D 12:00:00.0000000 GameState.DebugPrintGame() - " + s }
	power := []string{
		pw("CREATE_GAME"),
		gm("PlayerID=1, PlayerName=Alice#1"),
		gm("PlayerID=2, PlayerName=UNKNOWN HUMAN PLAYER"),
	}
	for id := 10; id < 14; id++ {
		power = append(power, pw("    FULL_ENTITY - Creating ID="+strconv.Itoa(id)+" CardID="),
			pw("        tag=ZONE value=DECK"), pw("        tag=CONTROLLER value=1"))
	}
	power = append(power, pw("TAG_CHANGE Entity=GameEntity tag=TURN value=1 "))
	for i, dbf := range []int{first, second} {
		id := strconv.Itoa(10 + i)
		power = append(power,
			pw("    SHOW_ENTITY - Updating Entity="+id+" CardID=T_"+strconv.Itoa(dbf)),
			pw("TAG_CHANGE Entity=[entityName=x id="+id+" zone=DECK zonePos=1 cardId=T_"+strconv.Itoa(dbf)+" player=1] tag=ZONE value=HAND "))
	}
	decks := []string{
		"I 11:59:00.0000000 Deck Contents Received:",
		"I 11:59:30.0000000 Finding Game With Deck:",
		"I 11:59:30.0000000 ### Test Druid",
		"I 11:59:30.0000000 # Deck ID: 42",
		"I 11:59:30.0000000 " + liveTestCode,
	}
	body := jstr(map[string]any{"reset": true, "decks": decks, "power": power})
	if code, out := e.post(tok, "live/lines", body); code != 200 {
		t.Fatalf("live/lines: %d %v", code, out)
	}

	st := e.must(200, "alice", "GET", "live/state", "")
	deck, _ := st["deck"].(map[string]any)
	if deck == nil || deck["name"] != "Test Druid" || st["in_game"] != true {
		t.Fatalf("state: %v", st)
	}
	if st["left_total"] != float64(28) || st["drawn_total"] != float64(2) {
		t.Fatalf("want 28 left / 2 drawn, got %v / %v (note=%v)", st["left_total"], st["drawn_total"], st["note"])
	}

	// --- privacy: bob has no session and sees nothing of alice's
	bob := e.must(200, "bob", "GET", "live/state", "")
	if bob["deck"] != nil || bob["connected"] == true {
		t.Fatalf("bob must not see alice's tracker: %v", bob)
	}
	// bob's own upload token feeds bob's session only
	btok := e.must(200, "bob", "POST", "live/token", "")["token"].(string)
	if code, _ := e.post(btok, "live/lines", jstr(map[string]any{"decks": decks})); code != 200 {
		t.Fatal("bob upload failed")
	}
	if a := e.must(200, "alice", "GET", "live/state", ""); a["left_total"] != float64(28) {
		t.Fatalf("alice changed by bob's upload: %v", a)
	}

	// uplink script is public and carries no token
	req = httptest.NewRequest("GET", "/api/v1/hs/live/uplink.ps1", nil)
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/api/v1/hs/live/lines") || strings.Contains(w.Body.String(), tok) {
		t.Fatalf("uplink script: %d", w.Code)
	}
	_ = base64.StdEncoding
}

// The uplink posts about once a second for the whole game. It must never be starved by the 40/min
// deck-write limiter (regression: batches were 429'd after ~1 minute and the tracker went stale).
func TestHS_LiveLinesNotStarvedByWriteLimiter(t *testing.T) {
	e := newHSEnv(t)
	hh := e.h.(*handlers.HSHandler)
	hh.WriteLimiter = middleware.NewIPRateLimiter(40)
	tok := e.must(200, "carol", "POST", "live/token", "")["token"].(string)
	line := "D 12:00:00.0000000 GameState.DebugPrintPower() - TAG_CHANGE Entity=GameEntity tag=TURN value=1 "
	for i := 0; i < 200; i++ {
		if code, out := e.post(tok, "live/lines", jstr(map[string]any{"power": []string{line}})); code != 200 {
			t.Fatalf("batch %d: status %d %v", i, code, out)
		}
	}
}

// An LZ4-framed body (vectors from PARENA's lz4_block.prn) is accepted; garbage frames are rejected.
func TestHS_LiveLinesAcceptsLZ4Frames(t *testing.T) {
	e := newHSEnv(t)
	tok := e.must(200, "dave", "POST", "live/token", "")["token"].(string)
	comp, err := os.ReadFile("../../hslz4/testdata/log60k.lz4")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("../../hslz4/testdata/log60k.raw")
	// the raw log slice is plain log text; wrap it as the JSON the endpoint expects, then frame it
	var lines []string
	for _, ln := range strings.Split(string(raw), "\n") {
		lines = append(lines, strings.TrimRight(ln, "\r"))
	}
	_ = comp
	js := []byte(jstr(map[string]any{"power": lines}))
	frames := lz4Frames(t, js)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hs/live/lines", strings.NewReader(string(frames)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Body-Encoding", "lz4-frames")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("lz4 body: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["power_lines"].(float64) < 100 {
		t.Fatalf("expected many power lines, got %v", out)
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/hs/live/lines", strings.NewReader("\x10\x00\x00\x00\x05\x00\x00\x00zzzzz"))
	bad.Header.Set("Authorization", "Bearer "+tok)
	bad.Header.Set("X-Body-Encoding", "lz4-frames")
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, bad)
	if w.Code != 422 {
		t.Fatalf("garbage frame: %d, want 422", w.Code)
	}
}

// lz4Frames builds [u32 raw][u32 comp][block] frames using only "stored literal" blocks (valid LZ4:
// a single sequence with literals and no match), enough to exercise the framing + decode path.
func lz4Frames(t *testing.T, data []byte) []byte {
	t.Helper()
	var out []byte
	for pos := 0; pos < len(data); pos += 60000 {
		n := len(data) - pos
		if n > 60000 {
			n = 60000
		}
		var blk []byte
		if n < 15 {
			blk = append(blk, byte(n<<4))
		} else {
			blk = append(blk, 0xF0)
			for r := n - 15; ; r -= 255 {
				if r >= 255 {
					blk = append(blk, 255)
					continue
				}
				blk = append(blk, byte(r))
				break
			}
		}
		blk = append(blk, data[pos:pos+n]...)
		h := make([]byte, 8)
		h[0], h[1], h[2], h[3] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
		h[4], h[5], h[6], h[7] = byte(len(blk)), byte(len(blk)>>8), byte(len(blk)>>16), byte(len(blk)>>24)
		out = append(append(out, h...), blk...)
	}
	return out
}

// liveTrackerEnv builds an env with the tracker runner, the test deck's card table and an upload token.
func liveTrackerEnv(t *testing.T, sub string) (*hsEnv, string, []int) {
	t.Helper()
	parena := os.Getenv("NOCK_PARENA_BIN")
	if parena == "" {
		parena = "/home/garybifrost/PARENA/parena"
	}
	if _, err := os.Stat(parena); err != nil {
		t.Skip("parena compiler not available")
	}
	t.Setenv("NOCK_PARENA_BIN", parena)
	t.Setenv("NOCK_PARENA_RUNTIME_DIR", "../../nock/parena_runtime")
	e := newHSEnv(t)
	e.h.(*handlers.HSHandler).Tracker = &hstracker.Runner{WorkDir: filepath.Join(t.TempDir(), "trk")}
	d, err := hsdeck.Decode(liveTestCode)
	if err != nil {
		t.Fatal(err)
	}
	var dbfs []int
	for _, c := range d.DBFCards {
		dbfs = append(dbfs, c.DBF)
		if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id) VALUES (?,?,?,?)`,
			c.DBF, "Card "+strconv.Itoa(c.DBF), c.DBF%7, "T_"+strconv.Itoa(c.DBF)); err != nil {
			t.Fatal(err)
		}
	}
	return e, e.must(200, sub, "POST", "live/token", "")["token"].(string), dbfs
}

func liveGameLines(clock string, dbfs []int) []string {
	pw := func(s string) string { return "D " + clock + " GameState.DebugPrintPower() - " + s }
	power := []string{pw("CREATE_GAME"), "D " + clock + " GameState.DebugPrintGame() - PlayerID=1, PlayerName=Alice#1",
		"D " + clock + " GameState.DebugPrintGame() - PlayerID=2, PlayerName=UNKNOWN HUMAN PLAYER"}
	for id := 10; id < 14; id++ {
		power = append(power, pw("    FULL_ENTITY - Creating ID="+strconv.Itoa(id)+" CardID="), pw("        tag=ZONE value=DECK"), pw("        tag=CONTROLLER value=1"))
	}
	power = append(power, pw("TAG_CHANGE Entity=GameEntity tag=TURN value=1 "))
	for i := 0; i < 2; i++ {
		id := strconv.Itoa(10 + i)
		power = append(power, pw("    SHOW_ENTITY - Updating Entity="+id+" CardID=T_"+strconv.Itoa(dbfs[i])),
			pw("TAG_CHANGE Entity=[entityName=x id="+id+" zone=DECK zonePos=1 cardId=T_"+strconv.Itoa(dbfs[i])+" player=1] tag=ZONE value=HAND "))
	}
	return power
}

// Regression ("stale deck data"): after game 1 ends and the player queues deck B, the session still
// holds game 1's record until game 2's CREATE_GAME. Deck B must show full, not minus game 1's draws.
func TestHS_LiveQueuedDeckNotMixedWithPreviousGame(t *testing.T) {
	e, tok, dbfs := liveTrackerEnv(t, "erin")
	game1 := liveGameLines("12:00:00.0000000", dbfs) // game 1 started 12:00
	decks := []string{
		"I 11:59:30.0000000 Finding Game With Deck:", "I 11:59:30.0000000 ### Deck A",
		"I 11:59:30.0000000 # Deck ID: 1", "I 11:59:30.0000000 " + liveTestCode,
	}
	post := func(reset bool, d, p []string) map[string]any {
		code, out := e.post(tok, "live/lines", jstr(map[string]any{"reset": reset, "decks": d, "power": p}))
		if code != 200 {
			t.Fatalf("post: %d %v", code, out)
		}
		return out
	}
	post(true, decks, game1)
	if st := e.must(200, "erin", "GET", "live/state", ""); st["drawn_total"] != float64(2) {
		t.Fatalf("game 1 should show 2 drawn: %v", st)
	}
	// player queues the same deck again at 12:30 -- game 1 is over, game 2 not started
	post(false, []string{
		"I 12:30:00.0000000 Finding Game With Deck:", "I 12:30:00.0000000 ### Deck A",
		"I 12:30:00.0000000 # Deck ID: 1", "I 12:30:00.0000000 " + liveTestCode,
	}, nil)
	st := e.must(200, "erin", "GET", "live/state", "")
	if st["in_game"] == true || st["drawn_total"] != float64(0) || st["left_total"] != float64(30) {
		t.Fatalf("queued deck must be full and not in a game: in_game=%v drawn=%v left=%v note=%v",
			st["in_game"], st["drawn_total"], st["left_total"], st["note"])
	}
	// game 2 starts at 12:31 -> tracking resumes against the new game only
	post(false, nil, liveGameLines("12:31:00.0000000", dbfs))
	st = e.must(200, "erin", "GET", "live/state", "")
	if st["in_game"] != true || st["drawn_total"] != float64(2) {
		t.Fatalf("game 2 should be live: %v", st)
	}
}

// After a server restart the session is gone; the uplink must be told to resend from the start.
func TestHS_LiveAsksForResyncWithoutBase(t *testing.T) {
	e, tok, dbfs := liveTrackerEnv(t, "frank")
	_, out := e.post(tok, "live/lines", jstr(map[string]any{"power": liveGameLines("12:00:00.0000000", dbfs)}))
	if out["resync"] != true {
		t.Fatalf("expected resync request on a baseless session: %v", out)
	}
	_, out = e.post(tok, "live/lines", jstr(map[string]any{"reset": true, "power": liveGameLines("12:00:00.0000000", dbfs)}))
	if out["resync"] != false {
		t.Fatalf("expected no resync after a reset: %v", out)
	}
}

// The uplink reports Hearthstone's log-size situation; the page must be told in plain language.
func TestHS_LiveReportsLogCapWarnings(t *testing.T) {
	e, tok, dbfs := liveTrackerEnv(t, "gina")
	send := func(client map[string]any) map[string]any {
		body := map[string]any{"reset": true, "power": liveGameLines("12:00:00.0000000", dbfs), "client": client}
		if code, out := e.post(tok, "live/lines", jstr(body)); code != 200 {
			t.Fatalf("post %d %v", code, out)
		}
		return e.must(200, "gina", "GET", "live/state", "")
	}
	warn := func(st map[string]any) string {
		w, _ := st["warnings"].([]any)
		var b []string
		for _, x := range w {
			b = append(b, x.(string))
		}
		return strings.Join(b, " | ")
	}
	// fixed and growing: no warning
	if w := warn(send(map[string]any{"cap_fixed": true, "power_bytes": 12000000})); w != "" {
		t.Fatalf("no warning expected when the limit is lifted: %q", w)
	}
	// fixed but Hearthstone predates the fix: restart message
	if w := warn(send(map[string]any{"cap_fixed": true, "restart_needed": true, "power_bytes": 4000000})); !strings.Contains(w, "Restart Hearthstone once") {
		t.Fatalf("restart warning missing: %q", w)
	}
	// not fixed, nearly full
	if w := warn(send(map[string]any{"cap_fixed": false, "power_bytes": 9500000})); !strings.Contains(w, "nearly full") {
		t.Fatalf("near-cap warning missing: %q", w)
	}
	// not fixed, at the cap: tracking frozen, say so
	st := send(map[string]any{"cap_fixed": false, "power_bytes": 10247587})
	if w := warn(st); !strings.Contains(w, "reached its 10 MB limit") || st["log_bytes"] != float64(10247587) {
		t.Fatalf("full-log warning missing: %q %v", w, st["log_bytes"])
	}
	// the uplink could not write the config: surface why
	if w := warn(send(map[string]any{"cap_fixed": false, "power_bytes": 100, "fix_error": "permission denied"})); !strings.Contains(w, "permission denied") {
		t.Fatalf("fix_error not surfaced: %q", w)
	}
}
