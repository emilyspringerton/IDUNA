package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/brawlpit"
	"iduna/internal/http/handlers"
)

func newBrawlpitLevelsTestHandler(t *testing.T) *handlers.BrawlpitLevelsHandler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE brawlpit_levels (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			name           TEXT NOT NULL,
			width          REAL NOT NULL DEFAULT 80,
			height         REAL NOT NULL DEFAULT 40,
			platforms_json TEXT NOT NULL DEFAULT '[]',
			guides_json    TEXT NOT NULL DEFAULT '[]',
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create brawlpit_levels: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_brawlpit_levels_name ON brawlpit_levels(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &handlers.BrawlpitLevelsHandler{Store: &brawlpit.LevelStore{DB: db}}
}

func TestBrawlpitLevelsHandler_CreateListGet(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	body, _ := json.Marshal(map[string]any{
		"name": "Final Destination", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": -5, "w": 60, "h": 10, "type": 0}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created brawlpit.Level
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Name != "Final Destination" || len(created.Platforms) != 1 {
		t.Fatalf("unexpected created level: %+v", created)
	}

	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels", nil))
	var list []brawlpit.LevelSummary
	json.Unmarshal(listRec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].PlatformCount != 1 {
		t.Fatalf("expected 1 level with platform_count=1 in the list, got %+v", list)
	}

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels/1", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", getRec.Code)
	}
}

func TestBrawlpitLevelsHandler_CreateRejectsInvalidPayload(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	body, _ := json.Marshal(map[string]any{"name": "No Platforms", "width": 80, "height": 40})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a level with no platforms, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBrawlpitLevelsHandler_UpdateSavesNewLayout(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Editable", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": 0, "w": 10, "h": 10, "type": 0}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	updateBody, _ := json.Marshal(map[string]any{
		"width": 100, "height": 50,
		"platforms": []map[string]any{
			{"x": 1, "y": 1, "w": 5, "h": 5, "type": 1},
			{"x": 2, "y": 2, "w": 6, "h": 6, "type": 0},
		},
	})
	updateRec := httptest.NewRecorder()
	h.ServeHTTP(updateRec, httptest.NewRequest(http.MethodPut, "/admin/nock/api/brawlpit-levels/1", bytes.NewReader(updateBody)))
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated brawlpit.Level
	json.Unmarshal(updateRec.Body.Bytes(), &updated)
	if updated.Width != 100 || len(updated.Platforms) != 2 {
		t.Fatalf("update didn't apply: %+v", updated)
	}
}

func TestBrawlpitLevelsHandler_RenameCloneDelete(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Original", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": 0, "w": 10, "h": 10, "type": 0}},
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	renameBody, _ := json.Marshal(map[string]any{"name": "Renamed"})
	renameRec := httptest.NewRecorder()
	h.ServeHTTP(renameRec, httptest.NewRequest(http.MethodPatch, "/admin/nock/api/brawlpit-levels/1", bytes.NewReader(renameBody)))
	if renameRec.Code != http.StatusOK {
		t.Fatalf("rename: expected 200, got %d: %s", renameRec.Code, renameRec.Body.String())
	}

	cloneBody, _ := json.Marshal(map[string]any{"name": "Cloned"})
	cloneRec := httptest.NewRecorder()
	h.ServeHTTP(cloneRec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels/1/clone", bytes.NewReader(cloneBody)))
	if cloneRec.Code != http.StatusCreated {
		t.Fatalf("clone: expected 201, got %d: %s", cloneRec.Code, cloneRec.Body.String())
	}

	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, httptest.NewRequest(http.MethodDelete, "/admin/nock/api/brawlpit-levels/1", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", delRec.Code)
	}

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels/1", nil))
	if getRec.Code != http.StatusNotFound {
		t.Errorf("expected the deleted level to 404, got %d", getRec.Code)
	}
}

// TestBrawlpitLevelsHandler_ExportMatchesNativeLoaderShape is the real, direct HTTP-layer guard
// on the S415-04 cross-repo contract (see internal/brawlpit's own equivalent store-layer test).
func TestBrawlpitLevelsHandler_ExportMatchesNativeLoaderShape(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Export Me", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": -5, "w": 60, "h": 10, "type": 0}},
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	exportRec := httptest.NewRecorder()
	h.ServeHTTP(exportRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels/1/export", nil))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d: %s", exportRec.Code, exportRec.Body.String())
	}
	var doc brawlpit.ExportDoc
	json.Unmarshal(exportRec.Body.Bytes(), &doc)
	if doc.Version != 1 || doc.Name != "Export Me" || len(doc.Platforms) != 1 || doc.Platforms[0].W != 60 {
		t.Errorf("export doc doesn't match the real native contract: %+v", doc)
	}
}

// TestBrawlpitLevelsHandler_SaveAndFetchGuides is the real, direct HTTP-layer guard on S418-01/02
// (NOCK — Guide-Based Snapping): guides save via their own PUT route, persist, and round-trip
// through a normal GET.
func TestBrawlpitLevelsHandler_SaveAndFetchGuides(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Guided Stage", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": 0, "w": 10, "h": 10, "type": 0}},
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	guidesBody, _ := json.Marshal(map[string]any{
		"guides": []map[string]any{
			{"axis": "vertical", "coord": 0, "locked": false, "is_mirror_axis": true},
			{"axis": "horizontal", "coord": 5, "locked": true, "is_mirror_axis": false},
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/nock/api/brawlpit-levels/1/guides", bytes.NewReader(guidesBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save guides: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var saved brawlpit.Level
	json.Unmarshal(rec.Body.Bytes(), &saved)
	if len(saved.Guides) != 2 {
		t.Fatalf("expected 2 guides in the save response, got %+v", saved.Guides)
	}

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels/1", nil))
	var fetched brawlpit.Level
	json.Unmarshal(getRec.Body.Bytes(), &fetched)
	if len(fetched.Guides) != 2 || !fetched.Guides[0].IsMirrorAxis {
		t.Errorf("guides didn't persist through a plain GET: %+v", fetched.Guides)
	}

	// The requirements doc's own explicit contract (1.4): guides must never reach the
	// client-facing export.
	exportRec := httptest.NewRecorder()
	h.ServeHTTP(exportRec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/brawlpit-levels/1/export", nil))
	if !bytes.Contains(exportRec.Body.Bytes(), []byte(`"platforms"`)) {
		t.Fatalf("export response looks wrong: %s", exportRec.Body.String())
	}
	if bytes.Contains(exportRec.Body.Bytes(), []byte(`guide`)) {
		t.Errorf("export response must never mention guides at all: %s", exportRec.Body.String())
	}
}

func TestBrawlpitLevelsHandler_SaveGuidesRejectsInvalidAxis(t *testing.T) {
	h := newBrawlpitLevelsTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Bad Guide Stage", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": 0, "w": 10, "h": 10, "type": 0}},
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	guidesBody, _ := json.Marshal(map[string]any{
		"guides": []map[string]any{{"axis": "sideways", "coord": 0}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/admin/nock/api/brawlpit-levels/1/guides", bytes.NewReader(guidesBody)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an invalid axis, got %d: %s", rec.Code, rec.Body.String())
	}
}
