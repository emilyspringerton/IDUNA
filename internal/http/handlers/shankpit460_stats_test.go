package handlers_test

// shankpit-460 results against the real migrations: the recorder's rows, the free-for-all rating, idempotency, guests
// and leavers, and the public reads WOTAN's pages use; then one whole match over a real WebSocket, from the queue to the
// ladder. The room's own record (who placed where, who left) is tested in internal/shankpit460.

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"iduna/internal/http/handlers"
	"iduna/internal/shankpit460"
)

func spPlayer(t *testing.T, e *gameEnv, id, name string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?, 'shankpit460')`,
		id, name, "iduna_sso", "sub-"+id); err != nil {
		t.Fatal(err)
	}
}

func spGet(t *testing.T, srv *httptest.Server, path string, into any) int {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if into != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

type spLine struct {
	PlayerID     string  `json:"player_id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Kills        int     `json:"kills"`
	Deaths       int     `json:"deaths"`
	Place        int     `json:"place"`
	Left         bool    `json:"left"`
	Result       string  `json:"result"`
	RatingBefore float64 `json:"rating_before"`
	RatingAfter  float64 `json:"rating_after"`
}

type spMatchJSON struct {
	ID         int64    `json:"id"`
	Reason     string   `json:"reason"`
	Players    int      `json:"players"`
	SignedIn   int      `json:"signed_in"`
	WinnerName string   `json:"winner_name"`
	WinnerID   string   `json:"winner_id"`
	TopKills   int      `json:"top_kills"`
	Counted    bool     `json:"counted"`
	Roster     []spLine `json:"roster"`
}

