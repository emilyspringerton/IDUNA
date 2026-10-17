package empire

import (
	"context"
	"time"
)

type seatState struct {
	conn    *Conn
	id      string
	name    string
	bot     int
	deck    uint32
	fac     int
	rating  float64
	discAt  time.Time
	emoteAt time.Time
}

type roomMsg struct {
	c       *Conn
	kind    byte
	a, b, d byte
}

// Room is one match: a board, two seats (a person or an AI each), and whoever is watching.
type Room struct {
	hub   *Hub
	id    uint32
	mode  string
	seed  uint32
	seats [2]seatState
	fast  bool

	in   chan roomMsg
	ctrl chan func()
	done chan struct{}

	// owned by the room's goroutine
	sim        *Sim
	spectators map[*Conn]struct{}
	started    bool
	resyncAt   map[*Conn]time.Time

	// read by Live()
	liveTick int32
	liveW    int32
}

func newRoom(h *Hub, mode string, seed uint32, seats [2]seatState) *Room {
	return &Room{hub: h, mode: mode, seed: seed, seats: seats, in: make(chan roomMsg, 256), ctrl: make(chan func(), 64),
		done: make(chan struct{}), spectators: map[*Conn]struct{}{}, resyncAt: map[*Conn]time.Time{}}
}

// do runs f on the room's goroutine; false if the match is already over.
func (r *Room) do(f func()) bool {
	select {
	case <-r.done:
		return false
	default:
	}
	select {
	case r.ctrl <- f:
		return true
	case <-r.done:
		return false
	}
}

func (r *Room) live() LiveRoom {
	l := LiveRoom{ID: r.id, Mode: r.mode, Tick: int(loadInt32(&r.liveTick)), Watchers: int(loadInt32(&r.liveW))}
	for i := 0; i < 2; i++ {
		l.Names[i], l.Bots[i], l.Factions[i] = r.seats[i].name, r.seats[i].bot, r.seats[i].fac
	}
	return l
}

func (r *Room) info(seat int, flags int, countdown time.Duration) matchInfo {
	you, opp := seat, 1-seat
	if seat < 0 {
		you, opp = 0, 1
	}
	k := 0
	if r.seats[opp].bot > 0 {
		k = 1
	}
	return matchInfo{
		ID: r.id, Seed: r.seed, Seat: you, OppKind: k, OppPers: r.seats[opp].bot, OppName: r.seats[opp].name,
		OppRating: int(r.seats[opp].rating + 0.5), YourRating: int(r.seats[you].rating + 0.5),
		Deck: [2]uint32{r.seats[0].deck, r.seats[1].deck}, Fac: [2]int{r.seats[0].fac, r.seats[1].fac},
		Flags: flags, CountdownMS: int(countdown / time.Millisecond), Seat0Name: r.seats[0].name,
	}
}

func (r *Room) humanSeats() int {
	n := 0
	for i := range r.seats {
		if r.seats[i].bot == 0 {
			n++
		}
	}
	return n
}

func (r *Room) flags() int {
	f := 0
	if r.mode == "ranked" {
		f |= FlagRanked
	}
	return f
}

func (r *Room) broadcast(b []byte) {
	for i := range r.seats {
		if c := r.seats[i].conn; c != nil {
			c.send(b)
		}
	}
	for c := range r.spectators {
		c.send(b)
	}
}

func (r *Room) addSpectator(c *Conn) {
	r.spectators[c] = struct{}{}
	storeInt32(&r.liveW, int32(len(r.spectators)))
	c.mu.Lock()
	c.watching = true
	c.mu.Unlock()
	if r.sim == nil {
		return
	}
	c.send(matchFrame(r.info(-1, r.flags()|FlagSpectator, 0)))
	c.send(snapshotFrame(r.sim.Tick(), r.sim.Snapshot()))
}

func (r *Room) detach(c *Conn) {
	if _, ok := r.spectators[c]; ok {
		delete(r.spectators, c)
		storeInt32(&r.liveW, int32(len(r.spectators)))
	}
	for i := range r.seats {
		if r.seats[i].conn == c {
			r.seats[i].conn = nil
			r.seats[i].discAt = time.Now()
		}
	}
}

// reattach seats a person who dropped and came back: the match frame again, then the board, then the turns carry on.
func (r *Room) reattach(id string, c *Conn) {
	for i := range r.seats {
		s := &r.seats[i]
		if s.bot != 0 || s.id != id {
			continue
		}
		s.conn = c
		s.discAt = time.Time{}
		c.mu.Lock()
		c.room, c.seat = r, i
		c.mu.Unlock()
		if r.sim != nil {
			c.send(matchFrame(r.info(i, r.flags(), 0)))
			c.send(snapshotFrame(r.sim.Tick(), r.sim.Snapshot()))
		}
	}
}

func (r *Room) seatOf(c *Conn) int {
	for i := range r.seats {
		if r.seats[i].conn == c {
			return i
		}
	}
	return -1
}

