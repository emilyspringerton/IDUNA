package empire

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"sync"
	"time"
)

// Config is the gateway's knobs; DefaultConfig is production, tests shorten the timers.
type Config struct {
	TickEvery      time.Duration // one sim tick (20 a second in production)
	Countdown      time.Duration // the pause between the match frame and the first tick
	HumanWindow    time.Duration // how long a lone person waits for another before an AI sits down
	AuthTimeout    time.Duration // from the socket opening to the token frame
	ReconnectGrace time.Duration // how long a dropped player has to come back before the match counts as resigned
	MaxFramesPerS  int           // browser frames relayed per second
	MaxRooms       int
	SettleTimeout  time.Duration // writing a finished match to the database
	LeagueEvery    time.Duration // an AI-against-AI match, played in real time so it can be watched; 0 = none
	LeagueFirst    time.Duration // the first one after start-up
	LeagueFast     bool          // tests: no pacing
}

// DefaultConfig is production.
func DefaultConfig() Config {
	return Config{
		TickEvery:      50 * time.Millisecond,
		Countdown:      3 * time.Second,
		HumanWindow:    6 * time.Second,
		AuthTimeout:    10 * time.Second,
		ReconnectGrace: 25 * time.Second,
		MaxFramesPerS:  80,
		MaxRooms:       24,
		SettleTimeout:  10 * time.Second,
		LeagueEvery:    15 * time.Minute,
		LeagueFirst:    45 * time.Second,
	}
}

// Identity is who the browser is playing as.
type Identity struct {
	ID    string
	Name  string
	Guest bool
}

// Resolver turns a bearer token into the EMPIRE player it plays as.
type Resolver func(ctx context.Context, token string) (Identity, error)

// Player is a ladder participant (a person or one of the AIs).
type Player struct {
	ID     string
	Name   string
	Bot    int // 0 a person, else the AI's number
	Rating float64
}

// SeatInfo is one side of a recorded match.
type SeatInfo struct {
	PlayerID string
	Name     string
	Bot      int
	Deck     uint32
	Faction  int
	Rating   float64
}

// Match is a finished match, as the referee saw it.
type Match struct {
	Mode    string // ranked | league
	Seed    uint32
	Version uint32
	Seats   [2]SeatInfo
	Result  Result
	Cmds    []Cmd
	Replay  []byte
	Counted bool
}

// Outcome is what recording a match changed.
type Outcome struct {
	Counted       bool
	MatchID       int64
	Before, After [2]float64
}

// Recorder persists things (IDUNA's database in production).
type Recorder interface {
	// Rating is a player's current ladder rating (the starting rating for one who has never played).
	Rating(ctx context.Context, playerID string) (float64, error)
	// BotPlayer finds or makes the ladder row for AI number n (1..8) named name.
	BotPlayer(ctx context.Context, n int, name string) (Player, error)
	Record(ctx context.Context, m *Match) (Outcome, error)
}

// BotNames are the eight AIs' names, by number 1..8 (index 0 unused). They match em_ai.prn's bot-name.
var BotNames = [9]string{"", "Marrow", "Quill", "Sable", "Ledger", "Vesper", "Brindle", "Thorn", "Hush"}

// Transport is the browser side of a connection.
type Transport interface {
	Send([]byte) error
	Close()
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
	rooms    map[uint32]*Room
	inRoom   map[string]*Room // person -> the live match they sit in
	nextRoom uint32
	bots     [9]Player
	lastBot  map[string]int
	leagueN  int
	wg       sync.WaitGroup
}

type queued struct {
	c       *Conn
	since   time.Time
	deck    uint32
	faction int
	want    int
	rating  float64
}

// NewHub builds a gateway and starts its matchmaker (and the league, if configured). Close stops them.
func NewHub(parent context.Context, cfg Config, eng *Engine, resolve Resolver, rec Recorder) *Hub {
	ctx, cancel := context.WithCancel(parent)
	h := &Hub{cfg: cfg, eng: eng, resolve: resolve, rec: rec, ctx: ctx, cancel: cancel,
		conns: map[*Conn]struct{}{}, byPlayer: map[string]*Conn{}, rooms: map[uint32]*Room{}, inRoom: map[string]*Room{}, lastBot: map[string]int{}}
	h.wg.Add(1)
	go h.matchmaker()
	if cfg.LeagueEvery > 0 {
		h.wg.Add(1)
		go h.league()
	}
	return h
}

// Close stops everything and waits for the rooms to wind down.
func (h *Hub) Close() {
	h.cancel()
	h.mu.Lock()
	cs := make([]*Conn, 0, len(h.conns))
	for c := range h.conns {
		cs = append(cs, c)
	}
	h.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
	h.wg.Wait()
}

// Version is what clients must have been built with.
func (h *Hub) Version() uint32 { return h.eng.Version() }

