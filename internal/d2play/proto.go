// Package d2play is the browser gateway for D2 (DEADWEIGHT_2), WOTAN's /d2/play.html.
//
// D2's match server (dw2_server, C, TCP) is authoritative and already queues, pairs a human with a waiting bot (dw2_bot) or
// another human, runs the packing clock, the 30-tick fight and the round-break mini-game. The browser cannot speak raw TCP,
// so the gateway is a WebSocket in front of one TCP connection per player, with three jobs the server does not do:
//
//   - identity: the first frame is the IDUNA bearer token (a guest account's or the signed-in SSO identity); the server is
//     run --no-auth, so the gateway introduces the player to it by name and is the only thing that knows who they are;
//   - validation: only the player-side frames are relayed, sized and rate limited;
//   - tracking: it watches the authoritative frames of the match (and the player's own packing and calls) and, when the
//     match ends, records it against the player's IDUNA account (Recorder), including a forfeit when the player leaves.
//
// The wire format on the WebSocket is dw2_server's own frames (core/protocol.h) with the u16 length dropped (one frame per
// WebSocket message), plus three gateway frames (GIdent, GRecorded, GError). Nothing else is invented, so the wasm client
// speaks the game's real protocol.
package d2play

import (
	"encoding/binary"
	"errors"
)

// Client frames (browser -> gateway -> server), dw2 core/protocol.h.
const (
	CHello     = 0x01
	CAuth      = 0x02
	CQueue     = 0x03
	CPlace     = 0x04
	CPanicCut  = 0x05
	CReady     = 0x06
	CLeave     = 0x07
	CPing      = 0x08
	CRoundCall = 0x09
)

// Server frames (server -> gateway -> browser).
const (
	SWelcome     = 0x81
	SQueued      = 0x82
	SMatchFound  = 0x83
	SPlaceAck    = 0x84
	SPlaceReject = 0x85
	SCutAck      = 0x86
	SCutReject   = 0x87
	SCombatStart = 0x88
	STick        = 0x89
	SMatchEnd    = 0x8A
	SPong        = 0x8B
	SRoundBreak  = 0x8C
	SRoundResult = 0x8D
	// SOppShip: once, right after COMBAT_START, the opponent's packed ship (packing is secret, the fight is not): [n u8] then 36 x [item row col rot]
	// (ruleset 2: a ship may hold a part to a cell; a cut is no longer a thing a ship does, so the cut flag is gone).
	SOppShip = 0x90
	// SOppCut: reserved (ruleset 1 let a ship be Panic-Cut in the fight): [idx u8].
	SOppCut = 0x91
	// SEvents: right after every TICK, what that second did, in order: [tick u8][n u8][dropped u8][structure_you u16][structure_opp u16] then n x [type side a b c d e]
	// (core/combat.h Dw2Event, the sides already turned so 0 is the recipient's own ship). The only server frame whose size depends on its content.
	SEvents = 0x92
	// MaxEvents is the most events one SEvents frame carries (DW2_WIRE_MAX_EVENTS).
	MaxEvents = 60
	SError    = 0x8F
)

// Gateway frames (gateway -> browser only).
const (
	// GIdent: [0xA1][guest u8][nameLen u8][name][idLen u8][id]. Sent once the token is accepted, before the server's WELCOME.
	GIdent = 0xA1
	// GRecorded: [0xA2][counted u8][opp_kind u8][rating_after i16][delta i16]. After the match is recorded (or not).
	GRecorded = 0xA2
	// GError: [0xAF][code u8][len u8][text].
	GError = 0xAF
)

// Gateway error codes.
const (
	ErrBadHello   = 1 // no/invalid token
	ErrBusy       = 2 // the same player opened the game elsewhere
	ErrServerDown = 3 // the match server is unreachable or went away
	ErrBadRequest = 4
)

// Match result values, dw2 DW2_RES_*.
const (
	ResLoss = 0
	ResWin  = 1
	ResTie  = 2
)

// End reasons, dw2 DW2_END_*.
const (
	EndHull    = 0
	EndTimeout = 1
	EndForfeit = 2
	EndServer  = 3
)

// payload sizes of the server frames (bytes after the type byte), from core/protocol.c payload_size.
var serverPayload = map[byte]int{
	SWelcome: 5, SQueued: 2, SMatchFound: 28, SPlaceAck: 1, SPlaceReject: 1, SCutAck: 1, SCutReject: 1,
	SCombatStart: 10, STick: 9, SMatchEnd: 7, SPong: 4, SRoundBreak: 6, SRoundResult: 6, SOppShip: 145, SOppCut: 1, SError: 1,
}

// MATCH_FOUND carries the packing time as a u16 (28 bytes, the older server) or a u32 (30 bytes: the packing clock is 120 s, which a u16 of ms cannot hold).
const matchFoundWide = 30

// payload sizes of the client frames the gateway relays.
var clientPayload = map[byte]int{
	CQueue: 0, CPlace: 4, CPanicCut: 1, CReady: 0, CLeave: 0, CPing: 4, CRoundCall: 1,
}

// ErrFrame is a malformed frame from the server.
var ErrFrame = errors.New("d2play: malformed frame")

// splitFrame takes one frame off the front of a TCP stream: it returns the frame without its u16 length, and how many
// bytes it consumed; (nil, 0, nil) when more bytes are needed.
func splitFrame(b []byte) (frame []byte, used int, err error) {
	if len(b) < 2 {
		return nil, 0, nil
	}
	n := int(binary.LittleEndian.Uint16(b))
	if n < 1 || n > 512 {
		return nil, 0, ErrFrame
	}
	if len(b) < 2+n {
		return nil, 0, nil
	}
	f := b[2 : 2+n]
	if f[0] == SEvents {
		// [tick][n][dropped][structure_you u16][structure_opp u16] then n x 7 bytes: the size says how many, and both must agree
		if n-1 < 7 || int(f[2]) > MaxEvents || n-1 != 7+7*int(f[2]) {
			return nil, 0, ErrFrame
		}
		return f, 2 + n, nil
	}
	if want, ok := serverPayload[f[0]]; !ok || (want != n-1 && !(f[0] == SMatchFound && n-1 == matchFoundWide)) {
		return nil, 0, ErrFrame
	}
	return f, 2 + n, nil
}

// withLen prefixes a frame with the u16 length dw2_server expects.
func withLen(f []byte) []byte {
	out := make([]byte, 2+len(f))
	binary.LittleEndian.PutUint16(out, uint16(len(f)))
	copy(out[2:], f)
	return out
}

// helloFrame is the D2 HELLO the gateway sends the server for a human: proto 1, kind 0, a 16-byte NUL padded name, no token.
func helloFrame(name string) []byte {
	f := make([]byte, 1+1+1+16+1)
	f[0], f[1], f[2] = CHello, 1, 0
	n := 0
	for i := 0; i < len(name) && n < 16; i++ {
		c := name[i]
		if c < 32 || c > 126 || c == '"' || c == '\\' {
			c = '_'
		}
		f[3+n] = c
		n++
	}
	return f
}

func u32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func u16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
