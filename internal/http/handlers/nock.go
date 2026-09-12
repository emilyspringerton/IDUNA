package handlers

// nock.go — JSON API backing the NOCK editor (founder real-time, 2026-09-12: "we are gonna need
// to build our own tools to create the textures... lets build it on top of imagemagic for now...
// lets yolo it into iduna"). Real, deliberate design: every operation here is a thin wrapper
// around internal/nock.Service — the exact same package cmd/nock's own CLI calls, so "same shape
// CLI and GUI" (the founder's own framing) is achieved by sharing one Go package as the only
// place any real logic lives, not by keeping two implementations in sync by hand.
//
// Multipart form uploads (not JSON bodies) back every endpoint that accepts an image file
// (layer-add, layer-mask) -- an image is binary, JSON isn't the right shape for it.
//
// See docs/NOCK_NORTHSTAR.md for the real, phased plan and what's deliberately deferred (PARENA
// backend, 3D modeler, level editor, drag-and-drop reorder, arbitrary-angle gradients).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockHandler serves every /admin/nock/api/... route.
type NockHandler struct {
	Svc *nock.Service
}

func (h *NockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	parts := strings.Split(rest, "/")

	switch {
	case rest == "projects" && r.Method == http.MethodGet:
		h.listProjects(w, r)
	case rest == "projects" && r.Method == http.MethodPost:
		h.createProject(w, r)
	case len(parts) == 2 && parts[0] == "projects" && r.Method == http.MethodGet:
		h.getProject(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "projects" && r.Method == http.MethodDelete:
		h.deleteProject(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "layers" && r.Method == http.MethodPost:
		h.addLayer(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "projects" && parts[2] == "layers" && r.Method == http.MethodDelete:
		h.removeLayer(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "opacity" && r.Method == http.MethodPatch:
		h.setOpacity(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "visible" && r.Method == http.MethodPatch:
		h.setVisible(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "move" && r.Method == http.MethodPatch:
		h.moveLayer(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "mask" && r.Method == http.MethodPost:
		h.setMask(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "mask" && r.Method == http.MethodDelete:
		h.clearMask(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "hue-sat" && r.Method == http.MethodPatch:
		h.hueSat(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "sharpen" && r.Method == http.MethodPatch:
		h.sharpen(w, r, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "gradient" && r.Method == http.MethodPost:
		h.addGradient(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "procedural" && r.Method == http.MethodPost:
		h.addProcedural(w, r, parts[1])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "procedural" && r.Method == http.MethodGet:
		h.getProceduralSource(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "projects" && parts[2] == "layers" && parts[4] == "procedural" && r.Method == http.MethodPatch:
		h.regenerateProcedural(w, r, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "generate" && r.Method == http.MethodPost:
		h.generateProcedural(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "resize" && r.Method == http.MethodPatch:
		h.resize(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "export" && r.Method == http.MethodGet:
		h.export(w, r, parts[1])
	default:
		http.NotFound(w, r)
	}
}

func (h *NockHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	names, err := h.Svc.ListProjects()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

type createProjectReq struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (h *NockHandler) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.CreateProject(req.Name, req.Width, req.Height)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *NockHandler) getProject(w http.ResponseWriter, r *http.Request, name string) {
	p, err := h.Svc.GetProject(name)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *NockHandler) deleteProject(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.Svc.DeleteProject(name); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// saveUploadedFile pulls the "file" multipart field into a real temp file and returns its path
// — every image-accepting endpoint below shares this rather than each re-implementing upload
// handling.
func saveUploadedFile(r *http.Request, fieldName string) (path string, cleanup func(), err error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return "", nil, fmt.Errorf("parse multipart form: %w", err)
	}
	file, header, err := r.FormFile(fieldName)
	if err != nil {
		return "", nil, fmt.Errorf("missing %q file field: %w", fieldName, err)
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "nock-upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, file); err != nil {
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("save upload: %w", err)
	}
	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}

func (h *NockHandler) addLayer(w http.ResponseWriter, r *http.Request, project string) {
	path, cleanup, err := saveUploadedFile(r, "file")
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()
	layerName := r.FormValue("name")
	p, err := h.Svc.AddLayer(project, layerName, path)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *NockHandler) removeLayer(w http.ResponseWriter, r *http.Request, project, layer string) {
	p, err := h.Svc.RemoveLayer(project, layer)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type opacityReq struct {
	Opacity int `json:"opacity"`
}

func (h *NockHandler) setOpacity(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req opacityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.SetOpacity(project, layer, req.Opacity)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type visibleReq struct {
	Visible bool `json:"visible"`
}

func (h *NockHandler) setVisible(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req visibleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.SetVisible(project, layer, req.Visible)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type moveReq struct {
	Delta int `json:"delta"`
}

func (h *NockHandler) moveLayer(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req moveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.MoveLayer(project, layer, req.Delta)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *NockHandler) setMask(w http.ResponseWriter, r *http.Request, project, layer string) {
	path, cleanup, err := saveUploadedFile(r, "file")
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer cleanup()
	p, err := h.Svc.SetMask(project, layer, path)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *NockHandler) clearMask(w http.ResponseWriter, r *http.Request, project, layer string) {
	p, err := h.Svc.ClearMask(project, layer)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type hueSatReq struct {
	Brightness int `json:"brightness"`
	Saturation int `json:"saturation"`
	Hue        int `json:"hue"`
}

func (h *NockHandler) hueSat(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req hueSatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.AdjustHueSaturation(project, layer, req.Brightness, req.Saturation, req.Hue)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type sharpenReq struct {
	Radius float64 `json:"radius"`
	Sigma  float64 `json:"sigma"`
	Amount float64 `json:"amount"`
}

func (h *NockHandler) sharpen(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req sharpenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.Sharpen(project, layer, req.Radius, req.Sigma, req.Amount)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type gradientReq struct {
	Name      string `json:"name"`
	From      string `json:"from"`
	To        string `json:"to"`
	Direction string `json:"direction"`
}

func (h *NockHandler) addGradient(w http.ResponseWriter, r *http.Request, project string) {
	var req gradientReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.AddGradientLayer(project, req.Name, req.From, req.To, req.Direction)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type proceduralReq struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

func (h *NockHandler) addProcedural(w http.ResponseWriter, r *http.Request, project string) {
	var req proceduralReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.AddProceduralLayer(project, req.Name, req.Source)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *NockHandler) getProceduralSource(w http.ResponseWriter, r *http.Request, project, layer string) {
	src, err := h.Svc.GetProceduralSource(project, layer)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"source": src})
}

type regenerateReq struct {
	Source string `json:"source"`
}

func (h *NockHandler) regenerateProcedural(w http.ResponseWriter, r *http.Request, project, layer string) {
	var req regenerateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.RegenerateProceduralLayer(project, layer, req.Source)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type generateReq struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// generateProcedural is the real "one-shot a texture from a text prompt" endpoint: calls Vertex
// AI (gen_vertex.go) for real PARENA source, then runs it through the exact same
// AddProceduralLayer path a human-written or CLI-supplied source goes through -- a model's own
// output is never treated as more trusted than anything else this pipeline compiles and runs.
func (h *NockHandler) generateProcedural(w http.ResponseWriter, r *http.Request, project string) {
	var req generateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.GetProject(project)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}

	token, err := nock.GcloudAccessToken()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "no Vertex AI credential available: "+err.Error())
		return
	}
	src, err := nock.GenerateProceduralTextureSource(r.Context(), token, req.Prompt, p.Width, p.Height)
	if err != nil {
		mmoWriteError(w, http.StatusBadGateway, "vertex generation failed: "+err.Error())
		return
	}

	updated, err := h.Svc.AddProceduralLayer(project, req.Name, src)
	if err != nil {
		// Real, honest failure mode, not papered over: the model's own output didn't validate
		// or didn't compile. Return the generated source too, so the caller (the GUI's own
		// "generate" panel) can show the user what was attempted and let them fix it by hand
		// rather than just a bare error.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  err.Error(),
			"source": src,
		})
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

type resizeReq struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (h *NockHandler) resize(w http.ResponseWriter, r *http.Request, project string) {
	var req resizeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	p, err := h.Svc.ResizeCanvas(project, req.Width, req.Height)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// export streams a flattened PNG or JPEG straight back over the response — the browser can point
// an <img> tag directly at this URL for a live composite preview, or a "download" link for a
// real export, without a separate polling/job step.
func (h *NockHandler) export(w http.ResponseWriter, r *http.Request, project string) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "png"
	}
	background := r.URL.Query().Get("background")

	tmp, err := os.CreateTemp("", "nock-export-out-*."+format)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	tmp.Close()

	if err := h.Svc.Export(project, tmp.Name(), background); err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	contentType := "image/png"
	if format == "jpg" || format == "jpeg" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}
