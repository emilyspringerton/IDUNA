package shankpit460

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Config is the match format. Defaults are founder's, 2026-10-16: first to 16 kills or six minutes.
type Config struct {
	KillLimit      int
	Duration       time.Duration
	Countdown      time.Duration // from the room forming to the match going live (others may join meanwhile)
	MaxPlayers     int
	LateJoinCutoff time.Duration // no new arrivals once this much of the match has been played
	Respawn        time.Duration // 180 ticks of 16 ms, RESPAWN_DELAY_TICKS
	SpawnProtect   time.Duration
	SnapshotEvery  time.Duration
	IdleTimeout    time.Duration
}

// DefaultConfig is the human-match format.
func DefaultConfig() Config {
	return Config{
		KillLimit: 16, Duration: 6 * time.Minute, Countdown: 6 * time.Second, MaxPlayers: 10,
		LateJoinCutoff: 4*time.Minute + 30*time.Second, Respawn: 2880 * time.Millisecond,
		SpawnProtect: time.Second, SnapshotEvery: 40 * time.Millisecond, IdleTimeout: 25 * time.Second,
	}
}

// Identity is who a connection is. ID is stable for a signed-in player (their game player row) and random for a
// guest; the stats pass attaches results to ID.
type Identity struct {
	ID     string
	Name   string
	Guest  bool
	Reject bool // a token was offered and refused: the player is a guest, but the page is told
}

// Resolver turns the bearer token from HELLO into an Identity. An empty token never reaches it (that is a guest).
type Resolver func(ctx context.Context, token string) (Identity, error)

// Conn is the transport: one binary frame out. Close ends the connection.
type Conn interface {
	Send(frame []byte) error
	Close()
}

// Client is one connected browser.
type Client struct {
	hub    *Hub
	conn   Conn
	out    chan []byte
	closed bool

	hello    bool
	id       Identity
	state    int // QIdle .. QLive
	queuedAt time.Time
	room     *Room
	slot     int
	lastSeen time.Time
}

// Hub matches humans into rooms. One mutex covers the hub and every room: the work per message is tiny and a
// single lock keeps the rules free of ordering bugs.
type Hub struct {
	cfg     Config
	arena   *Arena
	resolve Resolver
	now     func() time.Time

	mu        sync.Mutex
	clients   map[*Client]struct{}
	byID      map[string]*Client
	searching []*Client
	rooms     map[uint32]*Room
	nextRoom  uint32
	rng       *rand.Rand
	lastStat  time.Time
	syncSend  bool

	stop chan struct{}
	done sync.WaitGroup
}

// Option configures a Hub.
type Option func(*Hub)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(h *Hub) { h.now = now } }

// WithManualTicks stops the background ticker; the caller drives Tick (tests).
func WithManualTicks() Option { return func(h *Hub) { h.stop = nil } }

// WithSyncSend delivers frames inline instead of through a writer goroutine (tests, with in-memory transports).
func WithSyncSend() Option { return func(h *Hub) { h.syncSend = true } }

// WithSeed fixes the spawn / guest-name randomness (tests).
func WithSeed(s int64) Option { return func(h *Hub) { h.rng = rand.New(rand.NewSource(s)) } }

// NewHub starts a hub and its tick goroutine.
func NewHub(cfg Config, resolve Resolver, opts ...Option) (*Hub, error) {
	arena, err := ParseArena(mapFile)
	if err != nil {
		return nil, err
	}
	h := &Hub{cfg: cfg, arena: arena, resolve: resolve, now: time.Now,
		clients: map[*Client]struct{}{}, byID: map[string]*Client{}, rooms: map[uint32]*Room{},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())), stop: make(chan struct{})}
	for _, o := range opts {
		o(h)
	}
	if h.stop != nil {
		h.done.Add(1)
		go h.run()
	}
	return h, nil
}

// Close stops the ticker and drops every connection.
func (h *Hub) Close() {
	if h.stop != nil {
		close(h.stop)
		h.done.Wait()
	}
	h.mu.Lock()
	var all []*Client
	for c := range h.clients {
		all = append(all, c)
	}
	h.mu.Unlock()
	for _, c := range all {
		c.Close()
	}
}

func (h *Hub) run() {
	defer h.done.Done()
	t := time.NewTicker(h.cfg.SnapshotEvery)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.Tick(h.now())
		}
	}
}

