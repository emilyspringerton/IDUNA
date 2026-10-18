package matchlog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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

const line1 = `{"match_id":1,"seed":111,"names":["alice","bot-a"],"kinds":[0,1],"plays":[[1,1],[0,3]],"result":[1,0],"reason":0,"rounds":2}` + "\n"
const line2 = `{"match_id":2,"seed":222,"names":["bot-b","bot-c"],"kinds":[1,1],"plays":[[1,1]],"result":[0,1],"reason":0,"rounds":1,"mode":"draft","deck_ids":[9,10],"decks":[[1,2],[3,4]]}` + "\n"

func TestTailAndRecent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "matches.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond}
	if _, total := s.Recent(Query{}); total != 0 {
		t.Fatalf("missing file must be empty store")
	}
	appendTo(t, p, line1+line2)
	time.Sleep(time.Millisecond)

	matches, total := s.Recent(Query{})
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	// newest first
	if matches[0].MatchID != 2 || matches[1].MatchID != 1 {
		t.Fatalf("order: %+v", matches)
	}
	if matches[0].Mode != "draft" {
		t.Fatalf("match 2 should be draft mode: %+v", matches[0])
	}
	if matches[1].Mode != "" {
		t.Fatalf("match 1 should be card (random) mode: %+v", matches[1])
	}

	// player filter (case-insensitive substring on either seat)
	if matches, total = s.Recent(Query{Player: "ALICE"}); total != 1 || matches[0].MatchID != 1 {
		t.Fatalf("player filter: %+v total %d", matches, total)
	}
	// mode filter
	if matches, total = s.Recent(Query{Mode: "draft"}); total != 1 || matches[0].MatchID != 2 {
		t.Fatalf("mode filter: %+v total %d", matches, total)
	}
	if matches, total = s.Recent(Query{Mode: "card"}); total != 1 || matches[0].MatchID != 1 {
		t.Fatalf("card mode filter: %+v total %d", matches, total)
	}

	raw, sum, ok := s.Raw(2)
	if !ok || sum.MatchID != 2 || raw != line2[:len(line2)-1] {
		t.Fatalf("raw: ok=%v sum=%+v raw=%q", ok, sum, raw)
	}
	if _, _, ok := s.Raw(999); ok {
		t.Fatalf("unknown match id should not be found")
	}

	// incremental append, including a partial line that must not be parsed yet
	appendTo(t, p, `{"match_id":3,"seed":333,"names":["x","y"],"kinds":[0,0],"plays":[],"result":[1,0],"reason":1,"round`)
	time.Sleep(time.Millisecond)
	if _, total = s.Recent(Query{}); total != 2 {
		t.Fatalf("partial line should not be counted yet: total %d", total)
	}
	appendTo(t, p, `s":0}`+"\n")
	time.Sleep(time.Millisecond)
	if _, total = s.Recent(Query{}); total != 3 {
		t.Fatalf("completed line should now be counted: total %d", total)
	}
}

func TestEviction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "matches.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond, MaxRecent: 2}
	appendTo(t, p, line1+line2+`{"match_id":3,"seed":3,"names":["a","b"],"kinds":[0,0],"plays":[],"result":[1,0],"reason":1,"rounds":0}`+"\n")
	time.Sleep(time.Millisecond)
	matches, total := s.Recent(Query{})
	if total != 2 {
		t.Fatalf("eviction should cap at MaxRecent=2, got total %d: %+v", total, matches)
	}
	if _, _, ok := s.Raw(1); ok {
		t.Fatalf("oldest match (id 1) should have been evicted")
	}
	if _, _, ok := s.Raw(3); !ok {
		t.Fatalf("newest match (id 3) should still be present")
	}
}

