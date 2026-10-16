package handlers

// shankpit_styles.go -- admin CRUD for SHANKPIT cube styles (see shankpit/style_store.go).
//   GET    /admin/nock/api/shankpit-styles?level_id=N   global styles + that level's local styles
//   POST   /admin/nock/api/shankpit-styles               body = CubeStyle (level_id null = global)
//   PUT    /admin/nock/api/shankpit-styles/{id}          body = CubeStyle
//   DELETE /admin/nock/api/shankpit-styles/{id}

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
)

type ShankpitStylesHandler struct{ Store *shankpit.StyleStore }

func (h *ShankpitStylesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/admin/nock/api/shankpit-styles"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		lv, _ := strconv.ParseInt(r.URL.Query().Get("level_id"), 10, 64)
		list, err := h.Store.List(r.Context(), lv)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	case rest == "" && r.Method == http.MethodPost:
		var st shankpit.CubeStyle
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		out, err := h.Store.Create(r.Context(), st)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, out)
	case rest != "" && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if r.Method == http.MethodDelete {
			if err := h.Store.Delete(r.Context(), id); err != nil {
				mmoWriteError(w, http.StatusNotFound, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var st shankpit.CubeStyle
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		out, err := h.Store.Update(r.Context(), id, st)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	default:
		http.NotFound(w, r)
	}
}
