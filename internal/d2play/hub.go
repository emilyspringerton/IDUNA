package d2play

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// Config is the gateway's knobs; DefaultConfig is production, tests shorten the timers.
type Config struct {
	ServerAddr    string        // dw2_server, host:port (TCP)
	DialTimeout   time.Duration // connecting to the match server
	AuthTimeout   time.Duration // from the WebSocket opening to the token frame
	MaxFramesPerS int           // browser frames relayed per second
	SettleTimeout time.Duration // writing a finished match to the database
}

// DefaultConfig is the production layout: the deadweight2 Service in the emily namespace.
func DefaultConfig() Config {
	return Config{
		ServerAddr:    "deadweight2.emily.svc.cluster.local:7800",
		DialTimeout:   5 * time.Second,
		AuthTimeout:   10 * time.Second,
		MaxFramesPerS: 60,
		SettleTimeout: 10 * time.Second,
	}
}

// Identity is who the browser is playing as.
type Identity struct {
	ID    string // the d2 player_id (UUID)
	Name  string
	Guest bool
}

// Resolver turns a bearer token into the D2 player it plays as.
type Resolver func(ctx context.Context, token string) (Identity, error)

// Placement is one item the player put on their grid, in the order they placed it (that order is the placement index).
type Placement struct {
	Item int  `json:"item"`
	Row  int  `json:"row"`
	Col  int  `json:"col"`
	Rot  int  `json:"rot"`
	Cut  bool `json:"cut,omitempty"`
}

// RoundLog is one round break as the player lived it.
type RoundLog struct {
	No        int `json:"no"`
	Behind    int `json:"behind"`
	TargetMS  int `json:"target_ms"`
	CallMS    int `json:"call_ms"` // when the gateway saw the player's call, ms after the break (-1 none)
	YourCall  int `json:"your_call"`
	YourGrade int `json:"your_grade"`
	YourFx    int `json:"your_effect"`
	OppCall   int `json:"opp_call"`
	OppGrade  int `json:"opp_grade"`
	OppFx     int `json:"opp_effect"`
}

// Record is one finished (or forfeited) match, as the gateway saw it.
type Record struct {
	PlayerID    string
	Name        string
	Seat        int
	MatchID     uint32
	Seed        uint32
	OppName     string
	OppKind     string // bot | human
	OppPlayerID string // a human on this gateway; empty for a bot (the recorder maps bot names) or a native-client human
	Result      string // win | loss | tie | forfeit | aborted
	Reason      string // hull | timeout | forfeit | server
	Winner      int    // seat index that won, 2 = tie
	Ticks       int
	HullYou     int
	HullOpp     int
	ArmorYou    int
	ArmorOpp    int
	CargoYou    int
	CargoOpp    int
	Loadout     []Placement
	Rounds      []RoundLog
	Seconds     int
	Counted     bool // moves the ladder: a bot or a ladder human opponent and the match was not cut short by the server
	SkipStats   bool // the opponent's gateway session already settled the rating
}

// Outcome is what recording a match changed for the player.
type Outcome struct {
	Rated        bool
	RatingBefore float64
	RatingAfter  float64
	// Both seats, so the opponent's gateway session (a human) can report its own change without touching the ladder twice.
	Seat [2]struct{ Before, After float64 }
}

// Recorder persists a finished match (IDUNA's database in production).
type Recorder interface {
	Record(ctx context.Context, r Record) (Outcome, error)
}

// Transport is the browser side of a connection.
type Transport interface {
	Send([]byte) error
	Close()
}

type matchKey struct{ id, seed uint32 }

type settled struct {
	out Outcome
	at  time.Time
}

// Hub is the gateway.
type Hub struct {
	cfg     Config
	resolve Resolver
	rec     Recorder

	mu      sync.Mutex
	conns   map[*Conn]struct{}
	byID    map[string]*Conn
	seatIDs map[matchKey][2]string // who sat where in a match (both humans on this gateway register)
	seatAt  map[matchKey]time.Time
	settled map[matchKey]settled

	settleMu sync.Mutex
}

