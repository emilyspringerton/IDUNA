package redgardenplay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testPlayer = "0f1e2d3c-4b5a-4968-8776-655443322110"

var testSecret = []byte("test-secret-for-gateway-unit-tests")

// ---- a fake REDGARDEN: matchmaker + arena server speaking the real UDP protocol --------------------------------------

type fakeArena struct {
	t       *testing.T
	mm      *net.UDPConn
	srv     *net.UDPConn
	mu      sync.Mutex
	pkts    [][]byte
	client  *net.UDPAddr
	finds   int
	phase   int
	winner  int
	heroID  int
	kills   int
	deaths  int
	lobby   int
	badConn int
	silent  bool
	stop    chan struct{}
}

func newFakeArena(t *testing.T, lobby int) *fakeArena {
	t.Helper()
	mm, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeArena{t: t, mm: mm, srv: srv, lobby: lobby, stop: make(chan struct{}), heroID: 7, kills: 3, deaths: 1}
	go f.runMatchmaker()
	go f.runServer()
	go f.broadcast()
	t.Cleanup(func() { close(f.stop); mm.Close(); srv.Close() })
	return f
}

func (f *fakeArena) mmAddr() string { return f.mm.LocalAddr().String() }

func (f *fakeArena) runMatchmaker() {
	buf := make([]byte, 256)
	for {
		n, from, err := f.mm.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n >= 12 && buf[0] == pFindMatch {
			f.mu.Lock()
			f.finds++
			first := f.finds == 1
			f.mu.Unlock()
			if first {
				p := netHeader(pMatchFound, 0, 0)
				p = binary.LittleEndian.AppendUint16(p, uint16(f.srv.LocalAddr().(*net.UDPAddr).Port))
				p = append(p, 0, 0, 1, 2, 3, 4, 0, 0, 0, 0)
				_, _ = f.mm.WriteToUDP(p, from)
			}
		}
	}
}

// verifyTicket is apps/arena_server's verify_connect_ticket.
func verifyTicket(secret []byte, pkt []byte) (ok bool, player [16]byte) {
	if len(pkt) < netHeaderLen+TicketLen {
		return false, player
	}
	t := pkt[netHeaderLen : netHeaderLen+TicketLen]
	m := hmac.New(sha256.New, secret)
	m.Write(t[:ticketPayloadLen])
	if !hmac.Equal(t[ticketPayloadLen:], m.Sum(nil)[:ticketMacLen]) {
		return false, player
	}
	if int64(binary.LittleEndian.Uint32(t[16:20])) < time.Now().Unix() {
		return false, player
	}
	copy(player[:], t[:16])
	return true, player
}

func (f *fakeArena) runServer() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.srv.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p := append([]byte(nil), buf[:n]...)
		f.mu.Lock()
		if f.client == nil {
			if buf[0] == pConnect {
				ok, _ := verifyTicket(testSecret, p)
				if !ok {
					f.badConn++
					f.mu.Unlock()
					continue
				}
				f.client = from
				w := netHeader(pWelcome, 0, 0)
				w[1] = 2 // the player is hero slot 2
				_, _ = f.srv.WriteToUDP(w, from)
			}
		} else if buf[0] != pConnect {
			f.pkts = append(f.pkts, p)
		}
		f.mu.Unlock()
	}
}

func (f *fakeArena) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func (f *fakeArena) got(typ byte) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]byte
	for _, p := range f.pkts {
		if p[0] == typ {
			out = append(out, p)
		}
	}
	return out
}

