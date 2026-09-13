package handlers

// brawlpit_checkpoints.go — S420, the real, remote RL checkpoint registry (founder real-time:
// "lets make a checkpoint registry so we can train from multiple locations and then we can add
// checkpoints from colab?"). Upload is agent-auth gated (brawlpit.checkpoints.write, see
// migrations/truestore/202609131400_brawlpit_rl_checkpoints.sql); list/download stay public,
// same trust level GET /api/v1/brawlpit-levels already established.
//
// S453, founder real-time: "lets start a log streaming trail and iduna unified logging for when
// the brawlpit AI is changed on the server" -- every real "the live AI population changed" point
// in this file now emits into the same unified event log (main.go's own unifiedLog) every other
// real IDUNA code path already uses: a new checkpoint entering the league (upload), which one is
// the live opponent (activate), and which are eligible at all (disable/enable). Direct motive:
// diagnosing a real, observed ~1900 Elo lineage going dark (S452) had NO record of what actually
// happened to it -- founder confirmed directly afterward: "i must have enabled an old one where
// we had some movement before it all went to hell," meaning the reverse action (an ACTIVATE onto
// a stale/regressed checkpoint) is exactly as real a "the AI changed" event as a disable is.
// Query these via GET /services/search/jobs?search=type=iduna:brawlpit.checkpoint.* (or the
// /portal/logs UI) -- the same real search surface every other unified-log event already uses.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/brawlpit"
	"iduna/internal/userlog"
)

const weightsContentType = "application/x-brawlpit-weights"
const weightsContentTypeLZ4 = "application/x-brawlpit-weights-lz4"

// BrawlpitCheckpointsHandler serves every /api/v1/brawlpit-checkpoints... route.
type BrawlpitCheckpointsHandler struct {
	Store    *brawlpit.CheckpointStore
	EventLog userlog.EventLog // optional (S453); nil skips event emission entirely, same convention every other handler's EventLog field already uses
}

func (h *BrawlpitCheckpointsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/api/v1/brawlpit-checkpoints"
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
		h.upload(w, r)
	case len(parts) == 1 && parts[0] == "active" && r.Method == http.MethodGet:
		h.getActive(w, r)
	case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
		h.download(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "weights" && r.Method == http.MethodGet:
		h.downloadWeights(w, r, parts[0])
	case len(parts) == 1 && parts[0] == "match-result" && r.Method == http.MethodPost:
		h.recordMatchResult(w, r)
	default:
		http.NotFound(w, r)
	}
}

