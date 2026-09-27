package handlers

// nock_robots.go — CRUD/upload API for NOCK's robot registry (founder real-time, 2026-09-27:
// "upgrade shankpit and nock to formal rigid body physics we need to get goldenband rigged up
// with real robot data from industrial data sheets"). Thin wrapper over internal/nock.RobotStore;
// see that file's header for the provenance rules every upload must pass.
//
//	GET    /admin/nock/api/robots               list
//	POST   /admin/nock/api/robots               multipart: spec (.grobot.json), grobot, [gskel], [source_location]
//	GET    /admin/nock/api/robots/{id}          full row + decoded rig (per-joint datasheet table)
//	GET    /admin/nock/api/robots/{id}/spec     the cited spec, verbatim
//	GET    /admin/nock/api/robots/{id}/grobot   compiled physics rig
//	GET    /admin/nock/api/robots/{id}/gskel    display skeleton
//	DELETE /admin/nock/api/robots/{id}
//	POST   /admin/nock/api/robots/sync          re-import every robot from the GOLDENBAND git checkout

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
)

type NockRobotsHandler struct {
	Store *nock.RobotStore
	// GitDir is the GOLDENBAND working tree the sync endpoint reads (NOCK_ROBOTS_GIT_DIR). Empty
	// disables the endpoint.
	GitDir string
}

func (h *NockRobotsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/admin/nock/api/robots"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		list, err := h.Store.ListRobots(r.Context())
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.create(w, r)
	case len(parts) == 1 && parts[0] == "sync" && r.Method == http.MethodPost:
		if h.GitDir == "" {
			mmoWriteError(w, http.StatusServiceUnavailable, "robot git sync is disabled (NOCK_ROBOTS_GIT_DIR=off)")
			return
		}
		rep, err := h.Store.SyncFromGit(r.Context(), h.GitDir)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rep)
	case len(parts) == 1 && r.Method == http.MethodGet:
		if rb := h.load(w, r, parts[0]); rb != nil {
			rb.GRobotData, rb.GSkelData = nil, nil
			writeJSON(w, http.StatusOK, rb)
		}
	case len(parts) == 1 && r.Method == http.MethodDelete:
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		if err := h.Store.DeleteRobot(r.Context(), id); err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
	case len(parts) == 2 && r.Method == http.MethodGet && (parts[1] == "spec" || parts[1] == "grobot" || parts[1] == "gskel"):
		h.download(w, r, parts[0], parts[1])
	default:
		http.NotFound(w, r)
	}
}

func (h *NockRobotsHandler) load(w http.ResponseWriter, r *http.Request, idStr string) *nock.Robot {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return nil
	}
	rb, err := h.Store.GetRobot(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return nil
	}
	return rb
}

func (h *NockRobotsHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8*1024*1024)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return
	}
	spec, err := readFormFile(r, "spec")
	if err != nil || len(spec) == 0 {
		// Also accept the spec as a plain text field.
		spec = []byte(r.FormValue("spec"))
	}
	if len(spec) == 0 {
		mmoWriteError(w, http.StatusBadRequest, "missing spec (the robot's .grobot.json)")
		return
	}
	grobot, err := readFormFile(r, "grobot")
	if err != nil || len(grobot) == 0 {
		mmoWriteError(w, http.StatusBadRequest, "missing grobot (the compiled .grobot from `gbtool robot compile`)")
		return
	}
	gskel, _ := readFormFile(r, "gskel")
	rb, err := h.Store.CreateRobot(r.Context(), spec, grobot, gskel, r.FormValue("source_location"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	rb.GRobotData, rb.GSkelData = nil, nil
	writeJSON(w, http.StatusCreated, rb)
}

func (h *NockRobotsHandler) download(w http.ResponseWriter, r *http.Request, idStr, kind string) {
	rb := h.load(w, r, idStr)
	if rb == nil {
		return
	}
	var data []byte
	name := rb.Name + "." + kind
	ctype := "application/octet-stream"
	switch kind {
	case "spec":
		data, name, ctype = []byte(rb.SpecJSON), rb.Name+".grobot.json", "application/json"
	case "grobot":
		data = rb.GRobotData
	case "gskel":
		data = rb.GSkelData
	}
	if len(data) == 0 {
		mmoWriteError(w, http.StatusNotFound, fmt.Sprintf("robot %d has no %s", rb.ID, kind))
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Write(data) //nolint:errcheck
}
