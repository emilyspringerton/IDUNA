package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// SubscriptionHandler manages Emily+ subscription provisioning.
//
// Routes:
//
//	POST /api/v1/subscriptions        — provision or update subscription (requires subscriptions.admin)
//	GET  /api/v1/subscriptions/me     — get caller's own subscription status (requires iduna.me.read)
type SubscriptionHandler struct {
	Store store.IAMStore
}

func (h *SubscriptionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/me") {
		h.getMe(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.provision(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
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

	writeJSON(w, http.StatusOK, map[string]any{
		"subscribed":     sub.IsActive(),
		"plan":           sub.Plan,
		"status":         sub.Status,
		"expires_at":     expiresAt,
		"cap_query_full": sub.IsActive(),
	})
}

