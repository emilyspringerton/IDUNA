package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/brawlpit"
	"iduna/internal/http/handlers"
)

// newBrawlpitLevelsPublicTestHandler reuses the same real, admin-side LevelStore/DB setup
// (newBrawlpitLevelsTestHandler, brawlpit_levels_test.go) since both handlers share one store --
// only the write handler is used here to seed data, the public one to read it back.
func newBrawlpitLevelsPublicTestHandler(t *testing.T) (*handlers.BrawlpitLevelsHandler, *handlers.BrawlpitLevelsPublicHandler) {
	t.Helper()
	writeH := newBrawlpitLevelsTestHandler(t)
	// BrawlpitLevelsHandler doesn't expose its own Store field publicly for reuse across
	// packages in this test file the same way -- construct a second handler against the exact
	// same *brawlpit.LevelStore by reaching through the exported field.
	return writeH, &handlers.BrawlpitLevelsPublicHandler{Store: writeH.Store}
}

func TestBrawlpitLevelsPublicHandler_ListAndExport(t *testing.T) {
	writeH, publicH := newBrawlpitLevelsPublicTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Public Level", "width": 80, "height": 40,
		"platforms": []map[string]any{{"x": 0, "y": -5, "w": 60, "h": 10, "type": 0}},
	})
	writeH.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	listRec := httptest.NewRecorder()
	publicH.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-levels", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("public list: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var list []brawlpit.LevelSummary
	json.Unmarshal(listRec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "Public Level" {
		t.Fatalf("unexpected public list: %+v", list)
	}

	exportRec := httptest.NewRecorder()
	publicH.ServeHTTP(exportRec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-levels/1/export", nil))
	if exportRec.Code != http.StatusOK {
		t.Fatalf("public export: expected 200, got %d: %s", exportRec.Code, exportRec.Body.String())
	}
	var doc brawlpit.ExportDoc
	json.Unmarshal(exportRec.Body.Bytes(), &doc)
	if doc.Name != "Public Level" || doc.Width != 80 || len(doc.Platforms) != 1 {
		t.Errorf("unexpected public export doc: %+v", doc)
	}
}

// TestBrawlpitLevelsPublicHandler_RefusesWrites is the real security guard on this file's own
// central design claim: this handler can't accept a write at all, on ANY method, not just because
// nothing routes to it -- even a POST to the exact same path it lists on must be refused.
func TestBrawlpitLevelsPublicHandler_RefusesWrites(t *testing.T) {
	_, publicH := newBrawlpitLevelsPublicTestHandler(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		publicH.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/brawlpit-levels", bytes.NewReader([]byte(`{}`))))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rec.Code)
		}
	}
}

// TestBrawlpitLevelsPublicHandler_ExportLZ4 is the real, direct guard on S417-03's own wire
// contract: `?compress=lz4` must return bytes that this package's own real LZ4 codec (the exact
// same one BRAWLPIT's native client links) decompresses back into the identical JSON export.
func TestBrawlpitLevelsPublicHandler_ExportLZ4(t *testing.T) {
	writeH, publicH := newBrawlpitLevelsPublicTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{
		"name": "Compressed Level", "width": 80, "height": 40,
		"platforms": []map[string]any{
			{"x": 0, "y": -5, "w": 60, "h": 10, "type": 0},
			{"x": -15, "y": 8, "w": 12, "h": 1, "type": 1},
		},
	})
	writeH.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/nock/api/brawlpit-levels", bytes.NewReader(createBody)))

	rec := httptest.NewRecorder()
	publicH.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-levels/1/export?compress=lz4", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-brawlpit-lz4" {
		t.Errorf("expected application/x-brawlpit-lz4 content type, got %q", ct)
	}

	decompressed := brawlpit.DecompressLZ4(rec.Body.Bytes())
	if decompressed == nil {
		t.Fatal("DecompressLZ4 failed on the real server response")
	}
	var doc brawlpit.ExportDoc
	if err := json.Unmarshal(decompressed, &doc); err != nil {
		t.Fatalf("decompressed bytes weren't valid JSON: %v", err)
	}
	if doc.Name != "Compressed Level" || len(doc.Platforms) != 2 {
		t.Errorf("unexpected decompressed doc: %+v", doc)
	}
}

func TestBrawlpitLevelsPublicHandler_ExportNotFound(t *testing.T) {
	_, publicH := newBrawlpitLevelsPublicTestHandler(t)

	rec := httptest.NewRecorder()
	publicH.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/brawlpit-levels/999/export", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for a nonexistent level, got %d", rec.Code)
	}
}
