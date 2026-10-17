package empire

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- test doubles --------------------------------------------------------------------------------------------------------------------------

type fakeConn struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
	c      *Conn
	signal chan struct{}
}

func newFake() *fakeConn { return &fakeConn{signal: make(chan struct{}, 4096)} }
func (f *fakeConn) Send(b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("closed")
	}
	f.frames = append(f.frames, append([]byte(nil), b...))
	select {
	case f.signal <- struct{}{}:
	default:
	}
	return nil
}
func (f *fakeConn) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }
func (f *fakeConn) all() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.frames...)
}
func (f *fakeConn) waitFor(t *testing.T, typ byte, d time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	seen := 0
	for time.Now().Before(deadline) {
		fr := f.all()
		for ; seen < len(fr); seen++ {
			if fr[seen][0] == typ {
				return fr[seen]
			}
		}
		select {
		case <-f.signal:
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("no frame of type %#x within %v", typ, d)
	return nil
}
func (f *fakeConn) count(typ byte) int {
	n := 0
	for _, fr := range f.all() {
		if fr[0] == typ {
			n++
		}
	}
	return n
}

type fakeRec struct {
	mu      sync.Mutex
	ratings map[string]float64
	matches []*Match
	botIDs  map[int]string
}

func newRec() *fakeRec { return &fakeRec{ratings: map[string]float64{}, botIDs: map[int]string{}} }
func (r *fakeRec) Rating(ctx context.Context, id string) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.ratings[id]; ok {
		return v, nil
	}
	return 1500, nil
}
func (r *fakeRec) BotPlayer(ctx context.Context, n int, name string) (Player, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := "bot-" + name
	r.botIDs[n] = id
	return Player{ID: id, Name: name, Bot: n, Rating: 1500}, nil
}
func (r *fakeRec) Record(ctx context.Context, m *Match) (Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.matches = append(r.matches, m)
	var o Outcome
	o.Counted, o.MatchID = true, int64(len(r.matches))
	for i := 0; i < 2; i++ {
		o.Before[i] = r.ratingLocked(m.Seats[i].PlayerID)
		score := 0.5
		if m.Result.Winner == i {
			score = 1
		} else if m.Result.Winner == 1-i {
			score = 0
		}
		o.After[i] = o.Before[i] + 16*(score-0.5)
	}
	for i := 0; i < 2; i++ {
		r.ratings[m.Seats[i].PlayerID] = o.After[i]
	}
	return o, nil
}
func (r *fakeRec) ratingLocked(id string) float64 {
	if v, ok := r.ratings[id]; ok {
		return v
	}
	return 1500
}
func (r *fakeRec) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.matches) }

func testCfg() Config {
	return Config{TickEvery: time.Millisecond, Countdown: 5 * time.Millisecond, HumanWindow: 150 * time.Millisecond, AuthTimeout: 2 * time.Second,
		ReconnectGrace: 400 * time.Millisecond, MaxFramesPerS: 100000, MaxRooms: 8, SettleTimeout: 2 * time.Second}
}

func resolver(ctx context.Context, tok string) (Identity, error) {
	if tok == "" || tok == "bad" {
		return Identity{}, errors.New("no")
	}
	return Identity{ID: "p-" + tok, Name: "Player " + tok, Guest: true}, nil
}

func newTestHub(t *testing.T, cfg Config) (*Hub, *fakeRec) {
	t.Helper()
	e := newTestEngine(t)
	rec := newRec()
	h := NewHub(context.Background(), cfg, e, resolver, rec)
	t.Cleanup(h.Close)
	return h, rec
}

func auth(c *Conn, tok string) {
	b := []byte{CAuth, byte(len(tok)), 0}
	c.Handle(append(b, tok...))
}

func queueFrame(version, deck uint32, fac, want byte) []byte {
	b := make([]byte, 11)
	b[0] = CQueue
	binary.LittleEndian.PutUint32(b[1:], version)
	binary.LittleEndian.PutUint32(b[5:], deck)
	b[9], b[10] = fac, want
	return b
}

// a deck of eight different cards in eight nibbles
const starterDeck = uint32(0x0) | 0<<0 | 1<<4 | 2<<8 | 4<<12 | 5<<16 | 8<<20 | 11<<24 | 13<<28

// ---- tests ---------------------------------------------------------------------------------------------------------------------------------

