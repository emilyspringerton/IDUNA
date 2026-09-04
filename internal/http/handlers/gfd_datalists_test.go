package handlers

import (
	"strings"
	"testing"
)

func TestGfdDatalistHTML_RendersEveryOption(t *testing.T) {
	got := gfdDatalistHTML("test-list", []string{"a", "b", "c"})
	if !strings.HasPrefix(got, `<datalist id="test-list">`) {
		t.Fatalf("expected a datalist with the right id, got %q", got)
	}
	for _, v := range []string{"a", "b", "c"} {
		if !strings.Contains(got, `<option value="`+v+`">`) {
			t.Fatalf("expected an option for %q, got %q", v, got)
		}
	}
	if !strings.HasSuffix(got, `</datalist>`) {
		t.Fatalf("expected the datalist to be closed, got %q", got)
	}
}

func TestGfdDatalistHTML_EscapesValues(t *testing.T) {
	got := gfdDatalistHTML("x", []string{`"><script>alert(1)</script>`})
	if strings.Contains(got, "<script>") {
		t.Fatalf("expected the value to be HTML-escaped, got %q", got)
	}
}

func TestGfdMobKinds_MatchesRealSpawnDataConventions(t *testing.T) {
	// Real, checked spot-check: every kind data/mob_spawns.json (server/spawn) already uses in
	// this monorepo must be a real, known option here, or the datalist would be silently
	// incomplete for content that already exists.
	for _, want := range []string{"worm", "rabbit", "beetle", "hills-wolf", "cave-bat", "cave-spider", "skeleton", "leech", "slime", "lizard"} {
		found := false
		for _, k := range gfdMobKinds {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q in gfdMobKinds, real content already uses it", want)
		}
	}
}

func TestGfdArenaHeroIDs_HasRealCount(t *testing.T) {
	// arena_game.h's own #define ARENA_HERO_COUNT 30 -- checked directly against the enum.
	if len(gfdArenaHeroIDs) != 30 {
		t.Fatalf("expected 30 real ArenaHeroID entries (ARENA_HERO_COUNT), got %d", len(gfdArenaHeroIDs))
	}
}
