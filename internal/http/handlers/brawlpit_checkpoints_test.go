package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/brawlpit"
	"iduna/internal/http/handlers"
)

func newBrawlpitCheckpointsTestHandler(t *testing.T) (*handlers.BrawlpitCheckpointsHandler, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE brawlpit_rl_checkpoints (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			role            TEXT NOT NULL,
			generation      INTEGER NOT NULL,
			elo             REAL NOT NULL DEFAULT 1500,
			source_location TEXT NOT NULL DEFAULT '',
			filename        TEXT NOT NULL,
			sha256          TEXT NOT NULL,
			size_bytes      INTEGER NOT NULL,
			blob_path       TEXT NOT NULL,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create brawlpit_rl_checkpoints: %v", err)
	}
	blobDir := t.TempDir()
	return &handlers.BrawlpitCheckpointsHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: blobDir}}, blobDir
}

func multipartUploadBody(t *testing.T, role, generation, elo, sourceLocation, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("role", role)
	_ = w.WriteField("generation", generation)
	_ = w.WriteField("elo", elo)
	_ = w.WriteField("source_location", sourceLocation)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, w.FormDataContentType()
}

func TestBrawlpitCheckpointsHandler_UploadListDownload(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)

	body, contentType := multipartUploadBody(t, "main", "3", "1550.5", "colab", "main3.zip", []byte("real fake checkpoint bytes"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	uploadReq.Header.Set("Content-Type", contentType)
	uploadRec := httptest.NewRecorder()
	h.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}
	var created brawlpit.Checkpoint
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created checkpoint: %v", err)
	}
	if created.Role != "main" || created.Generation != 3 || created.SourceLocation != "colab" {
		t.Fatalf("unexpected created checkpoint: %+v", created)
	}

	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listRec.Code)
	}
	var list []brawlpit.Checkpoint
	json.Unmarshal(listRec.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 checkpoint in the list, got %d", len(list))
	}

	downloadRec := httptest.NewRecorder()
	h.ServeHTTP(downloadRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/1/download", nil))
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", downloadRec.Code)
	}
	if downloadRec.Body.String() != "real fake checkpoint bytes" {
		t.Errorf("downloaded content doesn't match what was uploaded: %q", downloadRec.Body.String())
	}
}

func TestBrawlpitCheckpointsHandler_UploadRejectsInvalidRole(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	body, contentType := multipartUploadBody(t, "not_a_role", "0", "1500", "colab", "x.zip", []byte("data"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an invalid role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBrawlpitCheckpointsHandler_ListFiltersByRole(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	for _, role := range []string{"main", "main", "league_exploiter"} {
		body, contentType := multipartUploadBody(t, role, "0", "1500", "colab", role+".zip", []byte("data"))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
		req.Header.Set("Content-Type", contentType)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints?role=main", nil))
	var list []brawlpit.Checkpoint
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("expected 2 main checkpoints, got %d", len(list))
	}
}

func TestBrawlpitCheckpointsHandler_DownloadNotFound(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/999/download", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a nonexistent checkpoint, got %d", rec.Code)
	}
}
