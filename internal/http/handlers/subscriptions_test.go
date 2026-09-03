package handlers_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func stripeWebhookSig(secret string, payload []byte, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + string(payload)))
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// TestSubscriptionStripeWebhook_NoSecretConfigured_FailsClosed -- the real, direct fix for the
// found gap: an unconfigured webhook secret used to mean "skip verification entirely, trust
// everything"; it must now mean "reject everything."
func TestSubscriptionStripeWebhook_NoSecretConfigured_FailsClosed(t *testing.T) {
	h := &handlers.SubscriptionHandler{Store: &stubSubStore{}}
	body := []byte(`{"id":"evt_1","type":"customer.subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/stripe", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no configured secret should fail closed (503), got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSubscriptionStripeWebhook_ForgedSignatureRejected -- THE real vulnerability this whole
// change closes: a forged/garbage Stripe-Signature header used to be accepted as long as it was
// non-empty. Confirms end to end, through the real HTTP handler, that a forged event granting
// an arbitrary user a subscription is rejected AND never actually applied to the store.
func TestSubscriptionStripeWebhook_ForgedSignatureRejected(t *testing.T) {
	store := &stubSubStore{}
	h := &handlers.SubscriptionHandler{Store: store, StripeWebhookSecret: "whsec_real"}
	body := []byte(`{"id":"evt_forged","type":"customer.subscription.created","data":{"object":{"metadata":{"iduna_user_id":"free-rider","gfd_tier_id":"emily_plus"}}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", "t=1700000000,v1=forged-not-a-real-hmac")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a forged signature should be rejected (401), got %d: %s", rr.Code, rr.Body.String())
	}
	if sub, _ := store.GetUserSubscription(context.Background(), "free-rider"); sub != nil {
		t.Fatal("the forged event must NOT have actually granted a subscription -- this is the real exploit this fix closes")
	}
}

// TestSubscriptionStripeWebhook_RealValidSignatureApplied -- the real, happy path: a genuinely,
// correctly signed event (as the real Stripe service would send) is accepted and actually
// applied to the store.
func TestSubscriptionStripeWebhook_RealValidSignatureApplied(t *testing.T) {
	store := &stubSubStore{}
	secret := "whsec_real"
	h := &handlers.SubscriptionHandler{Store: store, StripeWebhookSecret: secret}
	body := []byte(`{"id":"evt_real","type":"customer.subscription.created","data":{"object":{"metadata":{"iduna_user_id":"real-payer","gfd_tier_id":"emily_plus"}}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeWebhookSig(secret, body, time.Now().Unix()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("a real, correctly-signed event should be accepted, got %d: %s", rr.Code, rr.Body.String())
	}
	sub, _ := store.GetUserSubscription(context.Background(), "real-payer")
	if sub == nil || sub.Status != "active" {
		t.Fatalf("expected a real active subscription applied for real-payer, got %+v", sub)
	}
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
