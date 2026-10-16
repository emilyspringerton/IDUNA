package shankpit460

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

// ---- in-memory transport and a scripted client ------------------------------------------------------------

type fakeConn struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
}

func (f *fakeConn) Send(b []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, append([]byte(nil), b...))
	return nil
}
func (f *fakeConn) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

type tc struct {
	t    *testing.T
	c    *Client
	conn *fakeConn
	seen int
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type rig struct {
	t   *testing.T
	h   *Hub
	clk *clock
}

func newRig(t *testing.T, mut func(*Config), res Resolver) *rig {
	cfg := DefaultConfig()
	cfg.IdleTimeout = time.Hour // scripted clients are silent between messages
	if mut != nil {
		mut(&cfg)
	}
	if res == nil {
		res = func(ctx context.Context, token string) (Identity, error) {
			if token == "bad" {
				return Identity{}, errors.New("nope")
			}
			return Identity{ID: "p-" + token, Name: "Name-" + token}, nil
		}
	}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	h, err := NewHub(cfg, res, WithClock(clk.now), WithManualTicks(), WithSyncSend(), WithSeed(7))
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, h: h, clk: clk}
}

func (r *rig) client(token, name string) *tc {
	conn := &fakeConn{}
	c := r.h.Connect(conn)
	x := &tc{t: r.t, c: c, conn: conn}
	w := &writer{}
	w.u8(CHello)
	w.u8(Version)
	w.u16(len(token))
	w.b = append(w.b, token...)
	w.str(name)
	c.Handle(w.b)
	return x
}

// place puts a player somewhere without going through the speed limit (tests that need exact geometry)
func (r *rig) place(x *tc, px, py, pz float32) {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	p := x.c.room.players[x.c.slot]
	p.x, p.y, p.z, p.histN = px, py, pz, 0
	p.freeMoveUntil = r.clk.now().Add(time.Second)
}

// tick advances the clock and the hub in 40 ms steps
func (r *rig) run(d time.Duration) {
	for el := time.Duration(0); el < d; el += 40 * time.Millisecond {
		r.clk.add(40 * time.Millisecond)
		r.h.Tick(r.clk.now())
	}
}

func (x *tc) all(typ byte) [][]byte {
	x.conn.mu.Lock()
	defer x.conn.mu.Unlock()
	var out [][]byte
	for _, f := range x.conn.frames {
		if f[0] == typ {
			out = append(out, f)
		}
	}
	return out
}
func (x *tc) last(typ byte) []byte {
	a := x.all(typ)
	if len(a) == 0 {
		return nil
	}
	return a[len(a)-1]
}
func (x *tc) events(sub byte) [][]byte {
	var out [][]byte
	for _, f := range x.all(SEvent) {
		if f[1] == sub {
			out = append(out, f)
		}
	}
	return out
}
func (x *tc) queue() { x.c.Handle([]byte{CQueue}) }
func (x *tc) leave() { x.c.Handle([]byte{CLeave}) }
func (x *tc) state(xp, y, z float32, yaw float32, weapon, flags int) {
	w := &writer{}
	w.u8(CState)
	w.u16(1)
	for _, v := range []float32{xp, y, z, yaw, 0} {
		w.f32(v)
	}
	w.u8(weapon)
	w.u8(flags)
	x.c.Handle(w.b)
}
func (x *tc) hit(victim, weapon int, head bool) {
	h := 0
	if head {
		h = 1
	}
	x.c.Handle([]byte{CHit, byte(victim), byte(weapon), byte(h)})
}

func slotOf(t *testing.T, f []byte) int {
	if f == nil || f[0] != SMatch {
		t.Fatalf("no MATCH frame")
	}
	return int(f[5])
}

// two players in a live match, standing apart in the open
func liveMatch(t *testing.T, mut func(*Config)) (*rig, *tc, *tc, int, int) {
	r := newRig(t, mut, nil)
	a := r.client("a", "")
	b := r.client("b", "")
	a.queue()
	b.queue()
	r.run(7 * time.Second)
	sa, sb := slotOf(t, a.last(SMatch)), slotOf(t, b.last(SMatch))
	// let the spawn protection lapse, then put them in the open at 40 units
	r.run(1200 * time.Millisecond)
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -80)
	r.run(120 * time.Millisecond)
	return r, a, b, sa, sb
}

// ---- tests -------------------------------------------------------------------------------------------------

