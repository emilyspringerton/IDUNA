package shankpit460

import (
	"math"
	"sort"
	"time"
)

const (
	phaseCountdown = 1
	phaseLive      = 2
	phaseOver      = 3
)

type pos struct{ x, y, z float32 }

// player is one human in a room.
type player struct {
	c     *Client
	slot  int
	name  string
	color int

	x, y, z, yaw, pitch float32
	weapon, flags       int

	alive           bool
	health, shield  int
	kills, deaths   int
	respawnAt       time.Time
	protUntil       time.Time
	lastState       time.Time
	freeMoveUntil   time.Time
	strikes         int
	viol, totalViol int

	hist     [8]pos
	histN    int
	lastHist time.Time

	lastClaim  map[int]time.Time
	claimWin   time.Time
	claimCount int
}

// Room is one free-for-all match.
type Room struct {
	hub      *Hub
	id       uint32
	phase    int
	created  time.Time
	liveAt   time.Time
	endsAt   time.Time
	players  [10]*player
	finished bool
	lastSnap time.Time
	topKills int
}

func newRoom(h *Hub, id uint32, now time.Time) *Room {
	return &Room{hub: h, id: id, phase: phaseCountdown, created: now, liveAt: now.Add(h.cfg.Countdown)}
}

func (r *Room) count() int {
	n := 0
	for _, p := range r.players {
		if p != nil {
			n++
		}
	}
	return n
}

func (r *Room) joinable(now time.Time) bool {
	if r.finished || r.count() >= r.hub.cfg.MaxPlayers || r.count() >= len(r.players) {
		return false
	}
	switch r.phase {
	case phaseCountdown:
		return true
	case phaseLive:
		return now.Sub(r.liveAt) < r.hub.cfg.LateJoinCutoff && r.count() >= 1
	}
	return false
}

func (r *Room) msLeft(now time.Time) uint32 {
	if r.phase == phaseLive {
		d := r.endsAt.Sub(now)
		if d < 0 {
			d = 0
		}
		return uint32(d / time.Millisecond)
	}
	return uint32(r.hub.cfg.Duration / time.Millisecond)
}

// pickSpawn is the safest spawn: the one farthest from every other living player, with a little jitter.
func (r *Room) pickSpawn(except *player) (Spawn, float32) {
	sp := r.hub.arena.Spawns
	best, bi := float32(-1), 0
	for i, s := range sp {
		min := float32(1e9)
		for _, o := range r.players {
			if o == nil || o == except || !o.alive && r.phase == phaseLive {
				continue
			}
			dx, dz := o.x-s.X, o.z-s.Z
			if d := dx*dx + dz*dz; d < min {
				min = d
			}
		}
		score := min + r.hub.rng.Float32()*400
		if score > best {
			best, bi = score, i
		}
	}
	s := sp[bi]
	// face the middle of the arena
	yaw := -math.Atan2(float64(-s.X), float64(s.Z)) * 180 / math.Pi
	for yaw < 0 {
		yaw += 360
	}
	return s, float32(math.Mod(yaw, 360))
}

func (p *player) place(s Spawn, yaw float32, now time.Time, prot time.Duration) {
	p.x, p.y, p.z, p.yaw, p.pitch = s.X, s.Y, s.Z, yaw, 0
	p.alive = true
	p.health, p.shield = 100, 100
	p.protUntil = now.Add(prot)
	p.freeMoveUntil = now.Add(600 * time.Millisecond)
	p.weapon = WMagnum
	p.histN = 0
}

func (r *Room) join(c *Client, now time.Time) {
	slot := -1
	for i := range r.players {
		if r.players[i] == nil {
			slot = i
			break
		}
	}
	if slot < 0 {
		return
	}
	colorUsed := map[int]bool{}
	for _, o := range r.players {
		if o != nil {
			colorUsed[o.color] = true
		}
	}
	color := slot
	for k := 0; k < 10 && colorUsed[color]; k++ {
		color = (color + 1) % 10
	}
	p := &player{c: c, slot: slot, name: c.id.Name, color: color, lastClaim: map[int]time.Time{}, lastState: now}
	s, yaw := r.pickSpawn(nil)
	p.place(s, yaw, now, 0)
	if r.phase == phaseLive {
		p.protUntil = now.Add(r.hub.cfg.SpawnProtect)
	}
	r.players[slot] = p
	c.room, c.slot = r, slot
	if r.phase == phaseLive {
		c.state = QLive
	} else {
		c.state = QCountdown
	}
	// MATCH to the newcomer
	w := &writer{}
	w.u8(SMatch)
	w.u32(r.id)
	w.u8(slot)
	w.u8(r.hub.cfg.KillLimit)
	w.u16(int(r.hub.cfg.Duration / time.Second))
	w.u8(r.phase)
	ms := r.liveAt.Sub(now) / time.Millisecond
	if ms < 0 || r.phase == phaseLive {
		ms = 0
	}
	w.u16(int(ms))
	w.u32(r.msLeft(now))
	r.writeRoster(w)
	c.send(w.b)
	// everyone else hears the roster change; in a live match they also see the arrival appear
	rw := &writer{}
	rw.u8(SRoster)
	r.writeRoster(rw)
	for _, o := range r.players {
		if o != nil && o != p {
			o.c.send(rw.b)
		}
	}
	if r.phase == phaseLive {
		r.broadcastRespawn(p)
	}
}

