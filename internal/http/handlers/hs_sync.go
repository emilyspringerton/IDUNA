package handlers

// hs_sync.go -- at the end of a tracked game the deck that was played is synced into the owner's deck
// library. Rules (founder: "sync the deck up to the deck library at the end of the game if the deck ever
// changes always create a copy"):
//   - identical contents already in the library (same deckstring) -> nothing to do;
//   - a deck with the same Hearthstone deck id but different contents -> a NEW library entry "<name> (copy N)",
//     the earlier versions are never edited;
//   - a deck the owner deleted from the library is not resurrected;
//   - synced decks start private (owner-only) unless the player turned on auto_publish (PUT /settings);
//     the owner publishes with PATCH /decks/{id} {"private":false};
//   - the finished game is stored against the deck, so it already has a win rate when first published.

import (
	"encoding/json"
	"fmt"
	"strings"

	"iduna/internal/hsdeck"
)

// syncDeckToLibrary stores the deck used in a finished game. It returns the library id (0 when skipped).
func (h *HSHandler) syncDeckToLibrary(sub string, dq queuedDeck, deck *hsdeck.Deck) int64 {
	id := h.syncDeckRow(sub, dq, deck)
	return id
}

func (h *HSHandler) syncDeckRow(sub string, dq queuedDeck, deck *hsdeck.Deck) int64 {
	if sub == "" || deck == nil || dq.id == "" || deck.Deckstring == "" {
		return 0
	}
	var id int64
	var deleted int
	if err := h.DB.QueryRow(`SELECT id, deleted FROM hs_decks WHERE owner_sub=? AND deckstring=?`, sub, deck.Deckstring).Scan(&id, &deleted); err == nil {
		if deleted == 1 {
			return 0 // the owner removed this exact deck; do not bring it back
		}
		// Identical contents are already in the library. Remember where it came from so a later change of
		// this Hearthstone deck is recognised as a change, not as a first sighting.
		h.DB.Exec(`UPDATE hs_decks SET source_deck_id=? WHERE id=? AND source_deck_id=''`, dq.id, id)
		return id
	}
	h.resolveNames(deck)
	title := strings.TrimSpace(dq.name)
	if title == "" {
		title = deck.Class + " deck"
	}
	var versions int
	h.DB.QueryRow(`SELECT COUNT(*) FROM hs_decks WHERE owner_sub=? AND source_deck_id=?`, sub, dq.id).Scan(&versions)
	if versions > 0 {
		title = fmt.Sprintf("%s (copy %d)", title, versions)
	}
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80])
	}
	cards, _ := json.Marshal(deck.Cards)
	if deck.Cards == nil {
		cards = []byte("[]")
	}
	dbf, _ := json.Marshal(deck.DBFCards)
	res, err := h.DB.Exec(`INSERT INTO hs_decks (owner_sub,title,description,class,format,year,hero_dbf,deckstring,cards_json,dbf_json,card_count,names_resolved,private,source_deck_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sub, title, "Synced from your game tracker.", deck.Class, deck.Format, deck.Year, deck.HeroDBF, deck.Deckstring,
		string(cards), string(dbf), deck.CardCount, boolInt(deck.NamesResolved), boolInt(!h.autoPublish(sub)), dq.id)
	if err != nil {
		return 0
	}
	id, _ = res.LastInsertId()
	return id
}
