package handlers

// brawlpit_levels_public.go — S417-02, founder real-time: "brawlpit needs a level selection/
// browser interface it needs to work over https or some secure channel." A real, PUBLIC
// (unauthenticated), READ-ONLY API for the native BRAWLPIT client to list and fetch community
// levels -- distinct from brawlpit_levels.go's own admin-gated editing surface at
// /admin/nock/api/brawlpit-levels, which stays exactly as it is.
//
// Deliberately a SEPARATE, minimal handler type (not the write-capable BrawlpitLevelsHandler
// reused with a route restriction) -- this type has no create/update/rename/delete/clone methods
// at all, so a routing mistake can't accidentally expose a write path here. Served over the same
// real TLS termination every other okemily.com route already uses (this box's own existing
// HTTPS setup) -- "over https" is a property of how IDUNA is deployed, not something this
// handler does itself; there is no plaintext HTTP path to this same content.
//
// No auth is required by design: browsing already-created community levels is the same real
// shape as public discovery in any level-workshop feature (list + view, not edit) -- gating it
// would mean solving BPLE-12441 #286's full IDUNA-login-for-BRAWLPIT-players question (still
// deferred, see EMILY/BACKLOG.md SECTION 415/417) just to let a level be BROWSED, which the
// founder's own "get it online" framing doesn't ask for.

import (
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/brawlpit"
)

// BrawlpitLevelsPublicHandler serves every /api/v1/brawlpit-levels... GET route -- list and
// export only.
type BrawlpitLevelsPublicHandler struct {
	Store *brawlpit.LevelStore
}

func (h *BrawlpitLevelsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	path := r.URL.Path
	const prefix = "/api/v1/brawlpit-levels"
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

func (h *BrawlpitLevelsPublicHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListLevels(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// export returns the real, native-loader-facing document (BRAWLPIT/packages/common/
// level_format.h's own level_parse_json contract) -- exactly the same shape the admin-side
// export already returns (brawlpit_levels.go), just reachable without an admin cookie.
func (h *BrawlpitLevelsPublicHandler) export(w http.ResponseWriter, r *http.Request, idStr string) {
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
