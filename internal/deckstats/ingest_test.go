package deckstats

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseFastAgreesWithEncodingJSON(t *testing.T) {
	cases := []string{
		`{"event":"draft","deck_id":7,"t":1790000000,"player":"bot-wall-d","kind":1,"cards":[9,9,23],"names":["Call Option","Call Option","Quick Draw"]}`,
		`{"event":"match","deck_id":7,"match_id":3,"player":"bot-wall-d","kind":1,"result":"win","rounds":6}`,
		`{"event":"match","deck_id":7,"match_id":3,"player":"bot-wall-d","kind":1,"result":"loss","rounds":6}`,
		`{"event":"match","deck_id":7,"result":"draw"}`,
		`{ "event" : "draft" , "deck_id" : 8 , "cards" : [ 1 , 2 , 3 ] }`,
		`{"cards":[1,2],"deck_id":9,"event":"draft"}`,               // reordered
		`{"event":"draft","deck_id":9,"cards":[1,2],"cards":[3]}`,   // duplicate key: the last wins
		`{"event":"draft","deck_id":9,"cards":[]}`,                  // empty
		`{"event":"draft","deck_id":9,"cards":null}`,                // null falls back
		`{"event":"draft","deck_id":9,"cards":[1,300,2]}`,           // an id outside a byte
		`{"event":"draft","deck_id":9,"cards":[1,-1]}`,              // negative id
		`{"event":"draft","deck_id":9,"player":"a\"b","cards":[1]}`, // escape in a string: slow path
		`{"event":"draft","deck_id":9,"player":"éclair","cards":[1]}`,
		`{"event":"other","deck_id":9}`,                      // some other record
		`{"event":"draft","deck_id":4294967296,"cards":[1]}`, // too big for a deck id
		`{"event":"draft","deck_id":-1,"cards":[1]}`,
		`{"event":"draft","deck_id":1.5,"cards":[1]}`,                                  // not an integer
		`{"event":"draft","deck_id":1,"t":1.79e9,"cards":[1]}`,                         // exponent where an int64 is expected
		`{"event":"draft","deck_id":1,"x":{"a":[1,{"b":"}]"}],"c":"\"}"},"cards":[5]}`, // braces inside nested strings
		`{"event":"draft","deck_id":1,"x":[1,2,[3,[4]]],"y":true,"z":null,"w":-3.5e-2,"cards":[5]}`,
		`{"event":5,"deck_id":1}`, // wrong type for event
		`{"event":"draft","deck_id":1,}`,
		`{"event":"draft" "deck_id":1}`,
		`{"event":"draft","deck_id":1,"cards":[1 2]}`,
		`{"event":"draft","deck_id":1,"cards":[1,]}`,
		`{"event":"draft","deck_id":1`,
		`{"event":"draft","deck_id":1}}`,
		`[1,2,3]`,
		`not json`,
		``,
		`{}`,
	}
	rng := rand.New(rand.NewSource(1))
	players := []string{"bot-wall-d", "alice", "Runner-Y4DZ", "", "a b", `q"uote`, "tab\there", "日本語"}
	for n := 0; n < 20000; n++ {
		fields := []string{}
		add := func(k, v string) { fields = append(fields, fmt.Sprintf("%q:%s", k, v)) }
		if rng.Intn(10) > 0 {
			add("event", []string{`"draft"`, `"match"`, `"other"`}[rng.Intn(3)])
		}
		if rng.Intn(10) > 0 {
			add("deck_id", fmt.Sprint(rng.Intn(2000000)))
		}
		if rng.Intn(2) == 0 {
			add("t", fmt.Sprint(1790000000+rng.Intn(3000000)))
		}
		if rng.Intn(3) > 0 {
			pj, _ := json.Marshal(players[rng.Intn(len(players))])
			add("player", string(pj))
		}
		if rng.Intn(3) > 0 {
			add("kind", fmt.Sprint(rng.Intn(3)))
		}
		if rng.Intn(3) > 0 {
			cs := make([]string, rng.Intn(26))
			for i := range cs {
				cs[i] = fmt.Sprint(rng.Intn(110))
			}
			add("cards", "["+strings.Join(cs, ",")+"]")
		}
		if rng.Intn(3) > 0 {
			add("result", []string{`"win"`, `"loss"`, `"draw"`, `"x"`}[rng.Intn(4)])
		}
		if rng.Intn(3) == 0 {
			add("names", `["a","b\"c",{"k":[1,2]}]`)
		}
		if rng.Intn(4) == 0 {
			add("rounds", fmt.Sprint(rng.Intn(100)))
		}
		rng.Shuffle(len(fields), func(i, j int) { fields[i], fields[j] = fields[j], fields[i] })
		sep := []string{",", " , ", ",\t"}[rng.Intn(3)]
		cases = append(cases, "{"+strings.Join(fields, sep)+"}")
	}
	fastAccepted := 0
	for _, c := range cases {
		var fast, slow event
		fok := parseFast([]byte(c), &fast)
		sok := parseSlow([]byte(c), &slow)
		if fok {
			fastAccepted++
			if !sok {
				t.Fatalf("fast path accepted a line encoding/json rejects: %s", c)
			}
			if fast.typ != slow.typ || fast.deckID != slow.deckID || fast.t != slow.t || string(fast.player) != string(slow.player) ||
				fast.kind != slow.kind || fast.ncards != slow.ncards || fast.badCards != slow.badCards || fast.result != slow.result ||
				!reflect.DeepEqual(fast.cards[:fast.ncards], slow.cards[:slow.ncards]) {
				t.Fatalf("fast and slow disagree on %s\n fast=%+v\n slow=%+v", c, fast, slow)
			}
		}
		// parseLine is fast-or-slow, so it must accept exactly what encoding/json accepts.
		var both event
		if parseLine([]byte(c), &both) != sok {
			t.Fatalf("parseLine acceptance differs from encoding/json on %s", c)
		}
	}
	if fastAccepted < len(cases)/2 {
		t.Fatalf("the fast path only handled %d of %d lines", fastAccepted, len(cases))
	}
}

