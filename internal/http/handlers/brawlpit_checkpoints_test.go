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
			name            TEXT NOT NULL DEFAULT '',
			is_active_opponent INTEGER NOT NULL DEFAULT 0,
			weights_blob_path TEXT NOT NULL DEFAULT '',
			weights_size_bytes INTEGER NOT NULL DEFAULT 0,
			weights_sha256 TEXT NOT NULL DEFAULT '',
			is_disabled     INTEGER NOT NULL DEFAULT 0,
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

func TestBrawlpitCheckpointsHandler_GetActiveWhenNoneSet(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/active", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "null\n" && rec.Body.String() != "null" {
		t.Errorf("expected a real, honest null when no opponent is selected, got %q", rec.Body.String())
	}
}

func TestBrawlpitCheckpointActivateHandler_SetsAndGetsActive(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)

	body, contentType := multipartUploadBody(t, "main", "5", "1700", "this-box", "main5.zip", []byte("data"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	uploadReq.Header.Set("Content-Type", contentType)
	h.ServeHTTP(httptest.NewRecorder(), uploadReq)

	activateH := &handlers.BrawlpitCheckpointActivateHandler{Store: h.Store}
	rec := httptest.NewRecorder()
	activateH.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/admin/nock/api/brawlpit-checkpoints/1/activate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/active", nil))
	var active brawlpit.Checkpoint
	json.Unmarshal(getRec.Body.Bytes(), &active)
	if active.ID != 1 || !active.IsActiveOpponent {
		t.Fatalf("expected checkpoint 1 to be the real active opponent, got %+v", active)
	}
}

func TestBrawlpitCheckpointDisableHandler_TogglesAndPersists(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)

	body, contentType := multipartUploadBody(t, "main", "5", "1700", "this-box", "main5.zip", []byte("data"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	uploadReq.Header.Set("Content-Type", contentType)
	h.ServeHTTP(httptest.NewRecorder(), uploadReq)

	disableH := &handlers.BrawlpitCheckpointDisableHandler{Store: h.Store}
	rec := httptest.NewRecorder()
	disableReq := httptest.NewRequest(http.MethodPatch, "/admin/nock/api/brawlpit-checkpoints/1/disable",
		bytes.NewBufferString(`{"disabled": true}`))
	disableH.ServeHTTP(rec, disableReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var disabled brawlpit.Checkpoint
	json.Unmarshal(rec.Body.Bytes(), &disabled)
	if !disabled.IsDisabled {
		t.Fatalf("expected is_disabled=true in the response, got %+v", disabled)
	}

	// The public list must reflect it too, so the admin UI's checkbox stays in sync.
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints", nil))
	var listed []brawlpit.Checkpoint
	json.Unmarshal(listRec.Body.Bytes(), &listed)
	if len(listed) != 1 || !listed[0].IsDisabled {
		t.Fatalf("expected the listed checkpoint to show is_disabled=true, got %+v", listed)
	}

	// Real, reversible -- flip it back off.
	rec2 := httptest.NewRecorder()
	reenableReq := httptest.NewRequest(http.MethodPatch, "/admin/nock/api/brawlpit-checkpoints/1/disable",
		bytes.NewBufferString(`{"disabled": false}`))
	disableH.ServeHTTP(rec2, reenableReq)
	var reenabled brawlpit.Checkpoint
	json.Unmarshal(rec2.Body.Bytes(), &reenabled)
	if reenabled.IsDisabled {
		t.Fatalf("expected is_disabled=false after re-enabling, got %+v", reenabled)
	}
}

func TestBrawlpitCheckpointDisableHandler_RejectsBadJSON(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	body, contentType := multipartUploadBody(t, "main", "5", "1700", "this-box", "main5.zip", []byte("data"))
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	uploadReq.Header.Set("Content-Type", contentType)
	h.ServeHTTP(httptest.NewRecorder(), uploadReq)

	disableH := &handlers.BrawlpitCheckpointDisableHandler{Store: h.Store}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/nock/api/brawlpit-checkpoints/1/disable", bytes.NewBufferString("not json"))
	disableH.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", rec.Code)
	}
}

func multipartUploadBodyWithWeights(t *testing.T, role, generation, elo, sourceLocation, filename string, content []byte, weightsFilename string, weightsContent []byte) (*bytes.Buffer, string) {
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
	if weightsFilename != "" {
		wfw, err := w.CreateFormFile("weights_file", weightsFilename)
		if err != nil {
			t.Fatalf("CreateFormFile(weights_file): %v", err)
		}
		if _, err := wfw.Write(weightsContent); err != nil {
			t.Fatalf("write weights_file field: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, w.FormDataContentType()
}

func TestBrawlpitCheckpointsHandler_UploadWithWeightsAndDownloadLZ4(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)

	weightsPayload := []byte("BPMW-fake-real-weights-bytes-for-a-real-round-trip-test")
	body, contentType := multipartUploadBodyWithWeights(t, "main", "0", "1500", "this-box",
		"main0.zip", []byte("zip data"), "main0.weights.bin", weightsPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created brawlpit.Checkpoint
	json.Unmarshal(rec.Body.Bytes(), &created)
	if !created.HasWeights {
		t.Fatalf("expected HasWeights=true after uploading with a weights_file, got %+v", created)
	}

	// Default (no ?compress=) must be LZ4-compressed (S421-02, "make sure the model downloads
	// with lz4").
	lz4Rec := httptest.NewRecorder()
	h.ServeHTTP(lz4Rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/1/weights", nil))
	if lz4Rec.Code != http.StatusOK {
		t.Fatalf("weights download: expected 200, got %d: %s", lz4Rec.Code, lz4Rec.Body.String())
	}
	decompressed := brawlpit.DecompressLZ4(lz4Rec.Body.Bytes())
	if string(decompressed) != string(weightsPayload) {
		t.Errorf("LZ4-decompressed weights don't match the real uploaded bytes: got %q", decompressed)
	}

	// ?compress=none must return the exact raw bytes.
	rawRec := httptest.NewRecorder()
	h.ServeHTTP(rawRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/1/weights?compress=none", nil))
	if rawRec.Body.String() != string(weightsPayload) {
		t.Errorf("raw (uncompressed) weights download doesn't match the real uploaded bytes: got %q", rawRec.Body.String())
	}
}

func TestBrawlpitCheckpointsHandler_UploadWithoutWeightsHasNoWeights(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	body, contentType := multipartUploadBody(t, "main", "0", "1500", "this-box", "main0.zip", []byte("data"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var created brawlpit.Checkpoint
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.HasWeights {
		t.Error("a checkpoint uploaded with no weights_file must report HasWeights=false")
	}

	weightsRec := httptest.NewRecorder()
	h.ServeHTTP(weightsRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints/1/weights", nil))
	if weightsRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 fetching weights for a checkpoint with none, got %d", weightsRec.Code)
	}
}

func TestBrawlpitCheckpointsHandler_CreatedCheckpointHasARealName(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	body, contentType := multipartUploadBody(t, "league_exploiter", "0", "1500", "this-box", "x.zip", []byte("data"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var created brawlpit.Checkpoint
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name == "" {
		t.Error("a real, uploaded checkpoint must get a real, non-empty name")
	}
}

func TestBrawlpitCheckpointsHandler_RecordMatchResult(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)

	bodyA, contentTypeA := multipartUploadBody(t, "main", "0", "1500", "this-box", "a.zip", []byte("a"))
	reqA := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", bodyA)
	reqA.Header.Set("Content-Type", contentTypeA)
	h.ServeHTTP(httptest.NewRecorder(), reqA)

	bodyB, contentTypeB := multipartUploadBody(t, "league_exploiter", "0", "1500", "this-box", "b.zip", []byte("b"))
	reqB := httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints", bodyB)
	reqB.Header.Set("Content-Type", contentTypeB)
	h.ServeHTTP(httptest.NewRecorder(), reqB)

	resultBody, _ := json.Marshal(map[string]any{"a_id": 1, "b_id": 2, "score_a": 1.0})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints/match-result", bytes.NewReader(resultBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-checkpoints", nil))
	var list []brawlpit.Checkpoint
	json.Unmarshal(listRec.Body.Bytes(), &list)
	for _, c := range list {
		if c.ID == 1 && c.Elo <= 1500 {
			t.Errorf("winner's Elo should have moved up, got %v", c.Elo)
		}
		if c.ID == 2 && c.Elo >= 1500 {
			t.Errorf("loser's Elo should have moved down, got %v", c.Elo)
		}
	}
}

func TestBrawlpitCheckpointsHandler_RecordMatchResultRejectsBadJSON(t *testing.T) {
	h, _ := newBrawlpitCheckpointsTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/brawlpit-checkpoints/match-result", bytes.NewReader([]byte("not json"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}
