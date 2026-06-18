package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/userlog"

	"golang.org/x/crypto/bcrypt"
)

// LocalAuthHandler handles POST /api/v1/auth/local.
// Accepts email + password, verifies against the local_users projection,
// and returns an ES256 JWT with uid, permissions, and sub=local:{uid}.
//
// Webmaster (uid=0) receives full admin permissions.
// All other local users receive the permissions associated with their status.
type LocalAuthHandler struct {
	Keys  *authjwt.Keys
	Proj  userlog.UserProjector
	Issuer string
}

type localAuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type localAuthResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Sub       string `json:"sub"`
	UID       int    `json:"uid"`
}

func (h *LocalAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req localAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password required"})
		return
	}

	user, err := h.Proj.GetByEmail(r.Context(), req.Email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil || user.Status == "deleted" || user.Status == "suspended" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}
	exp := time.Now().UTC().Add(8 * time.Hour)
	sub := "local:" + itoa(user.LocalUID)
	claims := map[string]any{
		"sub":         sub,
		"local_uid":   user.LocalUID,
		"email":       user.Email,
		"display_name": user.DisplayName,
		"permissions": localUserPermissions(user),
		"iss":         issuer,
		"aud":         "farthq-ecosystem",
		"exp":         exp.Unix(),
	}
	token, err := authjwt.Sign(h.Keys, claims)
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, localAuthResponse{
		Token:     token,
		ExpiresAt: exp.Unix(),
		Sub:       sub,
		UID:       user.LocalUID,
	})
}

// localUserPermissions returns the permission set for a local user.
// uid=0 (webmaster) gets full admin access.
func localUserPermissions(u *userlog.LocalUser) []string {
	if u.LocalUID == 0 {
		return []string{
			"iduna.admin",
			"iduna.me.read",
			"users.admin",
			"apples.read",
			"apples.write",
			"drive.read",
			"drive.write",
			"subscriptions.admin",
		}
	}
	return []string{"iduna.me.read", "users.read.self"}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
