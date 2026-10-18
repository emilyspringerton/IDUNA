package deckstats

import (
	"encoding/json"
)

// maxCards is the longest deck we keep. A drafted deck is 23 cards (DEADWEIGHT core/draft.h DW_DECK_SIZE); the cap only
// bounds the per-line scratch array.
const maxCards = 64

// event is one parsed decks.ndjson line. player aliases the line buffer: copy it before the next line is read.
type event struct {
	typ      byte // 'd' draft, 'm' match, 0 = some other record (ignored)
	deckID   uint32
	t        int64
	player   []byte
	kind     int
	ncards   int
	cards    [maxCards]byte
	badCards bool // a card id outside 0..255, or more than maxCards cards: the draft is ignored
	result   byte // 'w' win, 'l' loss, 'd' anything else (draw)
}

// parseLine fills ev from one ndjson line. The fast path is a single forward scan with no allocation; anything it does
// not understand (escaped strings, floats, a different shape) falls back to encoding/json, so a line is only ever
// rejected when it is not a JSON object at all.
func parseLine(line []byte, ev *event) bool {
	if parseFast(line, ev) {
		return true
	}
	return parseSlow(line, ev)
}

type slowRec struct {
	Event  string `json:"event"`
	DeckID uint32 `json:"deck_id"`
	T      int64  `json:"t"`
	Player string `json:"player"`
	Kind   int    `json:"kind"`
	Cards  []int  `json:"cards"`
	Result string `json:"result"`
}

func parseSlow(line []byte, ev *event) bool {
	*ev = event{}
	var r slowRec
	if json.Unmarshal(line, &r) != nil {
		return false
	}
	switch r.Event {
	case "draft":
		ev.typ = 'd'
	case "match":
		ev.typ = 'm'
	default:
		ev.typ = 0
	}
	ev.deckID, ev.t, ev.kind = r.DeckID, r.T, r.Kind
	ev.player = []byte(r.Player)
	for _, c := range r.Cards {
		if c < 0 || c > 255 || ev.ncards >= maxCards {
			ev.badCards = true
			continue
		}
		ev.cards[ev.ncards] = byte(c)
		ev.ncards++
	}
	switch r.Result {
	case "win":
		ev.result = 'w'
	case "loss":
		ev.result = 'l'
	default:
		ev.result = 'd'
	}
	return true
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func skipWS(b []byte, i int) int {
	for i < len(b) && isWS(b[i]) {
		i++
	}
	return i
}

// scanString reads a JSON string starting at b[i] == '"'. It returns the content bounds and the index after the closing
// quote; ok is false for an escape sequence (the caller falls back to the slow path) or an unterminated string.
func scanString(b []byte, i int) (s, e, next int, ok bool) {
	if i >= len(b) || b[i] != '"' {
		return 0, 0, 0, false
	}
	i++
	s = i
	for i < len(b) {
		c := b[i]
		if c == '"' {
			return s, i, i + 1, true
		}
		if c == '\\' || c < 0x20 {
			return 0, 0, 0, false
		}
		i++
	}
	return 0, 0, 0, false
}

// scanInt reads a plain (optionally negative) integer. A fraction or exponent is not an integer here.
func scanInt(b []byte, i int) (n int64, next int, ok bool) {
	neg := false
	if i < len(b) && b[i] == '-' {
		neg = true
		i++
	}
	start := i
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		if n > (1<<62)/10 {
			return 0, 0, false
		}
		n = n*10 + int64(b[i]-'0')
		i++
	}
	if i == start {
		return 0, 0, false
	}
	if i < len(b) && (b[i] == '.' || b[i] == 'e' || b[i] == 'E') {
		return 0, 0, false
	}
	if neg {
		n = -n
	}
	return n, i, true
}

// skipValue steps over one JSON value of any shape. Escapes inside strings are fine here: only the quote boundaries
// matter, and the skipped value is not used.
func skipValue(b []byte, i int) (next int, ok bool) {
	if i >= len(b) {
		return 0, false
	}
	switch b[i] {
	case '"':
		i++
		for i < len(b) {
			switch b[i] {
			case '\\':
				i += 2
				continue
			case '"':
				return i + 1, true
			}
			i++
		}
		return 0, false
	case '[', '{':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				j, ok := skipValue(b, i)
				if !ok {
					return 0, false
				}
				i = j
				continue
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					return i + 1, true
				}
			}
			i++
		}
		return 0, false
	default: // number, true, false, null
		for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && !isWS(b[i]) {
			i++
		}
		return i, true
	}
}

func parseFast(b []byte, ev *event) bool {
	*ev = event{}
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return false
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		ev.result = 'd'
		return skipWS(b, i+1) == len(b)
	}
	for first := true; ; first = false {
		if i >= len(b) {
			return false
		}
		if !first {
			if b[i] == '}' {
				i++
				break
			}
			if b[i] != ',' {
				return false
			}
			i = skipWS(b, i+1)
		}
		ks, ke, ni, ok := scanString(b, i)
		if !ok {
			return false
		}
		i = skipWS(b, ni)
		if i >= len(b) || b[i] != ':' {
			return false
		}
		i = skipWS(b, i+1)
		if i >= len(b) {
			return false
		}
		switch string(b[ks:ke]) {
		case "event":
			s, e, ni, ok := scanString(b, i)
			if !ok {
				return false
			}
			switch string(b[s:e]) {
			case "draft":
				ev.typ = 'd'
			case "match":
				ev.typ = 'm'
			default:
				ev.typ = 0
			}
			i = ni
		case "deck_id":
			n, ni, ok := scanInt(b, i)
			if !ok || n < 0 || n > 0xFFFFFFFF {
				return false
			}
			ev.deckID, i = uint32(n), ni
		case "t":
			n, ni, ok := scanInt(b, i)
			if !ok {
				return false
			}
			ev.t, i = n, ni
		case "kind":
			n, ni, ok := scanInt(b, i)
			if !ok || n < -1<<30 || n > 1<<30 {
				return false
			}
			ev.kind, i = int(n), ni
		case "player":
			s, e, ni, ok := scanString(b, i)
			if !ok {
				return false
			}
			ev.player, i = b[s:e], ni
		case "result":
			s, e, ni, ok := scanString(b, i)
			if !ok {
				return false
			}
			switch string(b[s:e]) {
			case "win":
				ev.result = 'w'
			case "loss":
				ev.result = 'l'
			default:
				ev.result = 'd'
			}
			i = ni
		case "cards":
			if b[i] != '[' {
				return false
			}
			ev.ncards, ev.badCards = 0, false
			i = skipWS(b, i+1)
			if i < len(b) && b[i] == ']' {
				i++
				break
			}
			for {
				n, ni, ok := scanInt(b, i)
				if !ok {
					return false
				}
				if n < 0 || n > 255 || ev.ncards >= maxCards {
					ev.badCards = true
				} else {
					ev.cards[ev.ncards] = byte(n)
					ev.ncards++
				}
				i = skipWS(b, ni)
				if i >= len(b) {
					return false
				}
				if b[i] == ']' {
					i++
					break
				}
				if b[i] != ',' {
					return false
				}
				i = skipWS(b, i+1)
			}
		default:
			j, ok := skipValue(b, i)
			if !ok {
				return false
			}
			i = j
		}
		i = skipWS(b, i)
	}
	if ev.result == 0 {
		ev.result = 'd' // same as the slow path: anything that is not a win or a loss counts as a draw
	}
	return skipWS(b, i) == len(b)
}