func (r *Room) onMsg(m roomMsg) {
	seat := r.seatOf(m.c)
	if seat < 0 || r.sim == nil {
		return
	}
	switch m.kind {
	case CPlay:
		if !r.started || m.a > 3 || m.b > 71 || m.d > 127 {
			return
		}
		r.sim.Play(seat, int(m.a), int(m.b), int(m.d))
	case CEmote:
		if m.a > 3 || time.Since(r.seats[seat].emoteAt) < 3*time.Second {
			return
		}
		r.seats[seat].emoteAt = time.Now()
		r.broadcast([]byte{GEmote, byte(seat), m.a})
	case CResync:
		if time.Since(r.resyncAt[m.c]) < 2*time.Second {
			return
		}
		r.resyncAt[m.c] = time.Now()
		m.c.send(snapshotFrame(r.sim.Tick(), r.sim.Snapshot()))
	case CResign:
		if r.started {
			r.sim.Resign(seat)
		}
	}
}

// run is the match, start to finish.
func (r *Room) run() {
	defer close(r.done)
	defer r.hub.roomDone(r)
	ctx := r.hub.ctx
	sim, err := r.hub.eng.New(ctx)
	if err != nil {
		r.broadcast(errorFrame(ErrServer, "the table could not be laid"))
		return
	}
	defer sim.Close()
	if !sim.Begin(r.seed, r.seats[0].deck, r.seats[0].fac, r.seats[1].deck, r.seats[1].fac) {
		r.broadcast(errorFrame(ErrBadDeck, "a deck is eight different cards"))
		return
	}
	for i := range r.seats {
		if r.seats[i].bot > 0 {
			sim.AI(i, r.seats[i].bot)
		}
	}
	r.sim = sim
	cd := r.hub.cfg.Countdown
	if r.fast {
		cd = 0
	}
	for i := range r.seats {
		if c := r.seats[i].conn; c != nil {
			c.send(matchFrame(r.info(i, r.flags(), cd)))
		}
	}
	countdown := time.NewTimer(cd)
	defer countdown.Stop()
	ticker := time.NewTicker(r.hub.cfg.TickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-r.in:
			r.onMsg(m)
		case f := <-r.ctrl:
			f()
		case <-countdown.C:
			r.started = true
			if r.fast {
				for !r.tick() {
				}
				return
			}
		case <-ticker.C:
			if r.started && r.tick() {
				return
			}
		}
	}
}

// tick advances the board one step and tells everyone what happened. True when the match is over (and has been recorded).
func (r *Room) tick() bool {
	now := time.Now()
	for i := range r.seats {
		s := &r.seats[i]
		if s.bot == 0 && s.conn == nil && !s.discAt.IsZero() && now.Sub(s.discAt) > r.hub.cfg.ReconnectGrace {
			r.sim.Resign(i)
		}
	}
	before := r.sim.Tick()
	cmds, over := r.sim.Step()
	tick := r.sim.Tick()
	if tick != before {
		r.broadcast(turnFrame(tick, cmds))
		storeInt32(&r.liveTick, int32(tick))
		if tick%40 == 0 {
			r.broadcast(hashFrame(tick, r.sim.Hash()))
		}
	}
	if over {
		r.finish()
		return true
	}
	return false
}

func (r *Room) finish() {
	res := r.sim.Result()
	r.broadcast(overFrame(res))
	cmds := r.sim.Log()
	m := &Match{Mode: r.mode, Seed: r.seed, Version: r.hub.Version(), Result: res, Cmds: cmds, Counted: true}
	var names [2]string
	for i := range r.seats {
		s := r.seats[i]
		m.Seats[i] = SeatInfo{PlayerID: s.id, Name: s.name, Bot: s.bot, Deck: s.deck, Faction: s.fac, Rating: s.rating}
		names[i] = s.name
	}
	m.Replay = BuildReplay(m.Version, r.seed, [2]uint32{r.seats[0].deck, r.seats[1].deck}, [2]int{r.seats[0].fac, r.seats[1].fac}, names, res, cmds)
	ctx, cancel := context.WithTimeout(context.Background(), r.hub.cfg.SettleTimeout)
	defer cancel()
	out, err := r.hub.rec.Record(ctx, m)
	for i := range r.seats {
		c := r.seats[i].conn
		if c == nil {
			continue
		}
		if err != nil {
			c.send(recordedFrame(false, int(r.seats[i].rating+0.5), 0, 0))
			continue
		}
		after := int(out.After[i] + 0.5)
		delta := int(out.After[i] - out.Before[i])
		c.send(recordedFrame(out.Counted, after, delta, out.MatchID))
	}
	for c := range r.spectators {
		c.mu.Lock()
		c.watching = false
		c.mu.Unlock()
	}
	// the seats are free to search again
	for i := range r.seats {
		if c := r.seats[i].conn; c != nil {
			c.mu.Lock()
			if c.room == r {
				c.room = nil
			}
			c.mu.Unlock()
		}
	}
}
