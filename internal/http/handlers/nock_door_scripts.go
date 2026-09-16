package handlers

// nock_door_scripts.go -- real CRUD + compile API for NOCK's door script repository. Same real
// shape as nock_textures.go, wrapping internal/nock.DoorScriptStore.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockDoorScriptsHandler serves every /admin/nock/api/door-scripts... route.
type NockDoorScriptsHandler struct {
	Store *nock.DoorScriptStore
}

func (h *NockDoorScriptsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/door-scripts"
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
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "regenerate" && r.Method == http.MethodPatch:
		h.regenerate(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseDoorScriptID(idStr string) (int64, error) { return strconv.ParseInt(idStr, 10, 64) }

func (h *NockDoorScriptsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListDoorScripts(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *NockDoorScriptsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	d, err := h.Store.CreateDoorScript(r.Context(), req.Name, req.Source)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *NockDoorScriptsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseDoorScriptID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := h.Store.GetDoorScript(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *NockDoorScriptsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseDoorScriptID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteDoorScript(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NockDoorScriptsHandler) regenerate(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseDoorScriptID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := h.Store.RegenerateDoorScript(r.Context(), id, req.Source)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}
