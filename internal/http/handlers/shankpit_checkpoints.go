package handlers

// shankpit_checkpoints.go -- S459-49, the real, shared RL checkpoint registry for SHANKPIT.
// Founder real-time: "bring in the bot registry affordances on NOCK all the same - ability to
// disable - hide disabled - set default (defer this...) - for shankpit". Mirrors
// brawlpit_checkpoints.go field-for-field, scoped down: no weights upload/download (no
// SHANKPIT native-inference export exists yet) and no match-result/Elo endpoint (nothing calls
// it yet -- S459-48's own scripts/rl_train_packet.py doesn't push to a remote registry). Upload
// is agent-auth gated (shankpit.checkpoints.write, see migrations/truestore/
// 202609150022_shankpit_rl_checkpoints.sql); list/download stay public, same trust level GET
// /api/v1/shankpit-levels already established.
//
// Same real unified-logging discipline BRAWLPIT's own registry uses (S453) -- every point the
// live AI population can change (a checkpoint entering the registry, which one is the selected
// opponent, which are excluded) emits into the same event log every other real IDUNA code path
// uses, for the same real reason: an unrecorded change here is exactly the kind of gap S452
// found the hard way on the BRAWLPIT side.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/shankpit"
	"iduna/internal/userlog"
)

// ShankpitCheckpointsHandler serves every /api/v1/shankpit-checkpoints... route.
type ShankpitCheckpointsHandler struct {
	Store    *shankpit.CheckpointStore
	EventLog userlog.EventLog // optional; nil skips event emission entirely
}

func (h *ShankpitCheckpointsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/api/v1/shankpit-checkpoints"
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
	default:
		http.NotFound(w, r)
	}
}

// getActive returns the current global selection, or `null` (200, not 404) if none has ever been
// made -- a real, expected, honest state for a fresh registry.
func (h *ShankpitCheckpointsHandler) getActive(w http.ResponseWriter, r *http.Request) {
	c, err := h.Store.GetActiveOpponent(r.Context())
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *ShankpitCheckpointsHandler) list(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	list, err := h.Store.List(r.Context(), role)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// upload is the one write route this registry has -- real, multipart-form-encoded: fields
// `role`, `generation`, `elo`, `source_location`, and file field `file`. Gated by
// middleware.RequirePermission("shankpit.checkpoints.write") at the route-registration layer
// (main.go). Real, honest current status: nothing in this codebase actually calls this route yet
// (S459-48's own rl_train_packet.py is local-checkpoint-only) -- it exists so the NOCK UI has a
// real registry to browse once a real pusher exists, matching this repo's own established
// "build the real, honest v0, name what's deferred" convention.
func (h *ShankpitCheckpointsHandler) upload(w http.ResponseWriter, r *http.Request) {
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

	emitAuthEvent(r.Context(), h.EventLog, "iduna:shankpit.checkpoint.upload", "shankpit-checkpoints", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
		"source_location": c.SourceLocation,
	})

	writeJSON(w, http.StatusCreated, c)
}

func (h *ShankpitCheckpointsHandler) download(w http.ResponseWriter, r *http.Request, idStr string) {
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

// ShankpitCheckpointActivateHandler serves PATCH
// /admin/nock/api/shankpit-checkpoints/:id/activate -- real backend for the "Set as opponent"
// affordance, admin-gated the same way shankpit-levels' own editing surface already is. Real,
// deliberate status per the founder's own explicit instruction ("set default (defer this put the
// button then put like a daisy ui alert not implemented)"): this endpoint is real and working,
// but frontend/nock/src/ShankpitAiOpponents.tsx does NOT call it yet -- the UI button shows a
// DaisyUI "not implemented" alert instead. Wired here now so flipping that one client-side stub
// to a real call later needs no backend work.
type ShankpitCheckpointActivateHandler struct {
	Store    *shankpit.CheckpointStore
	EventLog userlog.EventLog
}

func (h *ShankpitCheckpointActivateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	const prefix = "/admin/nock/api/shankpit-checkpoints/"
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
	emitAuthEvent(r.Context(), h.EventLog, "iduna:shankpit.checkpoint.activate", "iduna-admin", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
	})
	writeJSON(w, http.StatusOK, c)
}

// ShankpitCheckpointDisableHandler serves PATCH
// /admin/nock/api/shankpit-checkpoints/:id/disable -- the real checkbox backend, same admin-gated
// trust level as the activate handler above. Body: {"disabled": bool}.
type ShankpitCheckpointDisableHandler struct {
	Store    *shankpit.CheckpointStore
	EventLog userlog.EventLog
}

func (h *ShankpitCheckpointDisableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	const prefix = "/admin/nock/api/shankpit-checkpoints/"
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
	eventType := "iduna:shankpit.checkpoint.enable"
	if req.Disabled {
		eventType = "iduna:shankpit.checkpoint.disable"
	}
	emitAuthEvent(r.Context(), h.EventLog, eventType, "iduna-admin", map[string]any{
		"id": c.ID, "role": c.Role, "generation": c.Generation, "elo": c.Elo,
	})
	writeJSON(w, http.StatusOK, c)
}
