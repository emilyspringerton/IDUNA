package handlers_test

// Bedrock Racers web over a real WebSocket against the real migrations: two guest accounts search, a lockstep room plays a whole match against the
// real wasm referee, and the match lands in the database -- the match row, each person's counts, the rated ladder and the replay -- and in the public
// stats routes the WOTAN pages read. The room and the wire themselves are tested in internal/bedrockracers.

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"iduna/internal/bedrockracers"
	"iduna/internal/http/handlers"
)

type brWS struct {
	t     *testing.T
	ws    *websocket.Conn
	mu    sync.Mutex
	ends  chan []byte
	ticks int
	errs  []byte
}

func dialBR(t *testing.T, srv *httptest.Server) *brWS {
	t.Helper()
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/bedrockracers/play", "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return &brWS{t: t, ws: ws, ends: make(chan []byte, 1)}
}

func (c *brWS) send(b []byte) {
	c.t.Helper()
	if err := websocket.Message.Send(c.ws, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *brWS) hello(simVer uint32, token, name string) {
	b := []byte{bedrockracers.CHello, bedrockracers.Version}
	b = binary.LittleEndian.AppendUint32(b, simVer)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(token)))
	b = append(b, token...)
	b = append(b, byte(len(name)))
	c.send(append(b, name...))
}

func (c *brWS) next(typ byte) []byte {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.ws.SetReadDeadline(deadline)
		var f []byte
		if err := websocket.Message.Receive(c.ws, &f); err != nil {
			c.t.Fatalf("waiting for frame 0x%02x: %v", typ, err)
		}
		if len(f) > 0 && f[0] == typ {
			return f
		}
	}
	c.t.Fatalf("no frame 0x%02x", typ)
	return nil
}

// run reads every frame (a client that does not is dropped as too slow) and keeps its car driving; leaveAt > 0 leaves after that many TICK frames
func (c *brWS) run(cmd [8]byte, leaveAt int, done <-chan struct{}) {
	_ = c.ws.SetReadDeadline(time.Time{})
	go func() {
		for {
			var f []byte
			if err := websocket.Message.Receive(c.ws, &f); err != nil {
				return
			}
			if len(f) == 0 {
				continue
			}
			switch f[0] {
			case bedrockracers.STick:
				c.mu.Lock()
				c.ticks++
				leave := leaveAt > 0 && c.ticks == leaveAt
				c.mu.Unlock()
				if leave {
					_ = websocket.Message.Send(c.ws, []byte{bedrockracers.CLeave})
				}
			case bedrockracers.SEnd:
				select {
				case c.ends <- f:
				default:
				}
			case bedrockracers.SError:
				c.mu.Lock()
				c.errs = append(c.errs, f[1])
				c.mu.Unlock()
			}
		}
	}()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				if websocket.Message.Send(c.ws, append([]byte{bedrockracers.CCmd}, cmd[:]...)) != nil {
					return
				}
			}
		}
	}()
}

func brGet(t *testing.T, srv *httptest.Server, path string, into any) []byte {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, b)
	}
	if into != nil {
		if err := json.Unmarshal(b, into); err != nil {
			t.Fatalf("GET %s: %v in %s", path, err, b)
		}
	}
	return b
}

func newBRPlay(t *testing.T, e *gameEnv) (*handlers.BedrockRacersPlay, *httptest.Server) {
	t.Helper()
	cfg := bedrockracers.DefaultConfig()
	cfg.TickEvery = 100 * time.Microsecond
	cfg.LobbyWindow = 400 * time.Millisecond
	cfg.StatusEvery = 200 * time.Millisecond
	play := handlers.NewBedrockRacersPlayWith(e.h, cfg)
	if play.Hub == nil {
		t.Fatal("the gateway did not start: the embedded wasm failed to load")
	}
	srv := httptest.NewServer(play)
	t.Cleanup(func() { srv.Close(); play.Hub.Close() })
	return play, srv
}

