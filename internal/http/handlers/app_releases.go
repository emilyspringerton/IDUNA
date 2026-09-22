package handlers

// app_releases.go -- S528, the real, shared app-release registry. Founder real-time: "we need an
// app repository in IDUNA signed in github somehow with emily session new for the build before
// each build and we can use session tokens in the app binaries figure out how to sign them with
// gpg keys or something". Mirrors shankpit_checkpoints.go field-for-field (see internal/apps/
// release_store.go's own doc comment for what session_tag/gpg_signature/github_* mean and why).
// Upload is agent-auth gated (apps.releases.write); list/download/latest stay public, same trust
// level GET /api/v1/shankpit-levels already established -- a released game binary is meant to be
// fetched by anyone (SHANKPIT's own lobby, or a player downloading directly), same as a level.

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/apps"
)

// AppReleasesHandler serves every /api/v1/app-releases... route.
type AppReleasesHandler struct {
	Store *apps.ReleaseStore
}

func (h *AppReleasesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/api/v1/app-releases"
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
	case len(parts) == 1 && parts[0] == "latest" && r.Method == http.MethodGet:
		h.getLatest(w, r)
	case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
		h.download(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func (h *AppReleasesHandler) list(w http.ResponseWriter, r *http.Request) {
	appSlug := r.URL.Query().Get("app")
	if appSlug == "" {
		mmoWriteError(w, http.StatusBadRequest, "app query param required")
		return
	}
	list, err := h.Store.List(r.Context(), appSlug)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// getLatest returns the current is_latest row for ?app=&platform=, or `null` (200, not 404) if
// nothing has ever been published for that pair -- a real, expected, honest state for a fresh
// app_slug, same convention ShankpitCheckpointsHandler.getActive already established.
func (h *AppReleasesHandler) getLatest(w http.ResponseWriter, r *http.Request) {
	appSlug := r.URL.Query().Get("app")
	platform := r.URL.Query().Get("platform")
	if appSlug == "" || platform == "" {
		mmoWriteError(w, http.StatusBadRequest, "app and platform query params required")
		return
	}
	rel, err := h.Store.GetLatest(r.Context(), appSlug, platform)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

// upload -- multipart form: file field "binary" (the built game binary), plus
// app_slug/platform/version/session_tag/gpg_signature/gpg_key_id/github_commit_sha/
// github_run_url text fields. Multipart (not raw-body-plus-header-metadata) matches
// ShankpitCheckpointsHandler.upload's own established convention exactly.
func (h *AppReleasesHandler) upload(w http.ResponseWriter, r *http.Request) {
	const maxUploadBytes = 200*1024*1024 + 1024*1024 // release cap + real form-overhead slack
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	file, header, err := r.FormFile("binary")
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "missing binary file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "read binary: "+err.Error())
		return
	}

	rel, err := h.Store.Create(r.Context(),
		r.FormValue("app_slug"), r.FormValue("platform"), r.FormValue("version"), r.FormValue("session_tag"),
		header.Filename, data,
		r.FormValue("gpg_signature"), r.FormValue("gpg_key_id"), r.FormValue("github_commit_sha"), r.FormValue("github_run_url"))
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

func (h *AppReleasesHandler) download(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rel, data, err := h.Store.ReadBlob(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, rel.Filename))
	w.Header().Set("X-SHA256", rel.SHA256)
	w.Header().Set("X-GPG-Key-ID", rel.GPGKeyID)
	_, _ = w.Write(data)
}
