package d2play

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---- test doubles ---------------------------------------------------------------------------------------------------

type fakeTransport struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
	cond   *sync.Cond
}

func newFakeTransport() *fakeTransport {
	f := &fakeTransport{}
	f.cond = sync.NewCond(&f.mu)
	return f
}
func (f *fakeTransport) Send(b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, append([]byte(nil), b...))
	f.cond.Broadcast()
	return nil
}
func (f *fakeTransport) Close() {
	f.mu.Lock()
	f.closed = true
	f.cond.Broadcast()
	f.mu.Unlock()
}

// next waits for the first not yet consumed frame of type typ (or any if typ == 0).
func (f *fakeTransport) next(t *testing.T, typ byte, d time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(d)
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		for i, fr := range f.frames {
			if typ == 0 || fr[0] == typ {
				f.frames = append(f.frames[:i], f.frames[i+1:]...)
				return fr
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no frame 0x%02x within %v (have %d other frames)", typ, d, len(f.frames))
		}
		go func() { time.Sleep(50 * time.Millisecond); f.cond.Broadcast() }()
		f.cond.Wait()
	}
}

type memRecorder struct {
	mu   sync.Mutex
	recs []Record
	fail bool
}

func (m *memRecorder) Record(ctx context.Context, r Record) (Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return Outcome{}, errors.New("db down")
	}
	m.recs = append(m.recs, r)
	o := Outcome{Rated: r.Counted && !r.SkipStats, RatingBefore: 1500, RatingAfter: 1516}
	if r.Seat == 1 {
		o.Seat[1].Before, o.Seat[1].After = 1500, 1516
	} else {
		o.Seat[0].Before, o.Seat[0].After = 1500, 1516
	}
	return o, nil
}
func (m *memRecorder) all() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.recs...)
}

func okResolver(ctx context.Context, token string) (Identity, error) {
	switch token {
	case "bad":
		return Identity{}, errors.New("nope")
	case "":
		return Identity{}, errors.New("empty")
	}
	return Identity{ID: "p-" + token, Name: "Player " + token, Guest: true}, nil
}

func authFrame(tok string) []byte {
	b := []byte{CAuth, 0, 0}
	binary.LittleEndian.PutUint16(b[1:], uint16(len(tok)))
	return append(b, tok...)
}

// ---- unit tests (no match server) ---------------------------------------------------------------------------------------

func TestSplitFrame(t *testing.T) {
	welcome := withLen([]byte{SWelcome, 1, 0, 0, 0, 0})
	f, used, err := splitFrame(append(welcome, 0x05))
	if err != nil || used != len(welcome) || f[0] != SWelcome {
		t.Fatalf("splitFrame = %v %d %v", f, used, err)
	}
	if f, used, err := splitFrame(welcome[:3]); f != nil || used != 0 || err != nil {
		t.Fatalf("a partial frame must wait for more bytes, got %v %d %v", f, used, err)
	}
	if _, _, err := splitFrame([]byte{3, 0, SWelcome, 1, 2}); err != ErrFrame {
		t.Fatalf("wrong payload size must be an error, got %v", err)
	}
	if _, _, err := splitFrame([]byte{0, 0}); err != ErrFrame {
		t.Fatalf("empty frame must be an error, got %v", err)
	}
	if _, _, err := splitFrame([]byte{2, 0, 0x55, 0}); err != ErrFrame {
		t.Fatalf("unknown type must be an error, got %v", err)
	}
}

