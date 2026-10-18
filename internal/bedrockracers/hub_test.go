package bedrockracers

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- test doubles -------------------------------------------------------------------------------------------------------------------------------

type fakeT struct {
	frames chan []byte
	once   sync.Once
	closed chan struct{}
}

func newFakeT() *fakeT { return &fakeT{frames: make(chan []byte, 8192), closed: make(chan struct{})} }
func (f *fakeT) Send(b []byte) error {
	select {
	case f.frames <- append([]byte(nil), b...):
	default:
	}
	return nil
}
func (f *fakeT) Close() { f.once.Do(func() { close(f.closed) }) }

// next waits for a frame of this type, discarding others (STATUS and PONG chatter, TICK frames when asked for something else).
func (f *fakeT) next(t *testing.T, typ byte, within time.Duration) []byte {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case b := <-f.frames:
			if b[0] == typ {
				return b
			}
		case <-deadline:
			t.Fatalf("no frame of type %#x within %v", typ, within)
		}
	}
}

func (f *fakeT) hasClosed() bool {
	select {
	case <-f.closed:
		return true
	default:
		return false
	}
}

type fakeRec struct {
	mu      sync.Mutex
	matches []*Match
	ratings map[string]float64
}

func (r *fakeRec) Rating(ctx context.Context, id string) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.ratings[id]; ok {
		return v, nil
	}
	return 1500, nil
}
func (r *fakeRec) Record(ctx context.Context, m *Match) (Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.matches = append(r.matches, m)
	var out Outcome
	out.MatchID = int64(len(r.matches)) + 4000
	out.Counted = m.Counted
	for i, s := range m.Seats {
		if s.PlayerID != "" && m.Counted {
			out.Seats[i] = SeatOutcome{Before: 1500, After: 1512.5, Rated: true}
		}
	}
	return out, nil
}
func (r *fakeRec) last() *Match {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.matches) == 0 {
		return nil
	}
	return r.matches[len(r.matches)-1]
}

func resolver(ctx context.Context, token string) (Identity, error) {
	switch token {
	case "ann", "bob", "cat":
		return Identity{ID: "p-" + token, Name: "Player " + token}, nil
	case "guest":
		return Identity{ID: "g-1", Name: "Guesty", Guest: true}, nil
	}
	return Identity{}, errors.New("no such token")
}

func testHub(t *testing.T, mod func(*Config)) (*Hub, *fakeRec) {
	t.Helper()
	eng := newEngine(t)
	cfg := DefaultConfig()
	cfg.TickEvery = 300 * time.Microsecond
	cfg.LobbyWindow = 400 * time.Millisecond
	cfg.AuthTimeout = 2 * time.Second
	cfg.StatusEvery = 100 * time.Millisecond
	if mod != nil {
		mod(&cfg)
	}
	rec := &fakeRec{}
	h := NewHub(context.Background(), cfg, eng, resolver, rec)
	t.Cleanup(h.Close)
	return h, rec
}

func helloFrame(simVer uint32, token, name string) []byte {
	b := []byte{CHello, Version}
	b = put32(b, simVer)
	b = put16(b, len(token))
	b = append(b, token...)
	b = append(b, byte(len(name)))
	return append(b, name...)
}

func queueFrame(team, level int) []byte { return []byte{CQueue, byte(team), byte(level)} }

func cmdFrame(c [8]byte) []byte { return append([]byte{CCmd}, c[:]...) }

type client struct {
	c *Conn
	t *fakeT
}

func connect(t *testing.T, h *Hub, token, name string) client {
	t.Helper()
	ft := newFakeT()
	c := h.Connect(ft)
	c.Handle(helloFrame(h.Version(), token, name))
	return client{c, ft}
}

// ---- the tests ------------------------------------------------------------------------------------------------------------------------------------

