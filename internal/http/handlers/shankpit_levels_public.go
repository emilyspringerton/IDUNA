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
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitLevelsPublicHandler serves every /api/v1/shankpit-levels... GET route -- list and
// export only.
type ShankpitLevelsPublicHandler struct {
	Store *shankpit.LevelStore
}

func (h *ShankpitLevelsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.Store.ListLevels(r.Context())
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
