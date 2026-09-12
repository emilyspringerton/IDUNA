package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
)

func newTexturesTestHandler(t *testing.T) *handlers.NockTexturesHandler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_textures (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			width         INTEGER NOT NULL,
			height        INTEGER NOT NULL,
			png_data      BLOB NOT NULL,
			parena_source TEXT,
			prompt        TEXT,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_textures: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_nock_textures_name ON nock_textures(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &handlers.NockTexturesHandler{Store: &nock.TextureStore{DB: db}}
}

func TestNockTexturesHandler_CreateFromPNGAndGet(t *testing.T) {
	h := newTexturesTestHandler(t)

	pngB64 := base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))
	body, _ := json.Marshal(map[string]any{"name": "brick", "width": 32, "height": 32, "png_base64": pngB64})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/textures", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created nock.Texture
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name != "brick" {
		t.Fatalf("unexpected created texture: %+v", created)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/admin/nock/api/textures/1/image", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("image: expected 200, got %d", rec2.Code)
	}
	if rec2.Body.String() != "fake-png-bytes" {
		t.Errorf("expected real image bytes to round-trip, got %q", rec2.Body.String())
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected image/png content type, got %q", ct)
	}
}

func TestNockTexturesHandler_ListRenameDelete(t *testing.T) {
	h := newTexturesTestHandler(t)
	pngB64 := base64.StdEncoding.EncodeToString([]byte("x"))
	body, _ := json.Marshal(map[string]any{"name": "brick", "width": 8, "height": 8, "png_base64": pngB64})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/textures", bytes.NewReader(body)))

	recList := httptest.NewRecorder()
	h.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, "/admin/nock/api/textures", nil))
	var list []nock.TextureSummary
	json.Unmarshal(recList.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "brick" {
		t.Fatalf("unexpected list: %+v", list)
	}

	renameBody, _ := json.Marshal(map[string]any{"name": "brick-v2"})
	recRename := httptest.NewRecorder()
	h.ServeHTTP(recRename, httptest.NewRequest(http.MethodPatch, "/admin/nock/api/textures/1", bytes.NewReader(renameBody)))
	if recRename.Code != http.StatusOK {
		t.Fatalf("rename: expected 200, got %d: %s", recRename.Code, recRename.Body.String())
	}

	recDelete := httptest.NewRecorder()
	h.ServeHTTP(recDelete, httptest.NewRequest(http.MethodDelete, "/admin/nock/api/textures/1", nil))
	if recDelete.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", recDelete.Code)
	}

	recGet := httptest.NewRecorder()
	h.ServeHTTP(recGet, httptest.NewRequest(http.MethodGet, "/admin/nock/api/textures/1", nil))
	if recGet.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted texture, got %d", recGet.Code)
	}
}

func TestNockTexturesHandler_Clone(t *testing.T) {
	h := newTexturesTestHandler(t)
	pngB64 := base64.StdEncoding.EncodeToString([]byte("original"))
	body, _ := json.Marshal(map[string]any{"name": "brick", "width": 8, "height": 8, "png_base64": pngB64})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/textures", bytes.NewReader(body)))

	cloneBody, _ := json.Marshal(map[string]any{"name": "brick-variant"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/textures/1/clone", bytes.NewReader(cloneBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var cloned nock.Texture
	json.Unmarshal(rec.Body.Bytes(), &cloned)
	if cloned.ID == 1 || cloned.Name != "brick-variant" {
		t.Fatalf("unexpected clone: %+v", cloned)
	}

	recList := httptest.NewRecorder()
	h.ServeHTTP(recList, httptest.NewRequest(http.MethodGet, "/admin/nock/api/textures", nil))
	var list []nock.TextureSummary
	json.Unmarshal(recList.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("expected both the original and the clone to exist independently, got %+v", list)
	}
}