// NewHub builds a gateway.
func NewHub(cfg Config, resolve Resolver, rec Recorder) (*Hub, error) {
	if cfg.ServerAddr == "" {
		return nil, errors.New("d2play: no match server address")
	}
	return &Hub{cfg: cfg, resolve: resolve, rec: rec, conns: map[*Conn]struct{}{}, byID: map[string]*Conn{},
		seatIDs: map[matchKey][2]string{}, seatAt: map[matchKey]time.Time{}, settled: map[matchKey]settled{}}, nil
}

// Status is the public /status answer.
type Status struct {
	Online    int `json:"online"`
	Searching int `json:"searching"`
	Playing   int `json:"playing"`
}

// Stats is what GET /status returns.
func (h *Hub) Stats() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{}
	for c := range h.conns {
		c.mu.Lock()
		if c.authed {
			st.Online++
			switch c.phase {
			case phQueued:
				st.Searching++
			case phMatch:
				st.Playing++
			}
		}
		c.mu.Unlock()
	}
	return st
}

// ---- connections ---------------------------------------------------------------------------------------------------

const (
	phIdle   = iota // connected, not searching
	phQueued        // in the server's queue
	phMatch         // MATCH_FOUND .. MATCH_END
)

type pending struct {
	cut bool
	p   Placement
	idx int // for a cut
}

// Conn is one browser connection (and, once it has signed in, one TCP connection to the match server).
type Conn struct {
	hub *Hub
	t   Transport

	mu      sync.Mutex
	ident   Identity
	authed  bool
	closed  bool
	srv     net.Conn
	phase   int
	m       *matchState
	pend    []pending
	tok     int
	tokAt   time.Time
	authTmr *time.Timer
}

// matchState is what the gateway remembers of the match in progress.
type matchState struct {
	key        matchKey
	seat       int
	oppName    string
	oppKind    string
	packMS     int
	startedAt  time.Time
	loadout    []Placement
	hullYou    int
	hullOpp    int
	armorYou   int
	armorOpp   int
	cargoYou   int
	cargoOpp   int
	ticks      int
	rounds     []RoundLog
	breakAt    time.Time
	ended      bool
	combatSeen bool
}