func TestShankpit460Stats_RecorderLadderAndReads(t *testing.T) {
	e := newGameEnv(t)
	for _, p := range [][2]string{{"pa", "Ash"}, {"pb", "Birch"}, {"pc", "Cedar"}} {
		spPlayer(t, e, p[0], p[1])
	}
	rec := handlers.NewShankpit460Recorder(e.db)
	live := time.Unix(1_800_000_000, 0)
	m := &shankpit460.Match{UID: "m1", RoomID: 1, Reason: shankpit460.EndKills, KillLimit: 16, LiveAt: live, EndedAt: live.Add(3 * time.Minute), Counted: true,
		Players: []shankpit460.MatchPlayer{
			{ID: "pa", Name: "Ash", Slot: 0, Kills: 16, Deaths: 4, Place: 1, Winner: true},
			{ID: "guest:1", Name: "Guest 0042", Guest: true, Slot: 1, Kills: 9, Deaths: 10, Place: 2},
			{ID: "pb", Name: "Birch", Slot: 2, Kills: 5, Deaths: 12, Place: 3},
			{ID: "pc", Name: "Cedar", Slot: 3, Kills: 1, Deaths: 3, Place: 4, Left: true, LeftAt: 70 * time.Second},
		}}
	ctx := context.Background()
	if err := rec.Record(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(ctx, m); err != nil { // a retry of the same match changes nothing
		t.Fatal(err)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM shankpit460_matches`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("matches rows = %d (%v), want 1", n, err)
	}
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM shankpit460_player_matches`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("player rows = %d (%v): one per signed-in person, none for the guest", n, err)
	}

	// the free-for-all rating: every pair by finishing order, K/(n-1) per pairing, the guest at the starting rating
	want := func(place int) float64 {
		places := []int{1, 2, 3, 4}
		d := 0.0
		for _, o := range places {
			if o == place {
				continue
			}
			s := 0.0
			if place < o {
				s = 1
			}
			d += 32.0 / 3.0 * (s - 0.5)
		}
		return 1500 + d
	}
	stats := map[string][5]float64{}
	rows, err := e.db.Query(`SELECT player_id, rating, wins, losses, draws, matches FROM game_player_stats WHERE game = 'shankpit460'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var r float64
		var w, l, d, mm int
		if err := rows.Scan(&id, &r, &w, &l, &d, &mm); err != nil {
			t.Fatal(err)
		}
		stats[id] = [5]float64{r, float64(w), float64(l), float64(d), float64(mm)}
	}
	rows.Close()
	if len(stats) != 3 {
		t.Fatalf("ladder rows %v", stats)
	}
	for id, place := range map[string]int{"pa": 1, "pb": 3, "pc": 4} {
		if math.Abs(stats[id][0]-want(place)) > 0.01 || stats[id][4] != 1 {
			t.Fatalf("%s: rating %.2f matches %v, want %.2f after 1", id, stats[id][0], stats[id][4], want(place))
		}
	}
	if stats["pa"][1] != 1 || stats["pb"][2] != 1 || stats["pc"][2] != 1 {
		t.Fatalf("a win for Ash, losses for Birch and Cedar (who left): %v", stats)
	}

	// a short abandoned match is recorded but moves nothing
	m2 := &shankpit460.Match{UID: "m2", RoomID: 2, Reason: shankpit460.EndAbandoned, KillLimit: 16, LiveAt: live, EndedAt: live.Add(5 * time.Second),
		Players: []shankpit460.MatchPlayer{
			{ID: "pa", Name: "Ash", Slot: 0, Place: 1, Winner: true},
			{ID: "pb", Name: "Birch", Slot: 1, Place: 2, Left: true, LeftAt: 5 * time.Second},
		}}
	if err := rec.Record(ctx, m2); err != nil {
		t.Fatal(err)
	}
	var r2 float64
	var mm int
	if err := e.db.QueryRow(`SELECT rating, matches FROM game_player_stats WHERE player_id = 'pa'`).Scan(&r2, &mm); err != nil ||
		math.Abs(r2-want(1)) > 0.01 || mm != 1 {
		t.Fatalf("an uncounted match moved Ash: %.2f / %d (%v)", r2, mm, err)
	}

	play, err := handlers.NewShankpit460Play(e.h)
	if err != nil {
		t.Fatal(err)
	}
	defer play.Hub.Close()
	srv := httptest.NewServer(play)
	defer srv.Close()

	var list struct {
		Matches []spMatchJSON `json:"matches"`
	}
	if code := spGet(t, srv, "/api/v1/shankpit460/matches", &list); code != 200 || len(list.Matches) != 2 {
		t.Fatalf("matches feed %d %+v", code, list)
	}
	first := list.Matches[1]
	if first.Reason != "kills" || !first.Counted || first.Players != 4 || first.SignedIn != 3 || first.WinnerName != "Ash" || first.WinnerID != "pa" ||
		first.TopKills != 16 || len(first.Roster) != 4 {
		t.Fatalf("match row %+v", first)
	}
	g := first.Roster[1]
	if g.Kind != "guest" || g.PlayerID != "" || g.Name != "Guest 0042" || g.Result != "loss" || g.Place != 2 {
		t.Fatalf("the guest is on the roster with no account: %+v", g)
	}
	c := first.Roster[3]
	if c.Result != "left" || !c.Left || c.Kind != "human" || c.RatingAfter >= c.RatingBefore {
		t.Fatalf("Cedar left and lost rating: %+v", c)
	}
	if list.Matches[0].Counted || list.Matches[0].Reason != "abandoned" {
		t.Fatalf("the short match: %+v", list.Matches[0])
	}

	var one spMatchJSON
	if code := spGet(t, srv, "/api/v1/shankpit460/matches/"+itoa64(first.ID), &one); code != 200 || one.ID != first.ID {
		t.Fatalf("one match %d %+v", code, one)
	}
	if code := spGet(t, srv, "/api/v1/shankpit460/matches/99999", nil); code != 404 {
		t.Fatalf("missing match = %d", code)
	}
	var mine struct {
		Matches []spMatchJSON `json:"matches"`
	}
	spGet(t, srv, "/api/v1/shankpit460/matches?player_id=pc", &mine)
	if len(mine.Matches) != 1 {
		t.Fatalf("Cedar played one match: %+v", mine)
	}

	var prof struct {
		Player struct {
			DisplayName string  `json:"display_name"`
			Rating      float64 `json:"rating"`
			Wins        int     `json:"wins"`
			Matches     int     `json:"matches"`
		} `json:"player"`
		Rank   int `json:"rank"`
		Totals struct {
			Matches, Kills, Deaths, Wins, Left int
			BestKills                          int `json:"best_kills"`
		} `json:"totals"`
		Recent []struct {
			ID     int64  `json:"id"`
			Place  int    `json:"place"`
			Result string `json:"result"`
		} `json:"recent"`
	}
	if code := spGet(t, srv, "/api/v1/shankpit460/players/pa", &prof); code != 200 {
		t.Fatalf("profile %d", code)
	}
	if prof.Player.DisplayName != "Ash" || prof.Rank != 1 || prof.Player.Wins != 1 || prof.Player.Matches != 1 ||
		prof.Totals.Kills != 16 || prof.Totals.Matches != 2 || prof.Totals.BestKills != 16 || len(prof.Recent) != 2 || prof.Recent[1].Result != "win" {
		t.Fatalf("profile %+v", prof)
	}
	if code := spGet(t, srv, "/api/v1/shankpit460/players/nobody", nil); code != 404 {
		t.Fatalf("unknown player = %d", code)
	}

	var lead struct {
		Stat    string `json:"stat"`
		Leaders []struct {
			Name  string  `json:"display_name"`
			Value float64 `json:"value"`
		} `json:"leaders"`
	}
	spGet(t, srv, "/api/v1/shankpit460/leaders?stat=kills", &lead)
	if lead.Stat != "kills" || len(lead.Leaders) != 3 || lead.Leaders[0].Name != "Ash" || lead.Leaders[0].Value != 16 {
		t.Fatalf("kill leaders %+v", lead)
	}
	spGet(t, srv, "/api/v1/shankpit460/leaders?stat=kd", &lead)
	if len(lead.Leaders) != 0 {
		t.Fatalf("K/D needs five matches: %+v", lead)
	}

	// the generic ladder WOTAN's board reads
	var board []struct {
		DisplayName string  `json:"display_name"`
		Rating      float64 `json:"rating"`
	}
	code, _, body := e.do("GET", "/api/v1/games/shankpit460/leaderboard?limit=10", "", nil)
	if code != 200 {
		t.Fatalf("leaderboard %d", code)
	}
	if err := json.Unmarshal(body, &board); err != nil || len(board) != 3 || board[0].DisplayName != "Ash" || board[2].DisplayName != "Cedar" {
		t.Fatalf("ladder order %+v (%v)", board, err)
	}
}

func itoa64(n int64) string { b, _ := json.Marshal(n); return string(b) }

// One real match over the wire: two signed-in players queue, the clock runs out at 0-0, and the draw is on the ladder.
func TestShankpit460Stats_AWholeMatchOverTheWireIsRecorded(t *testing.T) {
	t.Setenv("SHANKPIT460_MATCH_SECONDS", "10")
	t.Setenv("SHANKPIT460_COUNTDOWN_SECONDS", "1")
	e := newGameEnv(t)
	play, err := handlers.NewShankpit460Play(e.h)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(play)
	defer srv.Close()
	pa, ta := e.ssoIdentity(t, "ash@example.com")
	pb, tb := e.ssoIdentity(t, "birch@example.com")
	a, b := dialSP(t, srv), dialSP(t, srv)
	a.hello(ta, "")
	b.hello(tb, "")
	a.next(0x81)
	b.next(0x81)
	a.send([]byte{0x02})
	b.send([]byte{0x02})
	ends := make(chan []byte, 2)
	for _, c := range []*spWS{a, b} {
		go func(c *spWS) {
			deadline := time.Now().Add(30 * time.Second)
			_ = c.ws.SetReadDeadline(deadline)
			for {
				var f []byte
				if err := websocket.Message.Receive(c.ws, &f); err != nil {
					ends <- nil
					return
				}
				if len(f) > 0 && f[0] == 0x87 {
					ends <- f
					return
				}
			}
		}(c)
	}
	for i := 0; i < 2; i++ {
		if f := <-ends; f == nil || f[1] != 2 || f[2] != 255 {
			t.Fatalf("a time-limit draw END expected, got %v", f)
		}
	}
	play.Hub.Close() // waits for the recording
	for _, pid := range []string{pa, pb} {
		var d, m int
		if err := e.db.QueryRow(`SELECT draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'shankpit460'`, pid).Scan(&d, &m); err != nil || d != 1 || m != 1 {
			t.Fatalf("%s: draws %d matches %d (%v), want a recorded draw", pid, d, m, err)
		}
	}
	var reason, counted, players int
	if err := e.db.QueryRow(`SELECT reason, counted, players FROM shankpit460_matches`).Scan(&reason, &counted, &players); err != nil ||
		reason != 2 || counted != 1 || players != 2 {
		t.Fatalf("match row: reason %d counted %d players %d (%v)", reason, counted, players, err)
	}
}
