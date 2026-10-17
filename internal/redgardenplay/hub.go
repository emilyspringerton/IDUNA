package redgardenplay

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Pool is one matchmaker with its persistent bot pool behind it (REDGARDEN ops: redgarden-rnd / redgarden-stable).
type Pool struct {
	Name  string
	Mode  int    // 0 = 3v3, 1 = 10v10
	Addr  string // host:port of the matchmaker (UDP)
	Lobby int    // players per match, humans + bots
}

// Config is the gateway's knobs; DefaultConfig is production, tests shorten the timers.
type Config struct {
	Pools            []Pool
	Secret           []byte        // REDGARDEN_TICKET_SECRET, shared with apps/arena_server
	TicketTTL        time.Duration // how long a minted ticket is good for
	SnapshotEvery    time.Duration // how often a snapshot is forwarded to the browser
	FindResend       time.Duration // PACKET_FIND_MATCH resend (the matchmaker de-duplicates an address)
	ConnectResend    time.Duration // PACKET_CONNECT resend until PACKET_WELCOME
	ConnectTimeout   time.Duration // from MATCH_FOUND to WELCOME
	QueueTimeout     time.Duration // longest a search may run (the pool may be mid-match)
	PickDeadline     time.Duration // the gateway picks a hero for a player who has not, so the 60 s lobby clock cannot kill the match
	ServerSilence    time.Duration // no packet from the arena for this long = the arena is gone
	GhostLife        time.Duration // an abandoned search keeps its place in the queue this long (see ghost)
	MaxCommandsPerS  int
	MinCountedSecond int // matches shorter than this (in seconds) are recorded but never count toward win/loss
}

// DefaultConfig is the production layout (the services in `kubectl get svc -n emily`).
func DefaultConfig() Config {
	return Config{
		Pools: []Pool{
			{Name: "rnd-3v3", Mode: 0, Addr: "redgarden-rnd-udp.emily.svc.cluster.local:7780", Lobby: 6},
			{Name: "rnd-10v10", Mode: 1, Addr: "redgarden-rnd-udp.emily.svc.cluster.local:7778", Lobby: 20},
			{Name: "stable-10v10", Mode: 1, Addr: "redgarden-stable-udp.emily.svc.cluster.local:8778", Lobby: 20},
		},
		TicketTTL:        5 * time.Minute,
		SnapshotEvery:    50 * time.Millisecond,
		FindResend:       2 * time.Second,
		ConnectResend:    300 * time.Millisecond,
		ConnectTimeout:   12 * time.Second,
		QueueTimeout:     15 * time.Minute,
		PickDeadline:     38 * time.Second,
		ServerSilence:    12 * time.Second,
		GhostLife:        15 * time.Minute,
		MaxCommandsPerS:  90,
		MinCountedSecond: 20,
	}
}

// Identity is who the browser is playing as.
type Identity struct {
	ID    string // the REDGARDEN player_id (UUID)
	Name  string
	Guest bool
}

// Resolver turns a bearer token into the REDGARDEN player it plays as.
type Resolver func(ctx context.Context, token string) (Identity, error)

// MatchRecord is one finished (or abandoned) match, as the gateway saw it.
type MatchRecord struct {
	PlayerID   string
	Pool       string
	Mode       int
	Lobby      int
	Slot       int
	Team       int
	HeroID     int
	Result     int // ResWin, ResLoss, ResAbandoned, ResLost
	WinnerTeam int // 0 or 1, -1 when there was none
	Kills      int
	Deaths     int
	FlowEarned int
	XP         int
	Seconds    int
	Roster     []HeroStat
	Counted    bool // true when it should move the win/loss counters
}

// Recorder persists match records (IDUNA's database in production).
type Recorder interface {
	Record(ctx context.Context, m MatchRecord) error
}

// Transport is the browser side of a connection.
type Transport interface {
	Send([]byte) error
	Close()
}

// Hub is the gateway: it knows the pools and who is where.
type Hub struct {
	cfg     Config
	resolve Resolver
	rec     Recorder

	mu      sync.Mutex
	conns   map[*Conn]struct{}
	byID    map[string]*Conn // one live connection per player
	pools   []*poolState
	started time.Time
}

type poolState struct {
	Pool
	searching int
	playing   int
	ghosts    int
}

