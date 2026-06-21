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

type stubPushTokenStore struct {
	stubApplesStore
	tokens map[string]*auth.PushToken
	upsErr error
	getErr error
}

func (s *stubPushTokenStore) UpsertPushToken(_ context.Context, t auth.PushToken) error {
	if s.upsErr != nil {
		return s.upsErr
	}
	if s.tokens == nil {
		s.tokens = map[string]*auth.PushToken{}
	}
	now := time.Now()
	t.RegisteredAt = now
	t.LastUsedAt = now
	s.tokens[t.AgentName] = &t
	return nil
}

func (s *stubPushTokenStore) GetPushToken(_ context.Context, agentName string) (*auth.PushToken, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.tokens == nil {
		return nil, nil
	}
	return s.tokens[agentName], nil
}

func pushTokenHandlerWithAuth(keys *jwt.Keys, store *stubPushTokenStore) http.Handler {
	h := &handlers.PushTokensHandler{Store: store}
	mux := http.NewServeMux()
	h.Register(mux)
	return middleware.RequireAuth(keys)(mux)
}

func TestPushTokenUpsert(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubPushTokenStore{}
	h := pushTokenHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir-1", []string{"push_tokens.write"})
	body := `{"agent_name":"mjolnir-emily","platform":"android","fcm_token":"tok-abc123","fingerprint":"fp-xyz"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/push-tokens", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["agent_name"] != "mjolnir-emily" {
		t.Errorf("agent_name = %q, want mjolnir-emily", resp["agent_name"])
	}
	if store.tokens["mjolnir-emily"] == nil {
		t.Fatal("token not stored")
	}
	if store.tokens["mjolnir-emily"].FCMToken != "tok-abc123" {
		t.Errorf("fcm_token = %q, want tok-abc123", store.tokens["mjolnir-emily"].FCMToken)
	}
}

func TestPushTokenUpsertForbidden(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubPushTokenStore{}
	h := pushTokenHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "rogue", []string{"read.only"})
	body := `{"agent_name":"x","fcm_token":"y"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/push-tokens", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

func TestPushTokenUpsertMissingFields(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubPushTokenStore{}
	h := pushTokenHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "mjolnir-1", []string{"push_tokens.write"})
	body := `{"agent_name":"mjolnir-emily"}` // missing fcm_token
	req := httptest.NewRequest(http.MethodPost, "/api/v1/push-tokens", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400 for missing fcm_token, got %d", rr.Code)
	}
}

func TestPushTokenGet(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubPushTokenStore{
		tokens: map[string]*auth.PushToken{
			"mjolnir-emily": {
				AgentName:    "mjolnir-emily",
				Platform:     "android",
				FCMToken:     "tok-stored",
				RegisteredAt: time.Now(),
			},
		},
	}
	h := pushTokenHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "emily-prime", []string{"push_tokens.read"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/push-tokens/mjolnir-emily", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["fcm_token"] != "tok-stored" {
		t.Errorf("fcm_token = %v, want tok-stored", resp["fcm_token"])
	}
}

func TestPushTokenGetNotFound(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubPushTokenStore{}
	h := pushTokenHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "emily-prime", []string{"push_tokens.read"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/push-tokens/does-not-exist", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}
