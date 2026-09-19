package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/deckstats"
	"iduna/internal/http/middleware"
)

// DeckStatsHandler serves WOTAN's public, unauthenticated DEADWEIGHT draft-deck browser data (read-only):
//
//	GET /api/v1/games/deadweight/decks?sort=winrate|games|recent&min_games=N&kind=bot|human&player=..&card=ID&limit=&offset=
//	GET /api/v1/games/deadweight/decks/{deck_id}
//	GET /api/v1/games/deadweight/card-stats
//
// Data comes from dw_server's decks.ndjson (see internal/deckstats). Authenticated actions (tournaments etc.) are later work.
type DeckStatsHandler struct {
	Store   *deckstats.Store
	Limiter *middleware.IPRateLimiter // nil = unlimited (tests)
}

const deckStatsPrefix = "/api/v1/games/deadweight/"

func (h *DeckStatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow(clientIP(r)) {
		mmoWriteError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, deckStatsPrefix), "/")
	w.Header().Set("Cache-Control", "public, max-age=5")
	switch {
	case rest == "decks":
		q := r.URL.Query()
		qry := deckstats.Query{Sort: q.Get("sort"), Kind: q.Get("kind"), Player: q.Get("player"), Card: -1, Limit: 50}
		qry.MinGames = atoiDefault(q.Get("min_games"), 1)
		if c := q.Get("card"); c != "" {
			qry.Card = atoiDefault(c, -1)
		}
		qry.Limit = atoiDefault(q.Get("limit"), 50)
		if qry.Limit < 1 {
			qry.Limit = 1
		}
		if qry.Limit > 200 {
			qry.Limit = 200
		}
		qry.Offset = atoiDefault(q.Get("offset"), 0)
		if qry.Offset < 0 {
			qry.Offset = 0
		}
		decks, total := h.Store.List(qry)
		if decks == nil {
			decks = []deckstats.Deck{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"summary": h.Store.Summary(), "total": total, "offset": qry.Offset, "limit": qry.Limit, "decks": decks})
	case strings.HasPrefix(rest, "decks/"):
		id, err := strconv.ParseUint(strings.TrimPrefix(rest, "decks/"), 10, 32)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid deck id")
			return
		}
		d, ok := h.Store.Deck(uint32(id))
		if !ok {
			mmoWriteError(w, http.StatusNotFound, "no such deck")
			return
		}
		writeJSON(w, http.StatusOK, d)
	case rest == "card-stats":
		writeJSON(w, http.StatusOK, map[string]any{"summary": h.Store.Summary(), "cards": h.Store.CardStats()})
	default:
		http.NotFound(w, r)
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