func (f *fakeArena) broadcast() {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-tick.C:
		}
		f.mu.Lock()
		to, silent := f.client, f.silent
		phase, winner := f.phase, f.winner
		f.mu.Unlock()
		if to == nil || silent {
			continue
		}
		w := make([]byte, netHeaderLen+worldLen)
		w[0] = pArenaSnapshot
		b := w[netHeaderLen:]
		b[offCount], b[offWinner], b[offPhase] = byte(f.lobby), byte(winner), byte(phase)
		_, _ = f.srv.WriteToUDP(w, to)
		for c := 0; c < heroesPerMsg; c++ {
			h := make([]byte, netHeaderLen+heroesLen)
			h[0] = pSnapHeroes
			hb := h[netHeaderLen:]
			hb[0], hb[1] = byte(c), byte(f.lobby)
			for i := 0; i < heroChunkSize; i++ {
				slot := c*heroChunkSize + i
				e := hb[4+i*heroSnapLen:]
				e[hHeroID] = byte(slot + 5)
				if slot == 2 {
					e[hHeroID] = byte(f.heroID)
					e[hKills], e[hDeaths] = byte(f.kills), byte(f.deaths)
					binary.LittleEndian.PutUint16(e[hFlowEar:], 1234)
					binary.LittleEndian.PutUint16(e[hXP:], 321)
				}
			}
			_, _ = f.srv.WriteToUDP(h, to)
		}
	}
}

// ---- a fake browser --------------------------------------------------------------------------------------------------

type fakeTransport struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
}

func (t *fakeTransport) Send(b []byte) error {
	t.mu.Lock()
	t.frames = append(t.frames, append([]byte(nil), b...))
	t.mu.Unlock()
	return nil
}
func (t *fakeTransport) Close() { t.mu.Lock(); t.closed = true; t.mu.Unlock() }