// brPlayOut seats two registered guests on teams 1 and 2, plays a whole match and returns both END frames (b's is nil when it left)
func brPlayOut(t *testing.T, srv *httptest.Server, play *handlers.BedrockRacersPlay, tokA, tokB string, bLeavesAt int) (a, b *brWS, endA, endB []byte) {
	t.Helper()
	a, b = dialBR(t, srv), dialBR(t, srv)
	a.hello(play.Hub.Version(), tokA, "ignored")
	b.hello(play.Hub.Version(), tokB, "ignored")
	if w := a.next(bedrockracers.SWelcome); w[2] != 0 {
		t.Fatalf("a guest account is a guest (auth 0) that is still recorded, welcome = %v", w)
	}
	b.next(bedrockracers.SWelcome)
	a.send([]byte{bedrockracers.CQueue, 1, 1})
	b.send([]byte{bedrockracers.CQueue, 2, 1})
	a.next(bedrockracers.SMatch)
	b.next(bedrockracers.SMatch)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	a.run([8]byte{127, 20, 0, 0, 0, 0, 1, 0}, 0, done)
	b.run([8]byte{127, 236, 0, 0, 0, 0, 1, 0}, bLeavesAt, done)
	select {
	case endA = <-a.ends:
	case <-time.After(90 * time.Second):
		t.Fatal("the match did not end")
	}
	if bLeavesAt == 0 {
		select {
		case endB = <-b.ends:
		case <-time.After(10 * time.Second):
			t.Fatal("the second person was not told how it ended")
		}
	}
	return
}

type brTeamRow struct {
	pid, result string
	team        int
	before, aft float64
	goals       int
}

// expectedDelta is the four-team Elo of one team against the other three at equal ratings: K/3 per pairing, a leaver loses to all of them
func expectedDelta(scores [4]int, team int, left bool) float64 {
	var d float64
	for u := 0; u < 4; u++ {
		if u == team {
			continue
		}
		s := 0.5
		switch {
		case left || scores[team] < scores[u]:
			s = 0
		case scores[team] > scores[u]:
			s = 1
		}
		d += 32.0 / 3.0 * (s - 0.5)
	}
	return d
}

