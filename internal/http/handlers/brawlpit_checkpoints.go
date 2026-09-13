package handlers

// brawlpit_checkpoints.go — S420, the real, remote RL checkpoint registry (founder real-time:
// "lets make a checkpoint registry so we can train from multiple locations and then we can add
// checkpoints from colab?"). Upload is agent-auth gated (brawlpit.checkpoints.write, see
// migrations/truestore/202609131400_brawlpit_rl_checkpoints.sql); list/download stay public,
// same trust level GET /api/v1/brawlpit-levels already established.

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/brawlpit"
)

// BrawlpitCheckpointsHandler serves every /api/v1/brawlpit-checkpoints... route.
type BrawlpitCheckpointsHandler struct {
	Store *brawlpit.CheckpointStore
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
	case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
		h.download(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
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
