package handlers

// shankpit_widgets.go — real CRUD API for SHANKPIT Widgets (S482, founder real-time, direct
// correction of the earlier S479-follow-up door-composition work: "i dont want to make doors be
// levels please - make widget or something they are both objects but widgets just dont show up
// in the levels menu"). Deliberately much smaller than shankpit_levels.go's own handler -- a
// Widget has no dimension, no ground plane, no rename/clone/export/story-start/default-queue
// actions, just list/create/get/update/delete over walls+doors.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"iduna/internal/nock"
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
	case len(parts) == 1 && parts[0] == "import-gltf" && r.Method == http.MethodPost:
		h.importGLTF(w, r)
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

// widgetDefaultFriction matches the NOCK widget editor's own new-wall default (ShankpitWidgets.tsx).
const widgetDefaultFriction = 0.3

// WidgetFromGLTFBoxes turns nock.GLTFToWidgetBoxes output into real Widget walls/doors: wall ids
// 1..n in scene order, and one scriptless door (script_id 0) per box flagged IsDoor.
func WidgetFromGLTFBoxes(boxes []nock.WidgetBox) ([]shankpit.Wall, []shankpit.Door) {
	walls := make([]shankpit.Wall, 0, len(boxes))
	doors := []shankpit.Door{}
	for i, b := range boxes {
		id := i + 1
		walls = append(walls, shankpit.Wall{
			ID: id, X: b.CX, Y: b.CY, Z: b.CZ, SX: b.SX, SY: b.SY, SZ: b.SZ,
			R: b.R, G: b.G, B: b.B, Friction: widgetDefaultFriction, Name: b.Name,
		})
		if b.IsDoor {
			doors = append(doors, shankpit.Door{ID: len(doors) + 1, WallID: id})
		}
	}
	return walls, doors
}

// readGLTFUpload parses the multipart form every NOCK glTF -> boxes importer shares: `file` (a .glb,
// or a .gltf), zero or more `resource` files (the .bin / textures of a Blender "glTF Separate"
// export, matched to the .gltf by file name), and an optional positive `scale`. On any problem it
// writes the 4xx response itself and returns ok=false.
func readGLTFUpload(w http.ResponseWriter, r *http.Request) (file []byte, resources map[string][]byte, scale float64, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return nil, nil, 0, false
	}
	scale = 1.0
	if v := r.FormValue("scale"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil || parsed <= 0 {
			mmoWriteError(w, http.StatusBadRequest, "scale must be a positive number")
			return nil, nil, 0, false
		}
		scale = parsed
	}
	file, err := readFormFile(r, "file")
	if err != nil || len(file) == 0 {
		mmoWriteError(w, http.StatusBadRequest, "missing file field (drop a .glb or .gltf file)")
		return nil, nil, 0, false
	}
	resources = map[string][]byte{}
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["resource"] {
			f, err := fh.Open()
			if err != nil {
				mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("reading resource %q: %v", fh.Filename, err))
				return nil, nil, 0, false
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("reading resource %q: %v", fh.Filename, err))
				return nil, nil, 0, false
			}
			resources[path.Base(fh.Filename)] = data
		}
	}
	return file, resources, scale, true
}

// importGLTF is the NOCK glTF importer -> SHANKPIT Widget bridge (founder real-time, 2026-09-27:
// "we need a way to go from nock tools gltf importer into the shankpit widgets"). Multipart form:
// `file` (a .glb or embedded-buffer .gltf -- the same input the Animations tab's own import-gltf
// accepts), `name` (the new widget's name), optional `scale` (uniform multiplier, default 1).
// `preview=1` (form or query) returns the converted {walls, doors} without saving anything, so
// the UI can show the result before committing it. See internal/nock/gltf_widget.go for the
// mesh-node -> AABB mapping and its honest limits.
func (h *ShankpitWidgetsHandler) importGLTF(w http.ResponseWriter, r *http.Request) {
	fileData, resources, scale, ok := readGLTFUpload(w, r)
	if !ok {
		return
	}
	boxes, err := nock.GLTFToWidgetBoxesWithResources(fileData, scale, resources)
	if err != nil {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("model to widget conversion failed: %v", err))
		return
	}
	walls, doors := WidgetFromGLTFBoxes(boxes)
	if len(walls) > shankpit.MaxWalls {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("this file has %d mesh nodes; a widget holds at most %d walls (one per mesh node) -- join or delete meshes in Blender and re-export", len(walls), shankpit.MaxWalls))
		return
	}
	if len(doors) > shankpit.MaxDoors {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("this file has %d door* nodes; a widget holds at most %d doors", len(doors), shankpit.MaxDoors))
		return
	}
	if r.FormValue("preview") == "1" {
		writeJSON(w, http.StatusOK, map[string]any{"walls": walls, "doors": doors})
		return
	}
	widget, err := h.Store.CreateWidget(r.Context(), r.FormValue("name"), walls, doors)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, widget)
}