// Status is the public /status answer.
type Status struct {
	Online    int `json:"online"`
	Searching int `json:"searching"`
	Playing   int `json:"playing"`
	Rooms     int `json:"rooms"`
	Watching  int `json:"watching"`
}

// Stats is what GET /status returns.
func (h *Hub) Stats() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{Searching: len(h.queue), Rooms: len(h.rooms)}
	for c := range h.conns {
		c.mu.Lock()
		if c.authed {
			st.Online++
		}
		if c.watching {
			st.Watching++
		}
		c.mu.Unlock()
	}
	st.Playing = len(h.inRoom)
	return st
}

// LiveRoom is one match in progress, for the public list.
type LiveRoom struct {
	ID       uint32    `json:"id"`
	Names    [2]string `json:"names"`
	Bots     [2]int    `json:"bots"`
	Mode     string    `json:"mode"`
	Tick     int       `json:"tick"`
	Watchers int       `json:"watchers"`
	Factions [2]int    `json:"factions"`
}

// Live lists the matches being played right now.
func (h *Hub) Live() []LiveRoom {
	h.mu.Lock()
	rs := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rs = append(rs, r)
	}
	h.mu.Unlock()
	out := make([]LiveRoom, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.live())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ---- bots ----------------------------------------------------------------------------------------------------------------------------------

func (h *Hub) bot(n int) (Player, error) {
	h.mu.Lock()
	p := h.bots[n]
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(h.ctx, h.cfg.SettleTimeout)
	defer cancel()
	if p.ID == "" {
		var err error
		p, err = h.rec.BotPlayer(ctx, n, BotNames[n])
		if err != nil {
			return p, err
		}
		p.Bot = n
	}
	if r, err := h.rec.Rating(ctx, p.ID); err == nil {
		p.Rating = r
	}
	h.mu.Lock()
	h.bots[n] = p
	h.mu.Unlock()
	return p, nil
}

// pickBot chooses an AI to sit down with a player: one of the three nearest in rating, not the one they just had.
func (h *Hub) pickBot(rating float64, playerID string) int {
	type cand struct {
		n int
		d float64
	}
	var cs []cand
	for n := 1; n <= 8; n++ {
		p, err := h.bot(n)
		if err != nil {
			continue
		}
		d := p.Rating - rating
		if d < 0 {
			d = -d
		}
		if n == h.lastBotFor(playerID) {
			d += 400
		}
		cs = append(cs, cand{n, d})
	}
	if len(cs) == 0 {
		return 1 + rand.Intn(8)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].d < cs[j].d })
	k := 3
	if len(cs) < k {
		k = len(cs)
	}
	n := cs[rand.Intn(k)].n
	h.mu.Lock()
	h.lastBot[playerID] = n
	h.mu.Unlock()
	return n
}

func (h *Hub) lastBotFor(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastBot[id]
}

// ---- matchmaking -----------------------------------------------------------------------------------------------------------------------------

func (h *Hub) enqueue(q *queued) {
	h.mu.Lock()
	for _, e := range h.queue {
		if e.c == q.c {
			h.mu.Unlock()
			return
		}
	}
	h.queue = append(h.queue, q)
	waiting := len(h.queue)
	h.mu.Unlock()
	q.c.send([]byte{GQueued, byte(waiting), byte(waiting >> 8)})
}

func (h *Hub) dequeue(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.queue {
		if e.c == c {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			return
		}
	}
}

func (h *Hub) matchmaker() {
	defer h.wg.Done()
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
			h.pair()
		}
	}
}

func (h *Hub) pair() {
	now := time.Now()
	h.mu.Lock()
	if len(h.queue) == 0 {
		h.mu.Unlock()
		return
	}
	full := len(h.rooms) >= h.cfg.MaxRooms
	var start []func()
	used := map[*queued]bool{}
	// two people who asked for whoever is waiting
	if !full {
		for i, a := range h.queue {
			if used[a] || a.want != 0 {
				continue
			}
			for _, b := range h.queue[i+1:] {
				if used[b] || b.want != 0 || b.c.ident.ID == a.c.ident.ID {
					continue
				}
				used[a], used[b] = true, true
				a, b := a, b
				start = append(start, func() { h.startHumans(a, b) })
				break
			}
		}
		// a person who asked for an AI, or has waited long enough
		for _, a := range h.queue {
			if used[a] {
				continue
			}
			if a.want > 0 || now.Sub(a.since) >= h.cfg.HumanWindow {
				used[a] = true
				a := a
				start = append(start, func() { h.startBot(a) })
			}
		}
	}
	if len(used) > 0 {
		keep := h.queue[:0]
		for _, e := range h.queue {
			if !used[e] {
				keep = append(keep, e)
			}
		}
		h.queue = keep
	}
	h.mu.Unlock()
	for _, f := range start {
		f()
	}
}

func (h *Hub) newRoomID() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextRoom++
	return h.nextRoom
}

