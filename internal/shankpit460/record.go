package shankpit460

// record.go -- what a finished match leaves behind for the stats. Founder real-time, 2026-11-15: "we need esports and stats
// up asap ... start tracking and training bots asap for our various esports leagues". Every match that went live is handed
// to the hub's Recorder (handlers/shankpit460_stats.go writes it: the match feed, each player's line, the rated ladder),
// including the people who left before the end, who used to vanish from the END standings without a trace.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"
)

// MinCounted is how long an abandoned match must have been live before it moves anyone's record (a match played to the
// kill limit or the clock always counts).
const MinCounted = 20 * time.Second

// Match is one match that went live, with everyone who played in it in finishing order.
type Match struct {
	UID       string // unique across restarts: when the match went live, and the room
	RoomID    uint32
	Reason    int // EndKills | EndTime | EndAbandoned
	KillLimit int
	LiveAt    time.Time
	EndedAt   time.Time
	// Counted: at least two people played, and it was played out (kill limit or clock) or abandoned after MinCounted.
	// Only a counted match moves the ladder.
	Counted bool
	Players []MatchPlayer
}

// MatchPlayer is one person's line. Those who stayed to the end are ranked by kills, then fewer deaths; equal kills and
// deaths share a place. Those who left are placed below everyone who stayed, the latest to leave highest.
type MatchPlayer struct {
	ID     string // the shankpit460 player row for someone signed in; "guest:..." for a guest
	Name   string
	Guest  bool
	Slot   int
	Kills  int
	Deaths int
	Place  int
	Left   bool
	LeftAt time.Duration // how far into the live match they left
	// Winner: first place alone. A time-limit tie for first has no winner; in an abandoned match the one person still
	// there wins (a counted forfeit, as in D2), which the END frame itself does not say.
	Winner bool
}

// Recorder stores a finished match. The hub calls it on its own goroutine, never under its lock.
type Recorder interface {
	Record(ctx context.Context, m *Match) error
}

// WithRecorder gives the hub somewhere to keep results.
func WithRecorder(rec Recorder) Option { return func(h *Hub) { h.rec = rec } }

// departed is someone who left a live match: their numbers at the moment they went.
type departed struct {
	id            Identity
	name          string
	slot          int
	kills, deaths int
	at            time.Duration
}

// buildMatch is the record of this room, called by end() while the players are still seated.
func (r *Room) buildMatch(now time.Time, reason int) *Match {
	m := &Match{UID: fmt.Sprintf("%d-%d", r.liveAt.UnixNano(), r.id), RoomID: r.id, Reason: reason, KillLimit: r.hub.cfg.KillLimit,
		LiveAt: r.liveAt, EndedAt: now}
	var stay []MatchPlayer
	at := map[string]int{}
	for _, p := range r.players {
		if p == nil {
			continue
		}
		at[p.c.id.ID] = len(stay)
		stay = append(stay, MatchPlayer{ID: p.c.id.ID, Name: p.name, Guest: p.c.id.Guest, Slot: p.slot, Kills: p.kills, Deaths: p.deaths})
	}
	// someone who left and came back (a second tab replacing the first, a rejoin) is one line, with both stints
	var gone []MatchPlayer
	goneAt := map[string]int{}
	for _, d := range r.left {
		if i, ok := at[d.id.ID]; ok {
			stay[i].Kills += d.kills
			stay[i].Deaths += d.deaths
			continue
		}
		if i, ok := goneAt[d.id.ID]; ok {
			gone[i].Kills += d.kills
			gone[i].Deaths += d.deaths
			if d.at > gone[i].LeftAt {
				gone[i].LeftAt, gone[i].Slot, gone[i].Name = d.at, d.slot, d.name
			}
			continue
		}
		goneAt[d.id.ID] = len(gone)
		gone = append(gone, MatchPlayer{ID: d.id.ID, Name: d.name, Guest: d.id.Guest, Slot: d.slot, Kills: d.kills, Deaths: d.deaths,
			Left: true, LeftAt: d.at})
	}
	sort.SliceStable(stay, func(i, j int) bool {
		if stay[i].Kills != stay[j].Kills {
			return stay[i].Kills > stay[j].Kills
		}
		if stay[i].Deaths != stay[j].Deaths {
			return stay[i].Deaths < stay[j].Deaths
		}
		return stay[i].Slot < stay[j].Slot
	})
	for i := range stay {
		stay[i].Place = i + 1
		if i > 0 && stay[i].Kills == stay[i-1].Kills && stay[i].Deaths == stay[i-1].Deaths {
			stay[i].Place = stay[i-1].Place
		}
	}
	sort.SliceStable(gone, func(i, j int) bool { return gone[i].LeftAt > gone[j].LeftAt })
	for i := range gone {
		gone[i].Place = len(stay) + i + 1
	}
	// one person left standing (everyone else went) wins by forfeit; otherwise first place alone wins
	if len(stay) == 1 || (len(stay) > 1 && stay[1].Place > 1) {
		stay[0].Winner = true
	}
	m.Players = append(stay, gone...)
	m.Counted = len(m.Players) >= 2 && (reason != EndAbandoned || now.Sub(r.liveAt) >= MinCounted)
	return m
}

// record hands one match to the Recorder. A failure is logged: the match is over either way.
func (h *Hub) record(m *Match) {
	defer h.recWG.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.rec.Record(ctx, m); err != nil {
		log.Printf("shankpit460: recording match %s: %v", m.UID, err)
	}
}
