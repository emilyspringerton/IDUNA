package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/empire"
	"iduna/internal/store"
)

func empireDB(t *testing.T) *sql.DB {
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

func empirePlayer(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?, 'empire')`, id, name, "guest", "g:"+id); err != nil {
		t.Fatal(err)
	}
}

func newEmpireForTest(t *testing.T) (*EmpirePlay, *sql.DB) {
	t.Helper()
	db := empireDB(t)
	cfg := empire.DefaultConfig()
	cfg.LeagueEvery = 0
	p := NewEmpirePlayWith(&GameOnlineHandler{DB: db}, cfg)
	if p.Hub == nil {
		t.Fatal("the gateway did not start")
	}
	t.Cleanup(p.Hub.Close)
	return p, db
}

func empireGet(t *testing.T, p *EmpirePlay, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// a match between a person and AI number 2 that the person wins on a fallen king
func sampleMatch(t *testing.T, p *EmpirePlay, ann string) *empire.Match {
	t.Helper()
	ctx := context.Background()
	bot, err := p.rec.BotPlayer(ctx, 2, "Quill")
	if err != nil {
		t.Fatal(err)
	}
	var res empire.Result
	res.Winner, res.Reason, res.Ticks = 0, 1, 1400
	res.Crowns = [2]int{3, 1}
	res.TowerHP = [2]int{3000, 0}
	res.Towers = [2]int{2, 0}
	res.Plays[0][0], res.Plays[0][2], res.Plays[0][11] = 4, 6, 3
	res.Plays[1][0], res.Plays[1][4] = 5, 1
	res.Stats[0][2], res.Stats[1][2] = 13, 6
	deck := uint32(0 | 1<<4 | 2<<8 | 4<<12 | 5<<16 | 8<<20 | 11<<24 | 13<<28)
	return &empire.Match{Mode: "ranked", Seed: 42, Version: p.Hub.Version(), Result: res,
		Seats: [2]empire.SeatInfo{{PlayerID: ann, Name: "Ann", Deck: deck, Faction: 0}, {PlayerID: bot.ID, Name: "Quill", Bot: 2, Deck: 0x76543210, Faction: 2}},
		Cmds:  []empire.Cmd{{Tick: 100, Seat: 0, Slot: 1, X4: 10, Y4: 100}}, Replay: empire.BuildReplay(p.Hub.Version(), 42, [2]uint32{deck, 0x76543210}, [2]int{0, 2}, [2]string{"Ann", "Quill"}, res, nil), Counted: true}
}

func TestEmpireRecorderLadderCardsAndReplay(t *testing.T) {
	p, db := newEmpireForTest(t)
	ann := "11111111-1111-4111-8111-111111111111"
	empirePlayer(t, db, ann, "Ann")
	m := sampleMatch(t, p, ann)
	out, err := p.rec.Record(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Counted || out.Before[0] != 1500 || out.After[0] != 1516 || out.After[1] != 1484 || out.MatchID == 0 {
		t.Fatalf("outcome %+v", out)
	}
	var wins, losses, n int
	var rt float64
	if err := db.QueryRow(`SELECT rating, wins, losses, matches FROM game_player_stats WHERE player_id = ? AND game = 'empire'`, ann).Scan(&rt, &wins, &losses, &n); err != nil || rt != 1516 || wins != 1 || n != 1 {
		t.Fatalf("ann: %v %v %d %d %d", err, rt, wins, losses, n)
	}
	// the AI is a real player row and shows as a bot on the generic leaderboard
	var prov string
	if err := db.QueryRow(`SELECT provider FROM players WHERE display_name = 'Quill' AND game = 'empire'`).Scan(&prov); err != nil || prov != "deadweight_bot" {
		t.Fatalf("bot row: %v %q", err, prov)
	}
	// card statistics: the winner's deck gets a win, the loser's a game
	var games, cw, plays int
	if err := db.QueryRow(`SELECT games, wins, plays FROM empire_card_stats WHERE card = 11`).Scan(&games, &cw, &plays); err != nil || games != 1 || cw != 1 || plays != 3 {
		t.Fatalf("card 11: %v %d %d %d", err, games, cw, plays)
	}
	if err := db.QueryRow(`SELECT games, wins, plays FROM empire_card_stats WHERE card = 6`).Scan(&games, &cw, &plays); err != nil || games != 1 || cw != 0 {
		t.Fatalf("card 6 (the AI's): %v %d %d", err, games, cw)
	}

	// the public API
	w := empireGet(t, p, "/api/v1/empire/matches?limit=5")
	var list struct {
		Matches []empireMatchRow `json:"matches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Matches) != 1 {
		t.Fatalf("matches: %v %s", err, w.Body.String())
	}
	x := list.Matches[0]
	if x.Seats[1].Kind != "bot" || x.Seats[0].Kind != "human" || x.Winner != 0 || x.Seats[0].Before != 1500 || x.Seats[0].After != 1516 || x.Ticks != 1400 || len(x.Seats[0].Deck) != 8 {
		t.Fatalf("row %+v", x)
	}
	w = empireGet(t, p, "/api/v1/empire/matches/1")
	var one empireMatchRow
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil || one.Stats == nil || len(one.Plays[0]) == 0 {
		t.Fatalf("match: %v %s", err, w.Body.String())
	}
	w = empireGet(t, p, "/api/v1/empire/replays/1")
	if w.Code != 200 || w.Body.Len() != len(m.Replay) || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("replay: %d len %d", w.Code, w.Body.Len())
	}
	if empireGet(t, p, "/api/v1/empire/replays/99").Code != 404 {
		t.Fatal("a missing replay must be 404")
	}
	w = empireGet(t, p, "/api/v1/empire/players/"+ann)
	var prof struct {
		Player playerStats     `json:"player"`
		Rank   int             `json:"rank"`
		Streak int             `json:"streak"`
		Cards  []empireCardRow `json:"cards"`
		Last   []int           `json:"last_deck"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &prof); err != nil || prof.Player.Rating != 1516 || prof.Rank != 1 || prof.Streak != 1 || len(prof.Last) != 8 || prof.Cards[2].Plays != 6 {
		t.Fatalf("profile: %v %s", err, w.Body.String())
	}
	w = empireGet(t, p, "/api/v1/empire/cards")
	var cards struct {
		Matches int             `json:"matches"`
		Cards   []empireCardRow `json:"cards"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cards); err != nil || cards.Matches != 1 || len(cards.Cards) != 16 || cards.Cards[11].Rate != 1 || cards.Cards[6].Games != 1 {
		t.Fatalf("cards: %v %s", err, w.Body.String())
	}
	w = empireGet(t, p, "/api/v1/empire/bots")
	var bots struct {
		Bots []struct {
			Name    string  `json:"name"`
			Rating  float64 `json:"rating"`
			Matches int     `json:"matches"`
			Deck    []int   `json:"deck"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bots); err != nil || len(bots.Bots) != 8 || bots.Bots[1].Name != "Quill" || bots.Bots[1].Rating != 1484 || len(bots.Bots[0].Deck) != 8 {
		t.Fatalf("bots: %v %s", err, w.Body.String())
	}
	if st := empireGet(t, p, "/api/v1/empire/status"); st.Code != 200 {
		t.Fatalf("status %d", st.Code)
	}
}

func TestEmpireLeagueMatchRatesBothAIsAndAnythingWithoutTwoPlayersIsRefused(t *testing.T) {
	p, db := newEmpireForTest(t)
	cfg := empire.DefaultConfig()
	_ = cfg
	if _, err := p.rec.Record(context.Background(), &empire.Match{Mode: "league", Seats: [2]empire.SeatInfo{{PlayerID: "x"}, {PlayerID: "x"}}}); err == nil {
		t.Fatal("a match against yourself must not record")
	}
	a, _ := p.rec.BotPlayer(context.Background(), 1, "Marrow")
	b, _ := p.rec.BotPlayer(context.Background(), 3, "Sable")
	m := &empire.Match{Mode: "league", Seed: 7, Version: 1, Counted: true, Seats: [2]empire.SeatInfo{{PlayerID: a.ID, Name: "Marrow", Bot: 1, Deck: 0x76543210}, {PlayerID: b.ID, Name: "Sable", Bot: 3, Deck: 0x76543210}},
		Result: empire.Result{Winner: 2, Reason: 5, Ticks: 4800}}
	out, err := p.rec.Record(context.Background(), m)
	if err != nil || !out.Counted || out.After[0] != 1500 || out.After[1] != 1500 {
		t.Fatalf("a drawn league match between equals moves nothing: %+v %v", out, err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM empire_matches WHERE mode = 'league'`).Scan(&n)
	if n != 1 {
		t.Fatalf("league rows %d", n)
	}
	w := empireGet(t, p, "/api/v1/empire/matches?mode=league")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	_ = time.Second
}
