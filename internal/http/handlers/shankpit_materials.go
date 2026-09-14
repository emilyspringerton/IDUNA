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
}

func (h *ShankpitMaterialsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitMaterialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	m, err := h.Store.CreateMaterial(r.Context(), req.Name, req.ShaderName, req.TextureID, req.Specular, req.Shininess)
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
	m, err := h.Store.UpdateMaterial(r.Context(), id, req.ShaderName, req.TextureID, req.Specular, req.Shininess)
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
