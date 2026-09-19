package deckstats

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func draft(id int, player string, kind int, cards ...int) string {
	cs := ""
	for i, c := range cards {
		if i > 0 {
			cs += ","
		}
		cs += fmt.Sprint(c)
	}
	return fmt.Sprintf(`{"event":"draft","deck_id":%d,"t":%d,"player":%q,"kind":%d,"cards":[%s],"names":[]}`+"\n", id, 1000+id, player, kind, cs)
}
func match(id, mid int, result string) string {
	return fmt.Sprintf(`{"event":"match","deck_id":%d,"match_id":%d,"player":"x","kind":1,"result":%q,"rounds":9}`+"\n", id, mid, result)
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateAndIncremental(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond}
	if sum := s.Summary(); sum.Decks != 0 {
		t.Fatalf("missing file must be an empty store, got %+v", sum)
	}
	appendTo(t, p, draft(1, "alice", 0, 1, 1, 2)+draft(2, "bot-a", 1, 2, 3, 3)+match(1, 10, "win")+match(2, 10, "loss"))
	time.Sleep(time.Millisecond)
	l, total := s.List(Query{Card: -1, MinGames: 1})
	if total != 2 || l[0].ID != 1 || l[0].WinRate != 1 || l[1].WinRate != 0 {
		t.Fatalf("list: %+v total %d", l, total)
	}
	// incremental: append more, including a partial (unterminated) line that must not be parsed yet
	appendTo(t, p, match(1, 11, "loss")+match(2, 11, "win")+`{"event":"match","deck_id":1,"res`)
	time.Sleep(time.Millisecond)
	d, _ := s.Deck(1)
	if d.Games != 2 || d.Wins != 1 || d.Losses != 1 || d.WinRate != 0.5 {
		t.Fatalf("deck1 after incremental: %+v", d)
	}
	appendTo(t, p, `ult":"win","rounds":3}`+"\n") // completes the partial line
	time.Sleep(time.Millisecond)
	d, _ = s.Deck(1)
	if d.Games != 3 || d.Wins != 2 {
		t.Fatalf("partial line should complete: %+v", d)
	}
	sum := s.Summary()
	if sum.Decks != 2 || sum.BotDecks != 1 || sum.HumanDecks != 1 || sum.Matches != 2 {
		t.Fatalf("summary: %+v", sum) // 5 deck-match records = 2 whole matches (each match writes one per seat)
	}
}

func TestFiltersSortAndCardStats(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond}
	body := draft(1, "alice", 0, 5, 5, 6) + draft(2, "bot-a", 1, 6, 7) + draft(3, "bot-b", 1, 7)
	for i := 0; i < 4; i++ {
		body += match(1, i, "win")
	}
	body += match(2, 9, "win") + match(2, 10, "loss") + match(3, 11, "loss")
	appendTo(t, p, body)
	time.Sleep(time.Millisecond)
	if l, tot := s.List(Query{Card: -1, MinGames: 2}); tot != 2 || l[0].ID != 1 {
		t.Fatalf("min_games: %+v", l)
	}
	if l, _ := s.List(Query{Card: -1, Kind: "bot", MinGames: 1, Sort: "games"}); len(l) != 2 || l[0].ID != 2 {
		t.Fatalf("kind+games sort: %+v", l)
	}
	if l, _ := s.List(Query{Card: 7, MinGames: 1}); len(l) != 2 {
		t.Fatalf("card filter: %+v", l)
	}
	if l, _ := s.List(Query{Card: -1, Player: "ALI", MinGames: 1}); len(l) != 1 || l[0].Player != "alice" {
		t.Fatalf("player filter: %+v", l)
	}
	if l, tot := s.List(Query{Card: -1, MinGames: 1, Limit: 1, Offset: 1, Sort: "recent"}); tot != 3 || len(l) != 1 || l[0].ID != 2 {
		t.Fatalf("paging: %+v %d", l, tot)
	}
	var c5, c6 CardStat
	for _, c := range s.CardStats() {
		if c.Card == 5 {
			c5 = c
		}
		if c.Card == 6 {
			c6 = c
		}
	}
	if c5.Decks != 1 || c5.Copies != 2 || c5.Games != 4 || c5.Wins != 4 || c5.WinRate != 1 {
		t.Fatalf("card 5: %+v", c5)
	}
	if c6.Decks != 2 || c6.Games != 6 || c6.Wins != 5 || c6.Losses != 1 {
		t.Fatalf("card 6: %+v", c6)
	}
}

func TestTruncationResets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond}
	appendTo(t, p, draft(1, "a", 0, 1)+draft(2, "b", 0, 2))
	time.Sleep(time.Millisecond)
	if s.Summary().Decks != 2 {
		t.Fatal("want 2 decks")
	}
	os.WriteFile(p, []byte(draft(9, "c", 0, 3)), 0o644)
	time.Sleep(time.Millisecond)
	if sum := s.Summary(); sum.Decks != 1 {
		t.Fatalf("rotated/shrunk log must reset: %+v", sum)
	}
}

func TestWilsonRanksSampleSize(t *testing.T) {
	if !(wilson(9, 10) > wilson(2, 2)) {
		t.Fatalf("9-1 must outrank 2-0: %v vs %v", wilson(9, 10), wilson(2, 2))
	}
	if wilson(0, 0) != 0 || wilson(0, 5) != 0 || wilson(5, 5) >= 1 {
		t.Fatal("bounds")
	}
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond}
	body := draft(1, "a", 0, 1) + draft(2, "b", 0, 2) + match(1, 1, "win") + match(1, 2, "win")
	for i := 0; i < 10; i++ {
		r := "win"
		if i == 0 {
			r = "loss"
		}
		body += match(2, 10+i, r)
	}
	appendTo(t, p, body)
	time.Sleep(time.Millisecond)
	if l, _ := s.List(Query{Card: -1, MinGames: 1, Sort: "score"}); l[0].ID != 2 {
		t.Fatalf("score sort: %+v", l)
	}
	if l, _ := s.List(Query{Card: -1, MinGames: 1, Sort: "winrate"}); l[0].ID != 1 {
		t.Fatalf("raw winrate sort favours the 2-0 deck: %+v", l)
	}
}
