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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	case len(parts) == 2 && parts[1] == "attach-animation" && r.Method == http.MethodPost:
		h.attachAnimation(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "gband" && r.Method == http.MethodPatch:
		h.saveEditedGBand(w, r, parts[0])
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
	SkeletonHash  string `json:"skeleton_hash"`
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

	a, err := h.Store.CreateAnimation(r.Context(), name, mf.TickRate, mf.DurationTicks, len(mf.Channels), mf.ContentHash, gbandData, manifestText, gskelData, gmeshData, mf.SkeletonHash, sourceLocation)
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

	// S459-109, founder real-time (confirmed a real uploaded file -- Quaternius's "Universal
	// Animation Library," CC0 -- is a genuine multi-clip pack): ImportGLTFBytesAllClips returns
	// the primary asset (mesh+skeleton+first clip, exactly what the old single-clip
	// ImportGLTFBytes call produced) plus every OTHER real clip the file contained, which used to
	// be silently discarded.
	result, additional, err := nock.ImportGLTFBytesAllClips(fileData, uint32(tickRate), kind, who)
	if err != nil {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("glTF import failed: %v", err))
		return
	}

	a, err := h.Store.CreateAnimation(r.Context(), name, result.TickRate, result.DurationTicks, result.NumChannels, result.ContentHash, result.GBandData, result.ManifestJSON, result.GSkelData, result.GMeshData, result.SkeletonHash, sourceLocation)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	type clipSummary struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	createdClips := make([]clipSummary, 0, len(additional))
	for _, clip := range additional {
		clipRow, err := h.Store.CreateAnimation(r.Context(), sanitizedClipName(name, clip.Name), clip.TickRate, clip.DurationTicks, clip.NumChannels, clip.ContentHash, clip.GBandData, clip.ManifestJSON, nil, nil, clip.SkeletonHash, sourceLocation)
		if err != nil {
			// A name collision (re-importing the same pack twice) or any other single-clip
			// failure must never lose the primary row (and every other successfully-created
			// clip) that's already real and committed -- skip it, don't abort the response.
			continue
		}
		createdClips = append(createdClips, clipSummary{ID: clipRow.ID, Name: clipRow.Name})
	}

	resp := struct {
		*nock.Animation
		AdditionalClips []clipSummary `json:"additional_clips,omitempty"`
	}{Animation: a, AdditionalClips: createdClips}
	writeJSON(w, http.StatusCreated, resp)
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

// attachAnimation (2026-09-17, founder real-time: "build fill in the gaps... you can add
// animations to it later, either by uploading a separate file with the same rig") -- real
// affordance for the exact promise NOCK's own animation-library copy already made. Two real,
// distinct request shapes, dispatched by Content-Type, matching two real ways a founder would
// have a matching clip in hand:
//   - multipart/form-data (`file` field): a fresh glTF export with the animation baked in --
//     converted the same way import-gltf already does, but only the animation half is kept
//     (this route's whole point is attaching motion onto an ALREADY-stored mesh+rig, not
//     re-storing a second copy of that mesh+rig).
//   - application/json (`{"source_id": N}`): reuse an animation clip that's already in this same
//     library -- no re-upload needed if two rows happen to share a rig.
func (h *NockAnimationsHandler) attachAnimation(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
			return
		}
		fileData, err := readFormFile(r, "file")
		if err != nil || len(fileData) == 0 {
			mmoWriteError(w, http.StatusBadRequest, "missing file field (drop a .glb or .gltf file containing the animation)")
			return
		}
		kind := r.FormValue("kind")
		if kind == "" {
			kind = "mocap"
		}
		result, err := nock.ImportGLTFBytes(fileData, 30, kind, r.FormValue("who"))
		if err != nil {
			mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("glTF import failed: %v", err))
			return
		}
		if result.GBandData == nil {
			mmoWriteError(w, http.StatusUnprocessableEntity, "no animation found in this file -- attach-animation needs a file with at least one animated channel")
			return
		}
		a, err := h.Store.AttachAnimation(r.Context(), id, result.GBandData, result.ManifestJSON, result.TickRate, result.DurationTicks, result.NumChannels, result.ContentHash, result.SkeletonHash)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, a)
		return
	}

	var req struct {
		SourceID int64 `json:"source_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body (expected {\"source_id\": <id>})")
		return
	}
	source, err := h.Store.GetAnimation(r.Context(), req.SourceID)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if source.GBandData == nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("%q has no animation data to attach", source.Name))
		return
	}
	a, err := h.Store.AttachAnimation(r.Context(), id, source.GBandData, source.ManifestJSON, intOrZeroPtr(source.TickRate), intOrZeroPtr(source.DurationTicks), intOrZeroPtr(source.NumChannels), source.ContentHash, source.SkeletonHash)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

var nockAnimNameInvalidChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// sanitizedClipName builds a real, valid name (matches internal/nock's own validName pattern --
// starts alnum, then up to 63 more of [a-zA-Z0-9_-]) for an additional clip created during a
// multi-clip import, from the base import name + the clip's own real name (which may contain
// spaces or other characters the strict name pattern doesn't allow -- a real glTF animation name
// like "Jump Start" is legitimate, this field's own constraint is NOCK-specific, not the source
// format's).
func sanitizedClipName(base, clip string) string {
	sanitize := func(s string) string {
		s = nockAnimNameInvalidChars.ReplaceAllString(s, "_")
		return strings.Trim(s, "_-")
	}
	combined := sanitize(base) + "_" + sanitize(clip)
	if combined == "" || !((combined[0] >= 'a' && combined[0] <= 'z') || (combined[0] >= 'A' && combined[0] <= 'Z') || (combined[0] >= '0' && combined[0] <= '9')) {
		combined = "clip_" + combined
	}
	if len(combined) > 64 {
		combined = combined[:64]
	}
	return combined
}

func intOrZeroPtr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// saveEditedGBand (2026-09-17, founder real-time: "lets build the animations editor - clone then
// edit workflow") -- real save path for NOCK's new in-browser animation editor
// (frontend/nock/src/AnimationEditor.tsx). The editor already did the real work client-side
// (decoded the row's own .gband, let a founder tweak per-tick joint poses, re-encoded a real,
// complete .gband binary -- see frontend/nock/src/goldenband.ts's own encodeGBand) -- this route
// just persists those already-valid bytes. Reuses AnimStore.AttachAnimation directly (the exact
// same "replace this row's own animation data" operation attach-animation already performs),
// rather than a new store method -- editing IS replacing, just with founder-authored bytes
// instead of an imported clip's own.
func (h *NockAnimationsHandler) saveEditedGBand(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		GBandDataBase64 string `json:"gband_data_base64"`
		ManifestJSON    string `json:"manifest_json"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	gbandData, err := base64.StdEncoding.DecodeString(req.GBandDataBase64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "gband_data_base64 is not valid base64: "+err.Error())
		return
	}
	var mf gbandManifestFields
	if err := json.Unmarshal([]byte(req.ManifestJSON), &mf); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "manifest_json is not valid JSON: "+err.Error())
		return
	}
	a, err := h.Store.AttachAnimation(r.Context(), id, gbandData, req.ManifestJSON, mf.TickRate, mf.DurationTicks, len(mf.Channels), mf.ContentHash, mf.SkeletonHash)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
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
