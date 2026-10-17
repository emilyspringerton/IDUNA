package redgardenplay

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"time"
)

// run stages
const (
	stQueued = iota
	stConnecting
	stInMatch
	stDone
)

// pool accounting states
const (
	acSearching = iota
	acPlaying
	acGhost
	acReleased
)

// run is one search + match for one browser connection. A single goroutine (loop) owns the UDP socket: it queues with the
// matchmaker, connects to the arena the matchmaker names, reads the arena's snapshots, forwards them to the browser and
// watches the match so it can be recorded. Other goroutines only call command() (the browser's orders) and userLeft().
type run struct {
	c    *Conn
	hub  *Hub
	pool *poolState
	mode int

	conn *net.UDPConn
	mm   *net.UDPAddr

	mu     sync.Mutex
	stage  int
	srv    *net.UDPAddr
	owner  int
	lobby  int
	ghost  bool // the player left while queued: keep the place in line, do nothing when matched
	left   bool
	ahead  int
	acct   int
	seq    uint16
	picked bool

	// loop-owned
	startedAt   time.Time
	liveAt      time.Time
	draftAt     time.Time
	lastRecv    time.Time
	lastFwd     time.Time
	world       [worldLen]byte
	heroes      [heroesPerMsg * heroesLen]byte
	haveWorld   bool
	haveHeroes  [heroesPerMsg]bool
	dirty       bool
	lastPhase   int
	lastWinner  int
	resultSent  bool
	connectAt   time.Time
	sentConnect time.Time
}

func newRun(c *Conn, p *poolState, mode, ahead int) *run {
	return &run{c: c, hub: c.hub, pool: p, mode: mode, ahead: ahead, stage: stQueued, acct: acSearching, owner: -1, lastPhase: -1}
}

func (r *run) setAcct(to int) {
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	if r.acct == to {
		return
	}
	switch r.acct {
	case acSearching:
		r.pool.searching--
	case acPlaying:
		r.pool.playing--
	case acGhost:
		r.pool.ghosts--
	}
	switch to {
	case acSearching:
		r.pool.searching++
	case acPlaying:
		r.pool.playing++
	case acGhost:
		r.pool.ghosts++
	}
	r.acct = to
}

// userLeft is the browser leaving (LEAVE or a closed socket).
func (r *run) userLeft() {
	r.mu.Lock()
	r.left = true
	if r.stage == stQueued {
		// The matchmaker has no "never mind": our address stays in its queue, and when the bots come back it will
		// match it into a lobby. Keep the socket (the address) alive and let that lobby time out harmlessly.
		r.ghost = true
	}
	r.mu.Unlock()
	if !r.ghostNow() {
		return
	}
	r.setAcct(acGhost)
}

func (r *run) ghostNow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ghost
}

func (r *run) isLeft() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.left
}

func (r *run) setStage(s int) {
	r.mu.Lock()
	r.stage = s
	r.mu.Unlock()
}

func (r *run) getStage() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stage
}

func (r *run) udpSend(b []byte, to *net.UDPAddr) {
	if r.conn != nil && to != nil {
		_, _ = r.conn.WriteToUDP(b, to)
	}
}

func (r *run) nextSeq() uint16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq
}

func nowMS() uint32 { return uint32(time.Now().UnixMilli()) }

func (r *run) send(b []byte) {
	if r.ghostNow() || r.isLeft() {
		return
	}
	r.c.send(b)
}

func (r *run) fail(code int, msg string) {
	if r.ghostNow() || r.isLeft() {
		return
	}
	r.c.fail(code, msg)
}

func (r *run) status(state int, elapsed time.Duration) {
	if r.ghostNow() || r.isLeft() {
		return
	}
	r.c.status(state, r.mode, r.pool.Lobby, r.ahead, elapsed)
}

