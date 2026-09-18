package handlers

// game_checkpoints.go -- S503-06. /api/v1/game-checkpoints/{game}/... : the game-scoped checkpoint registry.
// It is BrawlpitCheckpointsHandler mounted with a per-game Prefix + Store.Game (no copied logic): same
// routes, fields and multipart format as /api/v1/brawlpit-checkpoints, isolated per game by the store.
// Reads are public; POST upload and POST match-result need the game's checkpoints-write permission.

import (
	"net/http"
	"strings"
	"sync"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/brawlpit"
	"iduna/internal/games"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"

	"database/sql"
)

type GameCheckpointsRouter struct {
	DB       *sql.DB
	Keys     *authjwt.Keys
	Games    map[string]games.Config // nil = games.Registry
	EventLog userlog.EventLog        // optional

	once   sync.Once
	gated  map[string]http.Handler
	public map[string]*BrawlpitCheckpointsHandler
}

func (h *GameCheckpointsRouter) init() {
	cfgs := h.Games
	if cfgs == nil {
		cfgs = games.Registry
	}
	h.gated = map[string]http.Handler{}
	h.public = map[string]*BrawlpitCheckpointsHandler{}
	for slug, cfg := range cfgs {
		inner := &BrawlpitCheckpointsHandler{
			Prefix:   "/api/v1/game-checkpoints/" + slug,
			Store:    &brawlpit.CheckpointStore{DB: h.DB, BlobDir: cfg.CheckpointBlobDir, Game: slug},
			EventLog: h.EventLog,
		}
		h.public[slug] = inner
		h.gated[slug] = middleware.RequireAuth(h.Keys)(middleware.RequirePermission(cfg.CheckpointsWritePerm)(inner))
	}
}

func (h *GameCheckpointsRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(h.init)
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/game-checkpoints/")
	slug := strings.SplitN(strings.Trim(rest, "/"), "/", 2)[0]
	pub, ok := h.public[slug]
	if !ok {
		mmoWriteError(w, http.StatusNotFound, "unknown game")
		return
	}
	isWrite := r.Method == http.MethodPost // upload (root) and /match-result are the only POST routes
	if isWrite {
		h.gated[slug].ServeHTTP(w, r)
		return
	}
	pub.ServeHTTP(w, r)
}