func TestParseFastDoesNotAllocate(t *testing.T) {
	line := []byte(`{"event":"draft","deck_id":77424,"t":1790018337,"player":"bot-ripper-d","kind":1,"cards":[9,9,23,85,51,7,7,7,91,91,76,76,24,24,49,8,8,67,29,4,80,83,47],"names":["Call Option","Call Option","Quick Draw"]}`)
	var ev event
	if n := testing.AllocsPerRun(200, func() { parseLine(line, &ev) }); n != 0 {
		t.Fatalf("parsing a draft line allocated %v times", n)
	}
}

func fileStore(t *testing.T, logPath, dbPath string) *Store {
	t.Helper()
	s := &Store{Path: logPath, DBPath: dbPath, MinRefresh: time.Nanosecond}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestResumesFromTheStoredOffset(t *testing.T) {
	dir := t.TempDir()
	lp, dbp := filepath.Join(dir, "decks.ndjson"), filepath.Join(dir, "deckstats.db")
	appendTo(t, lp, draft(1, "a", 1, 1, 2)+draft(2, "b", 1, 2, 3)+match(1, 1, "win")+match(2, 1, "loss"))
	s := fileStore(t, lp, dbp)
	s.Refresh()
	m, err := s.readMeta(s.db)
	if err != nil || m.offset == 0 || m.drafts != 2 {
		t.Fatalf("after first pass: %+v %v", m, err)
	}
	first := m.offset
	s.Close()

	// A new process: same database, same log. It must not re-read (and double count) what it already has.
	s2 := fileStore(t, lp, dbp)
	s2.Refresh()
	if sum := s2.Summary(); sum.Decks != 2 || sum.Matches != 1 {
		t.Fatalf("restart re-counted: %+v", sum)
	}
	appendTo(t, lp, match(1, 2, "win")+match(2, 2, "loss"))
	s2.Refresh()
	d, _ := s2.Deck(1)
	if d.Games != 2 || d.Wins != 2 {
		t.Fatalf("deck 1 after the restart: %+v", d)
	}
	m2, _ := s2.readMeta(s2.db)
	if m2.offset <= first {
		t.Fatalf("offset did not advance: %d -> %d", first, m2.offset)
	}
	var c1 CardStat
	for _, c := range s2.CardStats() {
		if c.Card == 1 {
			c1 = c
		}
	}
	if c1.Decks != 1 || c1.Games != 2 || c1.Wins != 2 {
		t.Fatalf("card stats after the restart: %+v", c1)
	}
}

func TestReplacedLogThatGrewPastTheOldOffsetResets(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	s := fileStore(t, lp, filepath.Join(dir, "d.db"))
	appendTo(t, lp, draft(1, "old", 0, 1)+match(1, 1, "win"))
	s.Refresh()
	if s.Summary().Decks != 1 {
		t.Fatal("want the old deck")
	}
	// A different log, longer than the old one: the size check alone would not notice.
	var b strings.Builder
	for i := 100; i < 120; i++ {
		b.WriteString(draft(i, "new", 1, 5, 6))
	}
	if err := os.WriteFile(lp, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Refresh()
	sum := s.Summary()
	if sum.Decks != 20 || sum.Matches != 0 {
		t.Fatalf("replaced log must be re-read from the start: %+v", sum)
	}
	if _, ok := s.Deck(1); ok {
		t.Fatal("the old log's deck survived the reset")
	}
}

func TestMatchesForDecksFromEarlierBatches(t *testing.T) {
	oldLines := batchLines
	batchLines = 3
	defer func() { batchLines = oldLines }()
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	var b strings.Builder
	for i := 1; i <= 6; i++ { // six drafts, then every deck plays: each match lands in a later batch than its draft
		b.WriteString(draft(i, fmt.Sprintf("p%d", i), 1, 10, 11, 11, i))
	}
	for i := 1; i <= 6; i++ {
		res := "win"
		if i%2 == 0 {
			res = "loss"
		}
		b.WriteString(match(i, 100+i, res))
	}
	b.WriteString(draft(3, "dup", 0, 99)) // a repeated draft record is ignored
	b.WriteString(match(3, 200, "draw"))
	appendTo(t, lp, b.String())
	s := fileStore(t, lp, filepath.Join(dir, "d.db"))
	s.Refresh()
	sum := s.Summary()
	if sum.Decks != 6 || sum.BotDecks != 6 || sum.Matches != 3 { // 7 match records / 2
		t.Fatalf("summary: %+v", sum)
	}
	d3, _ := s.Deck(3)
	if d3.Games != 2 || d3.Wins != 1 || d3.Draws != 1 || d3.Player != "p3" || len(d3.Cards) != 4 {
		t.Fatalf("deck 3: %+v", d3)
	}
	var c10, c11 CardStat
	for _, c := range s.CardStats() {
		switch c.Card {
		case 10:
			c10 = c
		case 11:
			c11 = c
		}
	}
	// card 10 is in all six decks: 6 wins/losses split 3-3 plus one draw from the repeated deck 3
	if c10.Decks != 6 || c10.Copies != 6 || c10.Games != 7 || c10.Wins != 3 || c10.Losses != 3 || c10.Draws != 1 {
		t.Fatalf("card 10: %+v", c10)
	}
	if c11.Copies != 12 {
		t.Fatalf("card 11 copies: %+v", c11)
	}
}

func TestGarbageAndOversizedLinesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	huge := strings.Repeat("x", 3<<20) // longer than the read buffer
	appendTo(t, lp, "not json\n\n"+draft(1, "a", 1, 1, 2)+`{"event":"draft","deck_id":2,"pad":"`+huge+`"}`+"\n"+draft(3, "c", 1, 3)+"{}\n"+match(1, 1, "win"))
	s := fileStore(t, lp, "")
	s.Refresh()
	sum := s.Summary()
	if sum.Decks != 2 {
		t.Fatalf("want decks 1 and 3 only: %+v", sum)
	}
	if d, _ := s.Deck(1); d.Wins != 1 {
		t.Fatalf("a line after the oversized one was lost: %+v", d)
	}
}

func TestBackgroundLoopWithConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	s := fileStore(t, lp, filepath.Join(dir, "d.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.List(Query{Card: -1, MinGames: 0, Sort: "score", Limit: 10})
				s.CardStats()
				s.Summary()
				s.Deck(5)
			}
		}()
	}
	for i := 0; i < 200; i++ {
		appendTo(t, lp, draft(i+1, "bot", 1, i%100, (i+1)%100)+match(i+1, i, "win"))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.Summary().Decks == 200 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if sum := s.Summary(); sum.Decks != 200 || sum.Warming {
		t.Fatalf("background ingest: %+v", sum)
	}
}