// Connect registers a browser connection. The first frame must be AUTH within AuthTimeout.
func (h *Hub) Connect(t Transport) *Conn {
	c := &Conn{hub: h, t: t}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	c.authTmr = time.AfterFunc(h.cfg.AuthTimeout, func() {
		c.mu.Lock()
		ok := c.authed
		c.mu.Unlock()
		if !ok {
			c.fail(ErrBadHello, "no sign-in")
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
	b := []byte{GError, byte(code), byte(len(msg))}
	c.send(append(b, msg...))
}

// Close drops the connection. Leaving a match that is not over forfeits it: the server awards the opponent the win, and the
// gateway records the loss.
func (c *Conn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	srv := c.srv
	c.srv = nil
	id := c.ident.ID
	var rec *Record
	if c.m != nil && !c.m.ended {
		rec = c.buildRecordLocked("forfeit", EndForfeit, 1-c.m.seat, c.m.ticks)
		c.m.ended = true
	}
	c.mu.Unlock()
	if c.authTmr != nil {
		c.authTmr.Stop()
	}
	if srv != nil {
		_ = srv.Close()
	}
	if rec != nil {
		go c.hub.settle(c, *rec, false)
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
	if now.Sub(c.tokAt) >= time.Second {
		c.tokAt = now
		c.tok = 0
	}
	c.tok++
	return c.tok <= c.hub.cfg.MaxFramesPerS
}

// Handle processes one frame from the browser.
func (c *Conn) Handle(msg []byte) {
	if len(msg) == 0 || len(msg) > 2048 {
		return
	}
	if msg[0] == CAuth {
		c.onAuth(msg)
		return
	}
	c.mu.Lock()
	ok, srv := c.authed, c.srv
	c.mu.Unlock()
	if !ok || srv == nil {
		c.fail(ErrBadHello, "sign in first")
		return
	}
	want, known := clientPayload[msg[0]]
	if !known || len(msg)-1 != want {
		return // anything but a player's own orders is dropped
	}
	if !c.allow() {
		return
	}
	c.mu.Lock()
	switch msg[0] {
	case CQueue:
		if c.phase == phIdle {
			c.phase = phQueued
		}
	case CLeave:
		if c.phase == phQueued {
			c.phase = phIdle
		}
	case CPlace:
		c.pend = append(c.pend, pending{p: Placement{Item: int(msg[1]), Row: int(msg[2]), Col: int(msg[3]), Rot: int(msg[4])}})
	case CPanicCut:
		c.pend = append(c.pend, pending{cut: true, idx: int(msg[1])})
	case CRoundCall:
		if c.m != nil && len(c.m.rounds) > 0 {
			r := &c.m.rounds[len(c.m.rounds)-1]
			if r.CallMS < 0 {
				r.CallMS = int(time.Since(c.m.breakAt) / time.Millisecond)
			}
		}
	}
	c.mu.Unlock()
	if len(c.pend) > 64 {
		c.mu.Lock()
		c.pend = c.pend[1:]
		c.mu.Unlock()
	}
	_, _ = srv.Write(withLen(msg))
}

func (c *Conn) onAuth(msg []byte) {
	c.mu.Lock()
	if c.authed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if len(msg) < 3 {
		c.fail(ErrBadHello, "bad sign-in")
		return
	}
	n := int(binary.LittleEndian.Uint16(msg[1:3]))
	if n == 0 || n > len(msg)-3 || n > 1500 {
		c.fail(ErrBadHello, "sign in or continue as a guest first")
		return
	}
	token := strings.TrimSpace(string(msg[3 : 3+n]))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	id, err := c.hub.resolve(ctx, token)
	if err != nil || id.ID == "" {
		c.fail(ErrBadHello, "your sign-in was not accepted; sign in again")
		return
	}
	srv, err := net.DialTimeout("tcp", c.hub.cfg.ServerAddr, c.hub.cfg.DialTimeout)
	if err != nil {
		c.fail(ErrServerDown, "the match server is not answering right now")
		return
	}
	if tc, ok := srv.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	c.mu.Lock()
	if c.closed || c.authed {
		c.mu.Unlock()
		_ = srv.Close()
		return
	}
	c.ident, c.authed, c.srv = id, true, srv
	c.mu.Unlock()
	// one live connection per player: a newer tab replaces an older one
	c.hub.mu.Lock()
	old := c.hub.byID[id.ID]
	c.hub.byID[id.ID] = c
	c.hub.mu.Unlock()
	if old != nil && old != c {
		old.fail(ErrBusy, "you opened the game in another tab")
		old.Close()
	}
	name := id.Name
	if len(name) > 40 {
		name = name[:40]
	}
	guest := byte(0)
	if id.Guest {
		guest = 1
	}
	b := []byte{GIdent, guest, byte(len(name))}
	b = append(b, name...)
	b = append(b, byte(len(id.ID)))
	b = append(b, id.ID...)
	c.send(b)
	_, _ = srv.Write(withLen(helloFrame(id.Name)))
	go c.readServer(srv)
}

// readServer reads the match server's frames until the connection ends.
func (c *Conn) readServer(srv net.Conn) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 2048)
	for {
		n, err := srv.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				f, used, ferr := splitFrame(buf)
				if ferr != nil {
					c.serverGone("the match server sent something this gateway does not understand")
					return
				}
				if f == nil {
					break
				}
				frame := append([]byte(nil), f...)
				buf = buf[used:]
				c.onServer(frame)
			}
		}
		if err != nil {
			c.serverGone("the match server closed the connection")
			return
		}
	}
}

