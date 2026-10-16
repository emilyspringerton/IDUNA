// Package shankpit460 is the matchmaker and match host for shankpit-460 web (WOTAN's browser build of the
// SHANKPIT-460 deathmatch). Founder real-time, 2026-10-16: "queue them into a bot match and say SEARCHING FOR
// MATCH; if there is at least 1 other human in matchmaking put them in a ffa lobby ... human matches are first
// to 16 or they end at 6 minutes".
//
// The browser plays a bot match locally the moment someone presses PLAY and opens one WebSocket here to ask for a
// human match. This package pairs searching humans into free-for-all rooms and then owns what a competitive match
// needs a referee for: the clock, the kill limit, health and shields, kills and deaths, respawns and spawn
// protection. Players' own movement is client-simulated (the engine is bit-for-bit the C engine's physics) and
// relayed through the room; a shot is a *claim* ("I hit slot 3 with the AR, head") that the room checks --
// weapon table, rate of fire, range, line of sight on the arena's real geometry -- before it moves anyone's
// health. That is a referee, not a full authoritative simulation: see the README for what it does and does not
// stop.
//
// Wire format: binary WebSocket frames, little-endian. The first byte is the message type.
//
//	client -> server
//	  0x01 HELLO  ver u8 | tokenLen u16 | token | nameLen u8 | name        (token may be empty: a guest)
//	  0x02 QUEUE                                                           start searching
//	  0x03 LEAVE                                                           stop searching / leave the room
//	  0x10 STATE  seq u16 | x y z yaw pitch f32 | weapon u8 | flags u8     ~30 Hz while in a match
//	  0x11 FIRE   weapon u8 | origin f32x3 | dir f32x3                     a shot, for everyone else's tracers
//	  0x12 HIT    victim u8 | weapon u8 | head u8                          a claim; the room decides
//	  0x13 PING   t u32
//	server -> client
//	  0x81 WELCOME ver u8 | auth u8 | nameLen u8 | name | online u16        auth: 0 guest, 1 signed in, 2 token rejected
//	  0x82 STATUS  qstate u8 | searching u8 | online u16 | elapsed u16      qstate: 0 idle 1 searching 2 countdown 3 live
//	  0x83 MATCH   roomId u32 | slot u8 | killLimit u8 | durationSec u16 | phase u8 | msToStart u16 | msLeft u32 | roster
//	  0x84 SNAP    msLeft u32 | n u8 | n x {slot u8, x y z yaw pitch f32, weapon u8, flags u8, health u8, shield u8,
//	                                         kills u8, deaths u8, state u8}
//	  0x85 EVENT   1 DAMAGE victim attacker amount health shield head weapon | 2 KILL attacker victim weapon |
//	               3 RESPAWN slot x y z yaw | 4 FIRE shooter weapon origin dir | 5 LIVE msLeft u32
//	  0x86 ROSTER  roster
//	  0x87 END     reason u8 | winner u8 | n u8 | n x {slot, kills, deaths}      reason: 1 kills 2 time 3 abandoned
//	  0x88 PONG    t u32
//	  0x8F ERROR   code u8 | msgLen u8 | msg
//
// roster = n u8 | n x {slot u8, color u8, nameLen u8, name, spawn x y z yaw f32}
package shankpit460

import (
	"encoding/binary"
	"math"
)

const Version = 1

// message types
const (
	CHello = 0x01
	CQueue = 0x02
	CLeave = 0x03
	CState = 0x10
	CFire  = 0x11
	CHit   = 0x12
	CPing  = 0x13

	SWelcome = 0x81
	SStatus  = 0x82
	SMatch   = 0x83
	SSnap    = 0x84
	SEvent   = 0x85
	SRoster  = 0x86
	SEnd     = 0x87
	SPong    = 0x88
	SError   = 0x8F
)

// EVENT sub-types
const (
	EvDamage  = 1
	EvKill    = 2
	EvRespawn = 3
	EvFire    = 4
	EvLive    = 5
)

// queue states reported in STATUS
const (
	QIdle      = 0
	QSearching = 1
	QCountdown = 2
	QLive      = 3
)

// END reasons
const (
	EndKills     = 1
	EndTime      = 2
	EndAbandoned = 3
)

// player states in SNAP
const (
	StAlive = 0
	StDead  = 1
)

// weapons: ids 0-5 are the engine's own; 6 is the sniper's storm projectile, 7 the katana dash
const (
	WKnife = iota
	WMagnum
	WAR
	WShotgun
	WSniper
	WKatana
	WStorm
	WDash
	numWeapons
)

var weaponDamage = [numWeapons]int{200, 45, 20, 128, 101, 40, 70, 50}

// rate of fire in 16 ms ticks (WPN_STATS.rof; the storm shoots every 8, the dash is on a 420 tick cooldown)
var weaponROF = [numWeapons]int{20, 25, 6, 17, 70, 28, 8, 420}

// the farthest a claim may reach, in arena units (the knife's real reach is sqrt(272) = 16.5)
var weaponRange = [numWeapons]float32{22, 900, 900, 900, 900, 9, 900, 16}

type writer struct{ b []byte }

func (w *writer) u8(v int)      { w.b = append(w.b, byte(v)) }
func (w *writer) u16(v int)     { w.b = binary.LittleEndian.AppendUint16(w.b, uint16(v)) }
func (w *writer) u32(v uint32)  { w.b = binary.LittleEndian.AppendUint32(w.b, v) }
func (w *writer) f32(v float32) { w.b = binary.LittleEndian.AppendUint32(w.b, math.Float32bits(v)) }
func (w *writer) str(s string) {
	if len(s) > 255 {
		s = s[:255]
	}
	w.u8(len(s))
	w.b = append(w.b, s...)
}

type reader struct {
	b   []byte
	off int
	bad bool
}

func (r *reader) need(n int) bool {
	if r.bad || r.off+n > len(r.b) {
		r.bad = true
		return false
	}
	return true
}
func (r *reader) u8() int {
	if !r.need(1) {
		return 0
	}
	v := r.b[r.off]
	r.off++
	return int(v)
}
func (r *reader) u16() int {
	if !r.need(2) {
		return 0
	}
	v := binary.LittleEndian.Uint16(r.b[r.off:])
	r.off += 2
	return int(v)
}
func (r *reader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}
func (r *reader) f32() float32 {
	return math.Float32frombits(r.u32())
}
func (r *reader) bytes(n int) []byte {
	if !r.need(n) {
		return nil
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v
}

// cleanName makes a display name the web engine's 8x16 ASCII font can draw: printable ASCII only, 1..20 chars.
func cleanName(s string) string {
	out := make([]byte, 0, 20)
	for _, c := range []byte(s) {
		if len(out) >= 20 {
			break
		}
		switch {
		case c >= 0x20 && c < 0x7f:
			out = append(out, c)
		case c >= 0x80:
			// a multi-byte character: one placeholder for its lead byte, none for the continuation bytes
			if c >= 0xc0 {
				out = append(out, '?')
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && out[0] == ' ' {
		out = out[1:]
	}
	return string(out)
}

func finite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }
