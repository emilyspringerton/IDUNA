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

type stubSubStore struct {
	stubApplesStore
	subs   map[string]*auth.Subscription
	upsErr error
}

func (s *stubSubStore) UpsertUserSubscription(_ context.Context, sub auth.Subscription) error {
	if s.upsErr != nil {
		return s.upsErr
	}
	if s.subs == nil {
		s.subs = map[string]*auth.Subscription{}
	}
	cp := sub
	s.subs[sub.UserID] = &cp
	return nil
}

func (s *stubSubStore) GetUserSubscription(_ context.Context, userID string) (*auth.Subscription, error) {
	if s.subs == nil {
		return nil, nil
	}
	return s.subs[userID], nil
}

func subHandlerWithAuth(keys *jwt.Keys, store *stubSubStore) http.Handler {
	h := &handlers.SubscriptionHandler{Store: store}
	return middleware.RequireAuth(keys)(h)
}

func TestSubscriptionProvision(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubSubStore{}
	h := subHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "billing-agent", []string{"subscriptions.admin"})
	body := `{"user_id":"user-123","plan":"emily_plus","status":"active","expires_at":"2027-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	if resp["user_id"] != "user-123" {
		t.Errorf("user_id = %q, want user-123", resp["user_id"])
	}
	if store.subs["user-123"] == nil {
		t.Fatal("subscription not stored")
	}
	if !store.subs["user-123"].ExpiresAt.IsZero() {
		// Verify the ExpiresAt round-trips correctly.
		want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		if !store.subs["user-123"].ExpiresAt.Equal(want) {
			t.Errorf("ExpiresAt = %v, want %v", store.subs["user-123"].ExpiresAt, want)
		}
	}
}

func TestSubscriptionProvisionForbidden(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubSubStore{}
	h := subHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "user-1", []string{"iduna.me.read"})
	body := `{"user_id":"user-999"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

func TestSubscriptionProvisionBadStatus(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubSubStore{}
	h := subHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "billing-agent", []string{"subscriptions.admin"})
	body := `{"user_id":"user-1","status":"invalid_status"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid status, got %d", rr.Code)
	}
}

func TestSubscriptionGetMeSubscribed(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubSubStore{
		subs: map[string]*auth.Subscription{
			"user-555": {
				UserID: "user-555",
				Plan:   "emily_plus",
				Status: "active",
			},
		},
	}
	h := subHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "user-555", []string{"iduna.me.read"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["subscribed"] != true {
		t.Errorf("subscribed = %v, want true", resp["subscribed"])
	}
	if resp["plan"] != "emily_plus" {
		t.Errorf("plan = %q, want emily_plus", resp["plan"])
	}
}

func TestSubscriptionGetMeNoSub(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	store := &stubSubStore{}
	h := subHandlerWithAuth(keys, store)

	token := makeAgentToken(t, keys, "user-not-subscribed", []string{"iduna.me.read"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp["subscribed"] != false {
		t.Errorf("subscribed = %v, want false for unsubscribed user", resp["subscribed"])
	}
}