// Connect registers a transport and returns its Client. The transport's reader must feed every frame to
// Client.Handle and call Client.Close when it ends.
func (h *Hub) Connect(conn Conn) *Client {
	c := &Client{hub: h, conn: conn, out: make(chan []byte, 128), slot: -1}
	h.mu.Lock()
	c.lastSeen = h.now()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	if !h.syncSend {
		go func() {
			for f := range c.out {
				if err := conn.Send(f); err != nil {
					conn.Close()
					// keep draining so senders never block
				}
			}
		}()
	}
	return c
}

// send queues a frame; a client that cannot keep up is dropped. Caller holds h.mu.
func (c *Client) send(f []byte) {
	if c.closed {
		return
	}
	if c.hub.syncSend {
		_ = c.conn.Send(f)
		return
	}
	select {
	case c.out <- f:
	default:
		go c.Close()
	}
}

// Close removes the client from its room or the queue and closes the transport. Safe to call twice.
func (c *Client) Close() {
	h := c.hub
	h.mu.Lock()
	if c.closed {
		h.mu.Unlock()
		return
	}
	h.detachLocked(c, false)
	c.closed = true
	delete(h.clients, c)
	if c.hello && h.byID[c.id.ID] == c {
		delete(h.byID, c.id.ID)
	}
	if !h.syncSend {
		close(c.out)
	}
	h.mu.Unlock()
	c.conn.Close()
}