func (r *Room) writeRoster(w *writer) {
	n := r.count()
	w.u8(n)
	for _, p := range r.players {
		if p == nil {
			continue
		}
		w.u8(p.slot)
		w.u8(p.color)
		w.str(p.name)
		w.f32(p.x)
		w.f32(p.y)
		w.f32(p.z)
		w.f32(p.yaw)
	}
}

func (r *Room) broadcast(f []byte) {
	for _, p := range r.players {
		if p != nil {
			p.c.send(f)
		}
	}
}

func (r *Room) broadcastRespawn(p *player) {
	w := &writer{}
	w.u8(SEvent)
	w.u8(EvRespawn)
	w.u8(p.slot)
	w.f32(p.x)
	w.f32(p.y)
	w.f32(p.z)
	w.f32(p.yaw)
	r.broadcast(w.b)
}

// leave removes a player; called with the hub lock held.
func (r *Room) leave(c *Client) {
	if c.slot < 0 || c.slot >= len(r.players) || r.players[c.slot] == nil || r.players[c.slot].c != c {
		return
	}
	r.players[c.slot] = nil
	remaining := r.count()
	if remaining > 0 {
		rw := &writer{}
		rw.u8(SRoster)
		r.writeRoster(rw)
		r.broadcast(rw.b)
	}
	switch {
	case r.phase == phaseCountdown && remaining < 2:
		// the match never started: send whoever is left back into the search
		r.finished = true
		for _, p := range r.players {
			if p != nil {
				p.c.room, p.c.slot = nil, -1
				r.players[p.slot] = nil
				p.c.state = QSearching
				p.c.queuedAt = r.hub.now()
				r.hub.searching = append([]*Client{p.c}, r.hub.searching...)
				r.hub.sendStatusLocked(p.c)
			}
		}
	case r.phase == phaseLive && remaining < 2:
		r.end(r.hub.now(), EndAbandoned)
	case remaining == 0:
		r.finished = true
	}
}

// ---- messages from players ---------------------------------------------------------------------------------

func (r *Room) handle(c *Client, frame []byte) {
	p := r.players[c.slot]
	if p == nil || p.c != c {
		return
	}
	now := r.hub.now()
	rd := &reader{b: frame, off: 1}
	switch frame[0] {
	case CState:
		rd.u16() // seq
		x, y, z, yaw, pitch := rd.f32(), rd.f32(), rd.f32(), rd.f32(), rd.f32()
		weapon, flags := rd.u8(), rd.u8()
		if rd.bad || r.phase != phaseLive || !p.alive {
			return
		}
		if !finite(x) || !finite(y) || !finite(z) || !finite(yaw) || !finite(pitch) || !r.hub.arena.Inside(x, y, z) {
			p.strikes++
			return
		}
		// a speed limit: the katana dash is 5.8 units per 16 ms tick (362/s); everything else is far slower. The
		// budget is capped at a quarter second so a long silence does not buy a long jump. A client that really
		// stalled (a background tab) and resumes elsewhere is believed after three updates in a row; a client that
		// keeps doing it is dropped.
		dt := now.Sub(p.lastState).Seconds()
		if dt < 0.005 {
			dt = 0.005
		}
		if dt > 0.25 {
			dt = 0.25
		}
		d := math.Sqrt(float64((x-p.x)*(x-p.x) + (z-p.z)*(z-p.z)))
		if now.After(p.freeMoveUntil) && d > 460*dt+14 {
			p.viol++
			p.totalViol++
			p.strikes++
			if p.totalViol > 12 {
				go c.Close()
				return
			}
			if p.viol < 3 {
				return
			}
		}
		p.viol = 0
		p.lastState = now
		p.x, p.y, p.z, p.yaw, p.pitch = x, y, z, yaw, pitch
		if weapon >= 0 && weapon <= WKatana {
			p.weapon = weapon
		}
		p.flags = flags
		if now.Sub(p.lastHist) >= 40*time.Millisecond {
			p.lastHist = now
			p.hist[p.histN%len(p.hist)] = pos{x, y, z}
			p.histN++
		}
	case CFire:
		w := rd.u8()
		var v [6]float32
		for i := range v {
			v[i] = rd.f32()
		}
		if rd.bad || r.phase != phaseLive || !p.alive || w >= numWeapons || !finite(v[0]) || !finite(v[3]) {
			return
		}
		out := &writer{}
		out.u8(SEvent)
		out.u8(EvFire)
		out.u8(p.slot)
		out.u8(w)
		for _, f := range v {
			out.f32(f)
		}
		for _, o := range r.players {
			if o != nil && o != p {
				o.c.send(out.b)
			}
		}
	case CHit:
		v, w, head := rd.u8(), rd.u8(), rd.u8()
		if rd.bad {
			return
		}
		r.claim(p, v, w, head != 0, now)
	}
}