func (c *Conn) serverGone(why string) {
	c.mu.Lock()
	closed := c.closed
	var rec *Record
	if !closed && c.m != nil && !c.m.ended {
		rec = c.buildRecordLocked("aborted", EndServer, 2, c.m.ticks)
		c.m.ended = true
	}
	c.mu.Unlock()
	if closed {
		return
	}
	if rec != nil {
		c.hub.settle(c, *rec, false)
	}
	c.fail(ErrServerDown, why)
	c.Close()
}

// onServer updates the gateway's picture of the match from one of the server's frames and relays it.
func (c *Conn) onServer(f []byte) {
	var after []byte // a gateway frame to send after the relayed one
	var settleRec *Record
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	switch f[0] {
	case SQueued:
		if c.phase == phIdle {
			c.phase = phQueued
		}
	case SMatchFound:
		m := &matchState{
			key:     matchKey{u32(f[1:]), u32(f[5:])},
			seat:    int(f[9]),
			oppName: cstr(f[10:26]),
			oppKind: "human",
			packMS:  int(u16(f[27:])),
			hullYou: 100, hullOpp: 100,
			startedAt: time.Now(),
		}
		if f[26] == 1 {
			m.oppKind = "bot"
		}
		c.m, c.phase, c.pend = m, phMatch, nil
		id := c.ident.ID
		key, seat := m.key, m.seat
		c.mu.Unlock()
		c.hub.registerSeat(key, seat, id)
		c.send(f)
		return
	case SPlaceAck, SPlaceReject, SCutAck, SCutReject:
		if c.m != nil && len(c.pend) > 0 {
			p := c.pend[0]
			c.pend = c.pend[1:]
			if f[0] == SPlaceAck && !p.cut {
				c.m.loadout = append(c.m.loadout, p.p)
			}
			if f[0] == SCutAck && p.cut && p.idx >= 0 && p.idx < len(c.m.loadout) {
				c.m.loadout[p.idx].Cut = true
			}
		}
	case SCombatStart:
		if c.m != nil {
			c.m.combatSeen = true
			c.m.hullYou, c.m.hullOpp = int(f[1]), int(f[2])
			c.m.armorYou, c.m.armorOpp = int(u16(f[3:])), int(u16(f[5:]))
			c.m.cargoYou, c.m.cargoOpp = int(u16(f[7:])), int(u16(f[9:]))
		}
	case STick:
		if c.m != nil {
			c.m.ticks = int(f[1])
			c.m.hullYou, c.m.hullOpp = int(f[2]), int(f[3])
			c.m.armorYou, c.m.armorOpp = int(u16(f[4:])), int(u16(f[6:]))
		}
	case SRoundBreak:
		if c.m != nil {
			c.m.breakAt = time.Now()
			c.m.rounds = append(c.m.rounds, RoundLog{No: int(f[1]), Behind: int(f[2]), TargetMS: int(u16(f[3:])), CallMS: -1, YourCall: 2, OppCall: 2})
		}
	case SRoundResult:
		if c.m != nil && len(c.m.rounds) > 0 {
			r := &c.m.rounds[len(c.m.rounds)-1]
			r.YourCall, r.YourGrade, r.YourFx = int(f[1]), int(f[2]), int(f[3])
			r.OppCall, r.OppGrade, r.OppFx = int(f[4]), int(f[5]), int(f[6])
		}
	case SMatchEnd:
		if c.m != nil && !c.m.ended {
			res := int(f[5])
			reason := int(f[6])
			c.m.ticks = int(f[7])
			name, winner := "loss", 1-c.m.seat
			switch res {
			case ResWin:
				name, winner = "win", c.m.seat
			case ResTie:
				name, winner = "tie", 2
			}
			rec := c.buildRecordLocked(name, reason, winner, c.m.ticks)
			c.m.ended = true
			c.phase = phIdle
			settleRec = rec
		}
	}
	c.mu.Unlock()
	c.send(f)
	if settleRec != nil {
		out := c.hub.settle(c, *settleRec, true)
		b := []byte{GRecorded, 0, 0, 0, 0, 0, 0}
		if out.Rated {
			b[1] = 1
		}
		if settleRec.OppKind == "bot" {
			b[2] = 1
		}
		binary.LittleEndian.PutUint16(b[3:], uint16(int16(out.RatingAfter+0.5)))
		binary.LittleEndian.PutUint16(b[5:], uint16(int16(out.RatingAfter-out.RatingBefore+signHalf(out.RatingAfter-out.RatingBefore))))
		after = b
	}
	if after != nil {
		c.send(after)
	}
}