func (r *run) loop() {
	defer r.finish()
	cfg := r.hub.cfg
	mm, err := net.ResolveUDPAddr("udp", r.pool.Addr)
	if err != nil {
		r.fail(ErrNoArena, "the arena could not be found")
		return
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		r.fail(ErrNoArena, "the arena could not be reached")
		return
	}
	r.conn, r.mm = conn, mm
	defer conn.Close()

	r.startedAt = time.Now()
	r.lastRecv = r.startedAt
	var nextFind, nextStatus time.Time
	buf := make([]byte, 4096)
	ticket := r.ticket()

	for {
		now := time.Now()
		stage := r.getStage()
		if stage == stDone {
			return
		}
		ghost := r.ghostNow()
		if r.isLeft() && !ghost {
			// left while connecting or in a match
			if stage == stInMatch && !r.liveAt.IsZero() && !r.resultSent {
				r.record(ResAbandoned, -1)
			}
			return
		}
		if ghost && now.Sub(r.startedAt) > cfg.GhostLife {
			return
		}
		switch stage {
		case stQueued:
			if now.Sub(r.startedAt) > cfg.QueueTimeout {
				r.fail(ErrTimeout, "no arena became free; try again in a moment")
				return
			}
			if !now.Before(nextFind) {
				r.udpSend(netHeader(pFindMatch, r.nextSeq(), nowMS()), mm)
				nextFind = now.Add(cfg.FindResend)
			}
			if !now.Before(nextStatus) {
				r.status(StSearching, now.Sub(r.startedAt))
				nextStatus = now.Add(time.Second)
			}
		case stConnecting:
			if now.Sub(r.connectAt) > cfg.ConnectTimeout {
				r.fail(ErrTimeout, "the arena did not answer")
				return
			}
			if now.Sub(r.sentConnect) >= cfg.ConnectResend {
				pkt := append(netHeader(pConnect, r.nextSeq(), nowMS()), ticket[:]...)
				r.mu.Lock()
				srv := r.srv
				r.mu.Unlock()
				r.udpSend(pkt, srv)
				r.sentConnect = now
			}
		case stInMatch:
			if now.Sub(r.lastRecv) > cfg.ServerSilence {
				if !r.liveAt.IsZero() && !r.resultSent {
					r.record(ResLost, -1)
				}
				r.fail(ErrArenaLost, "the arena stopped answering")
				return
			}
			r.maybeAutoPick(now)
			if r.dirty && now.Sub(r.lastFwd) >= cfg.SnapshotEvery {
				r.forward(now)
			}
		}

		_ = conn.SetReadDeadline(now.Add(40 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < netHeaderLen {
			continue
		}
		r.onPacket(buf[:n], from, time.Now())
		if r.getStage() == stDone {
			return
		}
	}
}

// ticket is the player's connect ticket, minted fresh for this search.
func (r *run) ticket() [TicketLen]byte {
	id, _ := parseUUID(r.c.ident.ID)
	return MintTicket(r.hub.cfg.Secret, id, uint32(time.Now().Add(r.hub.cfg.TicketTTL).Unix()))
}

func (r *run) onPacket(p []byte, from *net.UDPAddr, now time.Time) {
	typ := p[0]
	body := p[netHeaderLen:]
	stage := r.getStage()
	switch typ {
	case pMatchFound:
		if stage != stQueued || len(body) < 9 {
			return
		}
		port := int(binary.LittleEndian.Uint16(body[0:2]))
		if port == 0 {
			return
		}
		if r.ghostNow() || r.isLeft() {
			// The player already left. Not connecting means this lobby never fills and the arena shuts itself down
			// after its own 60 s lobby clock -- the bots simply requeue.
			r.setStage(stDone)
			return
		}
		r.mu.Lock()
		r.srv = &net.UDPAddr{IP: r.mm.IP, Port: port}
		r.stage = stConnecting
		r.mu.Unlock()
		r.setAcct(acPlaying)
		r.connectAt = now
		r.status(StConnecting, now.Sub(r.startedAt))
	case pWelcome:
		if stage != stConnecting || !r.fromServer(from) {
			return
		}
		r.mu.Lock()
		r.owner = int(p[1])
		r.lobby = r.pool.Lobby
		r.stage = stInMatch
		owner, lobby := r.owner, r.lobby
		r.mu.Unlock()
		r.lastRecv = now
		team := 0
		if lobby > 0 && owner >= lobby/2 {
			team = 1
		}
		r.send([]byte{SMatch, byte(owner), byte(lobby), byte(team), byte(r.mode)})
		r.status(StInMatch, now.Sub(r.startedAt))
	case pArenaSnapshot:
		if stage != stInMatch || !r.fromServer(from) || len(body) < worldLen {
			return
		}
		r.lastRecv = now
		copy(r.world[:], body[:worldLen])
		r.haveWorld = true
		r.dirty = true
		r.observe(now)
	case pSnapHeroes:
		if stage != stInMatch || !r.fromServer(from) || len(body) < heroesLen {
			return
		}
		r.lastRecv = now
		idx := int(body[0])
		if idx >= heroesPerMsg {
			return
		}
		copy(r.heroes[idx*heroesLen:], body[:heroesLen])
		r.haveHeroes[idx] = true
		r.dirty = true
	}
}

func (r *run) fromServer(from *net.UDPAddr) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.srv != nil && from != nil && from.IP.Equal(r.srv.IP) && from.Port == r.srv.Port
}

func (r *run) snap() *Snap {
	return &Snap{World: r.world[:], Heroes: r.heroes[:]}
}

// observe watches the world snapshot for the things the gateway tracks: when the match goes live, and who won.
func (r *run) observe(now time.Time) {
	s := r.snap()
	ph, win := s.Phase(), s.Winner()
	if ph == PhaseDraft && r.draftAt.IsZero() {
		r.draftAt = now
	}
	if ph == PhaseLive && r.liveAt.IsZero() {
		r.liveAt = now
	}
	r.lastPhase = ph
	if win != 0 && !r.resultSent {
		r.finishMatch(win, now)
	}
}

// maybeAutoPick: the arena throws the whole lobby away if the draft is still open when its 60 s lobby clock runs out
// (it starts when the server is spawned), so a player who is still choosing near the end gets a hero. Their own
// choice always wins if it got there first.
func (r *run) maybeAutoPick(now time.Time) {
	r.mu.Lock()
	picked := r.picked
	r.mu.Unlock()
	if picked || r.lastPhase != PhaseDraft {
		return
	}
	if now.Sub(r.connectAt) >= r.hub.cfg.PickDeadline {
		r.sendPick(randomHero())
	}
}

func (r *run) sendPick(hero int) {
	r.mu.Lock()
	r.picked = true
	srv := r.srv
	r.mu.Unlock()
	r.udpSend(append(netHeader(pArenaPick, r.nextSeq(), nowMS()), byte(hero)), srv)
}

func (r *run) forward(now time.Time) {
	if !r.haveWorld || !r.haveHeroes[0] || !r.haveHeroes[1] {
		return
	}
	r.dirty = false
	r.lastFwd = now
	f := make([]byte, 0, 1+worldLen+len(r.heroes))
	f = append(f, SSnap)
	f = append(f, r.world[:]...)
	f = append(f, r.heroes[:]...)
	r.send(f)
}

// finishMatch is the authoritative end: the arena says which team won.
func (r *run) finishMatch(winner int, now time.Time) {
	r.resultSent = true
	r.forward(now) // the final scoreboard
	s := r.snap()
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	me := s.Hero(owner)
	res := ResLoss
	if winner-1 == me.Team {
		res = ResWin
	}
	secs := 0
	if !r.liveAt.IsZero() {
		secs = int(now.Sub(r.liveAt) / time.Second)
	}
	recorded := r.recordWith(res, winner-1, secs)
	f := []byte{SResult, byte(winner), byte(res), byte(me.HeroID), byte(min(me.Kills, 255)), byte(min(me.Deaths, 255)), 0, 0, 0}
	binary.LittleEndian.PutUint16(f[6:], uint16(min(secs, 65535)))
	if recorded {
		f[8] = 1
	}
	r.send(f)
	r.setStage(stDone)
}

func (r *run) record(result, winnerTeam int) {
	secs := 0
	if !r.liveAt.IsZero() {
		secs = int(time.Since(r.liveAt) / time.Second)
	}
	r.resultSent = true
	r.recordWith(result, winnerTeam, secs)
}

func (r *run) recordWith(result, winnerTeam, secs int) bool {
	if r.hub.rec == nil || r.ghostNow() {
		return false
	}
	s := r.snap()
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	me := s.Hero(owner)
	roster := make([]HeroStat, 0, s.Count())
	for i := 0; i < s.Count() && i < maxRealHeroes; i++ {
		roster = append(roster, s.Hero(i))
	}
	rec := MatchRecord{
		PlayerID: r.c.ident.ID, Pool: r.pool.Name, Mode: r.mode, Lobby: s.Count(), Slot: owner, Team: me.Team,
		HeroID: me.HeroID, Result: result, WinnerTeam: winnerTeam, Kills: me.Kills, Deaths: me.Deaths,
		FlowEarned: me.FlowEarned, XP: me.XP, Seconds: secs, Roster: roster,
		Counted: (result == ResWin || result == ResLoss) && secs >= r.hub.cfg.MinCountedSecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.hub.rec.Record(ctx, rec) == nil
}

func (r *run) finish() {
	r.setStage(stDone)
	r.setAcct(acReleased)
	r.c.runEnded(r)
	if !r.ghostNow() && !r.isLeft() {
		r.c.status(StIdle, 0, 0, 0, 0)
	}
}

// command turns one browser order into the arena's UDP packet. Everything is validated here: the arena trusts a packet
// from a connected address, so the gateway is the only thing standing between a hand-made WebSocket frame and it.
func (r *run) command(m []byte) {
	r.mu.Lock()
	stage, owner, srv := r.stage, r.owner, r.srv
	r.mu.Unlock()
	if stage != stInMatch || srv == nil || owner < 0 {
		return
	}
	hdr := netHeader(0, r.nextSeq(), nowMS())
	hdr[1] = byte(owner)
	var pkt []byte
	switch m[0] {
	case CPick:
		if len(m) < 2 || m[1] > 30 {
			return
		}
		hdr[0] = pArenaPick
		pkt = append(hdr, m[1])
		r.mu.Lock()
		r.picked = true
		r.mu.Unlock()
	case CMove, CAMove:
		if len(m) < 9 {
			return
		}
		x, z := getF32(m, 1), getF32(m, 5)
		if !validCoord(x) || !validCoord(z) {
			return
		}
		hdr[0] = pArenaMove
		if m[0] == CAMove {
			hdr[0] = pAttackMove
		}
		pkt = putF32(putF32(hdr, x), z)
		pkt = append(pkt, byte(owner), 0, 0, 0)
	case CCast:
		if len(m) < 12 || m[1] > 2 {
			return
		}
		x, z := getF32(m, 4), getF32(m, 8)
		if !validCoord(x) || !validCoord(z) {
			x, z = 0, 0
		}
		hdr[0] = pArenaCast
		pkt = append(hdr, m[1], m[2], m[3], 0)
		pkt = putF32(putF32(pkt, x), z)
	case CAttack:
		if len(m) < 2 || int(m[1]) >= maxRealHeroes {
			return
		}
		hdr[0] = pArenaAttack
		pkt = append(hdr, m[1], byte(owner))
	case CBuy:
		if len(m) < 2 || m[1] >= 43 {
			return
		}
		hdr[0] = pShopBuy
		pkt = append(hdr, m[1])
	case CSell:
		if len(m) < 2 || m[1] >= 11 {
			return
		}
		hdr[0] = pShopSell
		pkt = append(hdr, m[1])
	case CActive:
		hdr[0] = pBlink
		pkt = hdr
	case CStop:
		hdr[0] = pStop
		pkt = append(hdr, byte(owner))
	case CHold:
		hdr[0] = pHold
		pkt = append(hdr, byte(owner))
	default:
		return
	}
	r.udpSend(pkt, srv)
}
