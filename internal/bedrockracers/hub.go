package bedrockracers

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"sort"
	"sync"
	"time"
)

// Config is the hub's knobs; DefaultConfig is production, tests shorten the timers.
type Config struct {
	TickEvery     time.Duration // one sim tick: 16 ms, the module's own step
	AuthTimeout   time.Duration // from the socket opening to HELLO
	LobbyWindow   time.Duration // how long a forming lobby waits for more people once it has the minimum
	MinHumans     int           // people needed to open a lobby
	MaxHumans     int           // a full lobby starts at once (twelve cars)
	MaxRooms      int
	MaxConns      int
	MaxFramesPerS int // frames a browser may send per second (CMD is ~62 a second at most)
	MaxTicks      int // a hard cap on a match: past it the leading team wins by decree
	SettleTimeout time.Duration
	StatusEvery   time.Duration
}

// DefaultConfig is production. A tick is ~32 microseconds in wazero, so rooms are cheap; the cap is about memory and sanity.
func DefaultConfig() Config {
	return Config{
		TickEvery:     16 * time.Millisecond,
		AuthTimeout:   10 * time.Second,
		LobbyWindow:   12 * time.Second,
		MinHumans:     2,
		MaxHumans:     12,
		MaxRooms:      30,
		MaxConns:      600,
		MaxFramesPerS: 260,
		MaxTicks:      26000, // 4 minutes of play, then up to 3 more of golden goal
		SettleTimeout: 10 * time.Second,
		StatusEvery:   time.Second,
	}
}

// Identity is who the browser is playing as. An empty ID is someone with no account: they play, nothing is recorded for them.
type Identity struct {
	ID    string
	Name  string
	Guest bool // an account made by the page for a visitor who did not sign in
}

// Resolver turns a bearer token into the Bedrock Racers player it plays as.
type Resolver func(ctx context.Context, token string) (Identity, error)

// Seat is one car of a recorded match.
type Seat struct {
	Slot     int
	Team     int
	PlayerID string // empty for a bot or someone with no account
	Name     string
	Kind     string // human | bot | anon
	Left     bool   // a person who left before the end (a bot finished their match)
	LeftTick int
	Stats    SlotStats
}

// Match is a finished match, as the referee saw it.
type Match struct {
	Mode          string
	Seed          uint32
	Version       uint32
	Level         int
	HumansAtStart int
	Result        Result
	Seats         [12]Seat
	Counted       bool // it moves the ladder: two or more people, and it was played out
	Desyncs       int  // browsers whose state hash disagreed with the referee's at a checkpoint
	Replay        []byte
	StartedAt     time.Time
}

// SeatOutcome is what recording changed for one seat.
type SeatOutcome struct {
	Before, After float64
	Rated         bool
}

// Outcome is what recording a match changed.
type Outcome struct {
	MatchID int64
	Counted bool
	Seats   [12]SeatOutcome
}

// Recorder persists matches (IDUNA's database in production).
type Recorder interface {
	// Rating is a player's current ladder rating (the starting rating for one who has never played).
	Rating(ctx context.Context, playerID string) (float64, error)
	Record(ctx context.Context, m *Match) (Outcome, error)
}

// Transport is the browser side of a connection.
type Transport interface {
	Send([]byte) error
	Close()
}

// Stats is what the public status route reports.
type Stats struct {
	Online    int
	Searching int
	Playing   int
	Rooms     int
	Lobby     int
}

// Hub is the gateway.
type Hub struct {
	cfg     Config
	eng     *Engine
	resolve Resolver
	rec     Recorder
	ctx     context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	conns    map[*Conn]struct{}
	byPlayer map[string]*Conn
	queue    []*queued
	lobby    *lobby
	rooms    map[uint32]*Room
	inRoom   map[string]*Room
	nextRoom uint32
	wg       sync.WaitGroup
}

type queued struct {
	c     *Conn
	since time.Time
	team  int
	level int
}

type lobby struct {
	members  []*queued
	deadline time.Time
}

