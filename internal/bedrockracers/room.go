package bedrockracers

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// the names the module gives its bots, by slot (cosmetic: it only has to match what the browsers show)
var botNames = [12]string{"Ada", "Bram", "Cleo", "Dov", "Edda", "Finn", "Gwen", "Hale", "Iris", "Juno", "Kit", "Lark"}

type seatState struct {
	c       *Conn
	id      string
	name    string
	human   bool // started as a person
	present bool // still a person in the sim
	left    bool
	leftAt  int
}

type roomMsg struct {
	c     *Conn
	slot  int
	kind  byte
	cmd   [8]byte
	ticks uint32
	hash  uint32
}

// Room is one match: the referee's copy of the module, twelve seats (people and bots), and the tick stream.
type Room struct {
	hub   *Hub
	id    uint32
	seed  uint32
	level int
	in    chan roomMsg
	done  chan struct{}

	mu      sync.Mutex
	seats   [12]seatState
	pending []int // slots whose person left since the last tick

	// owned by the room's goroutine
	cmds          [12][8]byte
	stream        [][]byte
	hashes        map[uint32]uint32
	desync        map[int]bool
	humansAtStart int
	startedAt     time.Time
	tick          int
}

func newRoom(h *Hub, id, seed uint32, level int) *Room {
	return &Room{hub: h, id: id, seed: seed, level: level, in: make(chan roomMsg, 2048), done: make(chan struct{}),
		hashes: map[uint32]uint32{}, desync: map[int]bool{}}
}

// seat sits a person at a slot (before the room runs).
func (r *Room) seat(slot int, c *Conn) {
	c.mu.Lock()
	id, name := c.ident.ID, c.ident.Name
	c.mu.Unlock()
	r.mu.Lock()
	r.seats[slot] = seatState{c: c, id: id, name: name, human: true, present: true}
	r.mu.Unlock()
}

func (r *Room) humanCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.seats {
		if s.present {
			n++
		}
	}
	return n
}

// leave is called from the connection's goroutines: the person at slot is gone, a bot takes the car at the next tick.
func (r *Room) leave(c *Conn, slot int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slot < 0 || slot > 11 {
		return
	}
	s := &r.seats[slot]
	if s.c != c || !s.present {
		return
	}
	s.c = nil
	for _, p := range r.pending {
		if p == slot {
			return
		}
	}
	r.pending = append(r.pending, slot)
}

func (r *Room) broadcast(b []byte) {
	r.mu.Lock()
	var cs [12]*Conn
	for i, s := range r.seats {
		if s.present {
			cs[i] = s.c
		}
	}
	r.mu.Unlock()
	for _, c := range cs {
		if c != nil {
			c.send(b)
		}
	}
}

func (r *Room) sendTo(slot int, b []byte) {
	r.mu.Lock()
	c := r.seats[slot].c
	r.mu.Unlock()
	if c != nil {
		c.send(b)
	}
}

// takeDrops returns (ascending) the people who left since the last tick, and marks them gone.
func (r *Room) takeDrops() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var drops []int
	for _, d := range r.pending {
		if r.seats[d].present {
			r.seats[d].present = false
			r.seats[d].left = true
			r.seats[d].leftAt = r.tick
			drops = append(drops, d)
		}
	}
	r.pending = nil
	sort.Ints(drops)
	return drops
}

func (r *Room) humanMask() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := 0
	for i, s := range r.seats {
		if s.present {
			m |= 1 << uint(i)
		}
	}
	return m
}

// drain reads what the browsers sent since the last tick: the newest command per person, and their hash reports.
func (r *Room) drain() {
	for {
		select {
		case m := <-r.in:
			switch m.kind {
			case CCmd:
				r.cmds[m.slot] = m.cmd
			case CHash:
				if want, ok := r.hashes[m.ticks]; ok && want != m.hash && !r.desync[m.slot] {
					r.desync[m.slot] = true
					log.Printf("bedrockracers: room %d slot %d is out of sync at tick %d (browser %08x, referee %08x)", r.id, m.slot, m.ticks, m.hash, want)
					r.sendTo(m.slot, errorFrame(ErrDesync, "your game has drifted from the match server's"))
				}
			}
		default:
			return
		}
	}
}

func (r *Room) rosterEntries() []rosterEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []rosterEntry
	for i, s := range r.seats {
		if s.present {
			out = append(out, rosterEntry{Slot: i, Name: s.name})
		}
	}
	return out
}

