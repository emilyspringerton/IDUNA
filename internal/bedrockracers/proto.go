// Package bedrockracers is IDUNA's side of BEDROCK RACERS web (WOTAN /bedrockracers/): the lockstep room and the referee.
//
// The game is ONE wasm module (PARENA/stdlib/bedrockracers/web, built by scripts/build_bedrockracers_web.sh) that the browsers play and that this
// package steps headlessly in wazero. A match is a deterministic function of (seed, level, which slots are people) and, every 16 ms tick, eight
// command bytes per person. So the server does not simulate on the clients' behalf and does not trust anything they say about the world: it
// SEQUENCES. Each tick it takes every person's newest command, writes them into its own copy of the module, steps it, and broadcasts one TICK frame
// carrying those bytes; every browser steps the same frames through the same module and arrives at the same match. Bots are part of the sim and
// cost nothing on the wire. The result that is recorded (the score, who scored, the rating) is what THIS copy reached, so a browser can lie about
// nothing but its own commands.
//
// A person who leaves becomes a bot at an exact tick (a "drop" in the tick frame), in every copy at once. Lockstep needs the same module
// everywhere, so HELLO carries the sim version of the module the browser was built with and the server refuses any other.
//
// Wire format: binary WebSocket frames, little-endian. The first byte is the message type. (The client side is PARENA/stdlib/bedrockracers/web/br_net.prn.)
//
//	client -> server
//	  0x01 HELLO  ver u8 | simVer u32 | tokenLen u16 | token | nameLen u8 | name          (token may be empty: no stats, a guest by name only)
//	  0x02 QUEUE  team u8 (0..3, 255 = any) | level u8 (1..3)                             start searching
//	  0x03 LEAVE                                                                          stop searching, or leave the match (a bot takes the car)
//	  0x10 CMD    8 command bytes                                                         the newest wins; thr steer pitch roll strafe (signed /127), lift (/255), flags, 0
//	  0x13 PING   t u32
//	  0x14 HASH   ticks u32 | hash u32                                                    every 250 ticks the browser played: compared with this copy's
//	server -> client
//	  0x81 WELCOME ver u8 | auth u8 | simVer u32 | nameLen u8 | name | online u16 | pidLen u8 | pid     auth: 0 guest, 1 signed in, 2 token rejected
//	  0x82 STATUS  qstate u8 | searching u8 | online u16 | elapsed u16 | lobbyHumans u8 | lobbyMs u16   qstate: 0 idle 1 searching 2 lobby counting down 3 live
//	  0x83 MATCH   roomId u32 | slot u8 | seed u32 | level u8 | humans u16 | n u8 | n x {slot u8, nameLen u8, name}
//	  0x84 TICK    tick u32 | flags u8 | [nd u8 | nd x slot u8] | 8 bytes per person left, ascending slot     flag bit 0: drops (become bots before this tick)
//	  0x87 END     reason u8 | winner u8 | scores 4 x u8 | matchId u32 | counted u8 | ratingBefore f32 | ratingAfter f32
//	  0x88 PONG    t u32
//	  0x8F ERROR   code u8 | msgLen u8 | msg
package bedrockracers

import (
	"encoding/binary"
	"math"
)

// Version is the wire protocol version in HELLO / WELCOME.
const Version = 1

// message types
const (
	CHello = 0x01
	CQueue = 0x02
	CLeave = 0x03
	CCmd   = 0x10
	CPing  = 0x13
	CHash  = 0x14

	SWelcome = 0x81
	SStatus  = 0x82
	SMatch   = 0x83
	STick    = 0x84
	SEnd     = 0x87
	SPong    = 0x88
	SError   = 0x8F
)

// queue states reported in STATUS
const (
	QIdle      = 0
	QSearching = 1
	QLobby     = 2
	QLive      = 3
)

// ERROR codes
const (
	ErrBadFrame = 1
	ErrVersion  = 2
	ErrBusy     = 3
	ErrAuth     = 4
	ErrRate     = 5
	ErrDesync   = 6
	ErrTaken    = 7
)

