package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	googleverify "iduna/internal/auth/google"
	"iduna/internal/auth/jwt"
	"iduna/internal/store"
)

// GoogleAuthHandler handles POST /api/v1/auth/google.
type GoogleAuthHandler struct {
	GoogleClientID string
	Keys           *jwt.Keys
	Store          store.IAMStore
	Issuer         string
}

func (h *GoogleAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IDToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "id_token is required",
		})
		return
	}

	gClaims, err := googleverify.Verify(r.Context(), body.IDToken, h.GoogleClientID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":    "ID_TOKEN_INVALID",
			"message": err.Error(),
		})
		return
	}

	user, _, err := h.Store.GetOrCreateUserByGoogleSubject(r.Context(), gClaims.Sub, gClaims.Email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to resolve identity",
		})
		return
	}

	if user.Status == "SUSPENDED" || user.Status == "BANNED" {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "IDENTITY_SUSPENDED",
			"message": "identity is suspended or banned",
		})
		return
	}

	perms, err := h.Store.GetEffectivePermissions(r.Context(), user.IDString)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to resolve permissions",
		})
		return
	}

	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}

	exp := time.Now().UTC().Add(time.Hour)
	jwtClaims := map[string]any{
		"sub":         user.IDString,
		"email":       user.Email,
		"gamertag":    user.Handle,
		"roles":       user.Roles,
		"permissions": perms,
		"iss":         issuer,
		"aud":         "farthq-ecosystem",
		"exp":         exp.Unix(),
	}

	token, err := jwt.Sign(h.Keys, jwtClaims)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to sign token",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"token_type":   "Bearer",
		"access_token": token,
		"expires_in":   3600,
	})
}

// AgentAuthHandler handles POST /api/v1/auth/agent — machine-to-machine
// credential exchange (spec HQ-SPEC-IAM-095 §3.1).
//
// Request body: {"agent_name": "EMILY", "agent_secret": "<raw key>"}
// Response:     {"access_token": "<JWT>", "token_type": "Bearer", "expires_in": 3600}
//
// The agent must be ACTIVE and have a credential set (via SetAgentCredential).
// The returned JWT embeds the agent's effective permissions so downstream
// services can enforce capability-level access control without calling IDUNA.
type AgentAuthHandler struct {
	Keys   *jwt.Keys
	Store  store.IAMStore
	Issuer string
}

func (h *AgentAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		AgentName   string `json:"agent_name"`
		AgentSecret string `json:"agent_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AgentName == "" || body.AgentSecret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "agent_name and agent_secret are required",
		})
		return
	}

	agent, err := h.Store.AuthenticateAgent(r.Context(), body.AgentName, body.AgentSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":    "AGENT_AUTH_FAILED",
			"message": "invalid agent credentials",
		})
		return
	}

	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}
	exp := time.Now().UTC().Add(time.Hour)
	jwtClaims := map[string]any{
		"sub":         agent.ID,
		"agent_name":  agent.Name,
		"agent_type":  agent.Type,
		"permissions": agent.Permissions,
		"iss":         issuer,
		"aud":         "farthq-ecosystem",
		"exp":         exp.Unix(),
	}
	token, err := jwt.Sign(h.Keys, jwtClaims)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to sign token",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"token_type":   "Bearer",
		"access_token": token,
		"expires_in":   3600,
	})
}