// EVENTS is the one server frame whose size depends on what is in it: [tick][n][dropped][structure x2 u16] then n x 7 bytes
func TestSplitFrameEvents(t *testing.T) {
	ev := []byte{SEvents, 7, 2, 0, 0x2D, 1, 0xFF, 0}
	ev = append(ev, 1, 0, 10, 3, 0, 0, 0, 2, 1, 20, 30, 15, 1, 3)
	f, used, err := splitFrame(withLen(ev))
	if err != nil || used != 2+len(ev) || f[0] != SEvents || f[2] != 2 {
		t.Fatalf("a two-event frame = %v %d %v", f, used, err)
	}
	empty := withLen([]byte{SEvents, 3, 0, 0, 0, 0, 0, 0})
	if _, _, err := splitFrame(empty); err != nil {
		t.Fatalf("a second with nothing in it is a valid frame: %v", err)
	}
	bad := append([]byte{}, ev...)
	bad[2] = 3 // claims three events, carries two
	if _, _, err := splitFrame(withLen(bad)); err != ErrFrame {
		t.Fatalf("a count that disagrees with the size must be an error, got %v", err)
	}
	huge := append([]byte{SEvents, 1, 61, 0, 0, 0, 0, 0}, make([]byte, 61*7)...)
	if _, _, err := splitFrame(withLen(huge)); err != ErrFrame {
		t.Fatalf("more than %d events must be an error, got %v", MaxEvents, err)
	}
	if _, _, err := splitFrame(withLen([]byte{SEvents, 1, 0})); err != ErrFrame {
		t.Fatalf("a truncated header must be an error, got %v", err)
	}
	max := append([]byte{SEvents, 1, MaxEvents, 0, 0, 0, 0, 0}, make([]byte, MaxEvents*7)...)
	if _, _, err := splitFrame(withLen(max)); err != nil {
		t.Fatalf("a full frame must fit (%d bytes): %v", len(max), err)
	}
}

func TestHelloFrameIsASanitisedHuman(t *testing.T) {
	f := helloFrame("Wraith \"Q\"\\ é long long name")
	if f[0] != CHello || f[1] != 1 || f[2] != 0 {
		t.Fatalf("hello header = %v", f[:3])
	}
	if len(f) != 1+1+1+16+1 || f[len(f)-1] != 0 {
		t.Fatalf("hello size = %d", len(f))
	}
	name := cstr(f[3:19])
	if len(name) != 16 {
		t.Fatalf("name = %q", name)
	}
	for _, c := range []byte(name) {
		if c < 32 || c > 126 || c == '"' || c == '\\' {
			t.Fatalf("unsafe byte %d in %q", c, name)
		}
	}
}

func TestMustSignInFirstAndBadTokenIsRefused(t *testing.T) {
	h, _ := NewHub(DefaultConfig(), okResolver, &memRecorder{})
	tr := newFakeTransport()
	c := h.Connect(tr)
	defer c.Close()
	c.Handle([]byte{CQueue})
	if e := tr.next(t, GError, time.Second); e[1] != ErrBadHello {
		t.Fatalf("a frame before the token must be refused, got %v", e)
	}
	c.Handle(authFrame("bad"))
	if e := tr.next(t, GError, time.Second); e[1] != ErrBadHello {
		t.Fatalf("a bad token must be refused, got %v", e)
	}
	c.Handle([]byte{CAuth, 9, 0, 'x'}) // a length that does not fit the frame
	if e := tr.next(t, GError, time.Second); e[1] != ErrBadHello {
		t.Fatalf("a token longer than the frame must be refused, got %v", e)
	}
}

func TestServerDownIsSaid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ServerAddr = "127.0.0.1:1" // nothing listens
	cfg.DialTimeout = 500 * time.Millisecond
	h, _ := NewHub(cfg, okResolver, &memRecorder{})
	tr := newFakeTransport()
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(authFrame("a"))
	if e := tr.next(t, GError, 2*time.Second); e[1] != ErrServerDown {
		t.Fatalf("an unreachable match server must be said, got %v", e)
	}
}

// fakeServer is a hand-rolled dw2_server end: it accepts one connection, reads frames, and lets the test push frames back.
type fakeServer struct {
	ln   net.Listener
	conn chan net.Conn
}