func (h *Hub) startHumans(a, b *queued) {
	seed := rand.Uint32() | 1
	seats := [2]seatState{
		{conn: a.c, id: a.c.ident.ID, name: a.c.ident.Name, deck: a.deck, fac: a.faction, rating: a.rating},
		{conn: b.c, id: b.c.ident.ID, name: b.c.ident.Name, deck: b.deck, fac: b.faction, rating: b.rating},
	}
	if rand.Intn(2) == 1 {
		seats[0], seats[1] = seats[1], seats[0]
	}
	h.launch(newRoom(h, "ranked", seed, seats))
}

func (h *Hub) startBot(a *queued) {
	n := a.want
	if n < 1 || n > 8 {
		n = h.pickBot(a.rating, a.c.ident.ID)
	}
	p, err := h.bot(n)
	if err != nil {
		a.c.send(errorFrame(ErrServer, "the AIs are not available right now"))
		return
	}
	s := h.newSimForBots()
	if s == nil {
		a.c.send(errorFrame(ErrServer, "the table could not be laid"))
		return
	}
	bd, bf := s.BotDeck(n), s.BotFaction(n)
	s.Close()
	seed := rand.Uint32() | 1
	me := seatState{conn: a.c, id: a.c.ident.ID, name: a.c.ident.Name, deck: a.deck, fac: a.faction, rating: a.rating}
	ai := seatState{id: p.ID, name: p.Name, bot: n, deck: bd, fac: bf, rating: p.Rating}
	seats := [2]seatState{me, ai}
	if rand.Intn(2) == 1 {
		seats[0], seats[1] = seats[1], seats[0]
	}
	h.launch(newRoom(h, "ranked", seed, seats))
}

// newSimForBots makes a throwaway board just to ask the module for the AIs' decks.
func (h *Hub) newSimForBots() *Sim {
	s, err := h.eng.New(h.ctx)
	if err != nil {
		return nil
	}
	return s
}

func (h *Hub) launch(r *Room) {
	h.mu.Lock()
	h.nextRoom++
	r.id = h.nextRoom
	h.rooms[r.id] = r
	for i := range r.seats {
		if r.seats[i].conn != nil {
			h.inRoom[r.seats[i].id] = r
		}
	}
	h.mu.Unlock()
	for i := range r.seats {
		if c := r.seats[i].conn; c != nil {
			c.mu.Lock()
			c.room, c.seat = r, i
			c.mu.Unlock()
		}
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		r.run()
	}()
}

func (h *Hub) roomDone(r *Room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms, r.id)
	for i := range r.seats {
		if id := r.seats[i].id; id != "" && h.inRoom[id] == r {
			delete(h.inRoom, id)
		}
	}
}

// ---- the league: the AIs play each other, in real time, where anyone can watch --------------------------------------------------------------

func (h *Hub) league() {
	defer h.wg.Done()
	wait := h.cfg.LeagueFirst
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = h.cfg.LeagueEvery
		h.leagueMatch()
	}
}

// leagueMatch seats two AIs close in rating against each other.
func (h *Hub) leagueMatch() bool {
	h.mu.Lock()
	full := len(h.rooms) >= h.cfg.MaxRooms-2
	h.leagueN++
	n := h.leagueN
	h.mu.Unlock()
	if full {
		return false
	}
	a := 1 + (n*5+rand.Intn(8))%8
	best, bd := 0, 1e9
	pa, err := h.bot(a)
	if err != nil {
		return false
	}
	for b := 1; b <= 8; b++ {
		if b == a {
			continue
		}
		pb, err := h.bot(b)
		if err != nil {
			continue
		}
		d := pb.Rating - pa.Rating
		if d < 0 {
			d = -d
		}
		d += float64(rand.Intn(120))
		if d < bd {
			best, bd = b, d
		}
	}
	if best == 0 {
		return false
	}
	pb, _ := h.bot(best)
	s := h.newSimForBots()
	if s == nil {
		return false
	}
	seats := [2]seatState{
		{id: pa.ID, name: pa.Name, bot: a, deck: s.BotDeck(a), fac: s.BotFaction(a), rating: pa.Rating},
		{id: pb.ID, name: pb.Name, bot: best, deck: s.BotDeck(best), fac: s.BotFaction(best), rating: pb.Rating},
	}
	s.Close()
	r := newRoom(h, "league", rand.Uint32()|1, seats)
	r.fast = h.cfg.LeagueFast
	h.launch(r)
	return true
}

// LeagueNow plays one AI-against-AI match now (tests, and an operator's curl through a handler if one is ever added).
func (h *Hub) LeagueNow() bool { return h.leagueMatch() }

var errNoRoom = errors.New("no such match")

// watch puts a connection in a room as a spectator.
func (h *Hub) watch(c *Conn, id uint32) error {
	h.mu.Lock()
	r := h.rooms[id]
	h.mu.Unlock()
	if r == nil {
		return errNoRoom
	}
	r.ctrl <- func() { r.addSpectator(c) }
	return nil
}
