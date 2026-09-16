package handlers

// nock_door_scripts_public.go -- a real, PUBLIC (unauthenticated), READ-ONLY download route for
// a compiled door script's own raw .so bytes -- the real consumer is SHANKPIT's own server
// (packages/world/level_boxes.h's level_boxes_fetch_url + story_doors.h's dlopen), which has no
// IDUNA login of its own. Same real reasoning shankpit_levels_public.go's own doc comment already
// gives for level exports, and the same trust level GET /api/v1/shankpit-checkpoints/:id/download
// already established for the SHANKPIT-RL checkpoint registry: a compiled .so is only meaningful
// to a real consumer that already dlopens/dlsyms door_tick specifically -- public discovery of a
// server-side game asset, not a write path or sensitive data. Deliberately a SEPARATE, minimal
// handler (download-only, no create/update/delete) from NockDoorScriptsHandler's own admin-gated
// authoring surface at /admin/nock/api/door-scripts, same "a routing mistake can't accidentally
// expose a write path here" posture that handler's own public sibling already established.

import (
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockDoorScriptsPublicHandler serves every /api/v1/nock-door-scripts/:id/download GET route.
type NockDoorScriptsPublicHandler struct {
	Store *nock.DoorScriptStore
}

func (h *NockDoorScriptsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	path := r.URL.Path
	const prefix = "/api/v1/nock-door-scripts"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "download" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := h.Store.GetDoorScript(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(d.CompiledSO) //nolint:errcheck
}
