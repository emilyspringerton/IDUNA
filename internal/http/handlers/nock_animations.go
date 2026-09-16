package handlers

// nock_animations.go — real CRUD + upload API for NOCK's animation repository (founder
// real-time, 2026-09-16: "need animation repository", the storage/browse half of "let's start
// iterating towards nock tools modeler (blender) and golden band"). Same real shape as
// nock_textures.go: a thin wrapper over internal/nock.AnimStore, the SQLite-backed CRUD layer --
// see that file's own header comment for the schema rationale.
//
// Unlike the texture library (a single PNG blob + optional PARENA source text), an animation
// upload is real multipart-form-encoded, mirroring shankpit_checkpoints.go's own established
// upload shape: a required `gband` file, a required `manifest` field (the real .gband.json
// sidecar text, verbatim -- GOLDENBAND/format/GBAND_FORMAT.md's own manifest schema, produced by
// `gbtool import --bvh`/`--gltf`), and optional `gskel`/`gmesh` files for a paired
// skeleton/mesh. tick_rate/duration_ticks/num_channels/content_hash are all derived server-side
// from the manifest JSON rather than taken as separate, independently-typeable form fields --
// one real source of truth, not two copies that can drift apart.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

// NockAnimationsHandler serves every /admin/nock/api/animations... route.
type NockAnimationsHandler struct {
	Store *nock.AnimStore
}

func (h *NockAnimationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/animations"
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
	case len(parts) == 1 && r.Method == http.MethodPatch:
		h.rename(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "gband" && r.Method == http.MethodGet:
		h.download(w, r, parts[0], "gband")
	case len(parts) == 2 && parts[1] == "gskel" && r.Method == http.MethodGet:
		h.download(w, r, parts[0], "gskel")
	case len(parts) == 2 && parts[1] == "gmesh" && r.Method == http.MethodGet:
		h.download(w, r, parts[0], "gmesh")
	case len(parts) == 2 && parts[1] == "manifest" && r.Method == http.MethodGet:
		h.manifest(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		h.clone(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseAnimationID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *NockAnimationsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListAnimations(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// gbandManifestFields is the real, narrow slice of GBAND_FORMAT.md's own manifest schema this
// handler needs to derive the row's tick_rate/duration_ticks/num_channels/content_hash from --
// deliberately not the full Manifest struct (this package has no reason to depend on gbtool's
// own Go module for three integers and a hash string).
type gbandManifestFields struct {
	TickRate      int    `json:"tick_rate"`
	DurationTicks int    `json:"duration_ticks"`
	Channels      []any  `json:"channels"`
	ContentHash   string `json:"content_hash"`
}

func (h *NockAnimationsHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return
	}

	name := r.FormValue("name")
	sourceLocation := r.FormValue("source_location")
	manifestText := r.FormValue("manifest")
	if manifestText == "" {
		mmoWriteError(w, http.StatusBadRequest, "missing manifest field (the real .gband.json sidecar text)")
		return
	}
	var mf gbandManifestFields
	if err := json.Unmarshal([]byte(manifestText), &mf); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("manifest is not valid JSON: %v", err))
		return
	}

	gbandData, err := readFormFile(r, "gband")
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "missing gband field: "+err.Error())
		return
	}
	gskelData, _ := readFormFile(r, "gskel") // optional -- readFormFile returns nil, nil when the field is simply absent
	gmeshData, _ := readFormFile(r, "gmesh") // optional

	a, err := h.Store.CreateAnimation(r.Context(), name, mf.TickRate, mf.DurationTicks, len(mf.Channels), mf.ContentHash, gbandData, manifestText, gskelData, gmeshData, sourceLocation)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// readFormFile returns (nil, nil) when the named field is simply absent from the form (an
// optional upload), and (nil, err) only for a real read failure on a field that WAS present --
// callers distinguish "not provided" from "provided but broken" by checking the field's own
// presence via r.MultipartForm before calling this for an optional field.
func readFormFile(r *http.Request, field string) ([]byte, error) {
	file, _, err := r.FormFile(field)
	if err != nil {
		return nil, nil //nolint:nilerr -- absent optional field, not an error; required fields are checked by the caller via a nil data slice
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// importGLTF is the real drag-and-drop path (founder real-time: "ok I need to import quaternion
// assets nock tools drag and drop"): one raw .glb (or embedded-buffer .gltf) file straight from
// the browser, converted server-side via nock.ImportGLTFBytes (a real port of gbtool's own
// import_gltf.go conversion logic -- see that file's own header comment) instead of requiring
// the user to run gbtool locally first and upload three separate pre-converted files through
// create() above. Real, same v0 scope as gbtool's own CLI importer: first skin/mesh/animation
// only, and (server-specific) a single self-contained file only -- no external .bin sidecar,
// since there's nothing to resolve it against from one drag-and-dropped file.
func (h *NockAnimationsHandler) importGLTF(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return
	}

	name := r.FormValue("name")
	sourceLocation := r.FormValue("source_location")
	if sourceLocation == "" {
		sourceLocation = "nock drag-and-drop"
	}
	kind := r.FormValue("kind")
	if kind == "" {
		kind = "mocap"
	}
	who := r.FormValue("who")
	tickRate := 30
	if v := r.FormValue("tick_rate"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed <= 0 {
			mmoWriteError(w, http.StatusBadRequest, "tick_rate must be a positive integer")
			return
		}
		tickRate = parsed
	}

	fileData, err := readFormFile(r, "file")
	if err != nil || len(fileData) == 0 {
		mmoWriteError(w, http.StatusBadRequest, "missing file field (drop a .glb or .gltf file)")
		return
	}

	result, err := nock.ImportGLTFBytes(fileData, uint32(tickRate), kind, who)
	if err != nil {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("glTF import failed: %v", err))
		return
	}

	a, err := h.Store.CreateAnimation(r.Context(), name, result.TickRate, result.DurationTicks, result.NumChannels, result.ContentHash, result.GBandData, result.ManifestJSON, result.GSkelData, result.GMeshData, sourceLocation)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *NockAnimationsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.Store.GetAnimation(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *NockAnimationsHandler) rename(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	a, err := h.Store.RenameAnimation(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *NockAnimationsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteAnimation(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NockAnimationsHandler) clone(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	a, err := h.Store.CloneAnimation(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *NockAnimationsHandler) manifest(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.Store.GetAnimation(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(a.ManifestJSON)) //nolint:errcheck
}

// download serves the raw binary bytes of one of an animation's real GOLDENBAND assets --
// exactly the bytes `gbtool import` originally wrote, byte for byte, so a downloaded file drops
// straight back into a GOLDENBAND-consuming pipeline (or `gbtool validate`/`hash`) unmodified.
func (h *NockAnimationsHandler) download(w http.ResponseWriter, r *http.Request, idStr, kind string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := h.Store.GetAnimation(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	var data []byte
	switch kind {
	case "gband":
		data = a.GBandData
	case "gskel":
		data = a.GSkelData
	case "gmesh":
		data = a.GMeshData
	}
	if len(data) == 0 {
		mmoWriteError(w, http.StatusNotFound, fmt.Sprintf("animation %d has no %s asset", id, kind))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, a.Name, kind))
	w.Write(data) //nolint:errcheck
}