func (t *fakeTransport) of(kind byte) [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out [][]byte
	for _, f := range t.frames {
		if f[0] == kind {
			out = append(out, f)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type memRecorder struct {
	mu   sync.Mutex
	recs []MatchRecord
	err  error
}

func (m *memRecorder) Record(_ context.Context, r MatchRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.recs = append(m.recs, r)
	return nil
}
func (m *memRecorder) all() []MatchRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MatchRecord(nil), m.recs...)
}

func testConfig(a *fakeArena) Config {
	return Config{
		Pools:            []Pool{{Name: "test-3v3", Mode: 0, Addr: a.mmAddr(), Lobby: a.lobby}},
		Secret:           testSecret,
		TicketTTL:        time.Minute,
		SnapshotEvery:    20 * time.Millisecond,
		FindResend:       100 * time.Millisecond,
		ConnectResend:    50 * time.Millisecond,
		ConnectTimeout:   2 * time.Second,
		QueueTimeout:     3 * time.Second,
		PickDeadline:     400 * time.Millisecond,
		ServerSilence:    400 * time.Millisecond,
		GhostLife:        2 * time.Second,
		MaxCommandsPerS:  50,
		MinCountedSecond: 0,
	}
}

func okResolver(_ context.Context, token string) (Identity, error) {
	if token != "good-token" {
		return Identity{}, errors.New("no")
	}
	return Identity{ID: testPlayer, Name: "Tester"}, nil
}

func hello(token string) []byte {
	b := []byte{CHello, Version, 0, 0}
	binary.LittleEndian.PutUint16(b[2:], uint16(len(token)))
	return append(b, token...)
}

func f32le(vs ...float32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func startPlayer(t *testing.T, a *fakeArena, rec Recorder) (*Hub, *Conn, *fakeTransport) {
	t.Helper()
	h, err := NewHub(testConfig(a), okResolver, rec)
	if err != nil {
		t.Fatal(err)
	}
	tr := &fakeTransport{}
	c := h.Connect(tr)
	t.Cleanup(c.Close)
	c.Handle(hello("good-token"))
	waitFor(t, "welcome", func() bool { return len(tr.of(SWelcome)) == 1 })
	return h, c, tr
}

// ---- tests -----------------------------------------------------------------------------------------------------------

func TestTicketMatchesTheArenasScheme(t *testing.T) {
	id, _ := parseUUID(testPlayer)
	tk := MintTicket(testSecret, id, uint32(time.Now().Add(time.Minute).Unix()))
	pkt := append(netHeader(pConnect, 1, 0), tk[:]...)
	if ok, got := verifyTicket(testSecret, pkt); !ok || got != id {
		t.Fatalf("a freshly minted ticket must verify: ok=%v", ok)
	}
	if ok, _ := verifyTicket([]byte("another secret"), pkt); ok {
		t.Fatal("a ticket signed with another secret must not verify")
	}
	old := MintTicket(testSecret, id, uint32(time.Now().Add(-time.Minute).Unix()))
	if ok, _ := verifyTicket(testSecret, append(netHeader(pConnect, 1, 0), old[:]...)); ok {
		t.Fatal("an expired ticket must not verify")
	}
}

func TestHelloRejectsAnUnknownToken(t *testing.T) {
	a := newFakeArena(t, 6)
	h, _ := NewHub(testConfig(a), okResolver, nil)
	tr := &fakeTransport{}
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(hello("forged"))
	waitFor(t, "an error", func() bool { return len(tr.of(SError)) == 1 })
	c.Handle([]byte{CQueue, 0})
	time.Sleep(50 * time.Millisecond)
	if st := h.Stats(); st.Searching != 0 {
		t.Fatalf("a refused token must not be able to queue: %+v", st)
	}
}

func TestQueueMatchPlayAndRecordAWin(t *testing.T) {
	a := newFakeArena(t, 6)
	rec := &memRecorder{}
	h, c, tr := startPlayer(t, a, rec)

	c.Handle([]byte{CQueue, 0})
	waitFor(t, "searching status", func() bool {
		for _, s := range tr.of(SStatus) {
			if s[1] == StSearching {
				return true
			}
		}
		return false
	})
	waitFor(t, "match frame", func() bool { return len(tr.of(SMatch)) == 1 })
	m := tr.of(SMatch)[0]
	if m[1] != 2 || m[2] != 6 || m[3] != 0 || m[4] != 0 {
		t.Fatalf("MATCH should say slot 2 of 6 on team 0 in mode 0, got %v", m)
	}
	if st := h.Stats(); st.Playing != 1 || st.Searching != 0 || st.Pools[0].State != "in_match" {
		t.Fatalf("pool accounting after the match was found: %+v", st)
	}
	waitFor(t, "snapshot", func() bool { return len(tr.of(SSnap)) > 0 })
	s := tr.of(SSnap)[0]
	if len(s) != 1+worldLen+heroesPerMsg*heroesLen {
		t.Fatalf("SNAP is %d bytes", len(s))
	}

	// the draft: picks go through, a hero outside the roster does not
	a.set(func() { a.phase = PhaseDraft })
	c.Handle([]byte{CPick, 31})
	c.Handle([]byte{CPick, 9})
	waitFor(t, "the pick", func() bool { return len(a.got(pArenaPick)) == 1 })
	if p := a.got(pArenaPick)[0]; p[netHeaderLen] != 9 {
		t.Fatalf("picked hero %d", p[netHeaderLen])
	}

	a.set(func() { a.phase = PhaseLive })
	c.Handle(append([]byte{CMove}, f32le(12.5, -7.25)...))
	c.Handle(append([]byte{CMove}, f32le(float32(math.NaN()), 1)...))
	c.Handle(append([]byte{CMove}, f32le(1e9, 1)...))
	c.Handle(append([]byte{CCast, 1, 0xFF, 1}, f32le(3, 4)...))
	c.Handle([]byte{CCast, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	c.Handle([]byte{CAttack, 4})
	c.Handle([]byte{CAttack, 25})
	c.Handle([]byte{CBuy, 12})
	c.Handle([]byte{CBuy, 99})
	c.Handle([]byte{CSell, 3})
	c.Handle([]byte{CActive})
	c.Handle([]byte{CStop})
	c.Handle([]byte{CHold})
	c.Handle(append([]byte{CAMove}, f32le(-3, 8)...))
	waitFor(t, "commands to arrive", func() bool { return len(a.got(pHold)) == 1 && len(a.got(pAttackMove)) == 1 })

	mv := a.got(pArenaMove)
	if len(mv) != 1 {
		t.Fatalf("exactly one valid MOVE must get through (NaN and out-of-range are dropped), got %d", len(mv))
	}
	b := mv[0][netHeaderLen:]
	if len(b) != 12 || getF32(b, 0) != 12.5 || getF32(b, 4) != -7.25 || b[8] != 2 {
		t.Fatalf("MOVE body %v (want x=12.5 z=-7.25 unit_owner=2, 12 bytes)", b)
	}
	cs := a.got(pArenaCast)
	if len(cs) != 1 || len(cs[0][netHeaderLen:]) != 12 {
		t.Fatalf("CAST: want one 12-byte body (slot 3 dropped), got %d", len(cs))
	}
	cb := cs[0][netHeaderLen:]
	if cb[0] != 1 || int8(cb[1]) != -1 || cb[2] != 1 || getF32(cb, 4) != 3 || getF32(cb, 8) != 4 {
		t.Fatalf("CAST body %v", cb)
	}
	at := a.got(pArenaAttack)
	if len(at) != 1 || at[0][netHeaderLen] != 4 || at[0][netHeaderLen+1] != 2 {
		t.Fatalf("ATTACK should be target 4 commanded by unit 2 (target 25 is outside the lobby cap): %v", at)
	}
	if len(a.got(pShopBuy)) != 1 || len(a.got(pShopSell)) != 1 || len(a.got(pBlink)) != 1 || len(a.got(pStop)) != 1 {
		t.Fatal("buy / sell / active / stop must each arrive once")
	}
	if am := a.got(pAttackMove)[0][netHeaderLen:]; len(am) != 12 || getF32(am, 0) != -3 || am[8] != 2 {
		t.Fatalf("ATTACK-MOVE body %v", am)
	}

	// team 0 wins
	a.set(func() { a.winner = 1 })
	waitFor(t, "result", func() bool { return len(tr.of(SResult)) == 1 })
	r := tr.of(SResult)[0]
	if r[1] != 1 || r[2] != ResWin || r[3] != 7 || r[4] != 3 || r[5] != 1 || r[8] != 1 {
		t.Fatalf("RESULT %v (want winner 1, win, hero 7, 3/1, recorded)", r)
	}
	waitFor(t, "record", func() bool { return len(rec.all()) == 1 })
	m0 := rec.all()[0]
	if m0.PlayerID != testPlayer || m0.Result != ResWin || m0.HeroID != 7 || m0.Kills != 3 || m0.Deaths != 1 || m0.Team != 0 ||
		m0.Slot != 2 || m0.FlowEarned != 1234 || m0.XP != 321 || m0.Lobby != 6 || !m0.Counted || m0.Mode != 0 || m0.Pool != "test-3v3" ||
		m0.WinnerTeam != 0 || len(m0.Roster) != 6 {
		t.Fatalf("record %+v", m0)
	}
	waitFor(t, "pool released", func() bool { s := h.Stats(); return s.Playing == 0 && s.Searching == 0 })
}

func TestTeamOneLosingIsALoss(t *testing.T) {
	a := newFakeArena(t, 4) // slot 2 of 4 is on team 1
	rec := &memRecorder{}
	_, c, tr := startPlayer(t, a, rec)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	if tr.of(SMatch)[0][3] != 1 {
		t.Fatalf("slot 2 of 4 should be on team 1: %v", tr.of(SMatch)[0])
	}
	a.set(func() { a.phase = PhaseLive; a.winner = 1 })
	waitFor(t, "record", func() bool { return len(rec.all()) == 1 })
	if r := rec.all()[0]; r.Result != ResLoss || r.WinnerTeam != 0 {
		t.Fatalf("team 1 loses when team 0 wins: %+v", r)
	}
}

func TestShortMatchesAreRecordedButDoNotCount(t *testing.T) {
	a := newFakeArena(t, 6)
	rec := &memRecorder{}
	h, _ := NewHub(func() Config { c := testConfig(a); c.MinCountedSecond = 60; return c }(), okResolver, rec)
	tr := &fakeTransport{}
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(hello("good-token"))
	waitFor(t, "welcome", func() bool { return len(tr.of(SWelcome)) == 1 })
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	a.set(func() { a.phase = PhaseLive })
	time.Sleep(100 * time.Millisecond)
	a.set(func() { a.winner = 1 })
	waitFor(t, "record", func() bool { return len(rec.all()) == 1 })
	if rec.all()[0].Counted {
		t.Fatal("a match that ended in a fraction of a second must not move win/loss")
	}
}

func TestAutoPickBeforeTheLobbyClockRunsOut(t *testing.T) {
	a := newFakeArena(t, 6)
	_, c, tr := startPlayer(t, a, nil)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	a.set(func() { a.phase = PhaseDraft })
	waitFor(t, "the gateway's backstop pick", func() bool { return len(a.got(pArenaPick)) == 1 })
}

func TestLeavingAWaitingSearchKeepsItsPlaceAndFreesTheSession(t *testing.T) {
	a := newFakeArena(t, 6)
	// a matchmaker that never answers: the bot pool is mid-match
	a.set(func() { a.finds = 99 })
	h, c, tr := startPlayer(t, a, nil)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "searching", func() bool { return h.Stats().Searching == 1 })
	c.Handle([]byte{CLeave})
	waitFor(t, "idle", func() bool {
		s := tr.of(SStatus)
		return len(s) > 0 && s[len(s)-1][1] == StIdle
	})
	st := h.Stats()
	if st.Searching != 0 || st.Pools[0].State != "in_match" {
		t.Fatalf("an abandoned search is a ghost that still holds its queue place (the pool is not free): %+v", st)
	}
	// the same session can search again straight away
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "searching again", func() bool { return h.Stats().Searching == 1 })
}

func TestTheArenaGoingSilentMidMatchIsRecordedAsLost(t *testing.T) {
	a := newFakeArena(t, 6)
	rec := &memRecorder{}
	_, c, tr := startPlayer(t, a, rec)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	a.set(func() { a.phase = PhaseLive })
	waitFor(t, "live", func() bool { return len(tr.of(SSnap)) > 3 })
	a.set(func() { a.silent = true })
	waitFor(t, "arena-lost error", func() bool {
		for _, e := range tr.of(SError) {
			if e[1] == ErrArenaLost {
				return true
			}
		}
		return false
	})
	waitFor(t, "record", func() bool { return len(rec.all()) == 1 })
	if r := rec.all()[0]; r.Result != ResLost || r.Counted {
		t.Fatalf("a lost arena is never a win or a loss: %+v", r)
	}
}

func TestClosingMidMatchIsAnAbandonNotALoss(t *testing.T) {
	a := newFakeArena(t, 6)
	rec := &memRecorder{}
	_, c, tr := startPlayer(t, a, rec)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	a.set(func() { a.phase = PhaseLive })
	waitFor(t, "live", func() bool { return len(tr.of(SSnap)) > 3 })
	c.Close()
	waitFor(t, "record", func() bool { return len(rec.all()) == 1 })
	if r := rec.all()[0]; r.Result != ResAbandoned || r.Counted {
		t.Fatalf("leaving a live match is recorded as abandoned and does not touch win/loss: %+v", r)
	}
}

func TestACommandFloodIsThrottled(t *testing.T) {
	a := newFakeArena(t, 6)
	_, c, tr := startPlayer(t, a, nil)
	c.Handle([]byte{CQueue, 0})
	waitFor(t, "match", func() bool { return len(tr.of(SMatch)) == 1 })
	for i := 0; i < 400; i++ {
		c.Handle([]byte{CStop})
	}
	time.Sleep(150 * time.Millisecond)
	if n := len(a.got(pStop)); n > 60 {
		t.Fatalf("a flood of 400 commands got %d through (cap is 50/s)", n)
	}
}

func TestOnePlayerOneConnection(t *testing.T) {
	a := newFakeArena(t, 6)
	h, _ := NewHub(testConfig(a), okResolver, nil)
	t1, t2 := &fakeTransport{}, &fakeTransport{}
	c1, c2 := h.Connect(t1), h.Connect(t2)
	defer c2.Close()
	c1.Handle(hello("good-token"))
	waitFor(t, "first welcome", func() bool { return len(t1.of(SWelcome)) == 1 })
	c2.Handle(hello("good-token"))
	waitFor(t, "second welcome", func() bool { return len(t2.of(SWelcome)) == 1 })
	waitFor(t, "first dropped", func() bool { t1.mu.Lock(); defer t1.mu.Unlock(); return t1.closed })
}

func TestNewHubNeedsTheTicketSecret(t *testing.T) {
	a := newFakeArena(t, 6)
	c := testConfig(a)
	c.Secret = nil
	if _, err := NewHub(c, okResolver, nil); err == nil {
		t.Fatal("without the arena's ticket secret every connect would be rejected; the hub must refuse to start")
	}
}

func TestPingPong(t *testing.T) {
	a := newFakeArena(t, 6)
	h, _ := NewHub(testConfig(a), okResolver, nil)
	tr := &fakeTransport{}
	c := h.Connect(tr)
	defer c.Close()
	c.Handle([]byte{CPing, 1, 2, 3, 4})
	if p := tr.of(SPong); len(p) != 1 || p[0][4] != 4 {
		t.Fatalf("pong %v", p)
	}
}

// ---- the database recorder, against the real migrations -----------------------------------------------------------------

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	// the play-permission INSERT in one of the migrations needs the table to exist
	if _, err := db.Exec(`CREATE TABLE permissions (id TEXT PRIMARY KEY, name TEXT UNIQUE, description TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"202607240002_player_game_stats.sql", "202607290001_redgarden_hero_stats.sql",
		"202610100005_redgarden_play_and_matches.sql", "202610170001_redgarden_web_matches.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "migrations", "truestore", f))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return db
}

func TestDBRecorderWritesAllFourTablesForACountedWin(t *testing.T) {
	db := openMigrated(t)
	rec := &DBRecorder{DB: db}
	m := MatchRecord{PlayerID: testPlayer, Pool: "rnd-3v3", Mode: 0, Lobby: 6, Slot: 1, Team: 0, HeroID: 9, Result: ResWin, WinnerTeam: 0,
		Kills: 5, Deaths: 2, FlowEarned: 900, XP: 80, Seconds: 421, Counted: true,
		Roster: []HeroStat{{Slot: 0, Team: 0, HeroID: 3}, {Slot: 1, Team: 0, HeroID: 9, Kills: 5, Deaths: 2}}}
	if err := rec.Record(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	m.Result, m.WinnerTeam, m.HeroID = ResLoss, 1, 4
	if err := rec.Record(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var w, l, n int
	if err := db.QueryRow(`SELECT wins, losses, matches_played FROM player_game_stats WHERE player_id=? AND game=?`, testPlayer, ArenaGame).Scan(&w, &l, &n); err != nil || w != 1 || l != 1 || n != 2 {
		t.Fatalf("player_game_stats %d/%d/%d err=%v", w, l, n, err)
	}
	var hw, hl int
	if err := db.QueryRow(`SELECT wins, losses FROM redgarden_hero_stats WHERE hero_id=9`).Scan(&hw, &hl); err != nil || hw != 1 || hl != 0 {
		t.Fatalf("hero 9 stats %d/%d err=%v", hw, hl, err)
	}
	var pm int
	_ = db.QueryRow(`SELECT COUNT(*) FROM redgarden_player_matches WHERE player_id=?`, testPlayer).Scan(&pm)
	if pm != 2 {
		t.Fatalf("player matches %d", pm)
	}
	var kills, secs int
	var roster, result string
	if err := db.QueryRow(`SELECT kills, seconds, roster, result FROM redgarden_web_matches ORDER BY id LIMIT 1`).Scan(&kills, &secs, &roster, &result); err != nil ||
		kills != 5 || secs != 421 || result != "win" || roster == "[]" {
		t.Fatalf("web match row %d %d %s %s err=%v", kills, secs, roster, result, err)
	}
}

func TestDBRecorderKeepsAbandonedMatchesOutOfTheCounters(t *testing.T) {
	db := openMigrated(t)
	rec := &DBRecorder{DB: db}
	if err := rec.Record(context.Background(), MatchRecord{PlayerID: testPlayer, Pool: "p", Result: ResAbandoned, HeroID: 2, WinnerTeam: -1}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM player_game_stats`).Scan(&n)
	if n != 0 {
		t.Fatal("an abandoned match must not create a stats row")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM redgarden_web_matches WHERE result='abandoned'`).Scan(&n)
	if n != 1 {
		t.Fatal("but it is still recorded in the match log")
	}
}