func TestHelloWelcomeGuestAndSignedIn(t *testing.T) {
	r := newRig(t, nil, nil)
	g := r.client("", "Wren")
	w := g.last(SWelcome)
	if w == nil || w[2] != 0 {
		t.Fatalf("guest welcome = %v", w)
	}
	if name := string(w[4 : 4+int(w[3])]); name != "Wren" {
		t.Fatalf("guest name = %q", name)
	}
	s := r.client("tok", "ignored")
	w = s.last(SWelcome)
	if w[2] != 1 || string(w[4:4+int(w[3])]) != "Name-tok" {
		t.Fatalf("signed-in welcome = %v", w)
	}
	bad := r.client("bad", "Pip")
	if w = bad.last(SWelcome); w[2] != 2 {
		t.Fatalf("rejected token should still welcome as a guest, flagged: %v", w)
	}
	anon := r.client("", "")
	w = anon.last(SWelcome)
	if n := string(w[4 : 4+int(w[3])]); len(n) < 6 || n[:5] != "Guest" {
		t.Fatalf("nameless guest = %q", n)
	}
}

func TestOneSeatPerIdentity(t *testing.T) {
	r := newRig(t, nil, nil)
	a := r.client("same", "")
	time.Sleep(5 * time.Millisecond)
	_ = r.client("same", "")
	time.Sleep(50 * time.Millisecond)
	a.conn.mu.Lock()
	closed := a.conn.closed
	a.conn.mu.Unlock()
	if !closed {
		t.Fatal("the first connection of a signed-in identity should be replaced by the second")
	}
}

func TestLoneSearcherWaitsThenPairs(t *testing.T) {
	r := newRig(t, nil, nil)
	a := r.client("a", "")
	a.queue()
	r.run(2100 * time.Millisecond)
	if a.last(SMatch) != nil {
		t.Fatal("one human alone must stay searching")
	}
	st := a.last(SStatus)
	if st == nil || st[1] != QSearching || st[2] != 1 {
		t.Fatalf("status = %v", st)
	}
	if el := binary.LittleEndian.Uint16(st[5:]); el < 1 || el > 3 {
		t.Fatalf("elapsed = %d", el)
	}
	b := r.client("b", "")
	b.queue()
	ma, mb := a.last(SMatch), b.last(SMatch)
	if ma == nil || mb == nil {
		t.Fatal("two searching humans must be put in a lobby at once")
	}
	if binary.LittleEndian.Uint32(ma[1:]) != binary.LittleEndian.Uint32(mb[1:]) {
		t.Fatal("same room expected")
	}
	if slotOf(t, ma) == slotOf(t, mb) {
		t.Fatal("distinct slots expected")
	}
	if ma[6] != 16 {
		t.Fatalf("kill limit %d, want 16", ma[6])
	}
	if d := binary.LittleEndian.Uint16(ma[7:]); d != 360 {
		t.Fatalf("duration %d, want 360 s", d)
	}
}

func TestCountdownAllowsMoreThenGoesLive(t *testing.T) {
	r := newRig(t, nil, nil)
	a, b := r.client("a", ""), r.client("b", "")
	a.queue()
	b.queue()
	r.run(2 * time.Second)
	c := r.client("c", "")
	c.queue()
	if c.last(SMatch) == nil {
		t.Fatal("a third human should join the lobby that is still counting down")
	}
	if a.last(SRoster) == nil {
		t.Fatal("existing players should hear the roster change")
	}
	if len(a.events(EvLive)) != 0 {
		t.Fatal("not live yet")
	}
	r.run(5 * time.Second)
	if len(a.events(EvLive)) != 1 || len(c.events(EvLive)) != 1 {
		t.Fatal("everyone should get one LIVE event")
	}
	if len(a.events(EvRespawn)) < 3 {
		t.Fatalf("each of three players should be placed at the start, got %d respawns", len(a.events(EvRespawn)))
	}
	if r.h.Stats().Playing != 3 {
		t.Fatalf("stats = %+v", r.h.Stats())
	}
}