func TestMatchAgainstABotIsPlayedAndRecorded(t *testing.T) {
	h, rec := newTestHub(t, testCfg())
	f := newFake()
	c := h.Connect(f)
	f.c = c
	auth(c, "ann")
	ident := f.waitFor(t, GIdent, 2*time.Second)
	if ident[2] == 0 {
		t.Fatal("no name in IDENT")
	}
	c.Handle(queueFrame(h.Version(), starterDeck, 0, 2)) // want AI number 2 (Quill)
	m := f.waitFor(t, GMatch, 3*time.Second)
	if len(m) != 61 {
		t.Fatalf("match frame is %d bytes", len(m))
	}
	if m[10] != 1 || m[11] != 2 {
		t.Fatalf("opponent should be bot 2, got kind %d pers %d", m[10], m[11])
	}
	if got := string(m[12:17]); got != "Quill" {
		t.Fatalf("opponent name %q", got)
	}
	f.waitFor(t, GOver, 20*time.Second)
	rf := f.waitFor(t, GRecorded, 5*time.Second)
	if rf[1] != 1 {
		t.Fatalf("match did not count: %v", rf)
	}
	if rec.count() != 1 {
		t.Fatalf("recorded %d matches", rec.count())
	}
	mt := rec.matches[0]
	if mt.Mode != "ranked" || mt.Result.Ticks == 0 || len(mt.Replay) < 56 {
		t.Fatalf("bad record %+v", mt.Result)
	}
	if f.count(GTurn) != mt.Result.Ticks {
		t.Fatalf("a turn per tick: %d turns for %d ticks", f.count(GTurn), mt.Result.Ticks)
	}
	// the human never played, so every executed play is the AI's, and the replay holds all of them
	nc := int(binary.LittleEndian.Uint16(mt.Replay[54:]))
	if nc != len(mt.Cmds) || nc == 0 {
		t.Fatalf("replay has %d commands, match %d", nc, len(mt.Cmds))
	}
	if f.count(GHash) == 0 {
		t.Fatal("no hash frames")
	}
}

func TestAClientThatAppliesTheTurnsAgreesWithTheServer(t *testing.T) {
	h, rec := newTestHub(t, testCfg())
	f := newFake()
	c := h.Connect(f)
	auth(c, "bo")
	f.waitFor(t, GIdent, 2*time.Second)
	c.Handle(queueFrame(h.Version(), starterDeck, 1, 6))
	m := f.waitFor(t, GMatch, 3*time.Second)
	f.waitFor(t, GOver, 20*time.Second)
	f.waitFor(t, GRecorded, 5*time.Second)
	// a second board, driven only by what the wire said
	e := newTestEngine(t)
	s, _ := e.New(context.Background())
	defer s.Close()
	seed := binary.LittleEndian.Uint32(m[5:])
	d0, d1 := binary.LittleEndian.Uint32(m[32:]), binary.LittleEndian.Uint32(m[37:])
	s.Begin(seed, d0, int(m[36]), d1, int(m[41]))
	hashes := 0
	for _, fr := range f.all() {
		switch fr[0] {
		case GTurn:
			tick := int(binary.LittleEndian.Uint32(fr[1:]))
			if tick != s.Tick()+1 {
				t.Fatalf("turn for tick %d when the client is at %d", tick, s.Tick())
			}
			for i := 0; i < int(fr[5]); i++ {
				o := 6 + 4*i
				s.Play(int(fr[o]), int(fr[o+1]), int(fr[o+2]), int(fr[o+3]))
			}
			s.Step()
		case GHash:
			tick := int(binary.LittleEndian.Uint32(fr[1:]))
			if tick != s.Tick() {
				t.Fatalf("hash for tick %d at %d", tick, s.Tick())
			}
			if got := binary.LittleEndian.Uint32(fr[5:]); got != s.Hash() {
				t.Fatalf("client board diverged at tick %d: %d vs %d", tick, got, s.Hash())
			}
			hashes++
		}
	}
	if hashes < 5 {
		t.Fatalf("only %d hash checks", hashes)
	}
	if s.Winner() != rec.matches[0].Result.Winner {
		t.Fatalf("client thinks %d won, server says %d", s.Winner(), rec.matches[0].Result.Winner)
	}
}

func TestTwoPeopleAreSeatedTogether(t *testing.T) {
	h, rec := newTestHub(t, testCfg())
	fa, fb := newFake(), newFake()
	a, b := h.Connect(fa), h.Connect(fb)
	auth(a, "ann")
	auth(b, "bea")
	fa.waitFor(t, GIdent, 2*time.Second)
	fb.waitFor(t, GIdent, 2*time.Second)
	a.Handle(queueFrame(h.Version(), starterDeck, 0, 0))
	b.Handle(queueFrame(h.Version(), starterDeck, 2, 0))
	ma := fa.waitFor(t, GMatch, 3*time.Second)
	mb := fb.waitFor(t, GMatch, 3*time.Second)
	if ma[10] != 0 || mb[10] != 0 {
		t.Fatal("both should be people")
	}
	if ma[9] == mb[9] {
		t.Fatalf("both seated at %d", ma[9])
	}
	if binary.LittleEndian.Uint32(ma[1:]) != binary.LittleEndian.Uint32(mb[1:]) {
		t.Fatal("different matches")
	}
	// one resigns once the match is running; the other is told and the match is recorded as a win for them
	fa.waitFor(t, GTurn, 3*time.Second)
	if ma[9] == 0 {
		a.Handle([]byte{CResign})
	} else {
		b.Handle([]byte{CResign})
	}
	fa.waitFor(t, GOver, 5*time.Second)
	fb.waitFor(t, GOver, 5*time.Second)
	fa.waitFor(t, GRecorded, 5*time.Second)
	if rec.count() != 1 || rec.matches[0].Result.Reason != 6 {
		t.Fatalf("expected one resigned match, got %d", rec.count())
	}
}