// NewHub builds a gateway.
func NewHub(cfg Config, resolve Resolver, rec Recorder) (*Hub, error) {
	if len(cfg.Pools) == 0 {
		return nil, errors.New("redgardenplay: no pools configured")
	}
	if len(cfg.Secret) == 0 {
		return nil, errors.New("redgardenplay: REDGARDEN_TICKET_SECRET is not set (the arena rejects every ticket without it)")
	}
	h := &Hub{cfg: cfg, resolve: resolve, rec: rec, conns: map[*Conn]struct{}{}, byID: map[string]*Conn{}, started: time.Now()}
	for _, p := range cfg.Pools {
		h.pools = append(h.pools, &poolState{Pool: p})
	}
	return h, nil
}

// PoolStatus is one pool as /status shows it.
type PoolStatus struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Lobby     int    `json:"lobby"`
	Searching int    `json:"searching"`
	Playing   int    `json:"playing"`
	State     string `json:"state"` // open | searching | in_match
}

// Status is the public /status answer.
type Status struct {
	Online    int          `json:"online"`
	Searching int          `json:"searching"`
	Playing   int          `json:"playing"`
	Pools     []PoolStatus `json:"pools"`
}

func modeName(m int) string {
	if m == 0 {
		return "3v3"
	}
	return "10v10"
}

// Stats is what GET /status returns.
func (h *Hub) Stats() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{Online: len(h.conns), Pools: []PoolStatus{}}
	for _, p := range h.pools {
		ps := PoolStatus{Name: p.Name, Mode: modeName(p.Mode), Lobby: p.Lobby, Searching: p.searching, Playing: p.playing, State: "open"}
		if p.playing+p.ghosts > 0 {
			ps.State = "in_match"
		} else if p.searching > 0 {
			ps.State = "searching"
		}
		st.Searching += p.searching
		st.Playing += p.playing
		st.Pools = append(st.Pools, ps)
	}
	return st
}

// pick chooses the pool for a mode: an idle one if there is one, else the least busy (the search then waits for the
// bot pool to come back from the match in progress -- the matchmaker queues in order).
func (h *Hub) pick(mode int) *poolState {
	var best *poolState
	score := 1 << 30
	for _, p := range h.pools {
		if p.Mode != mode {
			continue
		}
		s := p.playing*100 + p.ghosts*100 + p.searching
		if s < score {
			best, score = p, s
		}
	}
	return best
}

// Conn is one browser connection.
type Conn struct {
	hub *Hub
	t   Transport

	mu       sync.Mutex
	ident    Identity
	hello    bool
	run      *run // the current search / match, nil when idle
	lastSent time.Time
	cmdTok   int
	cmdAt    time.Time
	closed   bool
	helloBy  *time.Timer
}

// Connect registers a browser connection. The first frame must be HELLO within ten seconds.
func (h *Hub) Connect(t Transport) *Conn {
	c := &Conn{hub: h, t: t}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	c.helloBy = time.AfterFunc(10*time.Second, func() {
		c.mu.Lock()
		ok := c.hello
		c.mu.Unlock()
		if !ok {
			c.fail(ErrBadHello, "no hello")
			c.Close()
		}
	})
	return c
}

func (c *Conn) send(b []byte) {
	if c.t != nil {
		_ = c.t.Send(b)
	}
}

func (c *Conn) fail(code int, msg string) {
	if len(msg) > 200 {
		msg = msg[:200]
	}
	b := []byte{SError, byte(code), byte(len(msg))}
	c.send(append(b, msg...))
}

func (c *Conn) status(state, mode, lobby, ahead int, elapsed time.Duration) {
	b := []byte{SStatus, byte(state), byte(mode), byte(lobby), 0, 0, byte(ahead)}
	binary.LittleEndian.PutUint16(b[4:], uint16(min(int(elapsed/time.Second), 65535)))
	c.send(b)
}

// Close drops the connection; a match in progress is recorded as abandoned.
func (c *Conn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	r := c.run
	c.run = nil
	id := c.ident.ID
	c.mu.Unlock()
	if c.helloBy != nil {
		c.helloBy.Stop()
	}
	if r != nil {
		r.userLeft()
	}
	c.hub.mu.Lock()
	delete(c.hub.conns, c)
	if id != "" && c.hub.byID[id] == c {
		delete(c.hub.byID, id)
	}
	c.hub.mu.Unlock()
	if c.t != nil {
		c.t.Close()
	}
}

func (c *Conn) allow() bool {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.cmdAt) >= time.Second {
		c.cmdAt = now
		c.cmdTok = 0
	}
	c.cmdTok++
	return c.cmdTok <= c.hub.cfg.MaxCommandsPerS
}

