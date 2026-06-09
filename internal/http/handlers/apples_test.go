package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

// context is used by stub methods that satisfy the IAMStore interface.

// stubApplesStore wraps stubAgentStore and overrides Apples methods.
type stubApplesStore struct {
	apples   []auth.AppleRecord
	appendID int64
	appendErr error
	getErr    error
}

func (s *stubApplesStore) AppendApple(_ context.Context, a auth.AppleRecord) (int64, error) {
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	s.apples = append(s.apples, a)
	return s.appendID, nil
}

func (s *stubApplesStore) ListApples(_ context.Context, agentID, sourceRepo, appleType string, limit int) ([]auth.AppleRecord, error) {
	var out []auth.AppleRecord
	for _, a := range s.apples {
		if agentID != "" && a.AgentID != agentID {
			continue
		}
		if sourceRepo != "" && a.SourceRepo != sourceRepo {
			continue
		}
		if appleType != "" && a.AppleType != appleType {
			continue
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *stubApplesStore) GetApple(_ context.Context, id int64) (*auth.AppleRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for _, a := range s.apples {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, errors.New("not found")
}

// Unused IAMStore methods.
func (s *stubApplesStore) GetOrCreateUserByGoogleSubject(context.Context, string, string) (*auth.User, bool, error) {
	return nil, false, nil
}
func (s *stubApplesStore) GetUserByID(context.Context, string) (*auth.User, error)     { return nil, nil }
func (s *stubApplesStore) GetEffectivePermissions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (s *stubApplesStore) AppendIAMEvent(context.Context, string, string, string, string, []byte) error {
	return nil
}
func (s *stubApplesStore) UpdateUserStatus(context.Context, string, string, string) error { return nil }
func (s *stubApplesStore) ListUsers(context.Context, int) ([]auth.User, error)            { return nil, nil }
func (s *stubApplesStore) AssignRole(context.Context, string, string, string) error       { return nil }
func (s *stubApplesStore) RevokeRole(context.Context, string, string, string) error       { return nil }
func (s *stubApplesStore) ListRoles(context.Context) ([]auth.Role, error)                 { return nil, nil }
func (s *stubApplesStore) ListAgents(context.Context) ([]auth.Agent, error)               { return nil, nil }
func (s *stubApplesStore) CreateAgent(context.Context, string, string, string, string) (*auth.Agent, error) {
	return nil, nil
}
func (s *stubApplesStore) UpdateAgentStatus(context.Context, string, string, string) error { return nil }
func (s *stubApplesStore) ListIAMEvents(context.Context, int) ([]auth.IAMEvent, error) {
	return nil, nil
}
func (s *stubApplesStore) SetAgentCredential(context.Context, string, string, string) error {
	return nil
}
func (s *stubApplesStore) AuthenticateAgent(context.Context, string, string) (*auth.Agent, error) {
	return nil, nil
}
func (s *stubApplesStore) UpsertPushToken(context.Context, auth.PushToken) error { return nil }
func (s *stubApplesStore) GetPushToken(context.Context, string) (*auth.PushToken, error) {
	return nil, nil
}

func makeAgentToken(t *testing.T, keys *jwt.Keys, sub string, perms []string) string {
	t.Helper()
	token, err := jwt.Sign(keys, map[string]any{
		"sub":         sub,
		"permissions": perms,
		"iss":         "https://test.internal",
		"aud":         "farthq-ecosystem",
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func applesHandlerWithAuth(keys *jwt.Keys, store *stubApplesStore) http.Handler {
	h := &handlers.ApplesHandler{Store: store}
	return middleware.RequireAuth(keys)(h)
}

func TestApplesCreate_Success(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{appendID: 42}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.write"})
	h := applesHandlerWithAuth(keys, store)

	body, _ := json.Marshal(map[string]any{
		"source_repo": "iduna",
		"run_id":      "sha-abc123",
		"apple_type":  "improvement",
		"title":       "Apples implementation",
		"body":        "## Summary\n\nImplemented HQ-SPEC-IAM-096.",
		"metadata":    map[string]any{"gear": 1},
	})
	req := httptest.NewRequest("POST", "/api/v1/apples", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp["id"] != float64(42) {
		t.Errorf("id = %v, want 42", resp["id"])
	}
	if resp["recorded_at"] == "" {
		t.Error("expected recorded_at in response")
	}
}

func TestApplesCreate_MissingPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"}) // read only, not write
	h := applesHandlerWithAuth(keys, store)

	body, _ := json.Marshal(map[string]any{
		"source_repo": "iduna", "run_id": "x", "apple_type": "improvement",
		"title": "t", "body": "b",
	})
	req := httptest.NewRequest("POST", "/api/v1/apples", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestApplesCreate_MissingFields(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.write"})
	h := applesHandlerWithAuth(keys, store)

	body, _ := json.Marshal(map[string]any{"source_repo": "iduna"}) // missing required fields
	req := httptest.NewRequest("POST", "/api/v1/apples", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "BAD_REQUEST") {
		t.Errorf("body missing BAD_REQUEST: %s", rr.Body.String())
	}
}

func TestApplesCreate_NoAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{}
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("POST", "/api/v1/apples", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestApplesList_Success(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{
		apples: []auth.AppleRecord{
			{ID: 1, AgentID: "a1", SourceRepo: "iduna", RunID: "r1", AppleType: "improvement", Title: "T1", RecordedAt: time.Now()},
			{ID: 2, AgentID: "a1", SourceRepo: "emily", RunID: "r2", AppleType: "observation", Title: "T2", RecordedAt: time.Now()},
		},
	}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	apples, ok := resp["apples"].([]any)
	if !ok {
		t.Fatalf("expected apples array, got %T", resp["apples"])
	}
	if len(apples) != 2 {
		t.Errorf("len(apples) = %d, want 2", len(apples))
	}
}

func TestApplesList_FilterByRepo(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{
		apples: []auth.AppleRecord{
			{ID: 1, AgentID: "a1", SourceRepo: "iduna", RunID: "r1", AppleType: "improvement", Title: "T1", RecordedAt: time.Now()},
			{ID: 2, AgentID: "a1", SourceRepo: "emily", RunID: "r2", AppleType: "observation", Title: "T2", RecordedAt: time.Now()},
		},
	}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples?source_repo=iduna", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	apples := resp["apples"].([]any)
	if len(apples) != 1 {
		t.Errorf("len(apples) = %d, want 1 (filtered by source_repo=iduna)", len(apples))
	}
}

func TestApplesGet_Success(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{
		apples: []auth.AppleRecord{
			{ID: 7, AgentID: "a1", SourceRepo: "iduna", RunID: "r1", AppleType: "improvement",
				Title: "T1", Body: "## body", Metadata: []byte(`{"gear":1}`), RecordedAt: time.Now()},
		},
	}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples/7", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["body"] != "## body" {
		t.Errorf("body = %q, want %q", resp["body"], "## body")
	}
	if resp["metadata"] == nil {
		t.Error("expected metadata in response")
	}
}

func TestApplesGet_NotFound(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{getErr: errors.New("not found")}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples/999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestApplesGet_InvalidID(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{}
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.read"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples/notanumber", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestApplesAdminPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubApplesStore{}
	// apples.admin should be able to list
	token := makeAgentToken(t, keys, "agent-1", []string{"apples.admin"})
	h := applesHandlerWithAuth(keys, store)

	req := httptest.NewRequest("GET", "/api/v1/apples", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (apples.admin should be able to list)", rr.Code)
	}
}