func TestALonePersonGetsABotAfterTheWindow(t *testing.T) {
	h, _ := newTestHub(t, testCfg())
	f := newFake()
	c := h.Connect(f)
	auth(c, "cy")
	f.waitFor(t, GIdent, 2*time.Second)
	c.Handle(queueFrame(h.Version(), starterDeck, 0, 0))
	f.waitFor(t, GQueued, time.Second)
	start := time.Now()
	m := f.waitFor(t, GMatch, 3*time.Second)
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("the bot sat down before the window")
	}
	if m[10] != 1 {
		t.Fatal("expected a bot")
	}
}

func TestRefusals(t *testing.T) {
	h, _ := newTestHub(t, testCfg())
	f0 := newFake()
	c0 := h.Connect(f0)
	auth(c0, "bad")
	if e := f0.waitFor(t, GError, 2*time.Second); e[1] != ErrAuth {
		t.Fatalf("code %d", e[1])
	}
	f := newFake()
	c := h.Connect(f)
	auth(c, "dee")
	f.waitFor(t, GIdent, 2*time.Second)
	c.Handle(queueFrame(h.Version()+1, starterDeck, 0, 0))
	if e := f.waitFor(t, GError, 2*time.Second); e[1] != ErrVersion {
		t.Fatalf("want version error, got %d", e[1])
	}
	f2 := newFake()
	c2 := h.Connect(f2)
	auth(c2, "gus")
	f2.waitFor(t, GIdent, 2*time.Second)
	c2.Handle(queueFrame(h.Version(), 0x11111111, 0, 0)) // eight copies of one card
	if e := f2.waitFor(t, GError, 2*time.Second); e[1] != ErrBadDeck {
		t.Fatalf("a deck of one card: code %d", e[1])
	}
}

func TestADroppedPlayerCanComeBack(t *testing.T) {
	cfg := testCfg()
	cfg.TickEvery = 4 * time.Millisecond
	cfg.ReconnectGrace = 3 * time.Second
	h, _ := newTestHub(t, cfg)
	f := newFake()
	c := h.Connect(f)
	auth(c, "eli")
	f.waitFor(t, GIdent, 2*time.Second)
	c.Handle(queueFrame(h.Version(), starterDeck, 0, 4))
	f.waitFor(t, GMatch, 3*time.Second)
	f.waitFor(t, GTurn, 3*time.Second)
	c.Close()
	time.Sleep(60 * time.Millisecond)
	f2 := newFake()
	c2 := h.Connect(f2)
	auth(c2, "eli")
	f2.waitFor(t, GIdent, 2*time.Second)
	m := f2.waitFor(t, GMatch, 3*time.Second)
	if m[10] != 1 {
		t.Fatal("not the same match")
	}
	snap := f2.waitFor(t, GSnapshot, 3*time.Second)
	if int(binary.LittleEndian.Uint16(snap[5:])) != 12288 {
		t.Fatal("short snapshot")
	}
	f2.waitFor(t, GTurn, 3*time.Second)
}

func TestSpectatorsSeeTheLeague(t *testing.T) {
	cfg := testCfg()
	cfg.TickEvery = 3 * time.Millisecond
	h, _ := newTestHub(t, cfg)
	if !h.LeagueNow() {
		t.Fatal("no league match started")
	}
	var live []LiveRoom
	for i := 0; i < 100 && len(live) == 0; i++ {
		live = h.Live()
		time.Sleep(10 * time.Millisecond)
	}
	if len(live) != 1 || live[0].Mode != "league" || live[0].Bots[0] == 0 || live[0].Bots[1] == 0 {
		t.Fatalf("live: %+v", live)
	}
	f := newFake()
	c := h.Connect(f)
	auth(c, "fay")
	f.waitFor(t, GIdent, 2*time.Second)
	id := make([]byte, 5)
	id[0] = CWatch
	binary.LittleEndian.PutUint32(id[1:], live[0].ID)
	c.Handle(id)
	m := f.waitFor(t, GMatch, 3*time.Second)
	if m[42]&FlagSpectator == 0 {
		t.Fatal("spectator flag missing")
	}
	f.waitFor(t, GSnapshot, 3*time.Second)
	f.waitFor(t, GTurn, 3*time.Second)
}

func TestLeagueMatchIsRecordedWithoutAnyone(t *testing.T) {
	cfg := testCfg()
	cfg.LeagueFast = true
	h, rec := newTestHub(t, cfg)
	if !h.LeagueNow() {
		t.Fatal("no league match")
	}
	for i := 0; i < 200 && rec.count() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if rec.count() != 1 || rec.matches[0].Mode != "league" || rec.matches[0].Seats[0].Bot == 0 {
		t.Fatalf("league record: %d", rec.count())
	}
}