// Handle processes one frame from the browser.
func (c *Conn) Handle(msg []byte) {
	if len(msg) == 0 || len(msg) > 4096 {
		return
	}
	switch msg[0] {
	case CHello:
		c.onHello(msg)
		return
	case CPing:
		if len(msg) >= 5 {
			c.send([]byte{SPong, msg[1], msg[2], msg[3], msg[4]})
		}
		return
	}
	c.mu.Lock()
	ok := c.hello
	c.mu.Unlock()
	if !ok {
		c.fail(ErrBadHello, "hello first")
		return
	}
	switch msg[0] {
	case CQueue:
		if len(msg) >= 2 {
			c.onQueue(int(msg[1]))
		}
	case CLeave:
		c.onLeave()
	default:
		if msg[0] >= CPick && msg[0] <= CHold {
			if !c.allow() {
				return
			}
			c.mu.Lock()
			r := c.run
			c.mu.Unlock()
			if r != nil {
				r.command(msg)
			}
		}
	}
}

func (c *Conn) onHello(msg []byte) {
	c.mu.Lock()
	if c.hello {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if len(msg) < 4 {
		c.fail(ErrBadHello, "bad hello")
		return
	}
	n := int(binary.LittleEndian.Uint16(msg[2:4]))
	if n > len(msg)-4 || n == 0 || n > 3000 {
		c.fail(ErrBadHello, "sign in or continue as a guest first")
		return
	}
	token := strings.TrimSpace(string(msg[4 : 4+n]))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	id, err := c.hub.resolve(ctx, token)
	if err != nil || id.ID == "" {
		c.fail(ErrBadHello, "your sign-in was not accepted; sign in again")
		return
	}
	c.mu.Lock()
	c.ident, c.hello = id, true
	c.mu.Unlock()
	// one live connection per player: a newer tab replaces an older one
	c.hub.mu.Lock()
	old := c.hub.byID[id.ID]
	c.hub.byID[id.ID] = c
	online := len(c.hub.conns)
	c.hub.mu.Unlock()
	if old != nil && old != c {
		old.fail(ErrBusy, "you opened the arena in another tab")
		old.Close()
	}
	name := id.Name
	if len(name) > 40 {
		name = name[:40]
	}
	b := []byte{SWelcome, Version, 1, byte(len(name))}
	b = append(b, name...)
	b = append(b, byte(online), byte(online>>8))
	c.send(b)
}

func (c *Conn) onQueue(mode int) {
	if mode != 0 && mode != 1 {
		c.fail(ErrBadRequest, "unknown mode")
		return
	}
	c.mu.Lock()
	if c.run != nil || c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.hub.mu.Lock()
	p := c.hub.pick(mode)
	if p == nil {
		c.hub.mu.Unlock()
		c.fail(ErrNoArena, "no arena is configured for that mode")
		return
	}
	p.searching++
	ahead := p.playing + p.ghosts
	c.hub.mu.Unlock()
	r := newRun(c, p, mode, ahead)
	c.mu.Lock()
	c.run = r
	c.mu.Unlock()
	go r.loop()
}

func (c *Conn) onLeave() {
	c.mu.Lock()
	r := c.run
	c.run = nil
	c.mu.Unlock()
	if r != nil {
		r.userLeft()
	}
	c.status(StIdle, 0, 0, 0, 0)
}

// runEnded is called by a run when it is finished with the connection.
func (c *Conn) runEnded(r *run) {
	c.mu.Lock()
	if c.run == r {
		c.run = nil
	}
	c.mu.Unlock()
}

func randomHero() int {
	var b [1]byte
	_, _ = rand.Read(b[:])
	return int(b[0]) % 31
}

func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return out, fmt.Errorf("bad player id")
	}
	for i := 0; i < 16; i++ {
		var v byte
		for j := 0; j < 2; j++ {
			ch := s[i*2+j]
			var d byte
			switch {
			case ch >= '0' && ch <= '9':
				d = ch - '0'
			case ch >= 'a' && ch <= 'f':
				d = ch - 'a' + 10
			case ch >= 'A' && ch <= 'F':
				d = ch - 'A' + 10
			default:
				return out, fmt.Errorf("bad player id")
			}
			v = v<<4 | d
		}
		out[i] = v
	}
	return out, nil
}

var _ = net.IPv4len
