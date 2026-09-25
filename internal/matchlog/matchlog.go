// Package matchlog tails dw_server's matches.ndjson (the sibling file to decks.ndjson --
// internal/deckstats reads that one; this package reads the other) and keeps a bounded,
// in-memory index of the most recent matches so WOTAN's match-history/replay viewer (S547) can
// list recent matches and fetch one match's raw record without re-scanning a 90+MB, ever-growing
// file on every request.
//
// One raw ndjson line, written by DEADWEIGHT/apps/server/main.c's write_match_log:
//
//	{"match_id":N,"seed":N,"names":["p0","p1"],"kinds":[0|1,0|1],"plays":[[a,b],...],
//	 "result":[r0,r1],"reason":N,"rounds":N,"mode":"draft","deck_ids":[d0,d1],"decks":[[23],[23]]}
//
// ("mode"/"deck_ids"/"decks" are only present for draft matches -- see DEADWEIGHT's own
// write_match_log.) The raw line is kept verbatim (not just parsed fields) because it is exactly
// what DEADWEIGHT/tools/replay_dump.c expects on stdin to replay the match through the real,
// authoritative match core -- see internal/http/handlers/match_replay.go.
package matchlog

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Summary is one match's headline fields, parsed out of its raw log line, for the recent-matches list.
type Summary struct {
	MatchID uint32   `json:"match_id"`
	Seed    uint32   `json:"seed"`
	Names   [2]string `json:"names"`
	Kinds   [2]int   `json:"kinds"` // 0 human, 1 bot
	Result  [2]int   `json:"result"`
	Reason  int      `json:"reason"`
	Rounds  int      `json:"rounds"`
	Mode    string   `json:"mode"` // "draft" or "" (random/card mode)
}

type record struct {
	raw string
	sum Summary
}

// Store tails one matches.ndjson, keeping only the most recent MaxRecent matches in memory (older
// ones age out -- this is a live-spectator/recent-history feed, not a full archive; the full
// history stays on disk in the log file itself and in IDUNA's own game_matches/game_player_stats
// tables for aggregate stats).
type Store struct {
	Path      string
	MaxRecent int // default 2000
	// MinRefresh throttles file stats/reads (default 2s), same convention as deckstats.Store.
	MinRefresh time.Duration

	mu          sync.Mutex
	off         int64
	lastRefresh time.Time
	byID        map[uint32]*record
	order       []uint32 // match ids in log order, capped at MaxRecent (oldest evicted from the front)
}

func (s *Store) reset() {
	s.off = 0
	s.byID = map[uint32]*record{}
	s.order = nil
}

func (s *Store) maxRecent() int {
	if s.MaxRecent > 0 {
		return s.MaxRecent
	}
	return 2000
}

// refresh reads any new complete lines. Caller holds s.mu.
func (s *Store) refresh() {
	if s.byID == nil {
		s.reset()
	}
	min := s.MinRefresh
	if min == 0 {
		min = 2 * time.Second
	}
	if time.Since(s.lastRefresh) < min {
		return
	}
	s.lastRefresh = time.Now()
	f, err := os.Open(s.Path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if st.Size() < s.off {
		s.reset()
	}
	if st.Size() == s.off {
		return
	}
	if _, err := f.Seek(s.off, io.SeekStart); err != nil {
		return
	}
	buf, err := io.ReadAll(io.LimitReader(f, st.Size()-s.off))
	if err != nil {
		return
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return // no complete line yet
	}
	for _, line := range bytes.Split(buf[:end], []byte{'\n'}) {
		s.apply(line)
	}
	s.off += int64(end + 1)
}

func (s *Store) apply(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var sum Summary
	if json.Unmarshal(line, &sum) != nil || sum.MatchID == 0 {
		return
	}
	if _, dup := s.byID[sum.MatchID]; dup {
		return // ndjson is append-only; a repeat match_id shouldn't happen, but stay idempotent
	}
	s.byID[sum.MatchID] = &record{raw: string(line), sum: sum}
	s.order = append(s.order, sum.MatchID)
	if len(s.order) > s.maxRecent() {
		evict := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, evict)
	}
}

// Query filters/pages the recent-matches list.
type Query struct {
	Player string // case-insensitive substring against either name
	Mode   string // "", "draft", "card"
	Limit  int
	Offset int
}

// Recent returns the filtered page, newest first, and the total count before paging.
func (s *Store) Recent(q Query) ([]Summary, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	needle := strings.ToLower(q.Player)
	var out []Summary
	for i := len(s.order) - 1; i >= 0; i-- { // newest first
		r := s.byID[s.order[i]]
		if r == nil {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(r.sum.Names[0]), needle) && !strings.Contains(strings.ToLower(r.sum.Names[1]), needle) {
			continue
		}
		wantMode := q.Mode
		gotMode := r.sum.Mode
		if wantMode == "card" {
			wantMode = ""
		}
		if q.Mode != "" && gotMode != wantMode {
			continue
		}
		out = append(out, r.sum)
	}
	total := len(out)
	offset := q.Offset
	if offset > total {
		offset = total
	}
	out = out[offset:]
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total
}

// Raw returns one match's exact, verbatim ndjson line (what dw_replay_dump expects on stdin), and
// its parsed summary.
func (s *Store) Raw(matchID uint32) (string, Summary, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	r, ok := s.byID[matchID]
	if !ok {
		return "", Summary{}, false
	}
	return r.raw, r.sum, true
}

// Count returns how many matches are currently held in memory (for a health/summary field).
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	return len(s.order)
}
