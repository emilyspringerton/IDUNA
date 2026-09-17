package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/shankpit"
)

func newShankpitLevelsTestStore(t *testing.T) *shankpit.LevelStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE shankpit_levels (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			name                 TEXT NOT NULL,
			width                REAL NOT NULL DEFAULT 100,
			height               REAL NOT NULL DEFAULT 50,
			depth                REAL NOT NULL DEFAULT 100,
			ground_plane_enabled BOOLEAN NOT NULL DEFAULT 1,
			ground_plane_squares INTEGER NOT NULL DEFAULT 2,
			walls_json TEXT NOT NULL DEFAULT '[]',
			objects_json TEXT NOT NULL DEFAULT '[]',
			spawners_json TEXT NOT NULL DEFAULT '[]',
			doors_json TEXT NOT NULL DEFAULT '[]',
			is_default_queue BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_levels: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_shankpit_levels_name ON shankpit_levels(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &shankpit.LevelStore{DB: db}
}

func TestShankpitLevelsHandler_CreateListGet(t *testing.T) {
	store := newShankpitLevelsTestStore(t)
	h := &handlers.ShankpitLevelsHandler{Store: store}

	body, _ := json.Marshal(map[string]any{
		"name": "Black Mesa Transit", "width": 100, "height": 50, "depth": 100,
		"ground_plane_enabled": true, "ground_plane_squares": 2,
		"walls": []map[string]any{{"id": 1, "x": 0, "y": 0, "z": 0, "sx": 4, "sy": 4, "sz": 4, "r": 0.5, "g": 0.5, "b": 0.5, "friction": 0.8}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/shankpit-levels", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created shankpit.Level
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name != "Black Mesa Transit" || len(created.Walls) != 1 {
		t.Fatalf("unexpected created level: %+v", created)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/shankpit-levels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}
	var list []shankpit.LevelSummary
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].WallCount != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestShankpitLevelsHandler_CreateRejectsInvalidWall(t *testing.T) {
	h := &handlers.ShankpitLevelsHandler{Store: newShankpitLevelsTestStore(t)}
	body, _ := json.Marshal(map[string]any{
		"name": "Bad Level", "width": 100, "height": 50, "depth": 100,
		"walls": []map[string]any{{"id": 1, "sx": 0, "sy": 4, "sz": 4}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/shankpit-levels", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-positive wall size, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestShankpitLevelsHandler_UpdateReshapesWall(t *testing.T) {
	store := newShankpitLevelsTestStore(t)
	h := &handlers.ShankpitLevelsHandler{Store: store}
	if _, err := store.CreateLevel(context.Background(), "Test Level", 100, 50, 100, true, 2, []shankpit.Wall{
		{ID: 1, X: 0, Y: 0, Z: 0, SX: 4, SY: 4, SZ: 4, R: 0.5, G: 0.5, B: 0.5, Friction: 0.8},
	}, nil, nil, nil); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"width": 100, "height": 50, "depth": 100,
		"ground_plane_enabled": true, "ground_plane_squares": 2,
		"walls": []map[string]any{{"id": 1, "x": 3, "y": 0, "z": 0, "sx": 10, "sy": 4, "sz": 4, "r": 0.5, "g": 0.5, "b": 0.5, "friction": 0.8}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/nock/api/shankpit-levels/1", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated shankpit.Level
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if len(updated.Walls) != 1 || updated.Walls[0].SX != 10 {
		t.Fatalf("expected reshaped wall, got %+v", updated)
	}
}

func TestShankpitLevelsPublicHandler_NoWriteMethods(t *testing.T) {
	h := &handlers.ShankpitLevelsPublicHandler{Store: newShankpitLevelsTestStore(t)}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/shankpit-levels", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST against the public handler, got %d", rec.Code)
	}
}

func TestShankpitLevelsPublicHandler_ListAndExport(t *testing.T) {
	store := newShankpitLevelsTestStore(t)
	if _, err := store.CreateLevel(context.Background(), "Public Level", 100, 50, 100, true, 2, []shankpit.Wall{
		{ID: 1, X: 0, Y: 0, Z: 0, SX: 4, SY: 4, SZ: 4, R: 0.5, G: 0.5, B: 0.5, Friction: 0.8},
	}, nil, nil, nil); err != nil {
		t.Fatalf("seed create: %v", err)
	}
	h := &handlers.ShankpitLevelsPublicHandler{Store: store}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/shankpit-levels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/shankpit-levels/1/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var doc shankpit.ExportDoc
	json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.Version != 1 || len(doc.Walls) != 1 {
		t.Fatalf("unexpected export doc: %+v", doc)
	}
}