func TestSnapshotsFlowAndStateRelays(t *testing.T) {
	r, a, b, sa, _ := liveMatch(t, nil)
	a.state(-250, 0.5, -40, 90, WAR, 2)
	r.run(120 * time.Millisecond)
	snap := b.last(SSnap)
	if snap == nil {
		t.Fatal("no snapshot")
	}
	n := int(snap[5])
	found := false
	for i := 0; i < n; i++ {
		off := 6 + i*28
		if int(snap[off]) == sa {
			x := math.Float32frombits(binary.LittleEndian.Uint32(snap[off+1:]))
			if x != -250 || snap[off+21] != WAR || snap[off+22] != 2 {
				t.Fatalf("relayed state wrong: x=%v weapon=%d flags=%d", x, snap[off+21], snap[off+22])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("shooter missing from snapshot")
	}
}

func TestDamageShieldFirstAndHeadshots(t *testing.T) {
	r, a, b, sa, sb := liveMatch(t, nil)
	// magnum body: 45 into the shield
	a.hit(sb, WMagnum, false)
	d := a.last(SEvent)
	if d[1] != EvDamage || d[4] != 45 || d[5] != 100 || d[6] != 55 {
		t.Fatalf("body hit on a full shield: %v (want amount 45, health 100, shield 55)", d)
	}
	r.run(500 * time.Millisecond)
	// a head hit while the shield is up is NOT tripled
	a.hit(sb, WMagnum, true)
	d = b.last(SEvent)
	if d[4] != 45 || d[6] != 10 {
		t.Fatalf("head hit with a shield up should not triple: %v", d)
	}
	r.run(500 * time.Millisecond)
	// the shield breaks (10 absorbed, 35 to health); the next head shot with no shield triples
	a.hit(sb, WMagnum, false)
	d = a.last(SEvent)
	if d[5] != 65 || d[6] != 0 {
		t.Fatalf("overflow into health wrong: %v (want health 65, shield 0)", d)
	}
	r.run(500 * time.Millisecond)
	a.hit(sb, WMagnum, true)
	dm := a.events(EvDamage)
	d = dm[len(dm)-1]
	if d[4] != 135 || d[5] != 0 {
		t.Fatalf("an unshielded head shot is x3: %v", d)
	}
	kills := a.events(EvKill)
	if len(kills) != 1 || int(kills[0][2]) != sa || int(kills[0][3]) != sb {
		t.Fatalf("expected exactly one kill by the shooter: %v", kills)
	}
}

func TestRateOfFireAndRange(t *testing.T) {
	r, a, _, _, sb := liveMatch(t, nil)
	a.hit(sb, WSniper, false)
	n := len(a.events(EvDamage))
	a.hit(sb, WSniper, false) // same instant: the sniper cannot fire twice
	if len(a.events(EvDamage)) != n {
		t.Fatal("a second sniper claim in the same instant should be refused")
	}
	r.run(60 * time.Millisecond * 2)
	a.hit(sb, WSniper, false)
	if len(a.events(EvDamage)) != n {
		t.Fatal("still inside the sniper's 70-tick cooldown")
	}
	r.run(1 * time.Second)
	a.hit(sb, WSniper, false)
	if len(a.events(EvDamage)) != n+1 {
		t.Fatal("after the cooldown the claim should land")
	}
	// the knife reaches 16.5 units, not 40
	r.run(2 * time.Second)
	a.hit(sb, WKnife, false)
	if len(a.events(EvDamage)) != n+1 {
		t.Fatal("a knife claim from 40 units away should be refused")
	}
}

func TestWallsStopClaims(t *testing.T) {
	r, a, b, _, sb := liveMatch(t, nil)
	// the rock at (-180, 4, -120), 16x8x14: stand either side of it
	r.place(a, -200, 0, -120)
	r.place(b, -160, 0, -120)
	r.run(400 * time.Millisecond)
	n := len(a.events(EvDamage))
	a.hit(sb, WAR, false)
	if len(a.events(EvDamage)) != n {
		t.Fatal("a claim straight through a rock should be refused")
	}
	// out in the open it lands
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -60)
	r.run(400 * time.Millisecond)
	a.hit(sb, WAR, false)
	if len(a.events(EvDamage)) != n+1 {
		t.Fatal("a clear shot should land")
	}
}

func TestSpawnProtection(t *testing.T) {
	r := newRig(t, nil, nil)
	a, b := r.client("a", ""), r.client("b", "")
	a.queue()
	b.queue()
	r.run(6040 * time.Millisecond) // just live
	sb := slotOf(t, b.last(SMatch))
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -60)
	a.hit(sb, WMagnum, false)
	if len(a.events(EvDamage)) != 0 {
		t.Fatal("a fresh spawn is protected for a second")
	}
	r.run(1100 * time.Millisecond)
	a.hit(sb, WMagnum, false)
	if len(a.events(EvDamage)) != 1 {
		t.Fatal("protection should lapse")
	}
}

