package handlers

// nock_textures.go — real CRUD API for NOCK's texture library (founder real-time, 2026-09-12:
// "we are making a texture generator and manager so it needs to have CRUD and all that... we can
// build the human manual photoshop affordances after we get some basic texture management
// primitives built into the iduna nock console"). Every operation is a thin wrapper over
// internal/nock.TextureStore, the real SQLite-backed CRUD layer -- see that file's own header
// comment for the schema rationale and its real, explicit difference from CarePyre's resume-
// clone feature (many independent master textures, not one master with derived views).

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockTexturesHandler serves every /admin/nock/api/textures... route.
type NockTexturesHandler struct {
	Store *nock.TextureStore
}

func (h *NockTexturesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/textures"
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
	case len(parts) == 1 && parts[0] == "generate" && r.Method == http.MethodPost:
		h.generate(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.get(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPatch:
		h.update(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "image" && r.Method == http.MethodGet:
		h.image(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		h.clone(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "regenerate" && r.Method == http.MethodPatch:
		h.regenerate(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseTextureID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *NockTexturesHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListTextures(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createTextureReq struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Exactly one of Source (real PARENA source, compiled+rendered server-side) or
	// PNGBase64 (a plain, already-rendered image the caller supplies directly) is expected.
	Source    string `json:"source,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	PNGBase64 string `json:"png_base64,omitempty"`
}

func (h *NockTexturesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createTextureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Source != "" {
		t, err := h.Store.CreateProceduralTexture(r.Context(), req.Name, req.Source, req.Prompt, req.Width, req.Height)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, t)
		return
	}

	if req.PNGBase64 == "" {
		mmoWriteError(w, http.StatusBadRequest, "one of 'source' or 'png_base64' is required")
		return
	}
	pngData, err := base64.StdEncoding.DecodeString(req.PNGBase64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid png_base64: "+err.Error())
		return
	}
	t, err := h.Store.CreateTexture(r.Context(), req.Name, req.Width, req.Height, pngData, "", req.Prompt)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

type generateTextureReq struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// generate is the real "one-shot a texture from a prompt" endpoint for the texture library
// (distinct from nock.go's own project-scoped .../generate, which adds a Project layer instead
// of a library Texture row -- same real Vertex call underneath, different destination). A
// model's own failed output is a real, expected outcome, not papered over: the raw source is
// returned alongside the error so the caller can show what was attempted.
func (h *NockTexturesHandler) generate(w http.ResponseWriter, r *http.Request) {
	var req generateTextureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	token, err := nock.GcloudAccessToken()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "no Vertex AI credential available: "+err.Error())
		return
	}
	src, err := nock.GenerateProceduralTextureSource(r.Context(), token, req.Prompt, req.Width, req.Height)
	if err != nil {
		mmoWriteError(w, http.StatusBadGateway, "vertex generation failed: "+err.Error())
		return
	}
	t, err := h.Store.CreateProceduralTexture(r.Context(), req.Name, src, req.Prompt, req.Width, req.Height)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  err.Error(),
			"source": src,
		})
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *NockTexturesHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.Store.GetTexture(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *NockTexturesHandler) image(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.Store.GetTexture(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(t.PNGData)
}

type updateTextureReq struct {
	Name string `json:"name"`
}

func (h *NockTexturesHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateTextureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, err := h.Store.RenameTexture(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *NockTexturesHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteTexture(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type cloneTextureReq struct {
	Name string `json:"name"`
}

func (h *NockTexturesHandler) clone(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req cloneTextureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, err := h.Store.CloneTexture(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

type regenerateTextureReq struct {
	Source string `json:"source"`
}

func (h *NockTexturesHandler) regenerate(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseTextureID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req regenerateTextureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t, err := h.Store.RegenerateTexture(r.Context(), id, req.Source)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
