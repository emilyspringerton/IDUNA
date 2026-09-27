package handlers

// nock_animator.go — HTTP routes for NOCK's animator and rig-remapping tools (founder real-time,
// 2026-09-27: "continue to evolve NOCK tools into a total blender replacement we need a way to
// animate in NOCK also we need the primatives for remapping a mesh onto a new rig"). Thin
// wrappers over internal/nock's AnimStore operations (anim_rig.go), mounted under the same
// /admin/nock/api/animations/{id}/... prefix and iduna.admin gate as the rest of the library:
//
//	GET  {id}/keyframes[?every=N]  editable keyframe doc: stored source, else derived from the
//	                               baked clip every N ticks (default 5), else empty
//	POST {id}/keyframes            bake + save {keyframes, name, replace}: a new row on {id}'s
//	                               rig, or (replace) overwrite {id}'s own clip
//	GET  {id}/bone-map?target=T    automatic bone map from {id}'s rig to T's rig, for review
//	POST {id}/remap-mesh           {target_id, name, mode, fit, bone_map}: {id}'s mesh re-skinned
//	                               onto target's rig, saved as a new row
//	POST {id}/retarget             {target_id, name, bone_map}: {id}'s clip moved onto target's
//	                               rig, saved as a new row

import (
	"encoding/json"
	"net/http"
	"strconv"

	"iduna/internal/nock"
)

func (h *NockAnimationsHandler) getKeyframes(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	every := 5
	if v := r.URL.Query().Get("every"); v != "" {
		if every, err = strconv.Atoi(v); err != nil || every < 1 {
			mmoWriteError(w, http.StatusBadRequest, "every must be a positive integer")
			return
		}
	}
	doc, source, err := h.Store.EditableKeyframes(r.Context(), id, every)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "keyframes": doc})
}

func (h *NockAnimationsHandler) authorKeyframes(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024*1024)
	var req struct {
		Name      string           `json:"name"`
		Replace   bool             `json:"replace"`
		Keyframes nock.KeyframeDoc `json:"keyframes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	a, err := h.Store.AuthorClip(r.Context(), id, req.Name, &req.Keyframes, req.Replace)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if req.Replace {
		status = http.StatusOK
	}
	writeJSON(w, status, a)
}

func (h *NockAnimationsHandler) boneMap(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	target, err := parseAnimationID(r.URL.Query().Get("target"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "target query parameter must be an animation id")
		return
	}
	rep, err := h.Store.BoneMapBetween(r.Context(), id, target, nil)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (h *NockAnimationsHandler) remapMesh(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		TargetID int64        `json:"target_id"`
		Name     string       `json:"name"`
		Mode     string       `json:"mode"`
		Fit      bool         `json:"fit"`
		BoneMap  nock.BoneMap `json:"bone_map"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	a, rep, err := h.Store.RemapMeshOnto(r.Context(), id, req.TargetID, req.Name, nock.RemapOptions{Mode: nock.RemapMode(req.Mode), Fit: req.Fit, Overrides: req.BoneMap})
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"animation": a, "report": rep})
}

func (h *NockAnimationsHandler) retarget(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseAnimationID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		TargetID int64        `json:"target_id"`
		Name     string       `json:"name"`
		BoneMap  nock.BoneMap `json:"bone_map"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	a, rep, err := h.Store.RetargetOnto(r.Context(), id, req.TargetID, req.Name, req.BoneMap)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"animation": a, "report": rep})
}