// getActive is real, public read state (S421, "select a model for the opponent from the
// registry") -- the same trust level list/download already have. Returns the current selection
// as a bare Checkpoint, or `null` (200, not 404) if no selection has ever been made -- a real,
// expected, honest state for a fresh registry, matching GetActiveOpponent's own doc comment.
func (h *BrawlpitCheckpointsHandler) getActive(w http.ResponseWriter, r *http.Request) {
	c, err := h.Store.GetActiveOpponent(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *BrawlpitCheckpointsHandler) list(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	list, err := h.Store.List(r.Context(), role)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// upload is the one write route this registry has, real, multipart-form-encoded (not JSON --
// this endpoint's whole job is receiving a real binary .zip file, not a JSON document): fields
// `role`, `generation`, `elo`, `source_location`, and file field `file`. Gated by
// middleware.RequirePermission("brawlpit.checkpoints.write") at the route-registration layer
// (main.go), same as every other M2M-agent-gated write endpoint in this codebase.
func (h *BrawlpitCheckpointsHandler) upload(w http.ResponseWriter, r *http.Request) {
	// A real, sane cap on the WHOLE multipart body (not just the file field) -- matches
	// CheckpointStore's own 200MB per-checkpoint bound plus real headroom for the other form
	// fields, so a malformed/hostile request can't force this handler to buffer unbounded data
	// into memory before validateCheckpointInput ever runs.
	r.Body = http.MaxBytesReader(w, r.Body, 210*1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return
	}

	role := r.FormValue("role")
	sourceLocation := r.FormValue("source_location")
	generation, err := strconv.Atoi(r.FormValue("generation"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "generation must be a real integer")
		return
	}
	elo, err := strconv.ParseFloat(r.FormValue("elo"), 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "elo must be a real number")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("read uploaded file: %v", err))
		return
	}

	c, err := h.Store.Create(r.Context(), role, generation, elo, sourceLocation, header.Filename, data)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// weights_file is optional (S421-02, founder real-time: "ensure that the client actually
	// uses that model... make sure the model downloads with lz4") -- the real, exported native-
	// inference blob (scripts/export_policy_weights.py's own "BPMW" format) for this SAME
	// checkpoint, uploaded alongside the .zip in one request rather than a second round trip.
	// An older caller that never sends this field still creates a real checkpoint with no
	// weights attached (HasWeights=false) -- not a hard requirement of this endpoint.
	if weightsFile, _, err := r.FormFile("weights_file"); err == nil {
		defer weightsFile.Close()
		weightsData, err := io.ReadAll(weightsFile)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("read uploaded weights file: %v", err))
			return
		}
		c, err = h.Store.SetWeights(r.Context(), c.ID, weightsData)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// S453, founder real-time: "lets start a log streaming trail and iduna unified logging for
	// when the brawlpit AI is changed on the server" -- a real audit trail for every point the
	// live AI population can change, the same real gap named directly while diagnosing S452 (a
	// real, observed ~1900 Elo lineage going dark with no record of what happened to it). A new
	// checkpoint entering the league is real, load-bearing "the AI changed" -- it can be sampled
	// as an opponent or a resume target the moment it exists.
	emitAuthEvent(r.Context(), h.EventLog, "iduna:brawlpit.checkpoint.upload", "brawlpit-checkpoints", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
		"source_location": c.SourceLocation, "has_weights": c.HasWeights,
	})

	writeJSON(w, http.StatusCreated, c)
}

