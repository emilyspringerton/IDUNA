package handlers

// nock_edu_snippets.go -- NOCK EduVM snippet API (card #495). Admin-gated authoring CRUD at
// /admin/nock/api/edu-snippets and a PUBLIC read-only list at /api/v1/nock-edu-snippets[?widget=NAME] for the
// game client (SHANKPIT's lobby has no IDUNA login; same posture as nock_door_scripts_public.go -- a snippet is
// plain script text the game's sandboxed, capability-limited EduVM runs, not a write path or sensitive data).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

type NockEduSnippetsHandler struct {
	Store *nock.EduSnippetStore
}

func (h *NockEduSnippetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/admin/nock/api/edu-snippets"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		h.list(w, r)
	case rest == "" && r.Method == http.MethodPost:
		var req struct {
			Name   string `json:"name"`
			Widget string `json:"widget_name"`
			Source string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		e, err := h.Store.Create(r.Context(), req.Name, req.Widget, req.Source)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, e)
	case rest != "" && !strings.Contains(rest, "/"):
		id, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		switch r.Method {
		case http.MethodGet:
			e, err := h.Store.Get(r.Context(), id)
			if err != nil {
				mmoWriteError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, e)
		case http.MethodPut:
			var req struct {
				Source string `json:"source"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			e, err := h.Store.Update(r.Context(), id, req.Source)
			if err != nil {
				mmoWriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, e)
		case http.MethodDelete:
			if err := h.Store.Delete(r.Context(), id); err != nil {
				mmoWriteError(w, http.StatusNotFound, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (h *NockEduSnippetsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.List(r.Context(), r.URL.Query().Get("widget"))
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// NockEduSnippetsPublicHandler serves GET /api/v1/nock-edu-snippets[?widget=NAME] -- read-only.
type NockEduSnippetsPublicHandler struct {
	Store *nock.EduSnippetStore
}

func (h *NockEduSnippetsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	if strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/nock-edu-snippets"), "/") != "" {
		http.NotFound(w, r)
		return
	}
	list, err := h.Store.List(r.Context(), r.URL.Query().Get("widget"))
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
