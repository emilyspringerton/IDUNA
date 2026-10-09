// Package hsdeck decodes, validates and canonically re-encodes Hearthstone deck codes ("deckstrings")
// and parses the pasted deck-export text block players copy out of the game client.
//
// WOTAN HEARTHSTONE_NORTHSTAR.md §2: decoding a deckstring needs no card database -- it is a base64 varint
// stream of card dbfIds + counts. Card names/costs only exist in the pasted text block, so the two are merged
// by this package and cross-checked (total card counts must agree). Card names -> dbfId resolution (the real
// card table) is deliberately a later step.
//
// Deckstring layout (version 1): 0x00, varint version, varint format, varint heroCount, hero dbfIds,
// varint n1 + n1 dbfIds (1 copy), varint n2 + n2 dbfIds (2 copies), varint nN + nN (dbfId, count) pairs.
// Newer clients may append a sideboard section; it is tolerated and kept but not counted toward the deck size.
//
// Stopgap note (Core-Deps-PARENA-First): this is plain Go, not PARENA -- BURROW's Go emitter is currently
// scalar-only (no loops/Vec), which a varint stream codec needs. Tracked as a PARENA-replacement follow-up.
package hsdeck

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Format ids used in deckstrings.
var formatNames = map[int]string{1: "Wild", 2: "Standard", 3: "Classic", 4: "Twist"}

// FormatID returns the deckstring id for a format name (case-insensitive), 0 if unknown.
func FormatID(name string) int {
	for id, n := range formatNames {
		if strings.EqualFold(n, strings.TrimSpace(name)) {
			return id
		}
	}
	return 0
}

// Hero dbfId -> class for the base heroes. Alternate hero skins have their own dbfIds; for those the class
// falls back to the pasted "Class:" line, else "Unknown".
var heroClass = map[int]string{
	7: "Warrior", 31: "Hunter", 274: "Druid", 637: "Mage", 671: "Paladin", 813: "Priest",
	930: "Rogue", 1066: "Shaman", 893: "Warlock", 56550: "Demon Hunter", 78065: "Death Knight",
}

// Classes is the canonical class list (for filters/validation).
var Classes = []string{"Death Knight", "Demon Hunter", "Druid", "Hunter", "Mage", "Paladin", "Priest", "Rogue", "Shaman", "Warlock", "Warrior"}

// NormalizeClass maps a free-text class to its canonical spelling, "" if unrecognised.
func NormalizeClass(s string) string {
	s = strings.TrimSpace(s)
	for _, c := range Classes {
		if strings.EqualFold(c, s) {
			return c
		}
	}
	return ""
}

const (
	maxDeckCards = 40
	maxCodeLen   = 1024
)

// Card is one identity entry decoded from the deck code (dbfId + copies).
type Card struct {
	DBF   int `json:"dbf_id"`
	Count int `json:"count"`
}

// NamedCard is one display entry from the pasted text (name, mana cost, copies).
type NamedCard struct {
	Name  string `json:"name"`
	Cost  int    `json:"cost"`
	Count int    `json:"count"`
}

// Deck is the decoded/merged result.
type Deck struct {
	Title         string      `json:"title,omitempty"`
	Class         string      `json:"class"`
	Format        string      `json:"format"`
	FormatID      int         `json:"format_id"`
	Year          string      `json:"year,omitempty"`
	HeroDBF       int         `json:"hero_dbf"`
	DBFCards      []Card      `json:"dbf_cards"` // identity list, always present (from the code)
	Cards         []NamedCard `json:"cards"`     // display list, present when the paste had names
	CardCount     int         `json:"card_count"`
	Deckstring    string      `json:"deckstring"` // canonical re-encoding
	NamesResolved bool        `json:"names_resolved"`
	Warnings      []string    `json:"warnings,omitempty"`
}

type reader struct {
	b   []byte
	pos int
}

func (r *reader) varint() (int, error) {
	var v uint64
	for shift := uint(0); shift < 35; shift += 7 {
		if r.pos >= len(r.b) {
			return 0, errors.New("deck code is truncated")
		}
		c := r.b[r.pos]
		r.pos++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return int(v), nil
		}
	}
	return 0, errors.New("deck code has an invalid number")
}