func brMatchRows(t *testing.T, db *sql.DB) (scores [4]int, counted, humans, ticks int, rows []brTeamRow) {
	t.Helper()
	if err := db.QueryRow(`SELECT score0, score1, score2, score3, counted, humans, ticks FROM bedrockracers_matches`).
		Scan(&scores[0], &scores[1], &scores[2], &scores[3], &counted, &humans, &ticks); err != nil {
		t.Fatal(err)
	}
	rs, err := db.Query(`SELECT player_id, result, team, rating_before, rating_after, goals FROM bedrockracers_player_matches ORDER BY slot`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for rs.Next() {
		var r brTeamRow
		if err := rs.Scan(&r.pid, &r.result, &r.team, &r.before, &r.aft, &r.goals); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	return
}

func near(a, b float64) bool { d := a - b; return d < 0.01 && d > -0.01 }

func TestBedrockRacersPlay_RatedMatchIsRecordedAndShownInTheStats(t *testing.T) {
	if testing.Short() {
		t.Skip("plays a four-minute match as fast as the referee can step it")
	}
	e := newGameEnv(t)
	play, srv := newBRPlay(t, e)
	pidA, _, tokA := e.register(t, "bedrockracers", "Ann")
	pidB, _, tokB := e.register(t, "bedrockracers", "Bob")

	st := struct {
		Configured bool `json:"configured"`
		Version    int  `json:"version"`
	}{}
	brGet(t, srv, "/api/v1/bedrockracers/status", &st)
	if !st.Configured || st.Version < 1 {
		t.Fatalf("status before play: %+v", st)
	}

	_, _, endA, endB := brPlayOut(t, srv, play, tokA, tokB, 0)
	if endA[11] != 1 || endB[11] != 1 {
		t.Fatalf("two signed-in people played out: both END frames are counted, %v %v", endA, endB)
	}
	id := binary.LittleEndian.Uint32(endA[7:])
	if id == 0 || id != binary.LittleEndian.Uint32(endB[7:]) {
		t.Fatalf("END carries one recorded match id: %d / %d", id, binary.LittleEndian.Uint32(endB[7:]))
	}

	scores, counted, humans, ticks, rows := brMatchRows(t, e.db)
	if counted != 1 || humans != 2 || ticks < 14000 {
		t.Fatalf("match row: counted %d humans %d ticks %d", counted, humans, ticks)
	}
	if len(rows) != 2 || rows[0].pid != pidA || rows[1].pid != pidB || rows[0].team != 1 || rows[1].team != 2 {
		t.Fatalf("player rows: %+v", rows)
	}
	for _, r := range rows {
		want := 1500 + expectedDelta(scores, r.team, false)
		if !near(r.before, 1500) || !near(r.aft, want) {
			t.Fatalf("%s (team %d, scores %v): rating %.2f -> %.2f, want 1500 -> %.2f", r.pid, r.team, scores, r.before, r.aft, want)
		}
		var rating float64
		var w, l, d, m int
		if err := e.db.QueryRow(`SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'bedrockracers'`, r.pid).
			Scan(&rating, &w, &l, &d, &m); err != nil {
			t.Fatal(err)
		}
		if !near(rating, want) || m != 1 || w+l+d != 1 {
			t.Fatalf("ladder row of %s: rating %.2f wins %d losses %d draws %d matches %d", r.pid, rating, w, l, d, m)
		}
		if (r.result == "win") != (w == 1) || (r.result == "draw") != (d == 1) {
			t.Fatalf("%s: result %q but ladder wins %d draws %d", r.pid, r.result, w, d)
		}
	}
	// the replay is the module's own log: it starts with the file header
	b := brGet(t, srv, "/api/v1/bedrockracers/replays/"+brNum(int(id)), nil)
	if len(b) < 40 || string(b[:4]) != "BRP1" {
		t.Fatalf("replay: %d bytes", len(b))
	}

	// the public routes WOTAN reads
	var list struct {
		Matches []struct {
			ID      int64 `json:"id"`
			Counted bool  `json:"counted"`
			Humans  int   `json:"humans"`
			Cars    []struct {
				Name     string `json:"name"`
				PlayerID string `json:"player_id"`
				Kind     string `json:"kind"`
			} `json:"cars"`
		} `json:"matches"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/matches", &list)
	if len(list.Matches) != 1 || list.Matches[0].ID != int64(id) || !list.Matches[0].Counted || len(list.Matches[0].Cars) != 2 {
		t.Fatalf("matches feed (people only): %+v", list.Matches)
	}
	var one struct {
		Cars []struct {
			Slot int    `json:"slot"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"cars"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/matches/"+brNum(int(id)), &one)
	bots, people := 0, 0
	for _, c := range one.Cars {
		if c.Kind == "bot" {
			bots++
		} else {
			people++
		}
		if c.Name == "" {
			t.Fatalf("a car without a name: %+v", one.Cars)
		}
	}
	if len(one.Cars) != 12 || bots != 10 || people != 2 {
		t.Fatalf("one match shows all twelve cars: %d, %d bots %d people", len(one.Cars), bots, people)
	}
	var prof struct {
		Player struct {
			PlayerID string `json:"player_id"`
			Name     string `json:"display_name"`
			Matches  int    `json:"matches"`
		} `json:"player"`
		Rank   int `json:"rank"`
		Totals struct {
			Matches int `json:"matches"`
			Goals   int `json:"goals"`
		} `json:"totals"`
		Recent []struct {
			ID     int64  `json:"id"`
			Result string `json:"result"`
		} `json:"recent"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/players/"+pidA, &prof)
	if prof.Player.PlayerID != pidA || prof.Player.Name != "Ann" || prof.Player.Matches != 1 || prof.Rank < 1 || prof.Totals.Matches != 1 ||
		len(prof.Recent) != 1 || prof.Recent[0].ID != int64(id) || prof.Recent[0].Result != rows[0].result {
		t.Fatalf("profile: %+v", prof)
	}
	var feed struct {
		Matches []struct {
			ID int64 `json:"id"`
		} `json:"matches"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/matches?player_id="+pidB, &feed)
	if len(feed.Matches) != 1 {
		t.Fatalf("a person's own feed: %+v", feed)
	}
	brGet(t, srv, "/api/v1/bedrockracers/matches?player_id=nobody", &feed)
	if len(feed.Matches) != 0 {
		t.Fatalf("someone who never played has an empty feed: %+v", feed)
	}
	var leaders struct {
		Stat    string `json:"stat"`
		Leaders []struct {
			PlayerID string `json:"player_id"`
			Value    int    `json:"value"`
		} `json:"leaders"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/leaders?stat=speed", &leaders)
	if leaders.Stat != "speed" || len(leaders.Leaders) != 2 || leaders.Leaders[0].Value <= 0 {
		t.Fatalf("both cars moved, so both have a top speed: %+v", leaders)
	}
	brGet(t, srv, "/api/v1/bedrockracers/leaders?stat=drop%20table", &leaders)
	if leaders.Stat != "goals" {
		t.Fatalf("an unknown statistic is the default, never SQL: %+v", leaders)
	}
	// and the generic ladder every game shares sees both
	code, _, raw := e.do("GET", "/api/v1/games/bedrockracers/leaderboard", "", nil)
	if code != 200 {
		t.Fatalf("leaderboard: %d %s", code, raw)
	}
	var ladder []struct {
		PlayerID string  `json:"player_id"`
		Rating   float64 `json:"rating"`
		Matches  int     `json:"matches"`
	}
	if err := json.Unmarshal(raw, &ladder); err != nil || len(ladder) != 2 || ladder[0].Matches != 1 || ladder[1].Matches != 1 || ladder[0].Rating < ladder[1].Rating {
		t.Fatalf("the ladder lists both people once, best first: %v %s", err, raw)
	}
}

func TestBedrockRacersPlay_ALeaverLosesAndTheOtherPlaysOn(t *testing.T) {
	if testing.Short() {
		t.Skip("plays a four-minute match as fast as the referee can step it")
	}
	e := newGameEnv(t)
	play, srv := newBRPlay(t, e)
	pidA, _, tokA := e.register(t, "bedrockracers", "Ann")
	pidB, _, tokB := e.register(t, "bedrockracers", "Bob")

	_, _, endA, _ := brPlayOut(t, srv, play, tokA, tokB, 400)
	if endA[11] != 1 {
		t.Fatalf("a match that two people started and that played out is counted even though one left: %v", endA)
	}
	scores, counted, humans, _, rows := brMatchRows(t, e.db)
	if counted != 1 || humans != 2 || len(rows) != 2 {
		t.Fatalf("counted %d humans %d rows %+v", counted, humans, rows)
	}
	for _, r := range rows {
		left := r.pid == pidB
		want := 1500 + expectedDelta(scores, r.team, left)
		if left && r.result != "left" {
			t.Fatalf("Bob left: %+v", r)
		}
		if !left && r.pid == pidA && r.result == "left" {
			t.Fatalf("Ann stayed: %+v", r)
		}
		if !near(r.aft, want) {
			t.Fatalf("%s left=%v scores %v: rating %.2f, want %.2f", r.pid, left, scores, r.aft, want)
		}
	}
	var l, m int
	if err := e.db.QueryRow(`SELECT losses, matches FROM game_player_stats WHERE player_id = ? AND game = 'bedrockracers'`, pidB).Scan(&l, &m); err != nil || l != 1 || m != 1 {
		t.Fatalf("leaving is a loss on the ladder: losses %d matches %d (%v)", l, m, err)
	}
	var totals struct {
		Totals struct {
			Left int `json:"left"`
		} `json:"totals"`
	}
	brGet(t, srv, "/api/v1/bedrockracers/players/"+pidB, &totals)
	if totals.Totals.Left != 1 {
		t.Fatalf("the profile counts the match left: %+v", totals)
	}
}

func TestBedrockRacersPlay_AnonymousAndRefusedTokensPlayUnrecorded(t *testing.T) {
	e := newGameEnv(t)
	play, srv := newBRPlay(t, e)

	bad := dialBR(t, srv)
	bad.hello(play.Hub.Version(), "not-a-token", "Pip")
	if w := bad.next(bedrockracers.SWelcome); w[2] != 2 {
		t.Fatalf("a refused token is flagged, got %v", w)
	}
	none := dialBR(t, srv)
	none.hello(play.Hub.Version(), "", "Moss")
	if w := none.next(bedrockracers.SWelcome); w[2] != 0 || string(w[8:8+int(w[7])]) != "Moss" {
		t.Fatalf("no token is a guest by name: %v", w)
	}
	// a token of another game is not a Bedrock Racers player
	_, _, other := e.register(t, "deadweight", "Ada")
	wrong := dialBR(t, srv)
	wrong.hello(play.Hub.Version(), other, "Ada")
	if w := wrong.next(bedrockracers.SWelcome); w[2] != 2 {
		t.Fatalf("another game's token is refused: %v", w)
	}
	// an old page (another sim version) is told to reload and closed
	old := dialBR(t, srv)
	old.hello(play.Hub.Version()+1, "", "Old")
	f := old.next(bedrockracers.SError)
	if f[1] != bedrockracers.ErrVersion {
		t.Fatalf("version gate: %v", f)
	}
	for _, path := range []string{"/api/v1/bedrockracers/matches/999", "/api/v1/bedrockracers/replays/999", "/api/v1/bedrockracers/players/nobody", "/api/v1/bedrockracers/matches/x"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	resp, err := http.Post(srv.URL+"/api/v1/bedrockracers/matches", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("the stats are read-only: %d", resp.StatusCode)
	}
}

func brNum(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
