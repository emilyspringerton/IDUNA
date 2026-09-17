package handlers

// shankpit_widgets.go — real CRUD API for SHANKPIT Widgets (S482, founder real-time, direct
// correction of the earlier S479-follow-up door-composition work: "i dont want to make doors be
// levels please - make widget or something they are both objects but widgets just dont show up
// in the levels menu"). Deliberately much smaller than shankpit_levels.go's own handler -- a
// Widget has no dimension, no ground plane, no rename/clone/export/story-start/default-queue
// actions, just list/create/get/update/delete over walls+doors.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitWidgetsHandler serves every /admin/nock/api/shankpit-widgets... route.
type ShankpitWidgetsHandler struct {
	Store *shankpit.WidgetStore
}

func (h *ShankpitWidgetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/shankpit-widgets"
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
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseShankpitWidgetID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *ShankpitWidgetsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListWidgets(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createShankpitWidgetReq struct {
	Name  string          `json:"name"`
	Walls []shankpit.Wall `json:"walls"`
	Doors []shankpit.Door `json:"doors"`
}

func (h *ShankpitWidgetsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitWidgetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	widget, err := h.Store.CreateWidget(r.Context(), req.Name, req.Walls, req.Doors)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, widget)
}

func (h *ShankpitWidgetsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitWidgetID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	widget, err := h.Store.GetWidget(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, widget)
}

type updateShankpitWidgetReq struct {
	Walls []shankpit.Wall `json:"walls"`
	Doors []shankpit.Door `json:"doors"`
}

func (h *ShankpitWidgetsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitWidgetID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateShankpitWidgetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	widget, err := h.Store.UpdateWidget(r.Context(), id, req.Walls, req.Doors)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, widget)
}

func (h *ShankpitWidgetsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitWidgetID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteWidget(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
