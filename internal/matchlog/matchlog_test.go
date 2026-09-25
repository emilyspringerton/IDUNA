package matchlog

import (
	"os"
	"path/filepath"
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
