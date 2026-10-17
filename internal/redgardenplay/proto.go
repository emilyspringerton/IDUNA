// Package redgardenplay is the browser front of REDGARDEN's live arena: WOTAN's "Play" page (a PARENA wasm client)
// talks to one WebSocket here, and this package does what a browser cannot -- it speaks the arena's UDP protocol.
//
// Founder real-time, 2026-10-17: "publish redgarden web ... FULL WOTAN MATCH AND PLAYER TRACKING ... allow for guest
// play ... integrate with iduna sso ... use existing bot match infrastructure and bot pool". The infrastructure that
// already exists is REDGARDEN's matchmaker + 19-bot persistent pool (3v3 on :7780, 10v10 on :7778 / :8778) and the
// authoritative C arena server it spawns per match. Nothing about the game is re-implemented here or in the browser:
//
//	browser (wasm client)  <-- WebSocket (this file) -->  gateway  <-- UDP (REDGARDEN protocol.h) -->  matchmaker / arena_server
//
// The gateway mints the player's connect ticket itself (same HMAC scheme as /api/v1/redgarden/self-ticket and
// apps/arena_server's verify_connect_ticket), queues with the matchmaker, connects to the game server it names, relays
// the player's commands and a throttled snapshot stream, and -- because it sees the authoritative snapshots -- records
// the result (wins/losses, hero, kills/deaths, duration) against the player's IDUNA account.
//
// Wire format (binary WebSocket frames, little-endian, the first byte is the type):
//
//	client -> gateway
//	  0x01 HELLO   ver u8 | tokenLen u16 | token                     first frame; the IDUNA bearer token (game or SSO)
//	  0x02 QUEUE   mode u8                                           0 = 3v3, 1 = 10v10: find a match
//	  0x03 LEAVE                                                     cancel the search / leave the match
//	  0x10 PICK    hero u8
//	  0x11 MOVE    x f32 | z f32
//	  0x12 CAST    slot u8 | hover i8 | hasGround u8 | x f32 | z f32  slot 0 = Q, 1 = W (toggle), 2 = E (ultimate)
//	  0x13 ATTACK  owner u8                                          lock onto and auto-attack that hero
//	  0x14 BUY     item u8
//	  0x15 SELL    slot u8
//	  0x16 ACTIVE                                                    blink dagger / donkey glide
//	  0x17 STOP
//	  0x18 AMOVE   x f32 | z f32                                     attack-move
//	  0x19 HOLD
//	  0x1F PING    t u32
//	gateway -> client
//	  0x81 WELCOME ver u8 | auth u8 | nameLen u8 | name | online u16   auth: 1 signed in, 2 token rejected
//	  0x82 STATUS  state u8 | mode u8 | lobby u8 | elapsed u16 | ahead u8   state: 0 idle 1 searching 2 connecting 3 in match
//	  0x83 MATCH   owner u8 | lobby u8 | team u8 | mode u8             you are hero slot `owner`; team 0 or 1
//	  0x84 SNAP    world[1244] | heroes0[1460] | heroes1[1460]         the arena's own snapshot bodies, ~20 Hz
//	  0x85 RESULT  winner u8 | result u8 | hero u8 | kills u8 | deaths u8 | secs u16 | recorded u8
//	               result: 1 win, 2 loss, 3 abandoned, 4 arena lost
//	  0x86 ERROR   code u8 | msgLen u8 | msg
//	  0x8F PONG    t u32
package redgardenplay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Version of the browser<->gateway protocol.
const Version = 1

// browser -> gateway
const (
	CHello  = 0x01
	CQueue  = 0x02
	CLeave  = 0x03
	CPick   = 0x10
	CMove   = 0x11
	CCast   = 0x12
	CAttack = 0x13
	CBuy    = 0x14
	CSell   = 0x15
	CActive = 0x16
	CStop   = 0x17
	CAMove  = 0x18
	CHold   = 0x19
	CPing   = 0x1F
)

// gateway -> browser
const (
	SWelcome = 0x81
	SStatus  = 0x82
	SMatch   = 0x83
	SSnap    = 0x84
	SResult  = 0x85
	SError   = 0x86
	SPong    = 0x8F
)

// STATUS states
const (
	StIdle       = 0
	StSearching  = 1
	StConnecting = 2
	StInMatch    = 3
)

// RESULT results
const (
	ResWin       = 1
	ResLoss      = 2
	ResAbandoned = 3
	ResLost      = 4
)

// ERROR codes
const (
	ErrBadHello   = 1
	ErrBusy       = 2
	ErrNoArena    = 3
	ErrTimeout    = 4
	ErrArenaLost  = 5
	ErrRateLimit  = 6
	ErrNotInMatch = 7
	ErrBadRequest = 8
)

// ---- REDGARDEN's UDP protocol (packages/common/protocol.h; sizes checked against the real C structs) -------------

