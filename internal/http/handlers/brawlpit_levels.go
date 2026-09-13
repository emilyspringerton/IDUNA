package handlers

// brawlpit_levels.go — real CRUD API for the BRAWLPIT online level editor (S415-02/03, founder
// real-time: "get the brawlpit level editor online - web technologies - we already started
// building nock - can we finish building out some of that interface so we can kind of parlay it
// into an online brawlpit level editor?"). Every operation is a thin wrapper over
// internal/brawlpit.LevelStore -- same real, established shape as nock_textures.go's own handler
// (mirrored deliberately), served alongside it under the same /admin/nock/api/ surface so the
// NOCK React app's own existing UI shell hosts this new tab too, per the founder's explicit
// "parlay [NOCK's] interface" framing.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/brawlpit"
)

// BrawlpitLevelsHandler serves every /admin/nock/api/brawlpit-levels... route.
type BrawlpitLevelsHandler struct {
	Store *brawlpit.LevelStore
}

func (h *BrawlpitLevelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/brawlpit-levels"
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
	case len(parts) == 2 && parts[1] == "guides" && r.Method == http.MethodPut:
		h.saveGuides(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseLevelID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *BrawlpitLevelsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListLevels(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createLevelReq struct {
	Name      string              `json:"name"`
	Width     float64             `json:"width"`
	Height    float64             `json:"height"`
	Platforms []brawlpit.Platform `json:"platforms"`
}

func (h *BrawlpitLevelsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.CreateLevel(r.Context(), req.Name, req.Width, req.Height, req.Platforms)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lvl)
}

func (h *BrawlpitLevelsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
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

type updateLevelReq struct {
	Width     float64             `json:"width"`
	Height    float64             `json:"height"`
	Platforms []brawlpit.Platform `json:"platforms"`
}

// update is the real editor "save" action -- size + full platform layout replace the level's
// own current state together in one call (PUT, matching "replace this resource" semantics; the
// separate rename below is PATCH, matching the narrower single-field-update convention
// nock_textures.go's own update already established).
func (h *BrawlpitLevelsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.UpdateLevel(r.Context(), id, req.Width, req.Height, req.Platforms)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

type renameLevelReq struct {
	Name string `json:"name"`
}

func (h *BrawlpitLevelsHandler) rename(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req renameLevelReq
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

func (h *BrawlpitLevelsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
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

type cloneLevelReq struct {
	Name string `json:"name"`
}

func (h *BrawlpitLevelsHandler) clone(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req cloneLevelReq
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

type saveGuidesReq struct {
	Guides []brawlpit.Guide `json:"guides"`
}

// saveGuides replaces a level's own real guide set (S418-01/02, "NOCK — Guide-Based Snapping") --
// a separate action from update()'s own platform-layout save, since ruler/guide edits are a real,
// independent interaction in the editor UI. Guides never appear in export() below -- that's the
// requirements doc's own explicit contract (1.4: "the game client must never load or care about
// them"), enforced by internal/brawlpit.ExportDoc simply having no Guides field at all.
func (h *BrawlpitLevelsHandler) saveGuides(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req saveGuidesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.SaveGuides(r.Context(), id, req.Guides)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// export returns the real, native-loader-facing document (S415-04: the exact JSON shape
// BRAWLPIT/packages/common/level_format.h's own level_parse_json reads) -- a real, direct
// download a founder/tester can save as data/levels/<name>.json and drop straight into a
// BRAWLPIT checkout.
func (h *BrawlpitLevelsHandler) export(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseLevelID(idStr)
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
