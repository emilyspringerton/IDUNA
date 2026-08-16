package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/statuspage"
)

func newTestStatusStore(t *testing.T) *statuspage.Store {
	t.Helper()
	store, err := statuspage.Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("statuspage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestStatusHistoryHandler_MissingTarget(t *testing.T) {
	h := &handlers.StatusHistoryHandler{Store: newTestStatusStore(t), Targets: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/history", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing target, got %d", rec.Code)
	}
}

func TestStatusHistoryHandler_UnknownTarget(t *testing.T) {
	h := &handlers.StatusHistoryHandler{
		Store:   newTestStatusStore(t),
		Targets: []statuspage.Target{{Name: "iduna", Label: "Trust & Identity API"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/history?target=nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown target, got %d", rec.Code)
	}
}

func TestStatusHistoryHandler_KnownTargetEnvelope(t *testing.T) {
	// store.record is unexported (checker_test.go, same package, exercises
	// it directly) — this test instead confirms the handler's response
	// envelope for a known target that hasn't been checked yet, the honest
	// "no history" state every fresh target starts in.
	store := newTestStatusStore(t)
	targets := []statuspage.Target{{Name: "iduna", Label: "Trust & Identity API"}}

	h := &handlers.StatusHistoryHandler{Store: store, Targets: targets}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/history?target=iduna&hours=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Target string `json:"target"`
		Label  string `json:"label"`
		Hours  int    `json:"hours"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Target != "iduna" || body.Label != "Trust & Identity API" || body.Hours != 1 {
		t.Fatalf("unexpected response envelope: %+v", body)
	}
}

func TestStatusHistoryHandler_HoursCappedAtMax(t *testing.T) {
	h := &handlers.StatusHistoryHandler{
		Store:   newTestStatusStore(t),
		Targets: []statuspage.Target{{Name: "iduna", Label: "x"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status/history?target=iduna&hours=99999", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body struct {
		Hours int `json:"hours"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Hours != 168 {
		t.Fatalf("expected hours capped at 168, got %d", body.Hours)
	}
}
