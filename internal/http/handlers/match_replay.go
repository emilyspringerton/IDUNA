package handlers

// match_replay.go -- WOTAN S547 (EMILY/BACKLOG.md SECTION 547): public, read-only DEADWEIGHT
// match-history + replay data. Sibling to deck_stats.go (that one reads dw_server's decks.ndjson
// for deck win rates; this one reads matches.ndjson for individual match records) and to
// game_online.go's leaderboard()/stats() (those read the game_player_stats SQL table for
// aggregate rating/W-L-D; this file is per-MATCH, not per-player).
//
//	GET /api/v1/games/deadweight/matches?limit=&offset=&player=&mode=draft|card
//	GET /api/v1/games/deadweight/matches/{match_id}
//	GET /api/v1/games/deadweight/matches/{match_id}/replay
//
// The replay route shells out to DEADWEIGHT's own dw_replay_dump (DEADWEIGHT/tools/replay_dump.c),
// which replays the match through the real, authoritative C match core (core/match.c -- the same
// deterministic-replay guarantee DEADWEIGHT/tests/replay_check.c already proves) and prints a
// full round-by-round JSON trace. This process never touches dw_server/dw_bot (the live match
// services) -- it is a separate, read-only binary invoked fresh per request.

import (
	"bytes"
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"iduna/internal/http/middleware"
	"iduna/internal/matchlog"
)

const matchReplayPrefix = "/api/v1/games/deadweight/matches"

// DefaultReplayDumpBin is where DEADWEIGHT/scripts/deploy_user.sh installs dw_replay_dump,
// matching dw_server/dw_bot's own ~/.local/opt/deadweight/bin install convention.
const DefaultReplayDumpBin = "/home/fatbaby/.local/opt/deadweight/bin/dw_replay_dump"

// MatchReplayHandler serves the routes above.
type MatchReplayHandler struct {
	Store     *matchlog.Store
	ReplayBin string // "" = DefaultReplayDumpBin
	Limiter   *middleware.IPRateLimiter
}

func (h *MatchReplayHandler) replayBin() string {
	if h.ReplayBin != "" {
		return h.ReplayBin
	}
	return DefaultReplayDumpBin
}

func (h *MatchReplayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow(clientIP(r)) {
		mmoWriteError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, matchReplayPrefix), "/")
	w.Header().Set("Cache-Control", "public, max-age=5")
	switch {
	case rest == "":
		h.list(w, r)
	case strings.HasSuffix(rest, "/replay"):
		idStr := strings.TrimSuffix(rest, "/replay")
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid match id")
			return
		}
		h.replay(w, r, uint32(id))
	default:
		id, err := strconv.ParseUint(rest, 10, 32)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid match id")
			return
		}
		h.one(w, r, uint32(id))
	}
}

func (h *MatchReplayHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := matchlog.Query{
		Player: q.Get("player"),
		Mode:   q.Get("mode"),
		Limit:  atoiDefault(q.Get("limit"), 50),
		Offset: atoiDefault(q.Get("offset"), 0),
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	matches, total := h.Store.Recent(query)
	if matches == nil {
		matches = []matchlog.Summary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "count_in_memory": h.Store.Count(), "limit": query.Limit, "offset": query.Offset, "matches": matches,
	})
}

func (h *MatchReplayHandler) one(w http.ResponseWriter, r *http.Request, id uint32) {
	_, sum, ok := h.Store.Raw(id)
	if !ok {
		mmoWriteError(w, http.StatusNotFound, "no such match (or it aged out of the recent-matches window)")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (h *MatchReplayHandler) replay(w http.ResponseWriter, r *http.Request, id uint32) {
	raw, _, ok := h.Store.Raw(id)
	if !ok {
		mmoWriteError(w, http.StatusNotFound, "no such match (or it aged out of the recent-matches window)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.replayBin())
	cmd.Stdin = strings.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "replay engine failed: "+stderr.String())
		return
	}
	// dw_replay_dump already emits well-formed JSON -- forward it verbatim rather than
	// round-tripping through Go structs (its output shape is documented in its own header
	// comment; re-parsing here would just be a second place to keep in sync with the C tool).
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout.Bytes())
}
