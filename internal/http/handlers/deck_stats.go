package handlers

import (
	"context"
	"errors"
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
//	GET /api/v1/games/deadweight/bot-decks?tier=N[&tiers=5][&min_games=4][&n=24]
//
// bot-decks is what the constructed-mode bots draw their decks from: tier 1 of `tiers` is the weakest slice of the draft-deck
// ranking (Wilson score among decks with at least min_games games), tier `tiers` the strongest (the champions); `n` decks are
// spread evenly across the band.
//
// Data comes from dw_server's decks.ndjson (see internal/deckstats), which a background loop folds into SQLite: a request
// only ever reads the database, never the (882 MiB and growing) log. Authenticated actions (tournaments etc.) are later work.
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
	if err := h.Store.Err(); err != nil {
		deckStoreError(w, err)
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
		decks, total, err := h.Store.ListCtx(r.Context(), qry)
		if err != nil {
			deckStoreError(w, err)
			return
		}
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
	case rest == "bot-decks":
		q := r.URL.Query()
		tier := atoiDefault(q.Get("tier"), 0)
		tiers := atoiDefault(q.Get("tiers"), 5)
		if tier < 1 || tier > tiers || tiers > 10 {
			mmoWriteError(w, http.StatusBadRequest, "tier must be 1..tiers (tiers 1..10)")
			return
		}
		res, err := h.Store.TierDecks(r.Context(), tier, tiers, atoiDefault(q.Get("min_games"), 4), atoiDefault(q.Get("n"), 24))
		if errors.Is(err, deckstats.ErrBadTier) {
			mmoWriteError(w, http.StatusBadRequest, "min_games must be >= 1 and n 1..100")
			return
		}
		if err != nil {
			deckStoreError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(w, http.StatusOK, res)
	case rest == "card-stats":
		writeJSON(w, http.StatusOK, map[string]any{"summary": h.Store.Summary(), "cards": h.Store.CardStats()})
	default:
		http.NotFound(w, r)
	}
}

// deckStoreError says why a read failed, and keeps the failure out of every cache.
func deckStoreError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case errors.Is(err, deckstats.ErrBusy):
		w.Header().Set("Retry-After", "2")
		mmoWriteError(w, http.StatusServiceUnavailable, "busy -- try again in a moment")
	case errors.Is(err, context.DeadlineExceeded):
		mmoWriteError(w, http.StatusServiceUnavailable, "that filter took too long -- narrow it (raise min games, pick a player type)")
	case errors.Is(err, context.Canceled):
		mmoWriteError(w, http.StatusServiceUnavailable, "request cancelled")
	default:
		mmoWriteError(w, http.StatusServiceUnavailable, "the deck tracker is unavailable")
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
