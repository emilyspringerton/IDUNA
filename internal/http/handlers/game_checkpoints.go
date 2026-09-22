package handlers

// game_checkpoints.go -- S503-06. /api/v1/game-checkpoints/{game}/... : the game-scoped checkpoint registry.
// It is BrawlpitCheckpointsHandler mounted with a per-game Prefix + Store.Game (no copied logic): same
// routes, fields and multipart format as /api/v1/brawlpit-checkpoints, isolated per game by the store.
// Reads are public; POST upload and POST match-result need the game's checkpoints-write permission.

import (
	"net/http"
	"os"
	"strings"
	"sync"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/brawlpit"
	"iduna/internal/games"
	"iduna/internal/http/middleware"
	"iduna/internal/modelgit"
	"iduna/internal/userlog"

	"database/sql"
)

type GameCheckpointsRouter struct {
	DB       *sql.DB
	Keys     *authjwt.Keys
	Games    map[string]games.Config // nil = games.Registry
	EventLog userlog.EventLog        // optional
	// ModelGitRepoDirs maps a game slug to its real sibling repo root -- the destination this
	// game's model-repository git integration syncs checkpoint blobs into (founder real-time,
	// 2026-09-22: "we need to integrate the model repository with git lfs..."). A slug with no
	// entry (or a nil map) gets a zero-value modelgit.Syncer -- RepoDir empty is a real no-op,
	// not an error, same as every other real store this package constructs.
	ModelGitRepoDirs map[string]string

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
		gitSync := modelgit.Syncer{
			RepoDir: h.ModelGitRepoDirs[slug],
			RelDir:  "models/rl-checkpoints",
			// <SLUG>_MODEL_GIT_DISABLED (any non-empty value) -- the real, per-game "turned off"
			// half of the founder's own "on by default" requirement, checked at request time so
			// an operator doesn't need a redeploy to flip it... actually read once at init() time
			// (real, simple, matches every other env-driven config value in this file's own
			// sibling construction sites in main.go, e.g. DEADWEIGHT_STEAM_APPID) -- a redeploy
			// (or, if the founder later asks for it, a live admin toggle) is the real way to
			// change it, not a per-request env re-read.
			Disabled:  os.Getenv(strings.ToUpper(slug)+"_MODEL_GIT_DISABLED") != "",
			LogPrefix: slug + "-model-git",
		}
		inner := &BrawlpitCheckpointsHandler{
			Prefix:   "/api/v1/game-checkpoints/" + slug,
			Store:    &brawlpit.CheckpointStore{DB: h.DB, BlobDir: cfg.CheckpointBlobDir, Game: slug, GitSync: gitSync},
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

// GameCheckpointsAdminRouter serves the NOCK (admin-cookie) write actions for every game-scoped registry:
// PATCH /admin/nock/api/game-checkpoints/{game}/{id}/activate and .../disable. It reuses the BRAWLPIT handlers
// (same Store type, scoped by Game) by rewriting the path to the shape those handlers parse.
type GameCheckpointsAdminRouter struct {
	DB       *sql.DB
	Games    map[string]games.Config // nil = games.Registry
	EventLog userlog.EventLog

	once     sync.Once
	activate map[string]*BrawlpitCheckpointActivateHandler
	disable  map[string]*BrawlpitCheckpointDisableHandler
}

func (h *GameCheckpointsAdminRouter) init() {
	cfgs := h.Games
	if cfgs == nil {
		cfgs = games.Registry
	}
	h.activate = map[string]*BrawlpitCheckpointActivateHandler{}
	h.disable = map[string]*BrawlpitCheckpointDisableHandler{}
	for slug, cfg := range cfgs {
		st := &brawlpit.CheckpointStore{DB: h.DB, BlobDir: cfg.CheckpointBlobDir, Game: slug}
		h.activate[slug] = &BrawlpitCheckpointActivateHandler{Store: st, EventLog: h.EventLog}
		h.disable[slug] = &BrawlpitCheckpointDisableHandler{Store: st, EventLog: h.EventLog}
	}
}

func (h *GameCheckpointsAdminRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.once.Do(h.init)
	rest := strings.TrimPrefix(r.URL.Path, "/admin/nock/api/game-checkpoints/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	slug, id, verb := parts[0], parts[1], parts[2]
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/admin/nock/api/brawlpit-checkpoints/" + id + "/" + verb
	switch verb {
	case "activate":
		if hd, ok := h.activate[slug]; ok {
			hd.ServeHTTP(w, r2)
			return
		}
	case "disable":
		if hd, ok := h.disable[slug]; ok {
			hd.ServeHTTP(w, r2)
			return
		}
	}
	http.NotFound(w, r)
}
