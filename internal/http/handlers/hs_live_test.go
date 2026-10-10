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