// Why a match ended (the module's g-end-reason)
const (
	ReasonGoals   = 1
	ReasonTime    = 2
	ReasonGolden  = 3
	ReasonClosed  = 4
	ReasonAbandon = 9 // everyone left: never recorded
)

func put16(b []byte, v int) []byte { return append(b, byte(v), byte(v>>8)) }
func put32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
func putStr(b []byte, s string) []byte {
	if len(s) > 255 {
		s = s[:255]
	}
	b = append(b, byte(len(s)))
	return append(b, s...)
}
func putF32(b []byte, f float32) []byte { return put32(b, math.Float32bits(f)) }

func welcomeFrame(auth byte, simVer uint32, name string, online int, pid string) []byte {
	b := []byte{SWelcome, Version, auth}
	b = put32(b, simVer)
	b = putStr(b, name)
	b = put16(b, online)
	return putStr(b, pid)
}

func statusFrame(qstate byte, searching, online, elapsed, lobbyHumans, lobbyMs int) []byte {
	b := []byte{SStatus, qstate, byte(min(searching, 255))}
	b = put16(b, min(online, 65535))
	b = put16(b, min(elapsed, 65535))
	b = append(b, byte(min(lobbyHumans, 255)))
	return put16(b, min(lobbyMs, 65535))
}

type rosterEntry struct {
	Slot int
	Name string
}

func matchFrame(room uint32, slot int, seed uint32, level int, humans int, roster []rosterEntry) []byte {
	b := []byte{SMatch}
	b = put32(b, room)
	b = append(b, byte(slot))
	b = put32(b, seed)
	b = append(b, byte(level))
	b = put16(b, humans)
	b = append(b, byte(len(roster)))
	for _, r := range roster {
		b = append(b, byte(r.Slot))
		b = putStr(b, r.Name)
	}
	return b
}

// tickPayload is the part of a TICK frame after its type byte: what the replay stores too.
func tickPayload(tick uint32, drops []int, cmds [][8]byte) []byte {
	b := make([]byte, 0, 6+len(drops)+8*len(cmds))
	b = put32(b, tick)
	if len(drops) > 0 {
		b = append(b, 1, byte(len(drops)))
		for _, d := range drops {
			b = append(b, byte(d))
		}
	} else {
		b = append(b, 0)
	}
	for _, c := range cmds {
		b = append(b, c[:]...)
	}
	return b
}

func tickFrame(tick uint32, drops []int, cmds [][8]byte) []byte {
	p := tickPayload(tick, drops, cmds)
	return append([]byte{STick}, p...)
}

func endFrame(reason, winner int, scores [4]int, matchID uint32, counted bool, before, after float32) []byte {
	b := []byte{SEnd, byte(reason), byte(winner), byte(scores[0]), byte(scores[1]), byte(scores[2]), byte(scores[3])}
	b = put32(b, matchID)
	c := byte(0)
	if counted {
		c = 1
	}
	b = append(b, c)
	b = putF32(b, before)
	return putF32(b, after)
}

func pongFrame(t uint32) []byte { return put32([]byte{SPong}, t) }

func errorFrame(code int, msg string) []byte {
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return append([]byte{SError, byte(code), byte(len(msg))}, msg...)
}

// a parsed HELLO
type hello struct {
	Ver    byte
	SimVer uint32
	Token  string
	Name   string
}

func parseHello(msg []byte) (hello, bool) {
	var h hello
	if len(msg) < 1+1+4+2 {
		return h, false
	}
	h.Ver = msg[1]
	h.SimVer = binary.LittleEndian.Uint32(msg[2:])
	n := int(binary.LittleEndian.Uint16(msg[6:]))
	o := 8
	if n > 1900 || len(msg) < o+n+1 {
		return h, false
	}
	h.Token = string(msg[o : o+n])
	o += n
	nl := int(msg[o])
	o++
	if len(msg) < o+nl {
		return h, false
	}
	h.Name = sanitizeName(string(msg[o : o+nl]))
	return h, true
}

// sanitizeName keeps printable ASCII, at most 24 characters.
func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 24; i++ {
		c := s[i]
		if c >= 32 && c < 127 {
			out = append(out, c)
		}
	}
	return string(out)
}
