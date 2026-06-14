package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"iduna/internal/drive"
	"iduna/internal/http/middleware"
)

// DriveHandler handles /api/v1/drive/* routes for Google Drive sync operations.
//
// All routes require IDUNA agent auth with the appropriate permission:
//
//	POST /api/v1/drive/upload        — upload a file to Drive (drive.write)
//	GET  /api/v1/drive/files         — list files in configured folder (drive.read)
//	GET  /api/v1/drive/files/{id}    — get file metadata (drive.read)
//
// Configuration via env vars (set before starting IDUNA):
//
//	GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON  — service account key JSON (required)
//	GOOGLE_DRIVE_FOLDER_ID             — Drive folder ID for uploads (optional)
//
// The handler serves 503 if the Drive client is not configured, allowing
// IDUNA to start without Drive credentials in environments that don't need it.
type DriveHandler struct {
	Client *drive.Client // nil = not configured
}

func (h *DriveHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/v1/drive/upload", h)
	mux.Handle("/api/v1/drive/files", h)
	mux.Handle("/api/v1/drive/files/", h)
}

func (h *DriveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Client == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code":    "DRIVE_NOT_CONFIGURED",
			"message": "Google Drive not configured (set GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON)",
		})
		return
	}

	switch {
	case r.URL.Path == "/api/v1/drive/upload" && r.Method == http.MethodPost:
		h.upload(w, r)
	case r.URL.Path == "/api/v1/drive/files" && r.Method == http.MethodGet:
		h.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/drive/files/") && r.Method == http.MethodGet:
		fileID := strings.TrimPrefix(r.URL.Path, "/api/v1/drive/files/")
		h.getFile(w, r, fileID)
	default:
		http.NotFound(w, r)
	}
}

// POST /api/v1/drive/upload
// Accepts multipart/form-data:
//
//	file       — file content (required)
//	filename   — override filename (optional; uses original name)
//
// Response: { id, name, mimeType, size, webViewLink, createdTime }
func (h *DriveHandler) upload(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "drive.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "drive.write permission required",
		})
		return
	}

	if err := r.ParseMultipartForm(int64(drive.MaxUploadBytes) + 10240); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": fmt.Sprintf("parse form: %v", err),
		})
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "file field required in multipart form",
		})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code": "READ_ERROR", "message": "read file body",
		})
		return
	}

	filename := r.FormValue("filename")
	if filename == "" {
		filename = fileHeader.Filename
	}
	if filename == "" {
		filename = fmt.Sprintf("upload-%d", time.Now().Unix())
	}

	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = inferDriveMimeType(filename)
	}

	info, err := h.Client.Upload(filename, mimeType, data)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"code": "DRIVE_ERROR", "message": err.Error(),
		})
		return
	}

	log.Printf("drive upload: agent=%v file=%s id=%s size=%d link=%s",
		agentSubject(claims), info.Name, info.ID, len(data), info.WebViewLink)

	writeJSON(w, http.StatusCreated, info)
}

// GET /api/v1/drive/files?q=<optional_drive_query>
func (h *DriveHandler) list(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "drive.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "drive.read permission required",
		})
		return
	}

	files, err := h.Client.List(r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"code": "DRIVE_ERROR", "message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"files": files, "count": len(files)})
}

// GET /api/v1/drive/files/{id}
func (h *DriveHandler) getFile(w http.ResponseWriter, r *http.Request, fileID string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "drive.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "drive.read permission required",
		})
		return
	}

	info, err := h.Client.GetFile(fileID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "DRIVE_ERROR", "message": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// inferDriveMimeType returns a MIME type based on filename extension.
func inferDriveMimeType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".jsonl"):
		return "application/jsonl"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	case strings.HasSuffix(lower, ".py"):
		return "text/x-python"
	case strings.HasSuffix(lower, ".ipynb"):
		return "application/x-ipynb+json"
	default:
		return "application/octet-stream"
	}
}

// agentSubject extracts the sub claim from JWT claims for logging.
func agentSubject(claims map[string]any) string {
	if claims == nil {
		return "anonymous"
	}
	if sub, ok := claims["sub"].(string); ok {
		return sub
	}
	return "unknown"
}