func newFakeServer(t *testing.T) *fakeServer {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, conn: make(chan net.Conn, 4)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.conn <- c
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func readClientFrame(t *testing.T, c net.Conn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var l [2]byte
	if _, err := readFull(c, l[:]); err != nil {
		t.Fatalf("read length: %v", err)
	}
	b := make([]byte, binary.LittleEndian.Uint16(l[:]))
	if _, err := readFull(c, b); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return b
}
func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func matchFound(id, seed uint32, seat byte, opp string, kind byte) []byte {
	f := make([]byte, 29)
	f[0] = SMatchFound
	binary.LittleEndian.PutUint32(f[1:], id)
	binary.LittleEndian.PutUint32(f[5:], seed)
	f[9] = seat
	copy(f[10:26], opp)
	f[26] = kind
	binary.LittleEndian.PutUint16(f[27:], 60000)
	return f
}

func TestOnlyPlayerOrdersAreRelayedAndTheMatchIsTracked(t *testing.T) {
	srv := newFakeServer(t)
	cfg := DefaultConfig()
	cfg.ServerAddr = srv.ln.Addr().String()
	rec := &memRecorder{}
	h, _ := NewHub(cfg, okResolver, rec)
	tr := newFakeTransport()
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(authFrame("ada"))
	id := tr.next(t, GIdent, time.Second)
	if id[1] != 1 || string(id[3:3+int(id[2])]) != "Player ada" {
		t.Fatalf("GIdent = %q", id)
	}
	sc := <-srv.conn
	hello := readClientFrame(t, sc)
	if hello[0] != CHello || cstr(hello[3:19]) != "Player ada" {
		t.Fatalf("the server must be introduced to the player by name, got %v", hello)
	}
	// orders that are relayed, and ones that are not
	c.Handle([]byte{CQueue})
	if f := readClientFrame(t, sc); f[0] != CQueue {
		t.Fatalf("queue not relayed: %v", f)
	}
	c.Handle([]byte{CHello, 1, 1, 'x'})  // a second HELLO (would let the player pose as a bot)
	c.Handle([]byte{CPlace, 1, 2})       // wrong size
	c.Handle([]byte{0x7E, 0, 0, 0})      // unknown
	c.Handle([]byte{CPlace, 0, 0, 0, 0}) // the real thing
	if f := readClientFrame(t, sc); f[0] != CPlace {
		t.Fatalf("a malformed or foreign frame reached the match server: %v", f)
	}
	// the server finds a match; the player places one item (acked), one is rejected, then it ends in a win
	sc.Write(withLen(matchFound(7, 99, 1, "Ada Bot", 1)))
	tr.next(t, SMatchFound, time.Second)
	c.Handle([]byte{CPlace, 3, 0, 1, 0})
	readClientFrame(t, sc)
	c.Handle([]byte{CPlace, 3, 0, 1, 0})
	readClientFrame(t, sc)
	sc.Write(withLen([]byte{SPlaceAck, 0}))
	sc.Write(withLen([]byte{SPlaceReject, 2}))
	c.Handle([]byte{CPanicCut, 0})
	readClientFrame(t, sc)
	sc.Write(withLen([]byte{SCutAck, 0}))
	sc.Write(withLen([]byte{SCombatStart, 100, 100, 20, 0, 20, 0, 0x2C, 1, 0xC8, 0}))
	sc.Write(withLen([]byte{SRoundBreak, 1, 0, 0x66, 0x02, 0xB0, 0x04}))
	time.Sleep(30 * time.Millisecond)
	c.Handle([]byte{CRoundCall, 0})
	readClientFrame(t, sc)
	sc.Write(withLen([]byte{SRoundResult, 0, 3, 75, 1, 2, 0}))
	sc.Write(withLen([]byte{STick, 7, 80, 40, 20, 0, 0, 0, 0, 0}[:10]))
	end := []byte{SMatchEnd, 7, 0, 0, 0, ResWin, EndHull, 12}
	sc.Write(withLen(end))
	tr.next(t, SMatchEnd, time.Second)
	rg := tr.next(t, GRecorded, 2*time.Second)
	if rg[1] != 1 || rg[2] != 1 {
		t.Fatalf("GRecorded = %v, want counted and against a bot", rg)
	}
	if got := int16(binary.LittleEndian.Uint16(rg[3:])); got != 1516 {
		t.Fatalf("rating after = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(rg[5:])); got != 16 {
		t.Fatalf("delta = %d", got)
	}
	rs := rec.all()
	if len(rs) != 1 {
		t.Fatalf("records = %d", len(rs))
	}
	r := rs[0]
	if r.Result != "win" || r.Reason != "hull" || r.Winner != 1 || r.Seat != 1 || r.OppKind != "bot" || r.OppName != "Ada Bot" || !r.Counted ||
		r.Ticks != 12 || r.HullYou != 80 || r.HullOpp != 40 || r.CargoYou != 300 || r.CargoOpp != 200 || r.MatchID != 7 || r.Seed != 99 {
		t.Fatalf("record = %+v", r)
	}
	if len(r.Loadout) != 1 || !r.Loadout[0].Cut || r.Loadout[0].Item != 3 {
		t.Fatalf("loadout (one accepted, one rejected, then cut) = %+v", r.Loadout)
	}
	if len(r.Rounds) != 1 || r.Rounds[0].YourGrade != 3 || r.Rounds[0].YourFx != 75 || r.Rounds[0].OppCall != 1 || r.Rounds[0].CallMS < 0 || r.Rounds[0].TargetMS != 0x266 {
		t.Fatalf("rounds = %+v", r.Rounds)
	}
}

func TestLeavingALiveMatchIsAForfeitLoss(t *testing.T) {
	srv := newFakeServer(t)
	cfg := DefaultConfig()
	cfg.ServerAddr = srv.ln.Addr().String()
	rec := &memRecorder{}
	h, _ := NewHub(cfg, okResolver, rec)
	tr := newFakeTransport()
	c := h.Connect(tr)
	c.Handle(authFrame("bo"))
	sc := <-srv.conn
	readClientFrame(t, sc)
	sc.Write(withLen(matchFound(3, 5, 0, "Brig Bot", 1)))
	tr.next(t, SMatchFound, time.Second)
	c.Close() // closing the tab
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	rs := rec.all()
	if len(rs) != 1 || rs[0].Result != "forfeit" || rs[0].Reason != "forfeit" || rs[0].Winner != 1 || !rs[0].Counted {
		t.Fatalf("a forfeit must be recorded as a counted loss, got %+v", rs)
	}
	// and leaving only the queue is nothing to record
	tr2 := newFakeTransport()
	c2 := h.Connect(tr2)
	c2.Handle(authFrame("cy"))
	<-srv.conn
	c2.Handle([]byte{CQueue})
	c2.Close()
	time.Sleep(100 * time.Millisecond)
	if n := len(rec.all()); n != 1 {
		t.Fatalf("leaving the queue must not record anything, records = %d", n)
	}
}

func TestServerLossAbortsWithoutCounting(t *testing.T) {
	srv := newFakeServer(t)
	cfg := DefaultConfig()
	cfg.ServerAddr = srv.ln.Addr().String()
	rec := &memRecorder{}
	h, _ := NewHub(cfg, okResolver, rec)
	tr := newFakeTransport()
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(authFrame("di"))
	sc := <-srv.conn
	readClientFrame(t, sc)
	sc.Write(withLen(matchFound(4, 6, 0, "Cobalt Bot", 1)))
	tr.next(t, SMatchFound, time.Second)
	sc.Close() // the match server restarts mid-match
	e := tr.next(t, GError, 2*time.Second)
	if e[1] != ErrServerDown {
		t.Fatalf("error = %v", e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	rs := rec.all()
	if len(rs) != 1 || rs[0].Result != "aborted" || rs[0].Counted {
		t.Fatalf("a match the server lost must be recorded as aborted and not count: %+v", rs)
	}
}

func TestPerSecondFrameBudget(t *testing.T) {
	srv := newFakeServer(t)
	cfg := DefaultConfig()
	cfg.ServerAddr = srv.ln.Addr().String()
	cfg.MaxFramesPerS = 10
	h, _ := NewHub(cfg, okResolver, &memRecorder{})
	tr := newFakeTransport()
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(authFrame("ed"))
	sc := <-srv.conn
	readClientFrame(t, sc)
	for i := 0; i < 100; i++ {
		c.Handle([]byte{CPing, 0, 0, 0, byte(i)})
	}
	_ = sc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	got := 0
	for {
		var l [2]byte
		if _, err := readFull(sc, l[:]); err != nil {
			break
		}
		b := make([]byte, binary.LittleEndian.Uint16(l[:]))
		if _, err := readFull(sc, b); err != nil {
			break
		}
		got++
	}
	if got < 8 || got > 12 {
		t.Fatalf("relayed %d of 100 frames with a budget of 10/s", got)
	}
}

func TestNewTabReplacesTheOldOne(t *testing.T) {
	srv := newFakeServer(t)
	cfg := DefaultConfig()
	cfg.ServerAddr = srv.ln.Addr().String()
	h, _ := NewHub(cfg, okResolver, &memRecorder{})
	t1, t2 := newFakeTransport(), newFakeTransport()
	c1 := h.Connect(t1)
	c1.Handle(authFrame("fay"))
	t1.next(t, GIdent, time.Second)
	c2 := h.Connect(t2)
	defer c2.Close()
	c2.Handle(authFrame("fay"))
	t2.next(t, GIdent, time.Second)
	if e := t1.next(t, GError, time.Second); e[1] != ErrBusy {
		t.Fatalf("old tab error = %v", e)
	}
}

// ---- against the real dw2_server and dw2_bot ------------------------------------------------------------------------------

// TestAgainstTheRealServer runs the REAL dw2_server and bot pool (build them from github.com/emilyspringerton/DEADWEIGHT_2:
// point D2_BIN_DIR at a directory holding `dw2_server` and `dw2_bot`) and plays full matches through the gateway: the frame
// sizes, the pairing, the packing acks, the round-break timing and the result are the C implementation's, not this
// package's idea of it.
//
//	D2_BIN_DIR=/path go test -run TestAgainstTheRealServer -v ./internal/d2play/
func TestAgainstTheRealServer(t *testing.T) {
	dir := os.Getenv("D2_BIN_DIR")
	if dir == "" {
		t.Skip("D2_BIN_DIR is not set")
	}
	port := 21000 + os.Getpid()%2000
	start := func(name string, args ...string) {
		c := exec.Command(filepath.Join(dir, name), args...)
		c.Dir = t.TempDir()
		if os.Getenv("REAL_LOG") != "" {
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
		}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	}
	start("dw2_server", "--port", fmt.Sprint(port), "--no-auth", "--pack-ms", "10000", "--tick-ms", "40")
	time.Sleep(300 * time.Millisecond)
	start("dw2_bot", "--port", fmt.Sprint(port), "--count", "3")
	time.Sleep(800 * time.Millisecond)

	cfg := DefaultConfig()
	cfg.ServerAddr = fmt.Sprintf("127.0.0.1:%d", port)
	rec := &memRecorder{}
	h, err := NewHub(cfg, okResolver, rec)
	if err != nil {
		t.Fatal(err)
	}

	// play drives one gateway connection through a whole match like the browser would.
	play := func(t *testing.T, tok string, ships [][4]byte, leaveAfterFound bool) (*fakeTransport, *Conn) {
		tr := newFakeTransport()
		c := h.Connect(tr)
		c.Handle(authFrame(tok))
		tr.next(t, GIdent, 3*time.Second)
		w := tr.next(t, SWelcome, 3*time.Second)
		if len(w) != 6 {
			t.Fatalf("WELCOME = %v", w)
		}
		c.Handle([]byte{CQueue})
		tr.next(t, SQueued, 3*time.Second)
		mf := tr.next(t, SMatchFound, 8*time.Second)
		if leaveAfterFound {
			return tr, c
		}
		for _, s := range ships {
			c.Handle([]byte{CPlace, s[0], s[1], s[2], s[3]})
		}
		for range ships {
			a := tr.next(t, 0, 3*time.Second)
			if a[0] != SPlaceAck {
				t.Fatalf("placement answered with 0x%02x %v", a[0], a)
			}
		}
		_ = mf
		c.Handle([]byte{CReady})
		tr.next(t, SCombatStart, 15*time.Second)
		// the fight shows both ships: the opponent's packed ship arrives (n placements, then unused slots marked 255) and is relayed untouched
		if os := tr.next(t, SOppShip, 5*time.Second); len(os) != 146 || os[1] == 0 || os[1] > 36 || os[2] >= 24 || os[2+4*35] != 255 && os[1] < 36 {
			t.Fatalf("OPP_SHIP = %v", os)
		}
		for {
			f := tr.next(t, 0, 30*time.Second)
			switch f[0] {
			case SRoundBreak:
				time.Sleep(time.Duration(binary.LittleEndian.Uint16(f[3:])) * time.Millisecond)
				c.Handle([]byte{CRoundCall, 1})
			case SMatchEnd:
				return tr, c
			}
		}
	}
	// generator -> conductor -> railgun, then a bulwark: the same ship the D2 repo's own smoke test fights with
	ship := [][4]byte{{0, 0, 0, 0}, {1, 0, 1, 0}, {3, 0, 2, 0}, {4, 3, 3, 0}}

	t.Run("a human is paired with a bot, plays and is recorded", func(t *testing.T) {
		tr, c := play(t, "alice", ship, false)
		defer c.Close()
		rg := tr.next(t, GRecorded, 3*time.Second)
		if rg[1] != 1 || rg[2] != 1 {
			t.Fatalf("GRecorded = %v", rg)
		}
		var r Record
		for _, x := range rec.all() {
			if x.PlayerID == "p-alice" {
				r = x
			}
		}
		if r.PlayerID == "" || r.OppKind != "bot" || r.OppName == "" || !r.Counted || r.Reason == "" {
			t.Fatalf("record = %+v", r)
		}
		if len(r.Loadout) != 4 || r.Loadout[2].Item != 3 {
			t.Fatalf("the packed ship must be recorded as placed, got %+v", r.Loadout)
		}
		if len(r.Rounds) < 1 || r.Rounds[0].TargetMS < 300 || r.Rounds[0].YourCall != 1 || r.Rounds[0].YourGrade == 0 {
			t.Fatalf("the round-break log = %+v", r.Rounds)
		}
		if r.Ticks < 1 || r.HullYou < 0 || r.Seconds < 0 {
			t.Fatalf("numbers = %+v", r)
		}
	})

	t.Run("leaving a live match is a forfeit loss and the bot wins it", func(t *testing.T) {
		tr, c := play(t, "bob", nil, true)
		_ = tr
		c.Close()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			for _, x := range rec.all() {
				if x.PlayerID == "p-bob" && x.Result == "forfeit" {
					if !x.Counted || x.Winner == x.Seat {
						t.Fatalf("forfeit record = %+v", x)
					}
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("no forfeit record for bob")
	})
}

// TestTwoHumansMeet runs the real dw2_server alone: two browsers are paired with each other, and the ladder is moved once.
func TestTwoHumansMeet(t *testing.T) {
	dir := os.Getenv("D2_BIN_DIR")
	if dir == "" {
		t.Skip("D2_BIN_DIR is not set")
	}
	port := 23000 + os.Getpid()%2000
	c := exec.Command(filepath.Join(dir, "dw2_server"), "--port", fmt.Sprint(port), "--no-auth", "--pack-ms", "6000", "--tick-ms", "30")
	c.Dir = t.TempDir()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	time.Sleep(300 * time.Millisecond)
	cfg := DefaultConfig()
	cfg.ServerAddr = fmt.Sprintf("127.0.0.1:%d", port)
	rec := &memRecorder{}
	h, _ := NewHub(cfg, okResolver, rec)
	type who struct {
		tr *fakeTransport
		c  *Conn
	}
	mk := func(tok string) who {
		tr := newFakeTransport()
		cn := h.Connect(tr)
		cn.Handle(authFrame(tok))
		tr.next(t, GIdent, 3*time.Second)
		return who{tr, cn}
	}
	a, b := mk("hana"), mk("ivo")
	defer a.c.Close()
	defer b.c.Close()
	a.c.Handle([]byte{CQueue})
	b.c.Handle([]byte{CQueue})
	var wg sync.WaitGroup
	for i, w := range []who{a, b} {
		wg.Add(1)
		go func(i int, w who) {
			defer wg.Done()
			mf := w.tr.next(t, SMatchFound, 8*time.Second)
			if mf[26] != 0 {
				t.Errorf("opponent kind = %d, want a human", mf[26])
			}
			if i == 0 {
				w.c.Handle([]byte{CPlace, 0, 0, 0, 0})
				w.c.Handle([]byte{CPlace, 1, 0, 1, 0})
				w.c.Handle([]byte{CPlace, 3, 0, 2, 0})
			}
			w.c.Handle([]byte{CReady})
			for {
				f := w.tr.next(t, 0, 40*time.Second)
				if f[0] == SMatchEnd {
					break
				}
			}
			w.tr.next(t, GRecorded, 5*time.Second)
		}(i, w)
	}
	wg.Wait()
	rs := rec.all()
	if len(rs) != 2 {
		t.Fatalf("records = %d, want one per human", len(rs))
	}
	if rs[0].OppPlayerID == "" || rs[1].OppPlayerID == "" || rs[0].OppPlayerID == rs[0].PlayerID {
		t.Fatalf("each side must know the other's player id: %+v / %+v", rs[0], rs[1])
	}
	if rs[0].SkipStats == rs[1].SkipStats {
		t.Fatalf("exactly one of the two sessions may move the ladder: %v %v", rs[0].SkipStats, rs[1].SkipStats)
	}
	if rs[0].Result == rs[1].Result && rs[0].Result != "tie" {
		t.Fatalf("both sides cannot %s", rs[0].Result)
	}
}
