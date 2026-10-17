package empire

import "encoding/binary"

// The wire format between the browser (PARENA em_net.prn) and this gateway. One WebSocket binary message is one frame.
//
//	client -> gateway
//	  0x02 AUTH    [u16 len][token]                            the first frame; the bearer token (a game token or the WOTAN SSO JWT)
//	  0x03 QUEUE   [u32 sim version][u32 deck][u8 faction][u8 want-bot]   want-bot 0 = whoever is waiting, n = AI number n
//	  0x07 LEAVE                                               stop searching
//	  0x08 PING    [u32 echo]
//	  0x10 PLAY    [slot][x4][y4]                              a request: the board decides (x4 y4 are quarter tiles)
//	  0x11 EMOTE   [id]
//	  0x12 RESYNC                                              send me the board
//	  0x13 RESIGN
//	  0x14 WATCH   [u32 room]                                  look in on a match
//	gateway -> client
//	  0xA1 IDENT   [guest][nameLen][name][idLen][id]
//	  0xA0 QUEUED  [u16 waiting]
//	  0xA2 RECORDED [counted][u16 rating][i16 delta][u32 match id]
//	  0xA3 PONG    [u32 echo]
//	  0xAF ERROR   [code][len][text]
//	  0x20 MATCH   see matchFrame
//	  0x21 TURN    [u32 tick][n]{[seat][slot][x4][y4]}         every tick; the plays executed in it
//	  0x22 OVER    [winner][reason][u32 ticks][crowns0][crowns1]
//	  0x23 SNAPSHOT [u32 tick][u16 len][bytes]                 the board
//	  0x24 HASH    [u32 tick][u32 hash]
//	  0x25 EMOTE   [seat][id]
const (
	CAuth   = 0x02
	CQueue  = 0x03
	CLeave  = 0x07
	CPing   = 0x08
	CPlay   = 0x10
	CEmote  = 0x11
	CResync = 0x12
	CResign = 0x13
	CWatch  = 0x14

	GQueued   = 0xA0
	GIdent    = 0xA1
	GRecorded = 0xA2
	GPong     = 0xA3
	GError    = 0xAF
	GMatch    = 0x20
	GTurn     = 0x21
	GOver     = 0x22
	GSnapshot = 0x23
	GHash     = 0x24
	GEmote    = 0x25
)

// Error codes in a GError frame.
const (
	ErrBadHello = 1
	ErrAuth     = 2
	ErrVersion  = 3
	ErrBadDeck  = 4
	ErrBusy     = 5
	ErrServer   = 6
	ErrTaken    = 7
	ErrRate     = 8
	ErrNoRoom   = 9
)

// flags in a MATCH frame
const (
	FlagRanked    = 1
	FlagSpectator = 2
)

// clientLen is the exact payload length (after the type byte) of each client frame that has one.
var clientLen = map[byte]int{CQueue: 10, CLeave: 0, CPing: 4, CPlay: 3, CEmote: 1, CResync: 0, CResign: 0, CWatch: 4}

type matchInfo struct {
	ID          uint32
	Seed        uint32
	Seat        int
	OppKind     int // 0 human, 1 bot
	OppPers     int // 0 human, else the AI's number
	OppName     string
	OppRating   int
	YourRating  int
	Deck        [2]uint32
	Fac         [2]int
	Flags       int
	CountdownMS int
	Seat0Name   string
}

func putName(b []byte, s string) {
	for i := 0; i < 16; i++ {
		if i < len(s) && s[i] >= 32 && s[i] < 127 && i < 15 {
			b[i] = s[i]
		} else {
			b[i] = 0
		}
	}
}

