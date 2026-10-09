package hsdeck

import (
	"strings"
	"testing"
)

const filthyRat = `FilthyRat
Class: Warlock
Format: Standard
Year of the Scarab
#
2x (1) Mortal Coil
1x (2) Bloodmage Thalnos
1x (2) Brightwing
1x (2) Cult Neophyte
2x (2) Dark Peddler
2x (2) Dirty Rat
2x (2) Sunfury Protector
1x (2) Warden Maiev
2x (3) Menagerie Mug
2x (5) Doomguard
1x (5) The Curator
1x (7) Chillmaw
1x (7) Keymaster Alabaster
1x (7) Vanessa the Ringleader
1x (8) Archwitch Willow
1x (8) Lord Jaraxxus
2x (8) Mo'arg Forgefiend
2x (8) Twisting Nether
1x (9) Avatar of Hearthstone
1x (9) M.O.T.H.E.R.
2x (9) Voidlord
AAECAf0GDIWgBJegBKigBNCeBvSqB8ytB/bJB4jdB+vkB4TpB9HqB7r2BwmxnwSDoATRngbh5gagrAeTrQeBrgeCrget3wcAAA==
#
`

const rawCode = "AAECAf0GDIWgBJegBKigBNCeBvSqB8ytB/bJB4jdB+vkB4TpB9HqB7r2BwmxnwSDoATRngbh5gagrAeTrQeBrgeCrget3wcAAA=="

func TestParseFullExport(t *testing.T) {
	d, err := Parse(filthyRat)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "FilthyRat" || d.Class != "Warlock" || d.Format != "Standard" || d.Year != "Year of the Scarab" {
		t.Fatalf("meta: %+v", d)
	}
	if d.CardCount != 30 || len(d.Warnings) != 0 {
		t.Fatalf("count=%d warnings=%v", d.CardCount, d.Warnings)
	}
	if !d.NamesResolved || len(d.Cards) != 21 || d.Cards[0].Name != "Mortal Coil" {
		t.Fatalf("display list: %+v", d.Cards)
	}
	if d.HeroDBF != 893 || len(d.DBFCards) == 0 {
		t.Fatalf("hero/dbf: %d %d", d.HeroDBF, len(d.DBFCards))
	}
}

func TestBareCodeAndCanonicalRoundTrip(t *testing.T) {
	d, err := Parse(rawCode)
	if err != nil {
		t.Fatal(err)
	}
	if d.CardCount != 30 || d.NamesResolved || d.Class != "Warlock" {
		t.Fatalf("%+v", d)
	}
	// canonical re-encode is stable, and decodes to the same multiset
	d2, err := Decode(d.Deckstring)
	if err != nil || d2.Deckstring != d.Deckstring || d2.CardCount != 30 {
		t.Fatalf("round trip: %v %+v", err, d2)
	}
	// the original export decoded identically, so the dedupe key matches
	full, _ := Parse(filthyRat)
	if full.Deckstring != d.Deckstring {
		t.Fatal("canonical deckstring differs between bare code and full export")
	}
}

func TestCountMismatchRejected(t *testing.T) {
	bad := strings.Replace(filthyRat, "2x (9) Voidlord", "1x (9) Voidlord", 1)
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "don't match") {
		t.Fatalf("want mismatch error, got %v", err)
	}
}

func TestGarbageRejected(t *testing.T) {
	for _, in := range []string{"", "hello world", "AAAA", "!!!notbase64!!!", strings.Repeat("A", 2000)} {
		if _, err := Parse(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
	if _, err := Decode("AQID"); err == nil {
		t.Error("bad header should fail")
	}
}

func TestClassMismatchWarns(t *testing.T) {
	d, err := Parse(strings.Replace(filthyRat, "Class: Warlock", "Class: Mage", 1))
	if err != nil || d.Class != "Warlock" || len(d.Warnings) != 1 {
		t.Fatalf("%v %+v", err, d)
	}
}

func TestNormalizeClass(t *testing.T) {
	if NormalizeClass(" demon hunter ") != "Demon Hunter" || NormalizeClass("bard") != "" {
		t.Fatal("normalize")
	}
}
