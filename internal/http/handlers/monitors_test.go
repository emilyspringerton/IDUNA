package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

// stubMonitorsStore gives CreateMonitor/GetMonitorBySlug real in-memory
// behavior on top of stubApplesStore's no-op coverage of the rest of
// store.IAMStore -- everything monitors.go's create() actually needs for
// the slug get-or-create regression tests below.
type stubMonitorsStore struct {
	stubApplesStore
	monitors []auth.Monitor
	nextID   int64
}

func (s *stubMonitorsStore) CreateMonitor(_ context.Context, m auth.Monitor) (int64, error) {
	s.nextID++
	m.ID = s.nextID
	s.monitors = append(s.monitors, m)
	return m.ID, nil
}

func (s *stubMonitorsStore) GetMonitorBySlug(_ context.Context, slug string) (*auth.Monitor, error) {
	for i := range s.monitors {
		if s.monitors[i].Slug == slug {
			cp := s.monitors[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func monitorsHandlerWithAuth(keys *jwt.Keys, store *stubMonitorsStore) http.Handler {
	h := &handlers.MonitorsHandler{Store: store}
	return middleware.RequireAuth(keys)(h)
}

func monitorsCreateReq(keys *jwt.Keys, t *testing.T, body string) *http.Request {
	t.Helper()
	token := makeAgentToken(t, keys, "test-agent", []string{"monitors.create"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/monitors", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestMonitorsCreate_ClientSlug_FirstCallCreates(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubMonitorsStore{}
	h := monitorsHandlerWithAuth(keys, store)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, monitorsCreateReq(keys, t, `{"name":"Emily Prime cron","slug":"emily-prime-cron","kind":"cron"}`))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	if len(store.monitors) != 1 {
		t.Fatalf("want 1 monitor created, got %d", len(store.monitors))
	}
	if store.monitors[0].Slug != "emily-prime-cron" {
		t.Errorf("slug = %q, want the client-supplied one, not a random hex string", store.monitors[0].Slug)
	}
}

func TestMonitorsCreate_ClientSlug_SecondCallReturnsExisting(t *testing.T) {
	// This is the actual regression: EnsureCronMonitor posts the same slug
	// on every process startup, expecting idempotent get-or-create.
	keys, _ := jwt.GenerateKeys()
	store := &stubMonitorsStore{}
	h := monitorsHandlerWithAuth(keys, store)

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, monitorsCreateReq(keys, t, `{"name":"Emily Prime cron","slug":"emily-prime-cron","kind":"cron"}`))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201, body=%s", w1.Code, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, monitorsCreateReq(keys, t, `{"name":"Emily Prime cron","slug":"emily-prime-cron","kind":"cron"}`))
	if w2.Code != http.StatusOK {
		t.Fatalf("second call (same slug) status = %d, want 200 (existing monitor, not a new one), body=%s", w2.Code, w2.Body.String())
	}

	if len(store.monitors) != 1 {
		t.Fatalf("want still 1 monitor after a repeat create with the same slug, got %d (this is the exact duplication bug)", len(store.monitors))
	}

	var resp struct {
		Monitor auth.Monitor `json:"monitor"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Monitor.ID != store.monitors[0].ID {
		t.Errorf("second call should return the existing monitor's ID (%d), got %d", store.monitors[0].ID, resp.Monitor.ID)
	}
}

func TestMonitorsCreate_NoSlug_GeneratesRandomOne(t *testing.T) {
	// Callers that don't care about a specific slug (the general "create a
	// monitor" API path) keep the old random-slug behavior.
	keys, _ := jwt.GenerateKeys()
	store := &stubMonitorsStore{}
	h := monitorsHandlerWithAuth(keys, store)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, monitorsCreateReq(keys, t, `{"name":"Some other monitor"}`))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	if len(store.monitors) != 1 || store.monitors[0].Slug == "" {
		t.Fatalf("expected a generated slug, got monitors=%v", store.monitors)
	}
}

func TestMonitorsCreate_InvalidSlugRejected(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubMonitorsStore{}
	h := monitorsHandlerWithAuth(keys, store)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, monitorsCreateReq(keys, t, `{"name":"Bad slug test","slug":"Not A Valid Slug!"}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid slug, body=%s", w.Code, w.Body.String())
	}
	if len(store.monitors) != 0 {
		t.Errorf("should not have created a monitor with an invalid slug, got %d", len(store.monitors))
	}
}