// NewHub builds the gateway and starts its matchmaker. Close stops it.
func NewHub(parent context.Context, cfg Config, eng *Engine, resolve Resolver, rec Recorder) *Hub {
	ctx, cancel := context.WithCancel(parent)
	h := &Hub{cfg: cfg, eng: eng, resolve: resolve, rec: rec, ctx: ctx, cancel: cancel,
		conns: map[*Conn]struct{}{}, byPlayer: map[string]*Conn{}, rooms: map[uint32]*Room{}, inRoom: map[string]*Room{}}
	h.wg.Add(1)
	go h.matchmaker()
	return h
}

// Close stops the matchmaker and every room, and waits for them.
func (h *Hub) Close() {
	h.cancel()
	h.wg.Wait()
	h.mu.Lock()
	cs := make([]*Conn, 0, len(h.conns))
	for c := range h.conns {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

// Version is the sim version browsers must have been built with.
func (h *Hub) Version() uint32 { return h.eng.Version() }

// Stats reports who is about.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Stats{Rooms: len(h.rooms), Searching: len(h.queue)}
	if h.lobby != nil {
		st.Lobby = len(h.lobby.members)
		st.Searching += len(h.lobby.members)
	}
	for c := range h.conns {
		c.mu.Lock()
		if c.hello {
			st.Online++
		}
		c.mu.Unlock()
	}
	for _, r := range h.rooms {
		st.Playing += r.humanCount()
	}
	return st
}

func (h *Hub) online() int {
	n := 0
	for c := range h.conns {
		c.mu.Lock()
		if c.hello {
			n++
		}
		c.mu.Unlock()
	}
	return n
}

// ---- queueing ---------------------------------------------------------------------------------------------------------------------------------

func (h *Hub) enqueue(q *queued) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, o := range h.queue {
		if o.c == q.c {
			return
		}
	}
	if h.lobby != nil {
		for _, o := range h.lobby.members {
			if o.c == q.c {
				return
			}
		}
	}
	// a lobby that is counting down takes late arrivals until it is full
	if h.lobby != nil && len(h.lobby.members) < h.cfg.MaxHumans {
		h.lobby.members = append(h.lobby.members, q)
		h.statusLobbyLocked()
		return
	}
	h.queue = append(h.queue, q)
	h.sendStatusLocked(q, QSearching)
}

func (h *Hub) dequeue(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, q := range h.queue {
		if q.c == c {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			break
		}
	}
	if h.lobby != nil {
		for i, q := range h.lobby.members {
			if q.c == c {
				h.lobby.members = append(h.lobby.members[:i], h.lobby.members[i+1:]...)
				break
			}
		}
	}
}

func (h *Hub) sendStatusLocked(q *queued, state byte) {
	lh, lms := 0, 0
	if state == QLobby && h.lobby != nil {
		lh = len(h.lobby.members)
		lms = int(time.Until(h.lobby.deadline) / time.Millisecond)
		if lms < 0 {
			lms = 0
		}
	}
	searching := len(h.queue)
	if h.lobby != nil {
		searching += len(h.lobby.members)
	}
	q.c.send(statusFrame(state, searching, h.online(), int(time.Since(q.since)/time.Second), lh, lms))
}

func (h *Hub) statusLobbyLocked() {
	if h.lobby == nil {
		return
	}
	for _, q := range h.lobby.members {
		h.sendStatusLocked(q, QLobby)
	}
}

// ---- the matchmaker ---------------------------------------------------------------------------------------------------------------------------