func TestHelloWelcomeAndVersionGate(t *testing.T) {
	h, _ := testHub(t, nil)
	a := connect(t, h, "ann", "ignored")
	w := a.t.next(t, SWelcome, time.Second)
	if w[1] != Version || w[2] != 1 || binary.LittleEndian.Uint32(w[3:]) != h.Version() {
		t.Fatalf("welcome %v", w)
	}
	nl := int(w[7])
	if string(w[8:8+nl]) != "Player ann" {
		t.Fatalf("the name is the account's, not the one the browser typed: %q", w[8:8+nl])
	}
	g := connect(t, h, "", "Wanderer")
	if w := g.t.next(t, SWelcome, time.Second); w[2] != 0 {
		t.Fatalf("no token must be a guest, got auth %d", w[2])
	}
	bad := connect(t, h, "nonsense", "Eve")
	if w := bad.t.next(t, SWelcome, time.Second); w[2] != 2 {
		t.Fatalf("a rejected token is auth 2, got %d", w[2])
	}
	old := newFakeT()
	c := h.Connect(old)
	c.Handle(helloFrame(h.Version()+1, "ann", "x"))
	e := old.next(t, SError, time.Second)
	if e[1] != ErrVersion {
		t.Fatalf("a build on other rules must get ERROR %d, got %d", ErrVersion, e[1])
	}
	time.Sleep(50 * time.Millisecond)
	if !old.hasClosed() {
		t.Fatal("and be hung up on")
	}
	// anything before HELLO is refused
	raw := newFakeT()
	rc := h.Connect(raw)
	rc.Handle(queueFrame(0, 2))
	if e := raw.next(t, SError, time.Second); e[1] != ErrBadFrame {
		t.Fatalf("queueing before HELLO: %v", e)
	}
}

func TestOneConnectionPerPlayer(t *testing.T) {
	h, _ := testHub(t, nil)
	a1 := connect(t, h, "ann", "")
	a1.t.next(t, SWelcome, time.Second)
	a2 := connect(t, h, "ann", "")
	a2.t.next(t, SWelcome, time.Second)
	if e := a1.t.next(t, SError, time.Second); e[1] != ErrTaken {
		t.Fatalf("the first connection must be told it was taken over: %v", e)
	}
}

func TestSeatsFollowTheTeamsPeopleAskFor(t *testing.T) {
	mk := func(team int) *queued { return &queued{c: &Conn{}, team: team} }
	a, b, c, d, e := mk(2), mk(2), mk(2), mk(2), mk(-1)
	got := assignSeats([]*queued{a, b, c, d, e})
	if got[a] != 6 || got[b] != 7 || got[c] != 8 {
		t.Fatalf("three people on team 2 sit in slots 6,7,8: got %d %d %d", got[a], got[b], got[c])
	}
	if got[d]/3 == 2 {
		t.Fatalf("a fourth person cannot join a full team: slot %d", got[d])
	}
	seen := map[int]bool{}
	for _, s := range got {
		if seen[s] {
			t.Fatalf("two people in slot %d", s)
		}
		seen[s] = true
	}
	if got[e] == got[d] {
		t.Fatal("slots clash")
	}
}

