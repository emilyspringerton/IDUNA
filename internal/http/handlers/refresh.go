package handlers

import (
	"net/http"
	"strings"
	"time"

	authjwt "iduna/internal/auth/jwt"
)

// RefreshHandler handles POST /api/v1/auth/refresh.
//
// Accepts a valid, non-expired ES256 JWT in the Authorization header
// (Bearer <token>). Verifies the token against the IDUNA key set and
// issues a new 8-hour JWT with the same claims (all claims forwarded
// except exp and iat, which are reset).
type RefreshHandler struct {
	Keys   *authjwt.Keys
	Issuer string
}

type refreshResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

func (h *RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tokenStr := bearerToken(r)
	if tokenStr == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "MISSING_TOKEN",
			"message": "Authorization: Bearer <token> required",
		})
		return
	}

	claims, err := authjwt.Verify(h.Keys, tokenStr)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code":    "TOKEN_INVALID",
			"message": err.Error(),
		})
		return
	}

	// Build a new claims map copying all forwarded claims, then reset exp/iat.
	newClaims := make(map[string]any, len(claims)+2)
	for k, v := range claims {
		newClaims[k] = v
	}
	exp := time.Now().UTC().Add(8 * time.Hour)
	newClaims["exp"] = exp.Unix()
	newClaims["iat"] = time.Now().UTC().Unix()
	if h.Issuer != "" {
		newClaims["iss"] = h.Issuer
	}

	token, err := authjwt.Sign(h.Keys, newClaims)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"code":    "SIGN_FAILED",
			"message": "failed to sign token",
		})
		return
	}

	writeJSON(w, http.StatusOK, refreshResponse{
		Token:     token,
		ExpiresAt: exp.Unix(),
	})
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	if hdr == "" {
		return ""
	}
	const prefix = "bearer "
	if !strings.HasPrefix(strings.ToLower(hdr), prefix) {
		return ""
	}
	return strings.TrimSpace(hdr[len(prefix):])
}

