package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"iduna/internal/d2play"
	"iduna/internal/store"
)

// The D2 recorder against the REAL migrations (through the real mysql->sqlite translation).
func d2DB(t *testing.T) *sql.DB {
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

func d2Player(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?, 'd2')`, id, name, "guest", "g:"+id); err != nil {
		t.Fatal(err)
	}
}

func d2Rating(t *testing.T, db *sql.DB, id string) (r float64, w, l, d, m int) {
	t.Helper()
	if err := db.QueryRow(`SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'd2'`, id).Scan(&r, &w, &l, &d, &m); err != nil {
		t.Fatalf("stats for %s: %v", id, err)
	}
	return
}

func TestD2RecorderLadderAndWebRecord(t *testing.T) {
	db := d2DB(t)
	g := &GameOnlineHandler{DB: db}
	rec := &d2Recorder{g: g}
	d2Player(t, db, "11111111-1111-4111-8111-111111111111", "Wraith")
	ctx := context.Background()
	ship := []d2play.Placement{{Item: 0}, {Item: 1, Col: 1}, {Item: 3, Col: 2, Cut: false}}
	m := d2play.Record{PlayerID: "11111111-1111-4111-8111-111111111111", Name: "Wraith", Seat: 1, MatchID: 12, Seed: 3405691582,
		OppName: "Ada Bot", OppKind: "bot", Result: "win", Reason: "hull", Winner: 1, Ticks: 18, HullYou: 40, HullOpp: 0, Loadout: ship,
		Rounds: []d2play.RoundLog{{No: 1, TargetMS: 600, CallMS: 590, YourCall: 0, YourGrade: 3, YourFx: 75, OppCall: 1}}, Counted: true}
	out, err := rec.Record(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Rated || out.RatingBefore != 1500 || out.RatingAfter != 1516 {
		t.Fatalf("outcome = %+v, want 1500 -> 1516 (K 32, equal ratings)", out)
	}
	r, w, l, _, n := d2Rating(t, db, m.PlayerID)
	if r != 1516 || w != 1 || l != 0 || n != 1 {
		t.Fatalf("player stats = %v %d %d %d", r, w, l, n)
	}
	// the bot is a real player row, kind bot on the generic leaderboard, and lost the other way
	var botID, prov string
	if err := db.QueryRow(`SELECT player_id, provider FROM players WHERE display_name = 'Ada Bot' AND game = 'd2'`).Scan(&botID, &prov); err != nil || prov != "deadweight_bot" {
		t.Fatalf("bot player row: %v %q", err, prov)
	}
	if br, bw, bl, _, _ := d2Rating(t, db, botID); br != 1484 || bw != 0 || bl != 1 {
		t.Fatalf("bot stats = %v %d %d", br, bw, bl)
	}
	// the same match reported again changes nothing on the ladder
	if out2, err := rec.Record(ctx, m); err != nil || out2.Rated {
		t.Fatalf("a duplicate must not rate again: %+v %v", out2, err)
	}
	if r2, _, _, _, n2 := d2Rating(t, db, m.PlayerID); r2 != 1516 || n2 != 1 {
		t.Fatalf("duplicate moved the ladder: %v %d", r2, n2)
	}
	// a forfeit is a counted loss; an aborted match is a record and nothing else
	f := m
	f.MatchID, f.Result, f.Reason, f.Winner = 13, "forfeit", "forfeit", 0
	if _, err := rec.Record(ctx, f); err != nil {
		t.Fatal(err)
	}
	if _, w, l, _, n := d2Rating(t, db, m.PlayerID); w != 1 || l != 1 || n != 2 {
		t.Fatalf("after a forfeit: w=%d l=%d n=%d", w, l, n)
	}
	a := m
	a.MatchID, a.Result, a.Reason, a.Counted = 14, "aborted", "server", false
	if _, err := rec.Record(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, n := d2Rating(t, db, m.PlayerID); n != 2 {
		t.Fatalf("an aborted match moved the ladder: n=%d", n)
	}
	var rows, counted int
	_ = db.QueryRow(`SELECT COUNT(*), SUM(counted) FROM d2_web_matches WHERE player_id = ?`, m.PlayerID).Scan(&rows, &counted)
	if rows != 4 || counted != 3 {
		t.Fatalf("web records = %d (counted %d), want 4 and 3 (win, duplicate-of-win, forfeit; the abort is not counted)", rows, counted)
	}
	var lo, rd string
	_ = db.QueryRow(`SELECT loadout, rounds FROM d2_web_matches WHERE match_id = 12 ORDER BY id LIMIT 1`).Scan(&lo, &rd)
	var gotShip []d2play.Placement
	var gotRounds []d2play.RoundLog
	if json.Unmarshal([]byte(lo), &gotShip) != nil || len(gotShip) != 3 || json.Unmarshal([]byte(rd), &gotRounds) != nil || gotRounds[0].YourGrade != 3 {
		t.Fatalf("stored ship/rounds = %s / %s", lo, rd)
	}
}

func TestD2TwoHumansRateOnce(t *testing.T) {
	db := d2DB(t)
	rec := &d2Recorder{g: &GameOnlineHandler{DB: db}}
	a, b := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	d2Player(t, db, a, "Ann")
	d2Player(t, db, b, "Bea")
	ctx := context.Background()
	base := d2play.Record{MatchID: 5, Seed: 77, OppKind: "human", Winner: 0, Ticks: 30, Counted: true}
	ra := base
	ra.PlayerID, ra.Seat, ra.OppPlayerID, ra.OppName, ra.Result, ra.Reason = a, 0, b, "Bea", "win", "timeout"
	rb := base
	rb.PlayerID, rb.Seat, rb.OppPlayerID, rb.OppName, rb.Result, rb.Reason, rb.SkipStats = b, 1, a, "Ann", "loss", "timeout", true
	oa, err := rec.Record(ctx, ra)
	if err != nil || !oa.Rated {
		t.Fatalf("first side: %+v %v", oa, err)
	}
	if _, err := rec.Record(ctx, rb); err != nil {
		t.Fatal(err)
	}
	if r, w, _, _, n := d2Rating(t, db, a); r != 1516 || w != 1 || n != 1 {
		t.Fatalf("Ann = %v %d %d", r, w, n)
	}
	if r, _, l, _, n := d2Rating(t, db, b); r != 1484 || l != 1 || n != 1 {
		t.Fatalf("Bea = %v %d %d, the loser must be rated once", r, l, n)
	}
	var rows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM d2_web_matches`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("each human keeps their own record, got %d", rows)
	}
	// an opponent who is not on the ladder leaves the ladder alone
	rc := base
	rc.MatchID, rc.PlayerID, rc.OppPlayerID, rc.OppName, rc.Result, rc.Reason, rc.Counted = 6, a, "", "Native", "win", "hull", false
	if _, err := rec.Record(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, n := d2Rating(t, db, a); n != 1 {
		t.Fatalf("a match against someone off the ladder moved it: n=%d", n)
	}
}