// two people searching become a lobby, then a match: MATCH with their slots, TICK frames carrying their commands, a drop when one leaves.
func TestLobbyMatchTicksAndDrop(t *testing.T) {
	h, _ := testHub(t, nil)
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(0, 2))
	st := a.t.next(t, SStatus, time.Second)
	if st[1] != QSearching {
		t.Fatalf("a lone searcher is searching: qstate %d", st[1])
	}
	b.c.Handle(queueFrame(3, 2))
	lob := a.t.next(t, SStatus, 2*time.Second)
	for lob[1] != QLobby {
		lob = a.t.next(t, SStatus, 2*time.Second)
	}
	if lob[7] != 2 {
		t.Fatalf("the lobby has 2 people, STATUS says %d", lob[7])
	}
	ma := a.t.next(t, SMatch, 3*time.Second)
	mb := b.t.next(t, SMatch, 3*time.Second)
	if ma[5] != 0 || mb[5] != 9 {
		t.Fatalf("ann asked for team 0 (slot 0), bob for team 3 (slot 9): got %d and %d", ma[5], mb[5])
	}
	seed := binary.LittleEndian.Uint32(ma[6:])
	if seed != binary.LittleEndian.Uint32(mb[6:]) || ma[10] != 2 {
		t.Fatal("both get the same seed and level")
	}
	humans := int(binary.LittleEndian.Uint16(ma[11:]))
	if humans != 1<<0|1<<9 || ma[13] != 2 {
		t.Fatalf("humans mask %b roster %d", humans, ma[13])
	}
	// ticks arrive in order, two commands each, in ascending slot order
	first := a.t.next(t, STick, time.Second)
	if binary.LittleEndian.Uint32(first[1:]) != 0 || first[5] != 0 || len(first) != 1+4+1+16 {
		t.Fatalf("tick 0: %v", first)
	}
	a.c.Handle(cmdFrame([8]byte{127, 0, 0, 0, 0, 0, 5, 0}))
	b.c.Handle(cmdFrame([8]byte{1, 2, 3, 4, 5, 6, 7, 0}))
	var seenA, seenB bool
	prev := uint32(0)
	for i := 0; i < 400 && !(seenA && seenB); i++ {
		f := a.t.next(t, STick, time.Second)
		n := binary.LittleEndian.Uint32(f[1:])
		if i > 0 && n != prev+1 {
			t.Fatalf("ticks must be consecutive: %d after %d", n, prev)
		}
		prev = n
		c0 := f[6 : 6+8]
		c1 := f[6+8 : 6+16]
		if c0[0] == 127 && c0[6] == 5 {
			seenA = true
		}
		if c1[0] == 1 && c1[5] == 6 && c1[6] == 7 {
			seenB = true
		}
	}
	if !seenA || !seenB {
		t.Fatalf("both people's commands must reach the stream (ann %v, bob %v)", seenA, seenB)
	}
	// bob's connection dies: a bot takes slot 9, and ann's stream shows the drop once, then one command per tick
	b.c.Close()
	var dropTick uint32
	for i := 0; i < 2000; i++ {
		f := a.t.next(t, STick, time.Second)
		if f[5]&1 != 0 {
			if f[6] != 1 || f[7] != 9 {
				t.Fatalf("drop list %v", f[5:9])
			}
			if len(f) != 1+4+1+2+8 {
				t.Fatalf("after the drop the tick carries one command: len %d", len(f))
			}
			dropTick = binary.LittleEndian.Uint32(f[1:])
			break
		}
	}
	if dropTick == 0 {
		t.Fatal("no drop reached ann")
	}
	g := a.t.next(t, STick, time.Second)
	if g[5] != 0 || len(g) != 1+4+1+8 {
		t.Fatalf("one command per tick once bob is a bot: %v", g)
	}
}

func TestLobbyDisbandsWhenAPersonLeaves(t *testing.T) {
	h, _ := testHub(t, func(c *Config) { c.LobbyWindow = 3 * time.Second })
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(255, 2))
	b.c.Handle(queueFrame(255, 2))
	st := a.t.next(t, SStatus, 2*time.Second)
	for st[1] != QLobby {
		st = a.t.next(t, SStatus, 2*time.Second)
	}
	b.c.Handle([]byte{CLeave})
	st = a.t.next(t, SStatus, 3*time.Second)
	for st[1] != QSearching {
		st = a.t.next(t, SStatus, 3*time.Second)
	}
	if h.Stats().Rooms != 0 {
		t.Fatal("no room should open for one person")
	}
}

func TestHashDesyncIsReported(t *testing.T) {
	h, _ := testHub(t, nil)
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(255, 2))
	b.c.Handle(queueFrame(255, 2))
	a.t.next(t, SMatch, 3*time.Second)
	// wait until the referee has its hash for tick 250, then report a wrong one
	for {
		f := a.t.next(t, STick, time.Second)
		if binary.LittleEndian.Uint32(f[1:]) > 260 {
			break
		}
	}
	rep := []byte{CHash}
	rep = put32(rep, 250)
	rep = put32(rep, 0xDEADBEEF)
	a.c.Handle(rep)
	if e := a.t.next(t, SError, 2*time.Second); e[1] != ErrDesync {
		t.Fatalf("a wrong hash must be answered with ERROR %d: %v", ErrDesync, e)
	}
}

// a whole match, played out by the clock: it is recorded once, as counted, with the people in their seats and the module's own counts, and
// both browsers are told the outcome.
func TestMatchPlaysOutAndIsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("plays a four-minute match as fast as the referee can step it")
	}
	h, rec := testHub(t, func(c *Config) { c.TickEvery = 40 * time.Microsecond })
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(1, 1))
	b.c.Handle(queueFrame(2, 1))
	a.t.next(t, SMatch, 3*time.Second)
	b.t.next(t, SMatch, 3*time.Second)
	done := make(chan struct{})
	bEnd := make(chan []byte, 1)
	go func() { // keep both players' commands moving, and read b's frames (its END is kept) so its writer never backs up
		for {
			select {
			case <-done:
				return
			case f := <-b.t.frames:
				if f[0] == SEnd {
					select {
					case bEnd <- f:
					default:
					}
				}
			case <-time.After(5 * time.Millisecond):
				a.c.Handle(cmdFrame([8]byte{127, 20, 0, 0, 0, 0, 1, 0}))
				b.c.Handle(cmdFrame([8]byte{127, 236, 0, 0, 0, 0, 1, 0}))
			}
		}
	}()
	defer close(done)
	end := a.t.next(t, SEnd, 120*time.Second)
	m := rec.last()
	if m == nil {
		t.Fatal("the match was not recorded")
	}
	if m.Result.Phase != 2 || m.Result.Ticks < 14000 {
		t.Fatalf("a four-minute match ends after ~15000 ticks of play: ticks %d phase %d", m.Result.Ticks, m.Result.Phase)
	}
	if !m.Counted || m.HumansAtStart != 2 {
		t.Fatalf("two people, played out: counted %v humans %d", m.Counted, m.HumansAtStart)
	}
	if m.Seats[3].PlayerID != "p-ann" || m.Seats[3].Kind != "human" || m.Seats[6].PlayerID != "p-bob" || m.Seats[0].Kind != "bot" || m.Seats[0].Name != "Ada" {
		t.Fatalf("seats: %+v / %+v / %+v", m.Seats[3], m.Seats[6], m.Seats[0])
	}
	if len(m.Replay) < 40 || string(m.Replay[:4]) != "BRP1" {
		t.Fatalf("replay blob: %d bytes", len(m.Replay))
	}
	if end[1] != byte(m.Result.Reason) || end[7+0] == 0 && end[7+1] == 0 && end[7+2] == 0 && end[7+3] == 0 && m.Result.Scores != [4]int{} {
		t.Logf("end frame %v", end)
	}
	if binary.LittleEndian.Uint32(end[7:]) != 4001 || end[11] != 1 {
		t.Fatalf("END carries the recorded match id and the counted flag: %v", end)
	}
	if got := binary.LittleEndian.Uint32(end[16:]); got == 0 {
		t.Fatalf("END carries the new rating: %v", end)
	}
	select {
	case <-bEnd:
	case <-time.After(5 * time.Second):
		t.Fatal("the second person was not told how it ended")
	}
	time.Sleep(100 * time.Millisecond)
	if s := h.Stats(); s.Rooms != 0 || s.Playing != 0 {
		t.Fatalf("the room is gone after the match: %+v", s)
	}
}

// a match nobody is left in is never recorded
func TestEveryoneLeavingVoidsTheMatch(t *testing.T) {
	h, rec := testHub(t, nil)
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(255, 2))
	b.c.Handle(queueFrame(255, 2))
	a.t.next(t, SMatch, 3*time.Second)
	b.t.next(t, SMatch, 3*time.Second)
	a.c.Close()
	b.c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for h.Stats().Rooms != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.Stats().Rooms != 0 {
		t.Fatal("an empty room must close")
	}
	if rec.last() != nil {
		t.Fatal("and nothing is recorded")
	}
}

// the hard cap: a match that will not end is ended by decree, reason 4, and does not move the ladder
func TestAMatchThatWillNotEndIsClosed(t *testing.T) {
	h, rec := testHub(t, func(c *Config) { c.MaxTicks = 900 })
	a := connect(t, h, "ann", "")
	b := connect(t, h, "bob", "")
	a.t.next(t, SWelcome, time.Second)
	b.t.next(t, SWelcome, time.Second)
	a.c.Handle(queueFrame(255, 2))
	b.c.Handle(queueFrame(255, 2))
	end := a.t.next(t, SEnd, 10*time.Second)
	if end[1] != ReasonClosed {
		t.Fatalf("closed by decree is reason %d: %v", ReasonClosed, end)
	}
	m := rec.last()
	if m == nil || m.Counted {
		t.Fatalf("a match closed by decree is recorded but not counted: %+v", m)
	}
}

func TestStatsCountPeople(t *testing.T) {
	h, _ := testHub(t, func(c *Config) { c.LobbyWindow = 2 * time.Second })
	a := connect(t, h, "ann", "")
	a.t.next(t, SWelcome, time.Second)
	s := h.Stats()
	if s.Online != 1 || s.Searching != 0 || s.Rooms != 0 {
		t.Fatalf("%+v", s)
	}
	a.c.Handle(queueFrame(255, 2))
	time.Sleep(50 * time.Millisecond)
	if s := h.Stats(); s.Searching != 1 {
		t.Fatalf("%+v", s)
	}
}
