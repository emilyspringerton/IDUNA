package handlers

// shankpit_levels.go — real CRUD API for the SHANKPIT NOCK level editor v0 (EMILY/BACKLOG.md
// SECTION 459, founder real-time: "so v0 it and start working dont worry about the current
// levels lets just go full level select brawlpit repo exact model for now"). Every operation is a
// thin wrapper over internal/shankpit.LevelStore -- deliberately the same real, established shape
// as brawlpit_levels.go's own handler (mirrored field-for-field per the founder's own explicit
// "brawlpit repo exact model" instruction), served alongside it under the same /admin/nock/api/
// surface so the NOCK React app's own existing UI shell hosts this new tab too.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitLevelsHandler serves every /admin/nock/api/shankpit-levels... route.
type ShankpitLevelsHandler struct {
	Store *shankpit.LevelStore
}

func (h *ShankpitLevelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/shankpit-levels"
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
	case len(parts) == 0 && r.Method == http.MethodGet:
		h.list(w, r)
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.create(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.get(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.update(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPatch:
		h.rename(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		h.clone(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet:
		h.export(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseShankpitLevelID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *ShankpitLevelsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListLevels(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createShankpitLevelReq struct {
	Name               string          `json:"name"`
	Width              float64         `json:"width"`
	Height             float64         `json:"height"`
	Depth              float64         `json:"depth"`
	GroundPlaneEnabled bool            `json:"ground_plane_enabled"`
	GroundPlaneSquares int             `json:"ground_plane_squares"`
	Walls              []shankpit.Wall `json:"walls"`
}

func (h *ShankpitLevelsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.CreateLevel(r.Context(), req.Name, req.Width, req.Height, req.Depth, req.GroundPlaneEnabled, req.GroundPlaneSquares, req.Walls)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lvl)
}

func (h *ShankpitLevelsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lvl, err := h.Store.GetLevel(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

type updateShankpitLevelReq struct {
	Width              float64         `json:"width"`
	Height             float64         `json:"height"`
	Depth              float64         `json:"depth"`
	GroundPlaneEnabled bool            `json:"ground_plane_enabled"`
	GroundPlaneSquares int             `json:"ground_plane_squares"`
	Walls              []shankpit.Wall `json:"walls"`
}

// update is the real editor "save" action -- dimensions + the full wall layout replace the
// level's own current state together in one call (PUT, matching "replace this resource"
// semantics; the separate rename below is PATCH, matching brawlpit_levels.go's own established
// convention). This is the one endpoint both "create a cube" (append a default wall, save) and
// "face-drag editing" (adjust an existing wall's center/size, save) go through.
func (h *ShankpitLevelsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.UpdateLevel(r.Context(), id, req.Width, req.Height, req.Depth, req.GroundPlaneEnabled, req.GroundPlaneSquares, req.Walls)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

type renameShankpitLevelReq struct {
	Name string `json:"name"`
}

func (h *ShankpitLevelsHandler) rename(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req renameShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.RenameLevel(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

func (h *ShankpitLevelsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteLevel(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type cloneShankpitLevelReq struct {
	Name string `json:"name"`
}

func (h *ShankpitLevelsHandler) clone(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req cloneShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.CloneLevel(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lvl)
}

// export returns the real, native-loader-facing document -- the exact shape SHANKPIT's own
// map loader would read once it gains a JSON path (not built yet, see internal/shankpit's own
// package doc).
func (h *ShankpitLevelsHandler) export(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
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
