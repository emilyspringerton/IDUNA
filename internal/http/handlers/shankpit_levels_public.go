package handlers

// shankpit_levels_public.go — a real, PUBLIC (unauthenticated), READ-ONLY API for the native
// SHANKPIT client to eventually list and fetch community levels -- distinct from
// shankpit_levels.go's own admin-gated editing surface at /admin/nock/api/shankpit-levels, which
// stays exactly as it is. Mirrors brawlpit_levels_public.go's own established shape and rationale
// field-for-field (EMILY/BACKLOG.md SECTION 459, "brawlpit repo exact model").
//
// Deliberately a SEPARATE, minimal handler type (not the write-capable ShankpitLevelsHandler
// reused with a route restriction) -- this type has no create/update/rename/delete/clone methods
// at all, so a routing mistake can't accidentally expose a write path here.
//
// No auth required, same real reasoning brawlpit_levels_public.go's own doc comment already
// gives: browsing already-created community levels is public discovery, not editing; gating it
// would mean solving a full IDUNA-login-for-SHANKPIT-players question that hasn't been asked for
// yet, just to let a level be browsed.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iduna/internal/http/middleware"
	"iduna/internal/shankpit"
)

// ShankpitLevelsPublicHandler serves every /api/v1/shankpit-levels... GET route -- list and
// export only.
type ShankpitLevelsPublicHandler struct {
	Store *shankpit.LevelStore
	// SnapshotLimiter rate-limits POST .../snapshots per client IP; nil disables the limit (tests).
	SnapshotLimiter *middleware.IPRateLimiter
}

func (h *ShankpitLevelsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/shankpit-levels/snapshots" {
		h.snapshot(w, r)
		return
	}
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	path := r.URL.Path
	const prefix = "/api/v1/shankpit-levels"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	switch {
	case len(parts) == 0:
		h.list(w, r)
	case len(parts) == 2 && parts[1] == "export":
		h.export(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func (h *ShankpitLevelsPublicHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListLevelsIn(r.Context(), r.URL.Query().Get("collection"))
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *ShankpitLevelsPublicHandler) export(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	doc, err := h.Store.Export(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

type snapshotReq struct {
	SourceLevelID int64                `json:"source_level_id"`
	BrickDamage   []shankpit.BrickCell `json:"brick_damage"`
	// Collection "zombies" files the snapshot in the ZOMBIES level repository; SetDefault makes it the
	// level the sandbox loads next. Anything else = the main registry.
	Collection string `json:"collection"`
	SetDefault bool   `json:"set_default"`
}

// snapshot is the one narrow, unauthenticated WRITE this handler exposes (founder real-time, 2026-10-02:
// F1 in the game uploads a copy of the level; leaving a level auto-saves it). It can only ever INSERT a
// new row cloned from an existing level under a server-clocked second-precision name -- never update or
// delete -- and is per-IP rate limited and body-capped. A same-second repeat answers 200 with
// {"duplicate":true} instead of creating a second row. Real player/guest auth is the known follow-up.
func (h *ShankpitLevelsPublicHandler) snapshot(w http.ResponseWriter, r *http.Request) {
	if h.SnapshotLimiter != nil && !h.SnapshotLimiter.Allow(clientIPFor(r)) {
		w.Header().Set("Retry-After", "60")
		mmoWriteError(w, http.StatusTooManyRequests, "too many snapshots; try again shortly")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	var req snapshotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.SnapshotLevelTo(r.Context(), req.SourceLevelID, req.BrickDamage, time.Now(), req.Collection, req.SetDefault)
	if errors.Is(err, shankpit.ErrSnapshotExists) {
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
		return
	}
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": lvl.ID, "name": lvl.Name, "brick_cells": len(lvl.BrickDamage)})
}

func clientIPFor(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	return r.RemoteAddr
}