func (h *BrawlpitCheckpointsHandler) download(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, data, err := h.Store.ReadBlob(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, c.Filename))
	w.Header().Set("Content-Length", strconv.FormatInt(c.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// downloadWeights serves GET /api/v1/brawlpit-checkpoints/:id/weights -- the real, small,
// native-client-loadable inference blob (S421-02), distinct from download()'s own full .zip
// training-state artifact. `?compress=lz4` (default, matching this monorepo's own standing
// "LZ4 is always the default" convention -- founder real-time: "make sure the model downloads
// with lz4") runs the SAME bytes through this package's own PARENA-compiled LZ4 codec
// (internal/brawlpit.CompressLZ4, the identical cgo binding brawlpit_levels_public.go's own
// `?compress=lz4` export already uses) -- BRAWLPIT's native client links the identical codec
// directly (packages/common/lz4/), so it decompresses with the exact same real format this
// server compresses with. Pass `?compress=none` for the raw, uncompressed bytes.
func (h *BrawlpitCheckpointsHandler) downloadWeights(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	_, data, err := h.Store.ReadWeights(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}

	if r.URL.Query().Get("compress") == "none" {
		w.Header().Set("Content-Type", weightsContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	compressed := brawlpit.CompressLZ4(data)
	w.Header().Set("Content-Type", weightsContentTypeLZ4)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(compressed)
}

type recordMatchResultReq struct {
	AID    int64   `json:"a_id"`
	BID    int64   `json:"b_id"`
	ScoreA float64 `json:"score_a"`
}

// recordMatchResult serves POST /api/v1/brawlpit-checkpoints/match-result (S421-04, founder
// real-time: "can we start recording the match results with the actual outcomes?") -- the real,
// only thing that ever MOVES a checkpoint's Elo off its inherited value (see
// CheckpointStore.RecordMatchResult's own doc comment). Gated behind the same real M2M
// brawlpit.checkpoints.write permission the upload route already uses (main.go's own routing) --
// this is training-pipeline-reported data, not a human action, same trust level as pushing a new
// checkpoint file.
func (h *BrawlpitCheckpointsHandler) recordMatchResult(w http.ResponseWriter, r *http.Request) {
	var req recordMatchResultReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	updatedA, updatedB, err := h.Store.RecordMatchResult(r.Context(), req.AID, req.BID, req.ScoreA)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]*brawlpit.Checkpoint{"a": updatedA, "b": updatedB})
}

// BrawlpitCheckpointActivateHandler serves PATCH /admin/nock/api/brawlpit-checkpoints/:id/activate
// -- the one write action a human makes through the real selection UI (frontend/nock/src/
// AiOpponents.tsx), admin-gated the same way brawlpit-levels' own editing surface already is
// (a genuinely different trust level from the M2M-agent-gated upload above: a human picking an
// opponent through the UI, not a training pipeline pushing a new checkpoint file).
type BrawlpitCheckpointActivateHandler struct {
	Store    *brawlpit.CheckpointStore
	EventLog userlog.EventLog // optional (S453); nil skips event emission entirely
}

func (h *BrawlpitCheckpointActivateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	const prefix = "/admin/nock/api/brawlpit-checkpoints/"
	const suffix = "/activate"
	path := r.URL.Path
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		http.NotFound(w, r)
		return
	}
	idStr := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, err := h.Store.SetActiveOpponent(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// S453: real, live "which AI is actually playing right now" change -- the highest-signal
	// event this whole file emits (see brawlpit_checkpoints.go's own top-of-file S453 comment
	// for the full rationale).
	emitAuthEvent(r.Context(), h.EventLog, "iduna:brawlpit.checkpoint.activate", "iduna-admin", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
	})
	writeJSON(w, http.StatusOK, c)
}

// BrawlpitCheckpointDisableHandler serves PATCH /admin/nock/api/brawlpit-checkpoints/:id/disable
// -- the real checkbox backend (S428, founder real-time: "i want to reset training but not
// include certain models from the registry - can you add a checkbox to the registry backend to
// disable those models from the league?"). Same admin-gated trust level as the activate handler
// above -- a human excluding a model through the UI, not a training pipeline.
// Body: {"disabled": bool}.
type BrawlpitCheckpointDisableHandler struct {
	Store    *brawlpit.CheckpointStore
	EventLog userlog.EventLog // optional (S453); nil skips event emission entirely
}

func (h *BrawlpitCheckpointDisableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	const prefix = "/admin/nock/api/brawlpit-checkpoints/"
	const suffix = "/disable"
	path := r.URL.Path
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		http.NotFound(w, r)
		return
	}
	idStr := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	c, err := h.Store.SetDisabled(r.Context(), id, req.Disabled)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// S453, founder real-time: "lets start a log streaming trail and iduna unified logging for
	// when the brawlpit AI is changed on the server" -- this is the SPECIFIC action S452 traced
	// a real, lost ~1900 Elo lineage back to (94 checkpoints disabled at once, almost certainly
	// NOCK's own "Disable All" firing with no Role filter narrowed, with no record of it having
	// happened). eventType splits disable/enable rather than one generic event with a bool field
	// so a log search for exactly "iduna:brawlpit.checkpoint.disable" finds every real exclusion
	// directly, matching this codebase's own established convention (e.g. admin.go's own
	// suspend/unsuspend as two distinct event types, not one "suspend" event with a bool).
	eventType := "iduna:brawlpit.checkpoint.enable"
	if req.Disabled {
		eventType = "iduna:brawlpit.checkpoint.disable"
	}
	emitAuthEvent(r.Context(), h.EventLog, eventType, "iduna-admin", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
	})
	writeJSON(w, http.StatusOK, c)
}
