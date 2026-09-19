// Package deckstats aggregates DEADWEIGHT's draft deck log (dw_server --match-log DIR/decks.ndjson) into per-deck and
// per-card win rates for WOTAN's public deck browser. The log is append-only ndjson with two record kinds:
//
//	{"event":"draft","deck_id":N,"t":unix,"player":"..","kind":0|1,"cards":[23 ids],"names":[..]}
//	{"event":"match","deck_id":N,"match_id":M,"player":"..","kind":0|1,"result":"win|loss|draw","rounds":R}
//
// The store tails the file incrementally (only new complete lines are parsed) so a growing log stays cheap, and resets if
// the file shrinks or is replaced.
package deckstats

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Deck is one drafted deck and its results so far.
type Deck struct {
	ID      uint32  `json:"deck_id"`
	Player  string  `json:"player"`
	Kind    int     `json:"kind"` // 0 human, 1 bot
	Created int64   `json:"created"`
	Cards   []int   `json:"cards"` // 23 card ids in pick order (duplicates = copies)
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	WinRate float64 `json:"win_rate"` // wins / games (draws are not wins); 0 with no games
	Score   float64 `json:"score"`    // 95% Wilson lower bound of win_rate: ranks a 9-1 deck above a 2-0 deck
}

// wilson is the lower bound of the 95% Wilson score interval for wins out of games.
func wilson(wins, games int) float64 {
	if games == 0 {
		return 0
	}
	const z = 1.96
	n, p := float64(games), float64(wins)/float64(games)
	return (p + z*z/(2*n) - z*math.Sqrt((p*(1-p)+z*z/(4*n))/n)) / (1 + z*z/n)
}

// CardStat is a card's record across every deck that ran it.
type CardStat struct {
	Card    int     `json:"card"`
	Decks   int     `json:"decks"`  // decks containing the card
	Copies  int     `json:"copies"` // total copies across those decks
	Games   int     `json:"games"`  // games played by decks containing it
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	WinRate float64 `json:"win_rate"`
	Score   float64 `json:"score"` // Wilson lower bound across all games with this card
}

// Summary is the headline numbers.
type Summary struct {
	Decks      int   `json:"decks"`
	HumanDecks int   `json:"human_decks"`
	BotDecks   int   `json:"bot_decks"`
	Matches    int   `json:"matches"`
	Updated    int64 `json:"updated"`
}

// Query filters and orders the deck list.
type Query struct {
	Sort     string // winrate (default), score (sample-size adjusted), games, recent
	MinGames int
	Kind     string // "", "bot", "human"
	Player   string // case-insensitive substring
	Card     int    // -1 = any; else only decks containing this card id
	Limit    int
	Offset   int
}

type rec struct {
	Event  string `json:"event"`
	DeckID uint32 `json:"deck_id"`
	T      int64  `json:"t"`
	Player string `json:"player"`
	Kind   int    `json:"kind"`
	Cards  []int  `json:"cards"`
	Result string `json:"result"`
}

// Store tails one decks.ndjson.
type Store struct {
	Path string
	// MinRefresh throttles file stats/reads (default 2s).
	MinRefresh time.Duration

	mu          sync.Mutex
	off         int64
	lastRefresh time.Time
	decks       map[uint32]*Deck
	order       []uint32 // deck ids in log order
	matchRecs   int
	version     int
	cardVer     int
	cardStats   []CardStat
}

func (s *Store) reset() {
	s.off = 0
	s.decks = map[uint32]*Deck{}
	s.order = nil
	s.matchRecs = 0
	s.version++
}

// refresh reads any new complete lines. Caller holds s.mu.
func (s *Store) refresh() {
	if s.decks == nil {
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
	s.version++
}

func (s *Store) apply(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var r rec
	if json.Unmarshal(line, &r) != nil {
		return
	}
	switch r.Event {
	case "draft":
		if _, dup := s.decks[r.DeckID]; dup || len(r.Cards) == 0 {
			return
		}
		s.decks[r.DeckID] = &Deck{ID: r.DeckID, Player: r.Player, Kind: r.Kind, Created: r.T, Cards: r.Cards}
		s.order = append(s.order, r.DeckID)
	case "match":
		d := s.decks[r.DeckID]
		s.matchRecs++
		if d == nil {
			return
		}
		d.Games++
		switch r.Result {
		case "win":
			d.Wins++
		case "loss":
			d.Losses++
		default:
			d.Draws++
		}
		d.WinRate = float64(d.Wins) / float64(d.Games)
		d.Score = wilson(d.Wins, d.Games)
	}
}

// Summary returns the headline counts.
func (s *Store) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	sum := Summary{Decks: len(s.order), Matches: s.matchRecs / 2, Updated: s.lastRefresh.Unix()}
	for _, id := range s.order {
		if s.decks[id].Kind == 1 {
			sum.BotDecks++
		} else {
			sum.HumanDecks++
		}
	}
	return sum
}

// Deck returns one deck by id.
func (s *Store) Deck(id uint32) (Deck, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	d, ok := s.decks[id]
	if !ok {
		return Deck{}, false
	}
	return *d, true
}

// List returns the filtered, ordered page and the total number of matches before paging.
func (s *Store) List(q Query) ([]Deck, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	var out []Deck
	needle := strings.ToLower(q.Player)
	for _, id := range s.order {
		d := s.decks[id]
		if d.Games < q.MinGames {
			continue
		}
		if q.Kind == "bot" && d.Kind != 1 || q.Kind == "human" && d.Kind != 0 {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(d.Player), needle) {
			continue
		}
		if q.Card >= 0 && !hasCard(d.Cards, q.Card) {
			continue
		}
		out = append(out, *d)
	}
	switch q.Sort {
	case "games":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Games > out[j].Games })
	case "recent":
		sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	case "score":
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Score != out[j].Score {
				return out[i].Score > out[j].Score
			}
			return out[i].ID > out[j].ID
		})
	default: // winrate, ties broken by more games, then newest
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].WinRate != out[j].WinRate {
				return out[i].WinRate > out[j].WinRate
			}
			if out[i].Games != out[j].Games {
				return out[i].Games > out[j].Games
			}
			return out[i].ID > out[j].ID
		})
	}
	total := len(out)
	if q.Offset > total {
		q.Offset = total
	}
	out = out[q.Offset:]
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, total
}

func hasCard(cards []int, c int) bool {
	for _, x := range cards {
		if x == c {
			return true
		}
	}
	return false
}

// CardStats returns per-card records (ordered by card id), recomputed only when the log changed.
func (s *Store) CardStats() []CardStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	if s.cardStats != nil && s.cardVer == s.version {
		return s.cardStats
	}
	byCard := map[int]*CardStat{}
	for _, id := range s.order {
		d := s.decks[id]
		seen := map[int]bool{}
		for _, c := range d.Cards {
			cs := byCard[c]
			if cs == nil {
				cs = &CardStat{Card: c}
				byCard[c] = cs
			}
			cs.Copies++
			if !seen[c] {
				seen[c] = true
				cs.Decks++
				cs.Games += d.Games
				cs.Wins += d.Wins
				cs.Losses += d.Losses
				cs.Draws += d.Draws
			}
		}
	}
	out := make([]CardStat, 0, len(byCard))
	for _, cs := range byCard {
		if cs.Games > 0 {
			cs.WinRate = float64(cs.Wins) / float64(cs.Games)
			cs.Score = wilson(cs.Wins, cs.Games)
		}
		out = append(out, *cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Card < out[j].Card })
	s.cardStats, s.cardVer = out, s.version
	return out
}