// A long log must be read from its tail, not from the start: the production file is 660+ MiB and the pod has 768 MiB.
func TestLongLogIsReadFromTheTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "matches.ndjson")
	// ~30 MiB of matches (about 4x the tail window), with a long raw line so the file is big without a huge line count.
	pad := strings.Repeat("[1,2],", 400)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	const n = 12000
	for i := 1; i <= n; i++ {
		fmt.Fprintf(w, `{"match_id":%d,"seed":%d,"names":["a","b"],"kinds":[1,1],"plays":[%s[0,0]],"result":[1,0],"reason":0,"rounds":%d}`+"\n", i, i, pad, i%9)
	}
	w.Flush()
	f.Close()
	st, _ := os.Stat(p)
	if st.Size() < 3*tailBytes {
		t.Fatalf("fixture too small: %d bytes", st.Size())
	}
	s := &Store{Path: p, MinRefresh: time.Nanosecond, MaxRecent: 500}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	m, total := s.Recent(Query{Limit: 3})
	runtime.ReadMemStats(&after)
	if total != 500 || len(m) != 3 || m[0].MatchID != n {
		t.Fatalf("want the newest 500 matches with #%d first, got total=%d first=%+v", n, total, m)
	}
	if _, _, ok := s.Raw(1); ok {
		t.Fatal("match 1 is 30 MiB before the end: the first load must not have parsed it")
	}
	if _, _, ok := s.Raw(n - 400); !ok {
		t.Fatalf("match %d is inside the window and must be kept", n-400)
	}
	// Reading the file whole would have allocated 30+ MiB in one go; the bounded read never needs more than a chunk.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 40<<20 {
		t.Fatalf("first load allocated %d MiB", grew>>20)
	}
	// And it keeps following the file afterwards.
	appendTo(t, p, fmt.Sprintf(`{"match_id":%d,"seed":1,"names":["a","b"],"kinds":[1,1],"plays":[],"result":[0,1],"reason":0,"rounds":1}`+"\n", n+1))
	time.Sleep(time.Millisecond)
	if m, _ = s.Recent(Query{Limit: 1}); m[0].MatchID != n+1 {
		t.Fatalf("appended match not picked up: %+v", m)
	}
}

func TestIdleLogThatGrewALotSkipsToTheTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "matches.ndjson")
	s := &Store{Path: p, MinRefresh: time.Nanosecond, MaxRecent: 100}
	appendTo(t, p, line1)
	time.Sleep(time.Millisecond)
	if _, total := s.Recent(Query{}); total != 1 {
		t.Fatal("want the first match")
	}
	pad := strings.Repeat("[1,2],", 400)
	var b strings.Builder
	for i := 2; i <= 9000; i++ { // far more than tailBytes appended while nobody was looking
		fmt.Fprintf(&b, `{"match_id":%d,"seed":%d,"names":["a","b"],"kinds":[1,1],"plays":[%s[0,0]],"result":[1,0],"reason":0,"rounds":3}`+"\n", i, i, pad)
	}
	appendTo(t, p, b.String())
	time.Sleep(time.Millisecond)
	m, total := s.Recent(Query{Limit: 1})
	if total != 100 || m[0].MatchID != 9000 {
		t.Fatalf("after a long idle spell: total=%d newest=%+v", total, m)
	}
}

// TestRealLog loads a copy of the production matches.ndjson (DECKSTATS_REAL_MATCHES) and reports time and memory.
func TestRealLog(t *testing.T) {
	path := os.Getenv("DECKSTATS_REAL_MATCHES")
	if path == "" {
		t.Skip("set DECKSTATS_REAL_MATCHES to a copy of matches.ndjson")
	}
	st, _ := os.Stat(path)
	s := &Store{Path: path, MinRefresh: time.Nanosecond}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	m, total := s.Recent(Query{Limit: 5})
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	if total == 0 || len(m) == 0 {
		t.Fatalf("nothing loaded from %d MiB", st.Size()>>20)
	}
	t.Logf("%d MiB log: %d matches kept, newest #%d, first load %s, allocated %d MiB, heap now %d MiB",
		st.Size()>>20, total, m[0].MatchID, took.Round(time.Millisecond), (after.TotalAlloc-before.TotalAlloc)>>20, after.HeapAlloc>>20)
	if raw, _, ok := s.Raw(m[0].MatchID); !ok || !strings.HasPrefix(raw, `{"match_id":`) {
		t.Fatal("newest match has no raw line")
	}
}