// TestRealLogAgreesWithAnIndependentAggregation runs the store over a copy of the production log and checks every number
// against a plain encoding/json pass that keeps everything in maps. Skipped unless DECKSTATS_REAL_LOG names the file.
func TestRealLogAgreesWithAnIndependentAggregation(t *testing.T) {
	path := os.Getenv("DECKSTATS_REAL_LOG")
	if path == "" {
		t.Skip("set DECKSTATS_REAL_LOG to a copy of decks.ndjson")
	}
	type agg struct{ games, wins, losses, draws int }
	decks := map[uint32]*Deck{}
	cardDecks, cardCopies := map[int]int{}, map[int]int{}
	cardGames := map[int]*agg{}
	matchRecs, bots, humans := 0, 0, 0
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := sc.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var r slowRec
			if json.Unmarshal(line, &r) == nil {
				switch r.Event {
				case "draft":
					if _, dup := decks[r.DeckID]; !dup && len(r.Cards) > 0 {
						decks[r.DeckID] = &Deck{ID: r.DeckID, Player: r.Player, Kind: r.Kind, Cards: r.Cards}
						if r.Kind == 1 {
							bots++
						} else {
							humans++
						}
						seen := map[int]int{}
						for _, c := range r.Cards {
							seen[c]++
						}
						for c, n := range seen {
							cardDecks[c]++
							cardCopies[c] += n
						}
					}
				case "match":
					matchRecs++
					if d := decks[r.DeckID]; d != nil {
						d.Games++
						res := "d"
						switch r.Result {
						case "win":
							d.Wins++
							res = "w"
						case "loss":
							d.Losses++
							res = "l"
						default:
							d.Draws++
						}
						seen := map[int]bool{}
						for _, c := range d.Cards {
							if seen[c] {
								continue
							}
							seen[c] = true
							a := cardGames[c]
							if a == nil {
								a = &agg{}
								cardGames[c] = a
							}
							a.games++
							switch res {
							case "w":
								a.wins++
							case "l":
								a.losses++
							default:
								a.draws++
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	f.Close()

	s := fileStore(t, path, realDBPath(t))
	start := time.Now()
	s.Refresh()
	t.Logf("ingested %d decks / %d match records in %s", len(decks), matchRecs, time.Since(start).Round(time.Millisecond))
	sum := s.Summary()
	if sum.Decks != len(decks) || sum.BotDecks != bots || sum.HumanDecks != humans || sum.Matches != matchRecs/2 || sum.Warming {
		t.Fatalf("summary %+v vs decks=%d bots=%d humans=%d matchRecs=%d", sum, len(decks), bots, humans, matchRecs)
	}
	for _, cs := range s.CardStats() {
		g := cardGames[cs.Card]
		if g == nil {
			g = &agg{}
		}
		if cs.Decks != cardDecks[cs.Card] || cs.Copies != cardCopies[cs.Card] || cs.Games != g.games || cs.Wins != g.wins || cs.Losses != g.losses || cs.Draws != g.draws {
			t.Fatalf("card %d: store %+v vs expected decks=%d copies=%d %+v", cs.Card, cs, cardDecks[cs.Card], cardCopies[cs.Card], g)
		}
	}
	for _, th := range []int{1, 5, 10, 20} {
		want := 0
		for _, d := range decks {
			if d.Games >= th {
				want++
			}
		}
		if _, got := s.List(Query{Card: -1, MinGames: th, Sort: "score", Limit: 1}); got != want {
			t.Fatalf("decks with >=%d games: store %d vs expected %d", th, got, want)
		}
	}
	top, _ := s.List(Query{Card: -1, MinGames: 10, Sort: "score", Limit: 25})
	for _, d := range top {
		w := decks[d.ID]
		if w == nil || d.Games != w.Games || d.Wins != w.Wins || d.Losses != w.Losses || d.Draws != w.Draws || !reflect.DeepEqual(d.Cards, w.Cards) {
			t.Fatalf("deck %d differs: %+v vs %+v", d.ID, d, w)
		}
		if d.Score != wilson(w.Wins, w.Games) {
			t.Fatalf("deck %d score %v", d.ID, d.Score)
		}
	}
	for i := 1; i < len(top); i++ {
		if top[i].Score > top[i-1].Score {
			t.Fatalf("top decks are not in score order at %d", i)
		}
	}
	for _, q := range []Query{
		{Card: -1, MinGames: 10, Sort: "winrate", Limit: 50},
		{Card: -1, MinGames: 5, Sort: "games", Limit: 50},
		{Card: -1, MinGames: 1, Sort: "recent", Limit: 50},
		{Card: 7, MinGames: 10, Sort: "score", Limit: 50},
		{Card: -1, MinGames: 1, Kind: "human", Sort: "score", Limit: 50},
		{Card: -1, MinGames: 1, Player: "RIPPER", Sort: "score", Limit: 5, Offset: 1000},
	} {
		t0 := time.Now()
		got, total := s.List(q)
		t.Logf("query %+v -> %d rows of %d in %s", q, len(got), total, time.Since(t0).Round(time.Microsecond))
	}
}

// TestRealLogIngestMemory ingests a copy of the production log and reports how much memory that took. The original
// implementation held the whole file (an 882 MiB log -> an OOM kill in a 768 MiB pod); this one must stay far below that.
func TestRealLogIngestMemory(t *testing.T) {
	path := os.Getenv("DECKSTATS_REAL_LOG")
	if path == "" {
		t.Skip("set DECKSTATS_REAL_LOG to a copy of decks.ndjson")
	}
	hwm := func() int {
		b, _ := os.ReadFile("/proc/self/status")
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "VmHWM:") {
				var kb int
				fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(l, "VmHWM:")), "%d", &kb)
				return kb / 1024
			}
		}
		return -1
	}
	before := hwm()
	dbp := realDBPath(t)
	s := fileStore(t, path, dbp)
	start := time.Now()
	s.Refresh()
	took := time.Since(start)
	sum := s.Summary()
	st, _ := os.Stat(dbp)
	t.Logf("ingested %d decks, %d matches from %d MiB in %s; db %d MiB; peak RSS %d MiB (was %d MiB before)",
		sum.Decks, sum.Matches, sum.LogBytes>>20, took.Round(time.Millisecond), st.Size()>>20, hwm(), before)
	// VmHWM is a process-wide high-water mark: it only says something about this ingest in a fresh process.
	if peak := hwm(); before < 100 && peak > 300 {
		t.Fatalf("peak RSS %d MiB: ingest is holding too much", peak)
	}
}

// realDBPath is where the real-log tests build their database: a throwaway one, unless DECKSTATS_REAL_DB names a file to
// keep (so a slow ingest can be reused while tuning queries).
func realDBPath(t *testing.T) string {
	if p := os.Getenv("DECKSTATS_REAL_DB"); p != "" {
		return p
	}
	return filepath.Join(t.TempDir(), "real.db")
}

// TestListMatchesAnInMemoryReference compares ListCtx with a plain Go implementation of the original semantics (filter,
// stable sort, page) over random queries on a synthetic log.
func TestListMatchesAnInMemoryReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	players := []string{"bot-wall-d", "bot-ripper-d", "Alice", "alice2", "Bob"}
	var b strings.Builder
	type ref struct {
		d Deck
	}
	refs := map[uint32]*Deck{}
	for id := 1; id <= 400; id++ {
		p := players[rng.Intn(len(players))]
		kind := 0
		if strings.HasPrefix(p, "bot") {
			kind = 1
		}
		n := 3 + rng.Intn(8)
		cards := make([]int, n)
		for i := range cards {
			cards[i] = rng.Intn(20)
		}
		b.WriteString(draft(id, p, kind, cards...))
		refs[uint32(id)] = &Deck{ID: uint32(id), Player: p, Kind: kind, Created: int64(1000 + id), Cards: cards}
	}
	for m := 0; m < 1500; m++ {
		id := uint32(1 + rng.Intn(400))
		res := []string{"win", "loss", "draw"}[rng.Intn(3)]
		b.WriteString(match(int(id), m, res))
		d := refs[id]
		d.Games++
		switch res {
		case "win":
			d.Wins++
		case "loss":
			d.Losses++
		default:
			d.Draws++
		}
	}
	for _, d := range refs {
		if d.Games > 0 {
			d.WinRate = float64(d.Wins) / float64(d.Games)
		}
		d.Score = wilson(d.Wins, d.Games)
	}
	appendTo(t, lp, b.String())
	s := fileStore(t, lp, filepath.Join(dir, "d.db"))
	s.Refresh()

	reference := func(q Query) ([]Deck, int) {
		var out []Deck
		needle := strings.ToLower(strings.TrimSpace(q.Player))
		for id := uint32(1); id <= 400; id++ {
			d := refs[id]
			if d.Games < q.MinGames {
				continue
			}
			if q.Kind == "bot" && d.Kind != 1 || q.Kind == "human" && d.Kind != 0 {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(d.Player), needle) {
				continue
			}
			if q.Card >= 0 {
				has := false
				for _, c := range d.Cards {
					if c == q.Card {
						has = true
					}
				}
				if !has {
					continue
				}
			}
			out = append(out, *d)
		}
		less := map[string]func(a, b Deck) bool{
			"games":  func(a, b Deck) bool { return a.Games > b.Games },
			"recent": func(a, b Deck) bool { return a.ID > b.ID },
			"score":  func(a, b Deck) bool { return a.Score > b.Score || a.Score == b.Score && a.ID > b.ID },
			"winrate": func(a, b Deck) bool {
				return a.WinRate > b.WinRate || a.WinRate == b.WinRate && (a.Games > b.Games || a.Games == b.Games && a.ID > b.ID)
			},
		}
		f := less["winrate"]
		if g, ok := less[q.Sort]; ok {
			f = g
		}
		sortStable(out, f)
		total := len(out)
		off := q.Offset
		if off > total {
			off = total
		}
		out = out[off:]
		if q.Limit > 0 && len(out) > q.Limit {
			out = out[:q.Limit]
		}
		return out, total
	}
	sorts := []string{"", "winrate", "score", "games", "recent"}
	kinds := []string{"", "bot", "human"}
	names := []string{"", "ali", "BOT", "zzz", "wall", "b"}
	for n := 0; n < 600; n++ {
		q := Query{Sort: sorts[rng.Intn(len(sorts))], MinGames: rng.Intn(8), Kind: kinds[rng.Intn(3)], Player: names[rng.Intn(len(names))],
			Card: -1, Limit: rng.Intn(30), Offset: rng.Intn(120)}
		if rng.Intn(3) == 0 {
			q.Card = rng.Intn(22)
		}
		got, gotTotal, err := s.ListCtx(context.Background(), q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		want, wantTotal := reference(q)
		if gotTotal != wantTotal || len(got) != len(want) {
			t.Fatalf("%+v: got %d rows of %d, want %d of %d", q, len(got), gotTotal, len(want), wantTotal)
		}
		for i := range got {
			if got[i].ID != want[i].ID {
				t.Fatalf("%+v: row %d is deck %d, want %d", q, i, got[i].ID, want[i].ID)
			}
		}
		s.cacheMu.Lock()
		s.cache = map[string]cached{}
		s.cacheMu.Unlock()
	}
}

func sortStable(a []Deck, less func(x, y Deck) bool) {
	// insertion sort keeps the test free of imports and is stable; the data set is a few hundred decks
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && less(a[j], a[j-1]); j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func TestListFailsFastWhenTheQueryGateIsFull(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	appendTo(t, lp, draft(1, "a", 1, 1, 2)+match(1, 1, "win"))
	s := fileStore(t, lp, "")
	s.Refresh()
	for i := 0; i < cap(s.gate); i++ {
		s.gate <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := s.ListCtx(ctx, Query{Card: -1, MinGames: 1, Limit: 10}); err == nil || time.Since(start) > time.Second {
		t.Fatalf("a full gate must fail the caller promptly: err=%v after %s", err, time.Since(start))
	}
	for i := 0; i < cap(s.gate); i++ {
		<-s.gate
	}
	if l, tot, err := s.ListCtx(context.Background(), Query{Card: -1, MinGames: 1, Limit: 10}); err != nil || tot != 1 || len(l) != 1 {
		t.Fatalf("after the gate drains: %v %d %d", err, tot, len(l))
	}
}

// decks k = 1..n win k games and lose one, so their Wilson scores rise strictly with k.
func streakLog(n int) string {
	var b strings.Builder
	m := 0
	for k := 1; k <= n; k++ {
		b.WriteString(draft(k, "bot-wall-d", 1, k%100, (k+1)%100, (k+2)%100))
		for g := 0; g < k; g++ {
			m++
			b.WriteString(match(k, m, "win"))
		}
		m++
		b.WriteString(match(k, m, "loss"))
	}
	return b.String()
}

func TestTierDecksSplitTheRankingIntoStrengthBands(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	appendTo(t, lp, streakLog(120))
	s := fileStore(t, lp, "")
	s.Refresh()
	ctx := context.Background()

	var prevMin float64 = -1
	seen := map[uint32]int{}
	for tier := 1; tier <= 5; tier++ {
		r, err := s.TierDecks(ctx, tier, 5, 3, 10)
		if err != nil {
			t.Fatal(err)
		}
		if r.Eligible != 119 { // decks with k=1 have 2 games: below min_games 3
			t.Fatalf("eligible = %d, want 119", r.Eligible)
		}
		if len(r.Decks) == 0 || len(r.Decks) > 10 {
			t.Fatalf("tier %d returned %d decks", tier, len(r.Decks))
		}
		for i, d := range r.Decks {
			seen[d.ID]++
			if i > 0 && d.Score > r.Decks[i-1].Score {
				t.Fatalf("tier %d is not strongest-first", tier)
			}
			if d.Score < r.ScoreMin || d.Score > r.ScoreMax {
				t.Fatalf("tier %d deck %d is outside its own band [%v, %v]", tier, d.ID, r.ScoreMin, r.ScoreMax)
			}
		}
		// every deck of a stronger tier beats every deck of a weaker one
		if r.ScoreMin < prevMin {
			t.Fatalf("tier %d reaches below tier %d's strongest deck", tier, tier-1)
		}
		prevMin = r.ScoreMax
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("deck %d is in %d tiers", id, n)
		}
	}
	// the band widths: the strongest tier is the narrow top slice, the weakest the broad bottom
	top, _ := s.TierDecks(ctx, 5, 5, 3, 100)
	bottom, _ := s.TierDecks(ctx, 1, 5, 3, 100)
	if len(top.Decks) >= len(bottom.Decks) {
		t.Fatalf("top tier (%d decks) should be narrower than the bottom tier (%d)", len(top.Decks), len(bottom.Decks))
	}
	if top.From < 0.95 || top.To != 1 {
		t.Fatalf("top band: %+v", top)
	}
	// deterministic: asking again gives the same decks
	a, _ := s.TierDecks(ctx, 3, 5, 3, 10)
	b, _ := s.TierDecks(ctx, 3, 5, 3, 10)
	for i := range a.Decks {
		if a.Decks[i].ID != b.Decks[i].ID {
			t.Fatal("tier picks changed between calls")
		}
	}
}

func TestTierDecksSpreadsEvenlyAcrossTheBand(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	appendTo(t, lp, streakLog(300))
	s := fileStore(t, lp, "")
	s.Refresh()
	r, err := s.TierDecks(context.Background(), 1, 5, 3, 6) // the bottom 36%: ~107 decks
	if err != nil || len(r.Decks) != 6 {
		t.Fatalf("%v %d", err, len(r.Decks))
	}
	// deck ids are the streak length here, so the picks should be roughly equally spaced
	gap := int(r.Decks[0].ID) - int(r.Decks[1].ID)
	for i := 2; i < len(r.Decks); i++ {
		g := int(r.Decks[i-1].ID) - int(r.Decks[i].ID)
		if g < gap-3 || g > gap+3 {
			t.Fatalf("uneven spread: gaps %d then %d (ids %v)", gap, g, []uint32{r.Decks[0].ID, r.Decks[1].ID, r.Decks[2].ID, r.Decks[3].ID, r.Decks[4].ID, r.Decks[5].ID})
		}
	}
}

func TestTierDecksWithAHandfulOfDecks(t *testing.T) {
	dir := t.TempDir()
	lp := filepath.Join(dir, "decks.ndjson")
	appendTo(t, lp, streakLog(3)) // decks 1..3 have 2, 3 and 4 games
	s := fileStore(t, lp, "")
	s.Refresh()
	for tier := 1; tier <= 5; tier++ {
		r, err := s.TierDecks(context.Background(), tier, 5, 1, 24)
		if err != nil || len(r.Decks) == 0 {
			t.Fatalf("tier %d of three decks: %v %d", tier, err, len(r.Decks))
		}
	}
	if r, err := s.TierDecks(context.Background(), 2, 5, 99, 5); err != nil || r.Eligible != 0 || len(r.Decks) != 0 {
		t.Fatalf("nothing eligible must be an empty, not failed, answer: %v %+v", err, r)
	}
	for _, bad := range [][4]int{{0, 5, 1, 5}, {6, 5, 1, 5}, {1, 11, 1, 5}, {1, 5, 0, 5}, {1, 5, 1, 0}, {1, 5, 1, 101}} {
		if _, err := s.TierDecks(context.Background(), bad[0], bad[1], bad[2], bad[3]); err != ErrBadTier {
			t.Fatalf("%v should be refused with ErrBadTier, got %v", bad, err)
		}
	}
}

func TestTierBandCutsAreNestedAndCoverTheRanking(t *testing.T) {
	for _, tiers := range []int{1, 2, 5, 10} {
		prev := 0.0
		for tier := 1; tier <= tiers; tier++ {
			from, to := TierBand(tier, tiers)
			if from != prev || to <= from || to > 1 {
				t.Fatalf("tiers=%d tier=%d band (%v, %v) after %v", tiers, tier, from, to, prev)
			}
			prev = to
		}
		if prev != 1 {
			t.Fatalf("tiers=%d ends at %v", tiers, prev)
		}
	}
}

// TestRealLogTiers prints the strength tiers the constructed bots would get from the production log.
func TestRealLogTiers(t *testing.T) {
	path := os.Getenv("DECKSTATS_REAL_LOG")
	if path == "" {
		t.Skip("set DECKSTATS_REAL_LOG (and DECKSTATS_REAL_DB to reuse a database)")
	}
	s := fileStore(t, path, realDBPath(t))
	s.Refresh()
	for _, minGames := range []int{6, 3} {
		for tier := 1; tier <= 5; tier++ {
			t0 := time.Now()
			r, err := s.TierDecks(context.Background(), tier, 5, minGames, 24)
			if err != nil {
				t.Fatal(err)
			}
			var wr float64
			var games int
			for _, d := range r.Decks {
				wr += d.WinRate
				games += d.Games
			}
			t.Logf("min_games=%d tier %d: band %.2f-%.2f, eligible %d, %d decks, score %.3f..%.3f, mean win rate %.3f, mean games %.1f (%s)",
				minGames, tier, r.From, r.To, r.Eligible, len(r.Decks), r.ScoreMin, r.ScoreMax, wr/float64(len(r.Decks)), float64(games)/float64(len(r.Decks)), time.Since(t0).Round(time.Millisecond))
		}
	}
}