func (r *reader) more() bool { return r.pos < len(r.b) }

// Decode parses a bare deckstring into a Deck (no names). Counts and ids only.
func Decode(code string) (*Deck, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("deck code is empty")
	}
	if len(code) > maxCodeLen {
		return nil, errors.New("deck code is too long")
	}
	raw, err := base64.StdEncoding.DecodeString(padB64(code))
	if err != nil {
		return nil, errors.New("that doesn't look like a Hearthstone deck code (not valid base64)")
	}
	r := &reader{b: raw}
	if !r.more() || raw[0] != 0 {
		return nil, errors.New("that doesn't look like a Hearthstone deck code (bad header)")
	}
	r.pos++
	ver, err := r.varint()
	if err != nil {
		return nil, err
	}
	if ver != 1 {
		return nil, fmt.Errorf("unsupported deck code version %d", ver)
	}
	fid, err := r.varint()
	if err != nil {
		return nil, err
	}
	fname, ok := formatNames[fid]
	if !ok {
		return nil, fmt.Errorf("unknown deck format id %d", fid)
	}
	nHeroes, err := r.varint()
	if err != nil {
		return nil, err
	}
	if nHeroes < 1 || nHeroes > 4 {
		return nil, errors.New("deck code has an invalid hero count")
	}
	hero := 0
	for i := 0; i < nHeroes; i++ {
		h, err := r.varint()
		if err != nil {
			return nil, err
		}
		if i == 0 {
			hero = h
		}
	}
	counts := map[int]int{}
	for _, per := range []int{1, 2} {
		n, err := r.varint()
		if err != nil {
			return nil, err
		}
		if n > maxDeckCards {
			return nil, errors.New("deck code lists too many cards")
		}
		for i := 0; i < n; i++ {
			id, err := r.varint()
			if err != nil {
				return nil, err
			}
			counts[id] += per
		}
	}
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if n > maxDeckCards {
		return nil, errors.New("deck code lists too many cards")
	}
	for i := 0; i < n; i++ {
		id, err := r.varint()
		if err != nil {
			return nil, err
		}
		c, err := r.varint()
		if err != nil {
			return nil, err
		}
		counts[id] += c
	}
	// Anything left is the optional sideboard section: tolerated, not counted.
	d := &Deck{Format: fname, FormatID: fid, HeroDBF: hero, Class: heroClass[hero]}
	for id, c := range counts {
		if c < 1 || id < 1 {
			return nil, errors.New("deck code contains an invalid card entry")
		}
		d.DBFCards = append(d.DBFCards, Card{DBF: id, Count: c})
		d.CardCount += c
	}
	if d.CardCount == 0 {
		return nil, errors.New("deck code contains no cards")
	}
	if d.CardCount > maxDeckCards {
		return nil, errors.New("deck code lists too many cards")
	}
	sortCards(d.DBFCards)
	d.Deckstring = Encode(d)
	return d, nil
}

func padB64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

func sortCards(c []Card) {
	sort.Slice(c, func(i, j int) bool { return c[i].DBF < c[j].DBF })
}

func putVarint(b *bytes.Buffer, v int) {
	u := uint64(v)
	for u >= 0x80 {
		b.WriteByte(byte(u) | 0x80)
		u >>= 7
	}
	b.WriteByte(byte(u))
}