func TestKillRespawnAndProtection(t *testing.T) {
	r, a, b, sa, sb := liveMatch(t, nil)
	// knife: 200 kills through a full shield + health in one claim
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -50)
	r.run(200 * time.Millisecond)
	a.hit(sb, WKnife, false)
	if len(b.events(EvKill)) != 1 {
		t.Fatal("a 200 damage knife hit kills at full health and shield")
	}
	snap := func() (alive bool) {
		r.run(80 * time.Millisecond)
		s := a.last(SSnap)
		for i := 0; i < int(s[5]); i++ {
			off := 6 + i*28
			if int(s[off]) == sb {
				return s[off+27] == StAlive
			}
		}
		return false
	}
	if snap() {
		t.Fatal("the victim should be down")
	}
	before := len(a.events(EvRespawn))
	r.run(3 * time.Second)
	if len(a.events(EvRespawn)) != before+1 {
		t.Fatalf("one respawn event expected after the delay, got %d", len(a.events(EvRespawn))-before)
	}
	if !snap() {
		t.Fatal("the victim should be back")
	}
	_ = sa
}

func TestFirstTo16Wins(t *testing.T) {
	r, a, b, sa, sb := liveMatch(t, nil)
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -50)
	r.run(200 * time.Millisecond)
	for i := 0; i < 16; i++ {
		a.hit(sb, WKnife, false)
		if len(a.all(SEnd)) > 0 {
			break
		}
		r.run(4200 * time.Millisecond) // respawn delay + a second of protection
		r.place(a, -260, 0, -40)
		r.place(b, -260, 0, -50)
		r.run(200 * time.Millisecond)
	}
	end := a.last(SEnd)
	if end == nil {
		t.Fatal("no END after 16 kills")
	}
	if end[1] != EndKills || int(end[2]) != sa {
		t.Fatalf("end = %v, want reason kills, winner %d", end, sa)
	}
	rows := map[int][2]int{}
	for i := 0; i < int(end[3]); i++ {
		rows[int(end[4+i*3])] = [2]int{int(end[5+i*3]), int(end[6+i*3])}
	}
	if rows[sa][0] != 16 || rows[sb][1] != 16 {
		t.Fatalf("standings %v", rows)
	}
	if b.last(SEnd) == nil {
		t.Fatal("both players get the END")
	}
	if r.h.Stats().Playing != 0 || len(r.h.Stats().Rooms) != 0 {
		t.Fatalf("the room should be gone: %+v", r.h.Stats())
	}
	// and they can requeue straight away
	a.queue()
	b.queue()
	if len(a.all(SMatch)) != 2 {
		t.Fatal("a requeue after the end should find the other human at once")
	}
}

func TestSixMinuteLimitAndTies(t *testing.T) {
	r, a, b, sa, _ := liveMatch(t, nil)
	r.run(6 * time.Minute)
	end := a.last(SEnd)
	if end == nil || end[1] != EndTime {
		t.Fatalf("expected a time-limit END, got %v", end)
	}
	if end[2] != 255 {
		t.Fatalf("0-0 is a draw, winner %d", end[2])
	}
	_ = b
	_ = sa

	// with a kill each way a tie on kills falls to fewer deaths
	r2, a2, b2, sa2, sb2 := liveMatch(t, nil)
	r2.place(a2, -260, 0, -40)
	r2.place(b2, -260, 0, -50)
	r2.run(200 * time.Millisecond)
	a2.hit(sb2, WKnife, false)
	r2.run(6 * time.Minute)
	end = a2.last(SEnd)
	if end == nil || end[1] != EndTime || int(end[2]) != sa2 {
		t.Fatalf("the one with the kill should win on time: %v", end)
	}
	_ = b2
}

func TestLeaveLiveEndsAbandoned(t *testing.T) {
	r, a, b, _, _ := liveMatch(t, nil)
	b.c.Close()
	end := a.last(SEnd)
	if end == nil || end[1] != EndAbandoned {
		t.Fatalf("a lone player left in a live room gets an abandoned END: %v", end)
	}
	r.run(80 * time.Millisecond)
	if len(r.h.Stats().Rooms) != 0 {
		t.Fatal("room should close")
	}
}

func TestLeaveDuringCountdownReturnsToSearch(t *testing.T) {
	r := newRig(t, nil, nil)
	a, b := r.client("a", ""), r.client("b", "")
	a.queue()
	b.queue()
	r.run(2 * time.Second)
	b.c.Close()
	r.run(80 * time.Millisecond)
	st := a.last(SStatus)
	if st == nil || st[1] != QSearching {
		t.Fatalf("the remaining player should be searching again: %v", st)
	}
	c := r.client("c", "")
	c.queue()
	if c.last(SMatch) == nil || a.all(SMatch) == nil {
		t.Fatal("a newcomer should pair with the returned searcher")
	}
}