func signHalf(x float64) float64 {
	if x < 0 {
		return -0.5
	}
	return 0.5
}

// buildRecordLocked turns the match state into a Record. winner is a seat index (2 = tie). Caller holds c.mu.
func (c *Conn) buildRecordLocked(result string, reason, winner, ticks int) *Record {
	m := c.m
	rn := map[int]string{EndHull: "hull", EndTimeout: "timeout", EndForfeit: "forfeit", EndServer: "server"}[reason]
	rec := &Record{
		PlayerID: c.ident.ID, Name: c.ident.Name, Seat: m.seat, MatchID: m.key.id, Seed: m.key.seed,
		OppName: m.oppName, OppKind: m.oppKind, Result: result, Reason: rn, Winner: winner, Ticks: ticks,
		HullYou: m.hullYou, HullOpp: m.hullOpp, ArmorYou: m.armorYou, ArmorOpp: m.armorOpp, CargoYou: m.cargoYou, CargoOpp: m.cargoOpp,
		Loadout: append([]Placement(nil), m.loadout...), Rounds: append([]RoundLog(nil), m.rounds...),
		Seconds: int(time.Since(m.startedAt) / time.Second),
		Counted: reason != EndServer,
	}
	if result == "aborted" {
		rec.Counted = false
	}
	return rec
}

// registerSeat remembers who sits where, so the opponent's gateway session can find a human opponent's player id.
func (h *Hub) registerSeat(k matchKey, seat int, id string) {
	if seat < 0 || seat > 1 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for kk, t := range h.seatAt {
		if now.Sub(t) > 30*time.Minute {
			delete(h.seatAt, kk)
			delete(h.seatIDs, kk)
			delete(h.settled, kk)
		}
	}
	ids := h.seatIDs[k]
	ids[seat] = id
	h.seatIDs[k] = ids
	h.seatAt[k] = now
}

// settle records a match. Two humans on this gateway each call it for their own side; the ladder is only touched once.
func (h *Hub) settle(c *Conn, rec Record, wantOutcome bool) Outcome {
	h.settleMu.Lock()
	defer h.settleMu.Unlock()
	key := matchKey{rec.MatchID, rec.Seed}
	if rec.OppKind == "human" {
		h.mu.Lock()
		rec.OppPlayerID = h.seatIDs[key][1-rec.Seat]
		prev, done := h.settled[key]
		h.mu.Unlock()
		if rec.OppPlayerID == "" {
			rec.Counted = false // a human who is not on this ladder (the native client runs --no-auth)
		}
		if done {
			rec.SkipStats = true
			ctx, cancel := context.WithTimeout(context.Background(), h.cfg.SettleTimeout)
			defer cancel()
			out, _ := h.rec.Record(ctx, rec)
			out.Rated = prev.out.Rated
			out.RatingBefore, out.RatingAfter = prev.out.Seat[rec.Seat].Before, prev.out.Seat[rec.Seat].After
			return out
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.SettleTimeout)
	defer cancel()
	out, err := h.rec.Record(ctx, rec)
	if err != nil {
		return Outcome{}
	}
	if rec.OppKind == "human" && out.Rated {
		h.mu.Lock()
		h.settled[key] = settled{out: out, at: time.Now()}
		h.mu.Unlock()
	}
	return out
}

// Loadout and rounds as stored JSON.
func MarshalLoadout(p []Placement) string { b, _ := json.Marshal(p); return string(b) }
func MarshalRounds(r []RoundLog) string   { b, _ := json.Marshal(r); return string(b) }
