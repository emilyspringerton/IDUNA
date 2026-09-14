package handlers

// shankpit_sprays.go — real CRUD + public registry for SHANKPIT sprays (S459-19, founder
// real-time: "can we implement sprays? ... goes to sprays registry same treatment ... we need a
// nock sprays interface right now just to set the default"). Mirrors shankpit_materials.go's own
// real admin-CRUD-plus-public-registry split exactly.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitSpraysHandler serves every /admin/nock/api/shankpit-sprays... route.
type ShankpitSpraysHandler struct {
	Store *shankpit.SprayStore
}

func (h *ShankpitSpraysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/shankpit-sprays"
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
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "image" && r.Method == http.MethodGet:
		h.image(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "default" && r.Method == http.MethodPatch:
		h.setDefault(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseShankpitSprayID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *ShankpitSpraysHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListSprays(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createShankpitSprayReq struct {
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	PNGBase64 string `json:"png_base64"`
}

// create is the real target of NOCK's own "Export to Spray" button (founder: "CREATE SPRAYS FROM
// THE NOCK TEXTURE GENERATOR") -- the caller already has a rendered PNG (a Project's own export)
// and sends it here directly, same real png_base64 shape nock_textures.go's own create endpoint
// already established.
func (h *ShankpitSpraysHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitSprayReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	pngData, err := base64.StdEncoding.DecodeString(req.PNGBase64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid png_base64: "+err.Error())
		return
	}
	sp, err := h.Store.CreateSpray(r.Context(), req.Name, req.Width, req.Height, pngData)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

func (h *ShankpitSpraysHandler) image(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitSprayID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	sp, err := h.Store.GetSpray(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(sp.PNGData)
}

func (h *ShankpitSpraysHandler) setDefault(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitSprayID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	sp, err := h.Store.SetDefaultSpray(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (h *ShankpitSpraysHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitSprayID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteSpray(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ShankpitSpraysPublicHandler serves the real, live, unauthenticated sprays registry -- GET-only,
// matching ShankpitLevelsPublicHandler/ShankpitMaterialsPublicHandler's own established contract.
// The native client's own real entry points: list (the sprays menu) and default (startup
// fallback before the player has picked one).
type ShankpitSpraysPublicHandler struct {
	Store *shankpit.SprayStore
}

func (h *ShankpitSpraysPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	path := r.URL.Path
	const prefix = "/api/v1/shankpit-sprays"
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch {
	case len(parts) == 0:
		h.list(w, r)
	case len(parts) == 1 && parts[0] == "default":
		h.getDefault(w, r)
	case len(parts) == 2 && parts[1] == "image":
		h.image(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func (h *ShankpitSpraysPublicHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListSprays(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *ShankpitSpraysPublicHandler) getDefault(w http.ResponseWriter, r *http.Request) {
	sp, err := h.Store.GetDefaultSpray(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sp == nil {
		mmoWriteError(w, http.StatusNotFound, "no default spray set")
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (h *ShankpitSpraysPublicHandler) image(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitSprayID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	sp, err := h.Store.GetSpray(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(sp.PNGData)
}