func TestD2PublicReads(t *testing.T) {
	db := d2DB(t)
	g := &GameOnlineHandler{DB: db}
	rec := &d2Recorder{g: g}
	p := &D2Play{game: g, db: db}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	d2Player(t, db, id, "Cy")
	for i, res := range []string{"win", "loss", "win"} {
		w := 0
		if res == "loss" {
			w = 1
		}
		if _, err := rec.Record(context.Background(), d2play.Record{PlayerID: id, Seat: 0, MatchID: uint32(100 + i), Seed: uint32(9 + i), OppName: "Dune Bot", OppKind: "bot",
			Result: res, Reason: "hull", Winner: w, Ticks: 12, Counted: true, Loadout: []d2play.Placement{{Item: 3}}}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	st := get("/api/v1/d2/status")
	var sj map[string]any
	_ = json.Unmarshal(st.Body.Bytes(), &sj)
	if sj["configured"] != false {
		t.Fatalf("a gateway with no hub must say configured:false, got %v", sj)
	}
	var feed struct{ Matches []d2MatchRow }
	w := get("/api/v1/d2/web-matches?limit=2&player_id=" + id)
	_ = json.Unmarshal(w.Body.Bytes(), &feed)
	if w.Code != 200 || len(feed.Matches) != 2 || feed.Matches[0].OppName != "Dune Bot" || feed.Matches[0].Name != "Cy" {
		t.Fatalf("feed = %d %s", w.Code, w.Body.String())
	}
	var one d2MatchRow
	w = get("/api/v1/d2/web-matches/" + strconv.Itoa(feed.Matches[0].ID))
	_ = json.Unmarshal(w.Body.Bytes(), &one)
	if w.Code != 200 || len(one.Loadout) == 0 {
		t.Fatalf("one match = %d %s", w.Code, w.Body.String())
	}
	var prof struct {
		Player playerStats
		Rank   int
		Recent []d2MatchRow
	}
	w = get("/api/v1/d2/players/" + id)
	_ = json.Unmarshal(w.Body.Bytes(), &prof)
	if w.Code != 200 || prof.Player.Wins != 2 || prof.Player.Losses != 1 || prof.Rank != 1 || len(prof.Recent) != 3 || prof.Player.Kind != "human" {
		t.Fatalf("profile = %d %s", w.Code, w.Body.String())
	}
	if get("/api/v1/d2/players/nobody").Code != 404 || get("/api/v1/d2/web-matches/zzz").Code != 404 || get("/api/v1/d2/nope").Code != 404 {
		t.Fatal("unknown things must be 404")
	}
	if get("/api/v1/d2/play").Code != http.StatusServiceUnavailable {
		t.Fatal("play without a hub must be 503")
	}
}