// matchFrame is 61 bytes: type, id u32, seed u32, seat, opponent kind, opponent AI, opponent name[16], opponent rating u16, your rating u16,
// deck0 u32, faction0, deck1 u32, faction1, flags, countdown ms u16, seat-0 name[16] (a watcher needs both names).
func matchFrame(m matchInfo) []byte {
	b := make([]byte, 61)
	b[0] = GMatch
	binary.LittleEndian.PutUint32(b[1:], m.ID)
	binary.LittleEndian.PutUint32(b[5:], m.Seed)
	b[9] = byte(m.Seat)
	b[10] = byte(m.OppKind)
	b[11] = byte(m.OppPers)
	putName(b[12:28], m.OppName)
	binary.LittleEndian.PutUint16(b[28:], uint16(clampInt(m.OppRating, 0, 65535)))
	binary.LittleEndian.PutUint16(b[30:], uint16(clampInt(m.YourRating, 0, 65535)))
	binary.LittleEndian.PutUint32(b[32:], m.Deck[0])
	b[36] = byte(m.Fac[0])
	binary.LittleEndian.PutUint32(b[37:], m.Deck[1])
	b[41] = byte(m.Fac[1])
	b[42] = byte(m.Flags)
	binary.LittleEndian.PutUint16(b[43:], uint16(clampInt(m.CountdownMS, 0, 65535)))
	putName(b[45:61], m.Seat0Name)
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func turnFrame(tick int, cmds []Cmd) []byte {
	b := make([]byte, 6+4*len(cmds))
	b[0] = GTurn
	binary.LittleEndian.PutUint32(b[1:], uint32(tick))
	b[5] = byte(len(cmds))
	for i, c := range cmds {
		o := 6 + 4*i
		b[o], b[o+1], b[o+2], b[o+3] = c.Seat, c.Slot, c.X4, c.Y4
	}
	return b
}

func overFrame(r Result) []byte {
	b := make([]byte, 9)
	b[0] = GOver
	b[1] = byte(r.Winner)
	b[2] = byte(r.Reason)
	binary.LittleEndian.PutUint32(b[3:], uint32(r.Ticks))
	b[7], b[8] = byte(r.Crowns[0]), byte(r.Crowns[1])
	return b
}

func snapshotFrame(tick int, snap []byte) []byte {
	b := make([]byte, 7+len(snap))
	b[0] = GSnapshot
	binary.LittleEndian.PutUint32(b[1:], uint32(tick))
	binary.LittleEndian.PutUint16(b[5:], uint16(len(snap)))
	copy(b[7:], snap)
	return b
}

func hashFrame(tick int, h uint32) []byte {
	b := make([]byte, 9)
	b[0] = GHash
	binary.LittleEndian.PutUint32(b[1:], uint32(tick))
	binary.LittleEndian.PutUint32(b[5:], h)
	return b
}

func recordedFrame(counted bool, rating, delta int, matchID int64) []byte {
	b := make([]byte, 10)
	b[0] = GRecorded
	if counted {
		b[1] = 1
	}
	binary.LittleEndian.PutUint16(b[2:], uint16(clampInt(rating, 0, 65535)))
	binary.LittleEndian.PutUint16(b[4:], uint16(int16(clampInt(delta, -32000, 32000))))
	binary.LittleEndian.PutUint32(b[6:], uint32(matchID))
	return b
}

func errorFrame(code int, msg string) []byte {
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return append([]byte{GError, byte(code), byte(len(msg))}, msg...)
}

func identFrame(guest bool, name, id string) []byte {
	if len(name) > 60 {
		name = name[:60]
	}
	if len(id) > 60 {
		id = id[:60]
	}
	g := byte(0)
	if guest {
		g = 1
	}
	b := []byte{GIdent, g, byte(len(name))}
	b = append(b, name...)
	b = append(b, byte(len(id)))
	return append(b, id...)
}

// deckValid: eight different card ids 0..15 in eight nibbles.
func deckValid(d uint32) bool {
	var seen uint16
	for k := 0; k < 8; k++ {
		c := uint(d >> (4 * uint(k)) & 15)
		if seen&(1<<c) != 0 {
			return false
		}
		seen |= 1 << c
	}
	return true
}

// ---- the replay file ----------------------------------------------------------------------------------------------------------------------

// BuildReplay is the file WOTAN's replay viewer loads (PARENA em_ui2.prn describes the layout):
//
//	0 u32 sim version  4 u32 seed  8 u32 deck0  12 u8 fac0  13 u32 deck1  17 u8 fac1  18 u8 winner  19 u8 reason  20 u16 ticks
//	22 name0[16]  38 name1[16]  54 u16 commands  56 { u16 tick, u8 seat, u8 slot, u8 x4, u8 y4 } ...
func BuildReplay(version, seed uint32, deck [2]uint32, fac [2]int, names [2]string, r Result, cmds []Cmd) []byte {
	b := make([]byte, 56+6*len(cmds))
	binary.LittleEndian.PutUint32(b[0:], version)
	binary.LittleEndian.PutUint32(b[4:], seed)
	binary.LittleEndian.PutUint32(b[8:], deck[0])
	b[12] = byte(fac[0])
	binary.LittleEndian.PutUint32(b[13:], deck[1])
	b[17] = byte(fac[1])
	b[18], b[19] = byte(r.Winner), byte(r.Reason)
	binary.LittleEndian.PutUint16(b[20:], uint16(clampInt(r.Ticks, 0, 65535)))
	putName(b[22:38], names[0])
	putName(b[38:54], names[1])
	binary.LittleEndian.PutUint16(b[54:], uint16(len(cmds)))
	for i, c := range cmds {
		o := 56 + 6*i
		binary.LittleEndian.PutUint16(b[o:], uint16(clampInt(int(c.Tick), 0, 65535)))
		b[o+2], b[o+3], b[o+4], b[o+5] = c.Seat, c.Slot, c.X4, c.Y4
	}
	return b
}