// claim is a shot's referee. A claim moves health only if the weapon exists, the shooter could have fired it by
// now, the victim was within reach and not behind a wall (at any pose they held in the last ~300 ms, which is what
// latency can make a shooter's screen disagree by), and the victim is not spawn-protected.
func (r *Room) claim(p *player, v, w int, head bool, now time.Time) {
	if r.phase != phaseLive || !p.alive || w < 0 || w >= numWeapons || v < 0 || v >= len(r.players) {
		return
	}
	t := r.players[v]
	if t == nil || t == p || !t.alive || now.Before(t.protUntil) {
		return
	}
	// a flood of claims is a cheat or a bug
	if now.Sub(p.claimWin) > time.Second {
		p.claimWin, p.claimCount = now, 0
	}
	if p.claimCount++; p.claimCount > 60 {
		return
	}
	key := v<<8 | w
	min := time.Duration(weaponROF[w]) * 16 * time.Millisecond * 6 / 10
	if w == WDash {
		min = 4 * time.Second
	}
	if last, ok := p.lastClaim[key]; ok && now.Sub(last) < min {
		return
	}
	// reach (a shooter's screen shows the victim ~100 ms ago, hence the slack)
	dx, dy, dz := t.x-p.x, t.y-p.y, t.z-p.z
	if float32(math.Sqrt(float64(dx*dx+dy*dy+dz*dz))) > weaponRange[w]+9 {
		return
	}
	if !r.lineOK(p, t) {
		return
	}
	p.lastClaim[key] = now
	dmg := weaponDamage[w]
	if head && w <= WSniper && t.shield <= 0 {
		dmg *= 3
	}
	r.damage(p, t, dmg, head, w, now)
}

// lineOK: is there a clear line from the shooter's eye to the victim at any recent pose?
func (r *Room) lineOK(p, t *player) bool {
	a := r.hub.arena
	eye := func(q pos) pos { return pos{q.x, q.y + 2.59, q.z} }
	// the shooter's own screen is current; it is the victim they see a little late
	shooters := []pos{{p.x, p.y, p.z}}
	targets := []pos{{t.x, t.y, t.z}}
	for i := 1; i <= 6 && i <= t.histN; i++ {
		targets = append(targets, t.hist[(t.histN-i)%len(t.hist)])
	}
	for _, s := range shooters {
		e := eye(s)
		for _, q := range targets {
			if a.LineClear(e.x, e.y, e.z, q.x, q.y+2.2, q.z) {
				return true
			}
		}
	}
	return false
}

// damage: the shield soaks first, then health; a kill is credited and the victim is down for the respawn delay.
func (r *Room) damage(att, tgt *player, dmg int, head bool, w int, now time.Time) {
	rest := dmg
	if tgt.shield > 0 {
		if tgt.shield >= rest {
			tgt.shield -= rest
			rest = 0
		} else {
			rest -= tgt.shield
			tgt.shield = 0
		}
	}
	tgt.health -= rest
	if tgt.health < 0 {
		tgt.health = 0
	}
	amount := dmg
	if amount > 255 {
		amount = 255
	}
	hb := 0
	if head {
		hb = 1
	}
	ev := &writer{}
	ev.u8(SEvent)
	ev.u8(EvDamage)
	ev.u8(tgt.slot)
	ev.u8(att.slot)
	ev.u8(amount)
	ev.u8(tgt.health)
	ev.u8(tgt.shield)
	ev.u8(hb)
	ev.u8(w)
	att.c.send(ev.b)
	tgt.c.send(ev.b)
	if tgt.health > 0 {
		return
	}
	att.kills++
	tgt.deaths++
	tgt.alive = false
	tgt.respawnAt = now.Add(r.hub.cfg.Respawn)
	if att.kills > r.topKills {
		r.topKills = att.kills
	}
	k := &writer{}
	k.u8(SEvent)
	k.u8(EvKill)
	k.u8(att.slot)
	k.u8(tgt.slot)
	k.u8(w)
	r.broadcast(k.b)
	if att.kills >= r.hub.cfg.KillLimit {
		r.end(now, EndKills)
	}
}

