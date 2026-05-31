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
