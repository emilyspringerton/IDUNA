package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

// stubHeimdalStore embeds stubApplesStore (from apples_test.go) and overrides sprint methods.
type stubHeimdalStore struct {
	stubApplesStore
	sprints   []auth.SprintItem
	nextID    int64
	createErr error
}

func (s *stubHeimdalStore) CreateSprintItem(_ context.Context, item auth.SprintItem) (int64, error) {
	if s.createErr != nil {
		return 0, s.createErr
	}
	s.nextID++
	item.ID = s.nextID
	item.Status = "pending"
	item.CreatedAt = time.Now()
	s.sprints = append(s.sprints, item)
	return item.ID, nil
}

func (s *stubHeimdalStore) ListSprintItems(_ context.Context, agentName, status string, limit int) ([]auth.SprintItem, error) {
	var out []auth.SprintItem
	for _, item := range s.sprints {
		if agentName != "" && item.AgentName != agentName {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *stubHeimdalStore) GetSprintItem(_ context.Context, id int64) (*auth.SprintItem, error) {
	for _, item := range s.sprints {
		if item.ID == id {
			cp := item
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *stubHeimdalStore) UpdateSprintItem(_ context.Context, id int64, criteria, roadmapID, status string, appleID int64) error {
	for i := range s.sprints {
		if s.sprints[i].ID == id {
			if criteria != "" {
				s.sprints[i].CriteriaJSON = criteria
			}
			if roadmapID != "" {
				s.sprints[i].RoadmapID = roadmapID
			}
			if status != "" {
				s.sprints[i].Status = status
			}
			if appleID != 0 {
				s.sprints[i].AppleID = appleID
			}
			return nil
		}
	}
	return nil
}

func heimdalHandlerWithAuth(keys *jwt.Keys, store *stubHeimdalStore) http.Handler {
	h := &handlers.HeimdalHandler{Store: store}
	return middleware.RequireAuth(keys)(h)
}

func TestHeimdalSubmit(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubHeimdalStore{}
	h := heimdalHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir", []string{"heimdal.submit"})
	body := `{"requirement":"users should be able to filter apples by date range"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heimdal/sprints", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["id"] == nil {
		t.Errorf("response missing id: %v", resp)
	}
	if len(store.sprints) != 1 {
		t.Fatalf("expected 1 sprint in store, got %d", len(store.sprints))
	}
	if store.sprints[0].AgentName != "mjolnir" {
		t.Errorf("agent_name = %q, want mjolnir", store.sprints[0].AgentName)
	}
}

func TestHeimdalSubmitForbidden(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubHeimdalStore{}
	h := heimdalHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "rogue", []string{"read.only"})
	body := `{"requirement":"do something"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heimdal/sprints", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

func TestHeimdalSubmitEmptyRequirement(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubHeimdalStore{}
	h := heimdalHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir", []string{"heimdal.submit"})
	body := `{"requirement":"   "}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/heimdal/sprints", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400 for empty requirement, got %d", rr.Code)
	}
}

func TestHeimdalList(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubHeimdalStore{}
	store.sprints = []auth.SprintItem{
		{ID: 1, AgentName: "mjolnir", Requirement: "req A", Status: "pending"},
		{ID: 2, AgentName: "mjolnir", Requirement: "req B", Status: "queued"},
		{ID: 3, AgentName: "emily",   Requirement: "req C", Status: "pending"},
	}
	h := heimdalHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir", []string{"heimdal.submit"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/heimdal/sprints", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	items, _ := resp["sprints"].([]any)
	// Non-admin sees only own items (mjolnir has 2).
	if len(items) != 2 {
		t.Errorf("expected 2 items for mjolnir, got %d: resp=%v", len(items), resp)
	}
}

func TestHeimdalPatch(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubHeimdalStore{}
	store.sprints = []auth.SprintItem{
		{ID: 1, AgentName: "mjolnir", Requirement: "req A", Status: "pending"},
	}
	h := heimdalHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "emily-prime", []string{"heimdal.process"})
	body := `{"status":"queued","criteria_json":"[{\"ac\":\"do X\"}]","roadmap_id":"RSI-42"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/heimdal/sprints/1", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if store.sprints[0].Status != "queued" {
		t.Errorf("status = %q, want queued", store.sprints[0].Status)
	}
	if store.sprints[0].RoadmapID != "RSI-42" {
		t.Errorf("roadmap_id = %q, want RSI-42", store.sprints[0].RoadmapID)
	}
}
