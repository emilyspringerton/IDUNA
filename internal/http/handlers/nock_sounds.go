package handlers

// nock_sounds.go -- NOCK's sound library + shareable filter chains (founder real-time,
// 2026-09-27: "nock and shankpit engine need sound engineering primatives we need a way to
// upload and record in nock as well as pass filters around"). Thin HTTP layer over
// internal/nock.SoundStore; admin-gated in main.go like every other /admin/nock/api route.
//
//   GET    /admin/nock/api/sounds                      list (metadata only)
//   POST   /admin/nock/api/sounds?name=&source=&...    raw audio body (Content-Type: audio/*)
//   GET    /admin/nock/api/sounds/:id                  metadata
//   GET    /admin/nock/api/sounds/:id/audio            the audio bytes
//   PATCH  /admin/nock/api/sounds/:id                  {"name"}
//   DELETE /admin/nock/api/sounds/:id
//
//   GET    /admin/nock/api/sound-filters               list
//   POST   /admin/nock/api/sound-filters               {"name","description","chain"}
//   GET    /admin/nock/api/sound-filters/:id
//   PATCH  /admin/nock/api/sound-filters/:id           any of {"name","description","chain"}
//   POST   /admin/nock/api/sound-filters/:id/clone     {"name"}
//   DELETE /admin/nock/api/sound-filters/:id
//
// Upload is a raw body rather than base64-in-JSON (the texture API's shape) because audio is
// routinely tens of MB; metadata rides in the query string.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockSoundsHandler serves /admin/nock/api/sounds... and /admin/nock/api/sound-filters...
type NockSoundsHandler struct {
	Store *nock.SoundStore
}

func splitRest(path, prefix string) ([]string, bool) {
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		return nil, false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return []string{}, true
	}
	return strings.Split(rest, "/"), true
}

func (h *NockSoundsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if parts, ok := splitRest(r.URL.Path, "/admin/nock/api/sound-filters"); ok {
		h.serveFilters(w, r, parts)
		return
	}
	if parts, ok := splitRest(r.URL.Path, "/admin/nock/api/sounds"); ok {
		h.serveSounds(w, r, parts)
		return
	}
	http.NotFound(w, r)
}

func (h *NockSoundsHandler) serveSounds(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		list, err := h.Store.ListSounds(r.Context())
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.createSound(w, r)
	case len(parts) >= 1:
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			s, err := h.Store.GetSound(r.Context(), id)
			if err != nil {
				mmoWriteError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, s)
		case len(parts) == 2 && parts[1] == "audio" && r.Method == http.MethodGet:
			s, err := h.Store.GetSoundAudio(r.Context(), id)
			if err != nil {
				mmoWriteError(w, http.StatusNotFound, err.Error())
				return
			}
			w.Header().Set("Content-Type", s.MimeType)
			w.Header().Set("Content-Length", strconv.Itoa(len(s.AudioData)))
			w.Header().Set("ETag", `"`+s.ContentHash+`"`)
			w.Write(s.AudioData) //nolint:errcheck
		case len(parts) == 1 && r.Method == http.MethodPatch:
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			s, err := h.Store.RenameSound(r.Context(), id, req.Name)
			if err != nil {
				mmoWriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, s)
		case len(parts) == 1 && r.Method == http.MethodDelete:
			if err := h.Store.DeleteSound(r.Context(), id); err != nil {
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

func optInt64(q string) (*int64, error) {
	if q == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (h *NockSoundsHandler) createSound(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(k string) int64 {
		v, _ := strconv.ParseInt(q.Get(k), 10, 64)
		return v
	}
	parent, err := optInt64(q.Get("parent_id"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid parent_id")
		return
	}
	filter, err := optInt64(q.Get("filter_id"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid filter_id")
		return
	}
	source := q.Get("source")
	if source == "" {
		source = "upload"
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, nock.MaxSoundBytes+1))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "reading body: "+err.Error())
		return
	}
	if len(data) > nock.MaxSoundBytes {
		mmoWriteError(w, http.StatusRequestEntityTooLarge, "audio too large")
		return
	}
	s, err := h.Store.CreateSound(r.Context(), nock.SoundMeta{
		Name: q.Get("name"), MimeType: r.Header.Get("Content-Type"), Source: source,
		DurationMS: num("duration_ms"), SampleRate: num("sample_rate"), Channels: num("channels"),
		ParentID: parent, FilterID: filter,
	}, data)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

type soundFilterReq struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Chain       json.RawMessage `json:"chain"`
}

func (h *NockSoundsHandler) serveFilters(w http.ResponseWriter, r *http.Request, parts []string) {
	ctx := r.Context()
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			list, err := h.Store.ListFilters(ctx)
			if err != nil {
				mmoWriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, list)
		case http.MethodPost:
			var req soundFilterReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == nil || len(req.Chain) == 0 {
				mmoWriteError(w, http.StatusBadRequest, "expected JSON {name, description?, chain}")
				return
			}
			desc := ""
			if req.Description != nil {
				desc = *req.Description
			}
			f, err := h.Store.CreateFilter(ctx, *req.Name, desc, req.Chain)
			if err != nil {
				mmoWriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, f)
		default:
			http.NotFound(w, r)
		}
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		f, err := h.Store.GetFilter(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, f)
	case len(parts) == 1 && r.Method == http.MethodPatch:
		var req soundFilterReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		var chain []byte
		if len(req.Chain) > 0 {
			chain = req.Chain
		}
		f, err := h.Store.UpdateFilter(ctx, id, req.Name, req.Description, chain)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, f)
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		f, err := h.Store.CloneFilter(ctx, id, req.Name)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, f)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeleteFilter(ctx, id); err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// NockSoundFiltersPublicHandler: PUBLIC, READ-ONLY chain lookup by name for engines with no
// IDUNA login (SHANKPIT's server/client applying a designer's chain), same trust posture as
// nock_door_scripts_public.go: a filter chain is a list of numbers, not a write path or secret.
//
//	GET /api/v1/nock-sound-filters/<name>   -> {"name", "description", "chain"}
type NockSoundFiltersPublicHandler struct {
	Store *nock.SoundStore
}

func (h *NockSoundFiltersPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	parts, ok := splitRest(r.URL.Path, "/api/v1/nock-sound-filters")
	if !ok || len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	f, err := h.Store.GetFilterByName(r.Context(), parts[0])
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": f.Name, "description": f.Description, "chain": f.Chain})
}