func TestLateJoin(t *testing.T) {
	r, _, _, _, _ := liveMatch(t, nil)
	r.run(60 * time.Second)
	c := r.client("c", "")
	c.queue()
	m := c.last(SMatch)
	if m == nil || m[9] != phaseLive {
		t.Fatalf("a third human should drop into the running match: %v", m)
	}
	r.run(4 * time.Minute)
	d := r.client("d", "")
	d.queue()
	if d.last(SMatch) != nil {
		t.Fatal("no late joins after 4:30 of a six minute match")
	}
}

func TestSpeedLimitRejectsTeleports(t *testing.T) {
	r, a, b, sa, _ := liveMatch(t, nil)
	r.run(1200 * time.Millisecond)       // past the post-spawn window in which a respawn may move a player freely
	a.state(-100, 0, -40, 0, WMagnum, 0) // 160 units in ~one tick: a teleport
	r.run(80 * time.Millisecond)
	s := b.last(SSnap)
	for i := 0; i < int(s[5]); i++ {
		off := 6 + i*28
		if int(s[off]) == sa {
			if x := math.Float32frombits(binary.LittleEndian.Uint32(s[off+1:])); x != -260 {
				t.Fatalf("a teleport moved the player to %v", x)
			}
		}
	}
	// walking at a human pace is fine
	for i := 0; i < 10; i++ {
		r.run(40 * time.Millisecond)
		a.state(-260+float32(i+1)*2, 0, -40, 0, WMagnum, 0)
	}
	r.run(80 * time.Millisecond)
	s = b.last(SSnap)
	for i := 0; i < int(s[5]); i++ {
		off := 6 + i*28
		if int(s[off]) == sa {
			if x := math.Float32frombits(binary.LittleEndian.Uint32(s[off+1:])); x < -245 {
				t.Fatalf("normal movement was rejected: x=%v", x)
			}
		}
	}
}

func TestFireRelay(t *testing.T) {
	r, a, b, sa, _ := liveMatch(t, nil)
	w := &writer{}
	w.u8(CFire)
	w.u8(WAR)
	for _, v := range []float32{-260, 2.6, -40, 0, 0, -1} {
		w.f32(v)
	}
	a.c.Handle(w.b)
	r.run(40 * time.Millisecond)
	ev := b.events(EvFire)
	if len(ev) != 1 || int(ev[0][2]) != sa || ev[0][3] != WAR {
		t.Fatalf("fire relay: %v", ev)
	}
	if len(a.events(EvFire)) != 0 {
		t.Fatal("the shooter must not get their own shot back")
	}
}

func TestIdleClientsAreDropped(t *testing.T) {
	r := newRig(t, func(c *Config) { c.IdleTimeout = 20 * time.Second }, nil)
	a := r.client("a", "")
	r.run(30 * time.Second)
	time.Sleep(30 * time.Millisecond)
	a.conn.mu.Lock()
	closed := a.conn.closed
	a.conn.mu.Unlock()
	if !closed {
		t.Fatal("a silent client should time out")
	}
}

func TestPingPong(t *testing.T) {
	r := newRig(t, nil, nil)
	a := r.client("a", "")
	w := &writer{}
	w.u8(CPing)
	w.u32(0xdeadbeef)
	a.c.Handle(w.b)
	p := a.last(SPong)
	if p == nil || binary.LittleEndian.Uint32(p[1:]) != 0xdeadbeef {
		t.Fatalf("pong = %v", p)
	}
}

func TestMalformedFramesAreIgnored(t *testing.T) {
	r, a, _, _, _ := liveMatch(t, nil)
	for _, f := range [][]byte{{}, {CState}, {CState, 1}, {CHit}, {CHit, 200, 200, 1}, {CFire, 1}, {0xEE}, {CHello}} {
		a.c.Handle(f)
	}
	r.run(80 * time.Millisecond)
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"  Wren  ": "Wren", "Zoë": "Zo?", "": "", "a\x01b": "ab", "0123456789012345678901234": "01234567890123456789"} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q want %q", in, got, want)
		}
	}
}

func TestArenaParsesAndBlocks(t *testing.T) {
	a, err := ParseArena(mapFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Boxes) != 36 || len(a.Spawns) != 8 {
		t.Fatalf("boxes %d spawns %d", len(a.Boxes), len(a.Spawns))
	}
	if a.LineClear(-200, 3, -120, -160, 3, -120) {
		t.Fatal("the rock at -180,-120 blocks that line")
	}
	if !a.LineClear(-260, 3, -40, -260, 3, -80) {
		t.Fatal("open ground is clear")
	}
	if !a.Inside(0, 0, 0) || a.Inside(500, 0, 0) {
		t.Fatal("envelope")
	}
}