// ---- the clock ------------------------------------------------------------------------------------------------

func (r *Room) tick(now time.Time) {
	switch r.phase {
	case phaseCountdown:
		if !now.Before(r.liveAt) {
			r.goLive(now)
		}
	case phaseLive:
		for _, p := range r.players {
			if p != nil && !p.alive && !now.Before(p.respawnAt) {
				s, yaw := r.pickSpawn(p)
				p.place(s, yaw, now, r.hub.cfg.SpawnProtect)
				r.broadcastRespawn(p)
			}
		}
		if !now.Before(r.endsAt) {
			r.end(now, EndTime)
			return
		}
		if now.Sub(r.lastSnap) >= r.hub.cfg.SnapshotEvery {
			r.lastSnap = now
			r.snapshot(now)
		}
	case phaseOver:
		r.finished = true
	}
}

func (r *Room) goLive(now time.Time) {
	r.phase = phaseLive
	r.liveAt = now
	r.endsAt = now.Add(r.hub.cfg.Duration)
	// everyone starts from distinct spawns, full health, a second of protection
	taken := map[int]bool{}
	_ = taken
	for _, p := range r.players {
		if p == nil {
			continue
		}
		s, yaw := r.pickSpawn(p)
		p.place(s, yaw, now, r.hub.cfg.SpawnProtect)
		p.c.state = QLive
	}
	w := &writer{}
	w.u8(SEvent)
	w.u8(EvLive)
	w.u32(r.msLeft(now))
	r.broadcast(w.b)
	for _, p := range r.players {
		if p != nil {
			r.broadcastRespawn(p)
		}
	}
}

func (r *Room) snapshot(now time.Time) {
	w := &writer{}
	w.u8(SSnap)
	w.u32(r.msLeft(now))
	w.u8(r.count())
	for _, p := range r.players {
		if p == nil {
			continue
		}
		w.u8(p.slot)
		w.f32(p.x)
		w.f32(p.y)
		w.f32(p.z)
		w.f32(p.yaw)
		w.f32(p.pitch)
		w.u8(p.weapon)
		w.u8(p.flags)
		w.u8(p.health)
		w.u8(p.shield)
		w.u8(minInt(p.kills, 255))
		w.u8(minInt(p.deaths, 255))
		if p.alive {
			w.u8(StAlive)
		} else {
			w.u8(StDead)
		}
	}
	r.broadcast(w.b)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// end closes the match: the standings go to everyone, and every client is released to requeue.
func (r *Room) end(now time.Time, reason int) {
	if r.phase == phaseOver {
		return
	}
	r.phase = phaseOver
	type row struct{ slot, k, d int }
	var rows []row
	for _, p := range r.players {
		if p != nil {
			rows = append(rows, row{p.slot, p.kills, p.deaths})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].k != rows[j].k {
			return rows[i].k > rows[j].k
		}
		if rows[i].d != rows[j].d {
			return rows[i].d < rows[j].d
		}
		return rows[i].slot < rows[j].slot
	})
	winner := 255
	if len(rows) > 0 && (reason != EndTime || len(rows) == 1 || rows[0].k != rows[1].k || rows[0].d != rows[1].d) {
		winner = rows[0].slot
	}
	if reason == EndAbandoned && len(rows) > 0 {
		winner = 255
	}
	w := &writer{}
	w.u8(SEnd)
	w.u8(reason)
	w.u8(winner)
	w.u8(len(rows))
	for _, x := range rows {
		w.u8(x.slot)
		w.u8(minInt(x.k, 255))
		w.u8(minInt(x.d, 255))
	}
	for _, p := range r.players {
		if p != nil {
			p.c.send(w.b)
			p.c.room, p.c.slot = nil, -1
			p.c.state = QIdle
		}
	}
	r.players = [10]*player{}
	r.finished = true
}

func (r *Room) stats(now time.Time) RoomStats {
	ph := "countdown"
	left := int(r.liveAt.Sub(now) / time.Second)
	if r.phase == phaseLive {
		ph = "live"
		left = int(r.msLeft(now) / 1000)
	}
	if left < 0 {
		left = 0
	}
	return RoomStats{ID: r.id, Players: r.count(), Phase: ph, SecondsLeft: left, TopKills: r.topKills}
}
