package handlers_test

import (
	"testing"
	"time"

	"iduna/internal/auth/jwt"
)

// makePlayerTokenWithGame mints a JWT carrying an optional "game" claim
// (S241-01) -- game == "" omits the claim entirely, matching how
// PlayerEmailAuthHandler/ShankpitAuthHandler only set it when non-empty.
func makePlayerTokenWithGame(t *testing.T, keys *jwt.Keys, sub, game string) string {
	t.Helper()
	claims := map[string]any{
		"sub": sub,
		"iss": "https://test.internal",
		"aud": "shankpit",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	if game != "" {
		claims["game"] = game
	}
	token, err := jwt.Sign(keys, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}
