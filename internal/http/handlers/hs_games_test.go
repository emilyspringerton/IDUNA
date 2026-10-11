package handlers_test

import (
	"encoding/json"
	"testing"
)

// Regression (2026-10-11): a finished game's STORED my_class/opp_class must survive a mid-game hero
// transform (e.g. Dragon Soul Essence) the same way the live page already does
// (hs_live_test.go's TestHS_LiveMeSurvivesMidGameHeroTransform). hsGameRecord is decoded
// independently of hstracker.Record -- the live path's own type -- so the card0 fix that landed for
// classOfPlayer never reached this file's own classOf. Entity 201 (ours) reveals as HERO_P1 (maps to
// a class) then transforms to HERO_DRAGON_FORM (deliberately unmapped in hs_cards); classOf must
// still resolve "Death Knight" from Card0, not come up blank by looking at the post-transform Card.
func TestHS_CreateGameClassSurvivesHeroTransform(t *testing.T) {
	e := newHSEnv(t)
	if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id,class) VALUES (90010,'Me','0','HERO_P1','Death Knight')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id,class) VALUES (90011,'Opp','0','HERO_P2','Shaman')`); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"game_type": "RANKED", "format": "standard",
		"player1": "Alice#1", "player2": "UNKNOWN HUMAN PLAYER",
		"result1": 1, "result2": 2, "turns": 10, "complete": 1, "me": 1,
		"entities": []map[string]any{
			// same entity ID keeps its id but its Card mutates on transform -- Card0 must not.
			{"id": 201, "player": 1, "card": "HERO_DRAGON_FORM", "card0": "HERO_P1", "zone0": "PLAY"},
			{"id": 202, "player": 2, "card": "HERO_P2", "card0": "HERO_P2", "zone0": "PLAY"},
		},
		"timeline": []any{},
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	out := e.must(201, "alice", "POST", "games", string(body))
	if out["my_class"] != "Death Knight" || out["opp_class"] != "Shaman" {
		t.Fatalf("hero transform defeated stored class resolution, same bug class the live-path card0 fix already covers: %v", out)
	}

	list := e.must(200, "alice", "GET", "games", "")
	games, _ := list["games"].([]any)
	if len(games) != 1 {
		t.Fatalf("want 1 stored game, got %d: %v", len(games), list)
	}
	g := games[0].(map[string]any)
	if g["my_class"] != "Death Knight" || g["opp_class"] != "Shaman" {
		t.Fatalf("stored row kept the blank/wrong class: %v", g)
	}
}

// Without card0 at all (an older client, or a game with no hero transform -- Card0 always equals
// Card for an entity that never changed card), classOf must still work off the plain Card value.
func TestHS_CreateGameClassFallsBackWithoutTransform(t *testing.T) {
	e := newHSEnv(t)
	if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id,class) VALUES (90010,'Me','0','HERO_P1','Mage')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO hs_cards (dbf_id,name,cost,card_id,class) VALUES (90011,'Opp','0','HERO_P2','Priest')`); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"game_type": "RANKED", "format": "standard",
		"player1": "Alice#1", "player2": "UNKNOWN HUMAN PLAYER",
		"result1": 1, "result2": 2, "turns": 10, "complete": 1, "me": 1,
		"entities": []map[string]any{
			{"id": 201, "player": 1, "card": "HERO_P1", "card0": "HERO_P1", "zone0": "PLAY"},
			{"id": 202, "player": 2, "card": "HERO_P2", "card0": "HERO_P2", "zone0": "PLAY"},
		},
		"timeline": []any{},
	}
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	out := e.must(201, "alice", "POST", "games", string(body))
	if out["my_class"] != "Mage" || out["opp_class"] != "Priest" {
		t.Fatalf("ordinary (no-transform) game: %v", out)
	}
}