const (
	pConnect       = 0
	pWelcome       = 3
	pFindMatch     = 4
	pMatchFound    = 5
	pArenaMove     = 6
	pArenaCast     = 7
	pArenaSnapshot = 8
	pArenaPick     = 9
	pArenaAttack   = 10
	pShopBuy       = 11
	pShopSell      = 12
	pBlink         = 13
	pSnapHeroes    = 14
	pStop          = 15
	pAttackMove    = 16
	pHold          = 17

	netHeaderLen = 12 // NetHeader: type u8, client_id u8, sequence u16, timestamp u32, entity_count u16, 2 pad

	worldLen      = 1244 // sizeof(ArenaSnapshotMsg)
	heroesLen     = 1460 // sizeof(ArenaSnapshotHeroesMsg): chunk_index, total_count, 2 pad, 14 x ArenaHeroSnapshot
	heroSnapLen   = 104  // sizeof(ArenaHeroSnapshot)
	heroChunkSize = 14
	heroesPerMsg  = 2
	maxRealHeroes = 20

	ticketPayloadLen = 20
	ticketMacLen     = 16
	TicketLen        = ticketPayloadLen + ticketMacLen

	// ArenaSnapshotMsg field offsets
	offCount  = 0
	offWinner = 1
	offPhase  = 2
	offRes    = 852

	// ArenaHeroSnapshot field offsets
	hX       = 0
	hZ       = 4
	hHP      = 8
	hMaxHP   = 10
	hAlive   = 12
	hHeroID  = 13
	hFlowEar = 28
	hXP      = 30
	hKills   = 32
	hDeaths  = 33
)

// Arena phases (ARENA_PHASE_*)
const (
	PhaseWaiting = 0
	PhaseDraft   = 1
	PhaseLive    = 2
)

// MintTicket is apps/arena_server's connect ticket: the player's UUID (16 raw bytes) and an expiry (unix seconds,
// u32 LE), followed by the first 16 bytes of HMAC-SHA256(secret, payload). Same bytes /api/v1/redgarden/self-ticket
// hands the native client, hex-decoded.
func MintTicket(secret []byte, player [16]byte, expires uint32) [TicketLen]byte {
	var t [TicketLen]byte
	copy(t[0:16], player[:])
	binary.LittleEndian.PutUint32(t[16:20], expires)
	m := hmac.New(sha256.New, secret)
	m.Write(t[:ticketPayloadLen])
	copy(t[ticketPayloadLen:], m.Sum(nil)[:ticketMacLen])
	return t
}

func netHeader(typ byte, seq uint16, ms uint32) []byte {
	h := make([]byte, netHeaderLen)
	h[0] = typ
	binary.LittleEndian.PutUint16(h[2:], seq)
	binary.LittleEndian.PutUint32(h[4:], ms)
	return h
}

func putF32(b []byte, v float32) []byte {
	var x [4]byte
	binary.LittleEndian.PutUint32(x[:], math.Float32bits(v))
	return append(b, x[:]...)
}

func getF32(b []byte, off int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))
}

func validCoord(v float32) bool {
	f := float64(v)
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f > -200 && f < 200
}

// Snap is the part of a snapshot the gateway itself reads (to track the match); the rest is relayed untouched.
type Snap struct {
	World  []byte // worldLen bytes
	Heroes []byte // heroesPerMsg * heroesLen bytes
}

func (s *Snap) Count() int  { return int(s.World[offCount]) }
func (s *Snap) Winner() int { return int(s.World[offWinner]) }
func (s *Snap) Phase() int  { return int(s.World[offPhase]) }

func (s *Snap) hero(slot int) []byte {
	chunk := slot / heroChunkSize
	i := slot % heroChunkSize
	if chunk >= heroesPerMsg {
		return nil
	}
	base := chunk*heroesLen + 4 + i*heroSnapLen
	return s.Heroes[base : base+heroSnapLen]
}

// HeroStat is one hero slot's tracked numbers.
type HeroStat struct {
	Slot, Team, HeroID, Kills, Deaths int
	FlowEarned, XP                    int
}

// TeamSize is ARENA_TEAM_SIZE: apps/arena_server's arena_init_teams puts slots 0..9 on team 0 and 10..19 on team 1
// whatever the lobby size is (so a 6-player "3v3" lobby is six heroes on team 0 with nobody to fight -- which is why the
// web client offers only the 10v10 pools).
const TeamSize = 10

// TeamOf is the server's team for a hero slot.
func TeamOf(slot int) int {
	if slot >= TeamSize {
		return 1
	}
	return 0
}

// Hero reads slot's numbers.
func (s *Snap) Hero(slot int) HeroStat {
	h := s.hero(slot)
	n := s.Count()
	st := HeroStat{Slot: slot}
	if h == nil || slot < 0 || slot >= n {
		return st
	}
	st.Team = TeamOf(slot)
	st.HeroID = int(h[hHeroID])
	st.Kills = int(h[hKills])
	st.Deaths = int(h[hDeaths])
	st.FlowEarned = int(binary.LittleEndian.Uint16(h[hFlowEar:]))
	st.XP = int(binary.LittleEndian.Uint16(h[hXP:]))
	return st
}