func (h *Hub) matchmaker() {
	defer h.wg.Done()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	lastStatus := time.Now()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-tick.C:
		}
		h.mu.Lock()
		h.pruneLocked()
		now := time.Now()
		switch {
		case h.lobby == nil && len(h.queue) >= h.cfg.MinHumans:
			n := min(len(h.queue), h.cfg.MaxHumans)
			h.lobby = &lobby{members: append([]*queued(nil), h.queue[:n]...), deadline: now.Add(h.cfg.LobbyWindow)}
			h.queue = append([]*queued(nil), h.queue[n:]...)
			h.statusLobbyLocked()
		case h.lobby != nil:
			for len(h.queue) > 0 && len(h.lobby.members) < h.cfg.MaxHumans {
				h.lobby.members = append(h.lobby.members, h.queue[0])
				h.queue = h.queue[1:]
			}
			if len(h.lobby.members) < h.cfg.MinHumans {
				// people left: whoever remains goes back to searching
				h.queue = append(append([]*queued(nil), h.lobby.members...), h.queue...)
				for _, q := range h.lobby.members {
					h.sendStatusLocked(q, QSearching)
				}
				h.lobby = nil
			} else if len(h.lobby.members) >= h.cfg.MaxHumans || !now.Before(h.lobby.deadline) {
				if len(h.rooms) >= h.cfg.MaxRooms {
					h.lobby.deadline = now.Add(2 * time.Second) // every room is busy: wait
				} else {
					members := h.lobby.members
					h.lobby = nil
					h.startRoomLocked(members)
				}
			}
		}
		if now.Sub(lastStatus) >= h.cfg.StatusEvery {
			lastStatus = now
			for _, q := range h.queue {
				h.sendStatusLocked(q, QSearching)
			}
			h.statusLobbyLocked()
		}
		h.mu.Unlock()
	}
}

// pruneLocked forgets queued people whose connection has gone.
func (h *Hub) pruneLocked() {
	live := h.queue[:0]
	for _, q := range h.queue {
		q.c.mu.Lock()
		closed := q.c.closed
		q.c.mu.Unlock()
		if !closed {
			live = append(live, q)
		}
	}
	h.queue = live
	if h.lobby != nil {
		keep := h.lobby.members[:0]
		for _, q := range h.lobby.members {
			q.c.mu.Lock()
			closed := q.c.closed
			q.c.mu.Unlock()
			if !closed {
				keep = append(keep, q)
			}
		}
		h.lobby.members = keep
	}
}

// assignSeats puts the people on the four teams of three: a team they asked for if it has room, otherwise the emptiest team.
func assignSeats(members []*queued) map[*queued]int {
	var perTeam [4]int
	out := map[*queued]int{}
	// first pass: those who named a team with room
	for _, q := range members {
		if q.team >= 0 && q.team < 4 && perTeam[q.team] < 3 {
			out[q] = q.team*3 + perTeam[q.team]
			perTeam[q.team]++
		}
	}
	// second pass: everyone else to the emptiest team
	for _, q := range members {
		if _, ok := out[q]; ok {
			continue
		}
		best := 0
		for t := 1; t < 4; t++ {
			if perTeam[t] < perTeam[best] {
				best = t
			}
		}
		out[q] = best*3 + perTeam[best]
		perTeam[best]++
	}
	return out
}

func randomSeed() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint32(b[:]) & 0x7fffffff
}

// startRoomLocked opens a room for these people (hub lock held). The room runs on its own goroutine.
func (h *Hub) startRoomLocked(members []*queued) {
	members = append([]*queued(nil), members...)
	sort.SliceStable(members, func(i, j int) bool { return members[i].since.Before(members[j].since) })
	seats := assignSeats(members)
	level := 2
	if len(members) > 0 {
		level = members[0].level
	}
	if level < 1 || level > 3 {
		level = 2
	}
	h.nextRoom++
	r := newRoom(h, h.nextRoom, randomSeed(), level)
	for _, q := range members {
		slot := seats[q]
		r.seat(slot, q.c)
		if id := q.c.identID(); id != "" {
			h.inRoom[id] = r
		}
		q.c.mu.Lock()
		q.c.room = r
		q.c.slot = slot
		q.c.mu.Unlock()
	}
	h.rooms[r.id] = r
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		r.run()
		h.mu.Lock()
		delete(h.rooms, r.id)
		for _, s := range r.seats {
			if s.c != nil {
				if id := s.c.identID(); id != "" && h.inRoom[id] == r {
					delete(h.inRoom, id)
				}
			}
		}
		h.mu.Unlock()
	}()
}