// Encode produces the canonical deckstring for d (cards sorted by dbfId within each count group), so two
// pastes of the same deck always yield the same string -- the dedupe key.
func Encode(d *Deck) string {
	var one, two, many []Card
	for _, c := range d.DBFCards {
		switch c.Count {
		case 1:
			one = append(one, c)
		case 2:
			two = append(two, c)
		default:
			many = append(many, c)
		}
	}
	for _, s := range [][]Card{one, two, many} {
		sortCards(s)
	}
	var b bytes.Buffer
	b.WriteByte(0)
	putVarint(&b, 1)
	putVarint(&b, d.FormatID)
	putVarint(&b, 1)
	putVarint(&b, d.HeroDBF)
	putVarint(&b, len(one))
	for _, c := range one {
		putVarint(&b, c.DBF)
	}
	putVarint(&b, len(two))
	for _, c := range two {
		putVarint(&b, c.DBF)
	}
	putVarint(&b, len(many))
	for _, c := range many {
		putVarint(&b, c.DBF)
		putVarint(&b, c.Count)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

var (
	cardLineRe = regexp.MustCompile(`^(\d+)\s*[xX]\s*\((\d+)\)\s+(.+?)\s*$`)
	codeRe     = regexp.MustCompile(`^[A-Za-z0-9+/]{16,}={0,2}$`)
	yearRe     = regexp.MustCompile(`(?i)^year of the\b.*$`)
)

type textCard struct {
	name  string
	cost  int
	count int
}

// Parse accepts either a bare deck code or the full pasted export block and returns the merged Deck.
func Parse(text string) (*Deck, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil, errors.New("paste a deck code or a deck export")
	}
	if len(text) > 8192 {
		return nil, errors.New("that paste is too long to be a deck")
	}
	var title, className, formatName, year, code string
	var tcards []textCard
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := cardLineRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			cost, _ := strconv.Atoi(m[2])
			tcards = append(tcards, textCard{name: m[3], cost: cost, count: n})
			continue
		}
		switch {
		case strings.HasPrefix(line, "#"):
			t := strings.TrimSpace(strings.TrimLeft(line, "# "))
			if strings.HasPrefix(raw, "###") && title == "" && t != "" {
				title = t
			}
		case hasPrefixFold(line, "class:"):
			className = strings.TrimSpace(line[len("class:"):])
		case hasPrefixFold(line, "format:"):
			formatName = strings.TrimSpace(line[len("format:"):])
		case yearRe.MatchString(line):
			year = line
		case codeRe.MatchString(line) && !strings.ContainsAny(line, " \t") && len(line) > len(code):
			code = line
		default:
			if title == "" && len(line) <= 80 {
				title = line
			}
		}
	}
	if code == "" {
		return nil, errors.New("no deck code found -- paste the line starting with AAE… from the export")
	}
	d, err := Decode(code)
	if err != nil {
		return nil, err
	}
	d.Title = title
	d.Year = year
	if nc := NormalizeClass(className); nc != "" {
		if d.Class == "" {
			d.Class = nc
		} else if d.Class != nc {
			d.Warnings = append(d.Warnings, fmt.Sprintf("the text says %s but the deck code's hero is %s; using the code", nc, d.Class))
		}
	} else if className != "" {
		d.Warnings = append(d.Warnings, fmt.Sprintf("unrecognised class %q", className))
	}
	if d.Class == "" {
		d.Class = "Unknown"
	}
	if f := FormatID(formatName); f != 0 && f != d.FormatID {
		d.Warnings = append(d.Warnings, fmt.Sprintf("the text says %s but the deck code is %s; using the code", formatName, d.Format))
	}
	if len(tcards) > 0 {
		total := 0
		for _, c := range tcards {
			total += c.count
		}
		if total != d.CardCount {
			return nil, fmt.Errorf("the card list has %d cards but the deck code has %d -- they don't match", total, d.CardCount)
		}
		d.attachNames(tcards)
	}
	if d.CardCount != 30 {
		d.Warnings = append(d.Warnings, fmt.Sprintf("deck has %d cards (a standard deck has 30)", d.CardCount))
	}
	return d, nil
}

// attachNames stores the pasted display list. Without a card table the per-card dbfId <-> name mapping is
// not recoverable (the code and the text list the same multiset in different orders), so the two lists are
// kept side by side: DBFCards is the identity, Cards is what is shown. A later hs_cards backfill upgrades
// code-only decks; NamesResolved says whether a display list exists.
func (d *Deck) attachNames(t []textCard) {
	sort.SliceStable(t, func(i, j int) bool {
		if t[i].cost != t[j].cost {
			return t[i].cost < t[j].cost
		}
		return t[i].name < t[j].name
	})
	for _, c := range t {
		d.Cards = append(d.Cards, NamedCard{Name: c.name, Cost: c.cost, Count: c.count})
	}
	d.NamesResolved = true
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