// Handle processes one frame from the client.
func (c *Client) Handle(frame []byte) {
	if len(frame) == 0 {
		return
	}
	h := c.hub
	if frame[0] == CHello {
		c.handleHello(frame)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.closed || !c.hello {
		return
	}
	c.lastSeen = h.now()
	switch frame[0] {
	case CQueue:
		h.queueLocked(c)
	case CLeave:
		h.detachLocked(c, true)
		c.state = QIdle
		h.sendStatusLocked(c)
	case CPing:
		r := &reader{b: frame, off: 1}
		t := r.u32()
		if !r.bad {
			w := &writer{}
			w.u8(SPong)
			w.u32(t)
			c.send(w.b)
		}
	case CState, CFire, CHit:
		if c.room != nil {
			c.room.handle(c, frame)
		}
	}
}

func (c *Client) handleHello(frame []byte) {
	h := c.hub
	r := &reader{b: frame, off: 1}
	ver := r.u8()
	tl := r.u16()
	tok := r.bytes(tl)
	nl := r.u8()
	nm := r.bytes(nl)
	if r.bad || ver != Version || tl > 4096 {
		c.sendError(1, "bad hello")
		return
	}
	var id Identity
	if tl == 0 {
		id = Identity{Guest: true}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		id, err = h.resolve(ctx, string(tok))
		cancel()
		if err != nil || id.ID == "" {
			id = Identity{Guest: true, Reject: true}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.closed || c.hello {
		return
	}
	name := cleanName(id.Name)
	if id.Guest {
		name = cleanName(string(nm))
		if name == "" || len(name) < 2 {
			name = fmt.Sprintf("Guest %04d", h.rng.Intn(10000))
		}
		if id.ID == "" {
			id.ID = fmt.Sprintf("guest:%016x", h.rng.Uint64())
		}
	} else if name == "" {
		name = cleanName(string(nm))
		if name == "" {
			name = "Player"
		}
	}
	id.Name = name
	// one seat per identity: a second connection replaces the first
	if old := h.byID[id.ID]; old != nil && old != c && !id.Guest {
		go old.Close()
	}
	c.id = id
	c.hello = true
	h.byID[id.ID] = c
	w := &writer{}
	w.u8(SWelcome)
	w.u8(Version)
	auth := 0
	if !id.Guest {
		auth = 1
	} else if id.Reject {
		auth = 2
	}
	w.u8(auth)
	w.str(name)
	w.u16(h.onlineLocked())
	c.send(w.b)
}

func (c *Client) sendError(code int, msg string) {
	h := c.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	w := &writer{}
	w.u8(SError)
	w.u8(code)
	w.str(msg)
	c.send(w.b)
}

func (h *Hub) onlineLocked() int {
	n := 0
	for c := range h.clients {
		if c.hello {
			n++
		}
	}
	return n
}

// ---- matchmaking ---------------------------------------------------------------------------------------------

func (h *Hub) queueLocked(c *Client) {
	if c.room != nil {
		return // already in a match
	}
	for _, s := range h.searching {
		if s == c {
			h.sendStatusLocked(c)
			return
		}
	}
	c.state = QSearching
	c.queuedAt = h.now()
	h.searching = append(h.searching, c)
	h.pairLocked()
	if c.room == nil {
		h.sendStatusLocked(c)
	}
}

func (h *Hub) unqueueLocked(c *Client) {
	for i, s := range h.searching {
		if s == c {
			h.searching = append(h.searching[:i], h.searching[i+1:]...)
			return
		}
	}
}

// detachLocked takes a client out of the queue and out of its room. notify = the client stays connected.
func (h *Hub) detachLocked(c *Client, notify bool) {
	h.unqueueLocked(c)
	if c.room != nil {
		c.room.leave(c)
		c.room = nil
		c.slot = -1
	}
	c.state = QIdle
}

// pairLocked is the whole matchmaking rule: anyone searching joins a room that still has space and time; else, with
// at least two humans searching, they form a free-for-all together.
func (h *Hub) gcRoomsLocked() {
	for id, r := range h.rooms {
		if r.finished {
			delete(h.rooms, id)
		}
	}
}

func (h *Hub) pairLocked() {
	h.gcRoomsLocked()
	now := h.now()
	// 1. rooms that can take more people
	for len(h.searching) > 0 {
		var best *Room
		for _, r := range h.rooms {
			if r.joinable(now) && (best == nil || r.id < best.id) {
				best = r
			}
		}
		if best == nil {
			break
		}
		c := h.searching[0]
		h.searching = h.searching[1:]
		best.join(c, now)
	}
	// 2. at least two humans: a new room
	if len(h.searching) >= 2 {
		h.nextRoom++
		r := newRoom(h, h.nextRoom, now)
		h.rooms[r.id] = r
		n := len(h.searching)
		if n > h.cfg.MaxPlayers {
			n = h.cfg.MaxPlayers
		}
		batch := append([]*Client(nil), h.searching[:n]...)
		h.searching = h.searching[n:]
		for _, c := range batch {
			r.join(c, now)
		}
	}
}

func (h *Hub) sendStatusLocked(c *Client) {
	w := &writer{}
	w.u8(SStatus)
	w.u8(c.state)
	w.u8(len(h.searching))
	w.u16(h.onlineLocked())
	el := 0
	if c.state == QSearching {
		el = int(h.now().Sub(c.queuedAt) / time.Second)
	}
	w.u16(el)
	c.send(w.b)
}

// Tick advances every room and the queue to `now`. The background goroutine calls it every SnapshotEvery;
// tests call it by hand.
func (h *Hub) Tick(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.rooms {
		r.tick(now)
	}
	h.gcRoomsLocked()
	// idle connections
	for c := range h.clients {
		if now.Sub(c.lastSeen) > h.cfg.IdleTimeout {
			go c.Close()
		}
	}
	if now.Sub(h.lastStat) >= time.Second {
		h.lastStat = now
		for _, c := range h.searching {
			h.sendStatusLocked(c)
		}
	}
	// a lone searcher may have a room to join that opened since
	if len(h.searching) > 0 {
		h.pairLocked()
	}
}

// Stats is the public view of the hub (GET /api/v1/shankpit460/status).
type Stats struct {
	Online    int         `json:"online"`
	Searching int         `json:"searching"`
	Playing   int         `json:"playing"`
	Rooms     []RoomStats `json:"rooms"`
	KillLimit int         `json:"kill_limit"`
	Minutes   int         `json:"minutes"`
}

// RoomStats is one running match.
type RoomStats struct {
	ID          uint32 `json:"id"`
	Players     int    `json:"players"`
	Phase       string `json:"phase"`
	SecondsLeft int    `json:"seconds_left"`
	TopKills    int    `json:"top_kills"`
}

// Stats snapshots the hub.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gcRoomsLocked()
	s := Stats{Online: h.onlineLocked(), Searching: len(h.searching), KillLimit: h.cfg.KillLimit, Minutes: int(h.cfg.Duration / time.Minute), Rooms: []RoomStats{}}
	now := h.now()
	for _, r := range h.rooms {
		rs := r.stats(now)
		s.Playing += rs.Players
		s.Rooms = append(s.Rooms, rs)
	}
	return s
}
