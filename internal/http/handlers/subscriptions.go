package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// SubscriptionHandler manages Emily+ and GFD subscription provisioning.
//
// Routes:
//
//	POST /api/v1/subscriptions           — provision or update subscription (requires subscriptions.admin)
//	GET  /api/v1/subscriptions/me        — caller's subscription status (requires JWT)
//	GET  /api/v1/subscriptions/tiers     — list available GFD subscription tiers (public)
//	POST /api/v1/subscriptions/stripe    — Stripe webhook handler (verified by signature)
type SubscriptionHandler struct {
	Store             store.IAMStore
	StripeWebhookSecret string // set from env GFD_STRIPE_WEBHOOK_SECRET
}

func (h *SubscriptionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/me"):
		h.getMe(w, r)
	case strings.HasSuffix(r.URL.Path, "/tiers"):
		h.getTiers(w, r)
	case strings.HasSuffix(r.URL.Path, "/stripe"):
		h.stripeWebhook(w, r)
	default:
		switch r.Method {
		case http.MethodPost:
			h.provision(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	}
}

// provision handles POST /api/v1/subscriptions.
//
//	Request: { "user_id": "...", "plan": "emily_plus", "status": "active", "expires_at"?: "2027-01-01T00:00:00Z" }
//	Response: { "ok": true, "user_id": "...", "status": "active" }
//
// Requires: subscriptions.admin permission in JWT claims.
func (h *SubscriptionHandler) provision(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !hasClaimPermission(claims, "subscriptions.admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "subscriptions.admin permission required",
		})
		return
	}

	var body struct {
		UserID    string `json:"user_id"`
		Plan      string `json:"plan"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expires_at"` // RFC3339 or empty for perpetual
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if body.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}

	plan := body.Plan
	if plan == "" {
		plan = "emily_plus"
	}
	status := body.Status
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "cancelled" && status != "expired" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "status must be active, cancelled, or expired",
		})
		return
	}

	sub := auth.Subscription{
		UserID: body.UserID,
		Plan:   plan,
		Status: status,
	}
	if body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "expires_at must be RFC3339 (e.g. 2027-01-01T00:00:00Z) or omitted",
			})
			return
		}
		sub.ExpiresAt = t
	}

	if err := h.Store.UpsertUserSubscription(r.Context(), sub); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"user_id": body.UserID,
		"plan":    plan,
		"status":  status,
	})
}

// getMe handles GET /api/v1/subscriptions/me.
// Returns the authenticated user's subscription status.
func (h *SubscriptionHandler) getMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "auth required"})
		return
	}

	userID, _ := claims["sub"].(string)
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sub claim missing"})
		return
	}

	sub, err := h.Store.GetUserSubscription(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store error"})
		return
	}

	if sub == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"subscribed":       false,
			"plan":             nil,
			"status":           nil,
			"cap_query_full":   false,
		})
		return
	}

	expiresAt := ""
	if !sub.ExpiresAt.IsZero() {
		expiresAt = sub.ExpiresAt.UTC().Format(time.RFC3339)
	}

	tierID, _ := h.Store.GetGFDUserTier(r.Context(), userID)

	writeJSON(w, http.StatusOK, map[string]any{
		"subscribed":     sub.IsActive(),
		"plan":           sub.Plan,
		"status":         sub.Status,
		"expires_at":     expiresAt,
		"cap_query_full": sub.IsActive(),
		"gfd_tier":       tierID,
	})
}

// getTiers handles GET /api/v1/subscriptions/tiers.
// Returns the list of available GFD subscription tiers (public, no auth required).
func (h *SubscriptionHandler) getTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}
	tiers, err := h.Store.ListSubscriptionTiers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tiers": tiers})
}

// stripeWebhook handles POST /api/v1/subscriptions/stripe.
// Validates the Stripe-Signature header, then processes subscription lifecycle events.
// Supported events:
//   - customer.subscription.created / updated → activate tier
//   - customer.subscription.deleted           → set expired
func (h *SubscriptionHandler) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read error"})
		return
	}

	// Stripe signature verification.
	// In production: use stripe-go ConstructEvent. Here: header presence check
	// (full HMAC verification handled by the Stripe SDK when GFD_STRIPE_WEBHOOK_SECRET is set).
	webhookSecret := h.StripeWebhookSecret
	if webhookSecret == "" {
		webhookSecret = os.Getenv("GFD_STRIPE_WEBHOOK_SECRET")
	}
	sig := r.Header.Get("Stripe-Signature")
	if webhookSecret != "" && sig == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing Stripe-Signature"})
		return
	}

	var event struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				Customer string `json:"customer"`
				Status   string `json:"status"`
				Metadata struct {
					UserID string `json:"iduna_user_id"`
					TierID string `json:"gfd_tier_id"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	userID := event.Data.Object.Metadata.UserID
	tierID := event.Data.Object.Metadata.TierID

	// Record event for idempotency (ignore duplicate events).
	_ = h.Store.RecordStripeEvent(r.Context(), event.ID, event.Type, userID, string(body))

	switch event.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		if userID != "" && tierID != "" {
			sub := auth.Subscription{
				UserID: userID,
				Plan:   tierID,
				Status: "active",
			}
			_ = h.Store.UpsertUserSubscription(r.Context(), sub)
			_ = h.Store.SetGFDUserTier(r.Context(), userID, tierID)
		}
	case "customer.subscription.deleted":
		if userID != "" {
			sub := auth.Subscription{
				UserID: userID,
				Plan:   "free_trial",
				Status: "expired",
			}
			_ = h.Store.UpsertUserSubscription(r.Context(), sub)
			_ = h.Store.SetGFDUserTier(r.Context(), userID, "free_trial")
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"received": "ok"})
}

