package handlers

// shankpit_materials.go — real CRUD + public registry for SHANKPIT block materials (S459-16,
// founder real-time: "we will need the ability to add new materials and set their textures" /
// "registries for everything"). Mirrors shankpit_levels.go's own real admin-CRUD-plus-public-
// read-only-registry split exactly: /admin/nock/api/shankpit-materials for the NOCK editor's own
// add/edit/delete UI, /api/v1/shankpit-materials for the real, live, unauthenticated registry a
// native client (or anything else) can browse -- the same real shape the level registry already
// established (packages/world/level_boxes.h's own level_boxes_fetch_registry_list).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitMaterialsHandler serves every /admin/nock/api/shankpit-materials... route.
type ShankpitMaterialsHandler struct {
	Store *shankpit.MaterialStore
}

func (h *ShankpitMaterialsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/shankpit-materials"
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
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.update(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseShankpitMaterialID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *ShankpitMaterialsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListMaterials(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createShankpitMaterialReq struct {
	Name       string  `json:"name"`
	ShaderName string  `json:"shader_name"`
	TextureID  *int64  `json:"texture_id"`
	Specular   float64 `json:"specular"`
	Shininess  float64 `json:"shininess"`
	// Friction (S478b) -- omitted/zero in an old client's request body decodes to a real 0.0,
	// NOT the DB column's own 0.30 default (an explicit INSERT column list, see CreateMaterial),
	// so a stale frontend build would silently create frictionless materials. Guarded against
	// exactly that in create() below.
	Friction float64 `json:"friction"`
}

// shankpitMaterialFrictionUnset is the real sentinel a request body's own JSON decode can't tell
// apart from "the author deliberately typed 0" -- Go's zero value for float64 is 0, same as a
// missing key. Real, honest tradeoff (not solved with a *float64, to keep the request struct/
// frontend contract simple): a genuinely-intended 0.0 (frictionless ice) gets the same real 0.30
// fallback as an old client omitting the field entirely. Named here, not silently accepted --
// picking a deliberate near-zero (e.g. 0.01) instead of exactly 0 is the real, current way to
// author a truly frictionless material until this gets a sharper (non-zero-collapsing) contract.
const shankpitMaterialFrictionDefault = 0.30

func (h *ShankpitMaterialsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitMaterialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	friction := req.Friction
	if friction == 0 {
		friction = shankpitMaterialFrictionDefault
	}
	m, err := h.Store.CreateMaterial(r.Context(), req.Name, req.ShaderName, req.TextureID, req.Specular, req.Shininess, friction)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

type updateShankpitMaterialReq struct {
	ShaderName string  `json:"shader_name"`
	TextureID  *int64  `json:"texture_id"`
	Specular   float64 `json:"specular"`
	Shininess  float64 `json:"shininess"`
	Friction   float64 `json:"friction"`
}

func (h *ShankpitMaterialsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitMaterialID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateShankpitMaterialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	friction := req.Friction
	if friction == 0 {
		friction = shankpitMaterialFrictionDefault
	}
	m, err := h.Store.UpdateMaterial(r.Context(), id, req.ShaderName, req.TextureID, req.Specular, req.Shininess, friction)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *ShankpitMaterialsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitMaterialID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteMaterial(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ShankpitMaterialsPublicHandler serves the real, live, unauthenticated materials registry --
// GET-only, matching ShankpitLevelsPublicHandler's own established contract exactly.
type ShankpitMaterialsPublicHandler struct {
	Store *shankpit.MaterialStore
}

func (h *ShankpitMaterialsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	list, err := h.Store.ListMaterials(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
