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

// PushTokensHandler handles /api/v1/push-tokens routes.
//
//   POST /api/v1/push-tokens           — register or update an FCM token (push_tokens.write)
//   GET  /api/v1/push-tokens/{name}    — get the current token for agent_name (push_tokens.read)
type PushTokensHandler struct {
	Store store.IAMStore
}

func (h *PushTokensHandler) Register(mux *http.ServeMux) {
	mux.Handle("/api/v1/push-tokens", h)
	mux.Handle("/api/v1/push-tokens/", h)
}

func (h *PushTokensHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/push-tokens")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		if r.Method == http.MethodPost {
			h.upsert(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	// path is the agent_name
	if r.Method == http.MethodGet {
		h.get(w, r, path)
	} else {
		http.NotFound(w, r)
	}
}

// POST /api/v1/push-tokens
// Body: { "agent_name": "mjolnir-emily", "platform": "android", "fcm_token": "...", "fingerprint": "..." }
func (h *PushTokensHandler) upsert(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "push_tokens.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "push_tokens.write permission required",
		})
		return
	}

	var body struct {
		AgentName   string `json:"agent_name"`
		Platform    string `json:"platform"`
		FCMToken    string `json:"fcm_token"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "invalid JSON body",
		})
		return
	}
	if body.AgentName == "" || body.FCMToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "agent_name and fcm_token are required",
		})
		return
	}
	if body.Platform == "" {
		body.Platform = "android"
	}

	token := auth.PushToken{
		AgentName:   body.AgentName,
		Platform:    body.Platform,
		FCMToken:    body.FCMToken,
		Fingerprint: body.Fingerprint,
	}
	if err := h.Store.UpsertPushToken(r.Context(), token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to store push token",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent_name":    body.AgentName,
		"platform":      body.Platform,
		"registered_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// GET /api/v1/push-tokens/{agent_name}
func (h *PushTokensHandler) get(w http.ResponseWriter, r *http.Request, agentName string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "push_tokens.read") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "push_tokens.read permission required",
		})
		return
	}

	token, err := h.Store.GetPushToken(r.Context(), agentName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to retrieve push token",
		})
		return
	}
	if token == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"code":    "NOT_FOUND",
			"message": "no push token registered for " + agentName,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            token.ID,
		"agent_name":    token.AgentName,
		"platform":      token.Platform,
		"fcm_token":     token.FCMToken,
		"fingerprint":   token.Fingerprint,
		"registered_at": token.RegisteredAt.UTC().Format(time.RFC3339),
		"last_used_at":  token.LastUsedAt.UTC().Format(time.RFC3339),
	})
}