// run is the room's goroutine: one TICK frame every TickEvery until the match is over or nobody is left.
func (r *Room) run() {
	defer close(r.done)
	ctx := r.hub.ctx
	sim, err := r.hub.eng.New(ctx)
	if err != nil {
		log.Printf("bedrockracers: room %d: %v", r.id, err)
		r.fail("the match server could not start the match")
		return
	}
	defer sim.Close()
	defer func() {
		if p := recover(); p != nil {
			log.Printf("bedrockracers: room %d: the module failed: %v", r.id, p)
			r.fail("the match server hit an error: the match is void")
		}
	}()
	humans := r.humanMask()
	r.humansAtStart = popcount(humans)
	r.startedAt = time.Now()
	sim.Begin(r.seed, r.level, humans)
	roster := r.rosterEntries()
	for i := 0; i < 12; i++ {
		if humans>>uint(i)&1 != 0 {
			r.sendTo(i, matchFrame(r.id, i, r.seed, r.level, humans, roster))
		}
	}
	ticker := time.NewTicker(r.hub.cfg.TickEvery)
	defer ticker.Stop()
	for r.tick = 0; ; r.tick++ {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			r.fail("the match server is restarting")
			return
		}
		r.drain()
		drops := r.takeDrops()
		for _, d := range drops {
			sim.Drop(d)
		}
		mask := r.humanMask()
		if mask == 0 {
			return // everyone left: nothing to record
		}
		var cmds [][8]byte
		for i := 0; i < 12; i++ {
			if mask>>uint(i)&1 != 0 {
				cmds = append(cmds, r.cmds[i])
				sim.SetCmd(i, r.cmds[i])
			}
		}
		sim.Tick()
		payload := tickPayload(uint32(r.tick), drops, cmds)
		r.stream = append(r.stream, payload)
		r.broadcast(append([]byte{STick}, payload...))
		if n := r.tick + 1; n%250 == 0 {
			r.hashes[uint32(n)] = sim.Hash()
		}
		if sim.Phase() == 2 {
			r.finish(sim)
			return
		}
		if r.tick+1 >= r.hub.cfg.MaxTicks {
			sim.EndNow()
			r.finish(sim)
			return
		}
	}
}

func popcount(m int) int {
	n := 0
	for ; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// fail tells whoever is still here that the match is void.
func (r *Room) fail(msg string) {
	r.broadcast(errorFrame(ErrBusy, msg))
	r.release()
}

// release frees the connections to queue again.
func (r *Room) release() {
	r.mu.Lock()
	cs := []*Conn{}
	for _, s := range r.seats {
		if s.c != nil {
			cs = append(cs, s.c)
		}
	}
	r.mu.Unlock()
	for _, c := range cs {
		c.mu.Lock()
		if c.room == r {
			c.room = nil
		}
		c.mu.Unlock()
	}
}

// replayBlob is what the replay route serves: a header, then the tick stream deflated.
//
//	'BRP1' | simVer u32 | seed u32 | level u8 | humans u16 | ticks u32 | n u32 (frames) | zlib( n x { len u16 | tick payload } )
func (r *Room) replayBlob(humans int, ticks int) []byte {
	var raw bytes.Buffer
	for _, p := range r.stream {
		var l [2]byte
		binary.LittleEndian.PutUint16(l[:], uint16(len(p)))
		raw.Write(l[:])
		raw.Write(p)
	}
	var out bytes.Buffer
	out.WriteString("BRP1")
	hd := make([]byte, 0, 19)
	hd = put32(hd, r.hub.Version())
	hd = put32(hd, r.seed)
	hd = append(hd, byte(r.level))
	hd = put16(hd, humans)
	hd = put32(hd, uint32(ticks))
	hd = put32(hd, uint32(len(r.stream)))
	out.Write(hd)
	zw := zlib.NewWriter(&out)
	_, _ = zw.Write(raw.Bytes())
	_ = zw.Close()
	return out.Bytes()
}

// finish records the match and tells everyone how it went.
func (r *Room) finish(sim *Sim) {
	res := sim.Result()
	m := &Match{Mode: "lobby", Seed: r.seed, Version: r.hub.Version(), Level: r.level, HumansAtStart: r.humansAtStart, Result: res,
		StartedAt: r.startedAt, Desyncs: len(r.desync)}
	humansAtStart := 0
	r.mu.Lock()
	for i, s := range r.seats {
		seat := Seat{Slot: i, Team: i / 3, Name: botNames[i], Kind: "bot", Stats: res.Slots[i]}
		if s.human {
			humansAtStart++
			seat.Name = s.name
			seat.PlayerID = s.id
			seat.Kind = "human"
			if s.id == "" {
				seat.Kind = "anon"
			}
			seat.Left, seat.LeftTick = s.left, s.leftAt
		}
		m.Seats[i] = seat
	}
	r.mu.Unlock()
	natural := res.Reason == ReasonGoals || res.Reason == ReasonTime || res.Reason == ReasonGolden
	m.Counted = natural && humansAtStart >= 2
	m.Replay = r.replayBlob(r.humansAtStartMask(), res.Ticks)
	ctx, cancel := context.WithTimeout(context.Background(), r.hub.cfg.SettleTimeout)
	out, err := r.hub.rec.Record(ctx, m)
	cancel()
	if err != nil {
		log.Printf("bedrockracers: room %d: recording failed: %v", r.id, err)
		out = Outcome{}
	}
	winner := res.Winner
	if winner < 0 {
		winner = 255
	}
	for i := 0; i < 12; i++ {
		so := out.Seats[i]
		r.sendTo(i, endFrame(res.Reason, winner, res.Scores, uint32(out.MatchID), out.Counted && so.Rated, float32(so.Before), float32(so.After)))
	}
	r.release()
}

func (r *Room) humansAtStartMask() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := 0
	for i, s := range r.seats {
		if s.human {
			m |= 1 << uint(i)
		}
	}
	return m
}

func (r *Room) String() string { return fmt.Sprintf("room %d", r.id) }
