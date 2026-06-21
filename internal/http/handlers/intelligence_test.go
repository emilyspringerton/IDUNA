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

type stubIntelStore struct {
	stubApplesStore
	obs    []auth.CameraObservation
	nextID int64
}

func (s *stubIntelStore) CreateCameraObservation(_ context.Context, o auth.CameraObservation) (int64, error) {
	s.nextID++
	o.ID = s.nextID
	o.Status = "pending"
	o.CreatedAt = time.Now()
	s.obs = append(s.obs, o)
	return o.ID, nil
}

func (s *stubIntelStore) ListCameraObservations(_ context.Context, agentName, status string, limit int) ([]auth.CameraObservation, error) {
	var out []auth.CameraObservation
	for _, o := range s.obs {
		if agentName != "" && o.AgentName != agentName {
			continue
		}
		if status != "" && o.Status != status {
			continue
		}
		out = append(out, o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *stubIntelStore) GetCameraObservation(_ context.Context, id int64) (*auth.CameraObservation, error) {
	for _, o := range s.obs {
		if o.ID == id {
			cp := o
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *stubIntelStore) UpdateCameraObservation(_ context.Context, id int64, analysis string, appleID int64, status string) error {
	for i := range s.obs {
		if s.obs[i].ID == id {
			s.obs[i].Analysis = analysis
			s.obs[i].AppleID = appleID
			s.obs[i].Status = status
			return nil
		}
	}
	return nil
}

func intelHandlerWithAuth(keys *jwt.Keys, store *stubIntelStore) http.Handler {
	h := &handlers.IntelligenceHandler{Store: store}
	mux := http.NewServeMux()
	h.Register(mux)
	return middleware.RequireAuth(keys)(mux)
}

func TestIntelObserveSuccess(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubIntelStore{}
	h := intelHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir-1", []string{"intelligence.observe"})
	body := `{"image_data":"aGVsbG8=","media_type":"image/jpeg","prompt":"what is this?"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intelligence/observe", bytes.NewBufferString(body))
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
	if resp["status"] != "pending" {
		t.Errorf("status = %q, want pending", resp["status"])
	}
	if len(store.obs) != 1 {
		t.Fatalf("expected 1 obs in store, got %d", len(store.obs))
	}
	if store.obs[0].AgentName != "mjolnir-1" {
		t.Errorf("agent_name = %q, want mjolnir-1", store.obs[0].AgentName)
	}
}

func TestIntelObserveForbidden(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubIntelStore{}
	h := intelHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "rogue", []string{"read.only"})
	body := `{"image_data":"aGVsbG8="}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intelligence/observe", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

func TestIntelObserveMissingImageData(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubIntelStore{}
	h := intelHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir-1", []string{"intelligence.observe"})
	body := `{"media_type":"image/jpeg"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intelligence/observe", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing image_data, got %d", rr.Code)
	}
}

func TestIntelListAndPatch(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubIntelStore{}
	// Pre-seed one observation for mjolnir-1.
	store.obs = []auth.CameraObservation{
		{ID: 1, AgentName: "mjolnir-1", ImageData: "abc", Status: "pending", CreatedAt: time.Now()},
	}
	h := intelHandlerWithAuth(keys, store)

	// List — owner can read own observations.
	token := makeAgentToken(t, keys, "mjolnir-1", []string{"intelligence.read"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/intelligence/observations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var listResp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&listResp)
	obs, _ := listResp["observations"].([]any)
	if len(obs) != 1 {
		t.Errorf("list: expected 1 obs, got %d", len(obs))
	}

	// Patch — emily-prime marks it done with analysis.
	patchToken := makeAgentToken(t, keys, "emily-prime", []string{"intelligence.observe"})
	patchBody := `{"analysis":"this is a picture of a cat","apple_id":42,"status":"done"}`
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/intelligence/observations/1", bytes.NewBufferString(patchBody))
	patchReq.Header.Set("Authorization", "Bearer "+patchToken)
	patchRR := httptest.NewRecorder()
	h.ServeHTTP(patchRR, patchReq)

	if patchRR.Code != http.StatusOK {
		t.Fatalf("patch: want 200, got %d: %s", patchRR.Code, patchRR.Body.String())
	}
	if store.obs[0].Analysis != "this is a picture of a cat" {
		t.Errorf("analysis = %q, want 'this is a picture of a cat'", store.obs[0].Analysis)
	}
	if store.obs[0].Status != "done" {
		t.Errorf("status = %q, want done", store.obs[0].Status)
	}
}
