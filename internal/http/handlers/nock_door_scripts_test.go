package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
)

const testDoorScriptSource = `(module doorscript)
(import math)

(defn door-tick [(dist-to-player : F64) (state : F64)] : F64
  (if (< dist-to-player 3.0)
    1.0
    (if (> dist-to-player 5.0)
      0.0
      state)))
`

func newDoorScriptsTestStore(t *testing.T) *nock.DoorScriptStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_door_scripts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL,
			parena_source TEXT NOT NULL,
			compiled_so   BLOB NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_door_scripts: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_nock_door_scripts_name ON nock_door_scripts(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &nock.DoorScriptStore{DB: db}
}

func TestNockDoorScriptsHandler_CreateListGet(t *testing.T) {
	store := newDoorScriptsTestStore(t)
	h := &handlers.NockDoorScriptsHandler{Store: store}

	body, _ := json.Marshal(map[string]any{"name": "hallway-door", "source": testDoorScriptSource})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/door-scripts", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created nock.DoorScript
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "hallway-door" {
		t.Errorf("unexpected created script: %+v", created)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/admin/nock/api/door-scripts", nil))
	var list []nock.DoorScriptSummary
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "hallway-door" {
		t.Errorf("unexpected list: %+v", list)
	}

	// Public download route serves the real, real compiled .so bytes.
	pub := &handlers.NockDoorScriptsPublicHandler{Store: store}
	rec3 := httptest.NewRecorder()
	pub.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/api/v1/nock-door-scripts/1/download", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("download: expected 200, got %d: %s", rec3.Code, rec3.Body.String())
	}
	got := rec3.Body.Bytes()
	if len(got) < 4 || string(got[0:4]) != "\x7fELF" {
		t.Errorf("downloaded bytes don't look like a real ELF shared object, len=%d", len(got))
	}
}

func TestNockDoorScriptsHandler_CreateRejectsBadSource(t *testing.T) {
	store := newDoorScriptsTestStore(t)
	h := &handlers.NockDoorScriptsHandler{Store: store}

	body, _ := json.Marshal(map[string]any{"name": "bad", "source": "not valid parena at all"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/door-scripts", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad source, got %d: %s", rec.Code, rec.Body.String())
	}
}
