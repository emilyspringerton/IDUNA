package handlers_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

func racerTicketHandlerWithAuth(keys *jwt.Keys, secret []byte) http.Handler {
	h := &handlers.RacerTicketHandler{Secret: secret}
	return middleware.RequireAuth(keys)(h)
}

func TestRacerTicket_MintsValidTicket(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("test-racer-shared-secret")
	playerID := uuid.New()
	token := makeAgentToken(t, keys, playerID.String(), nil)

	h := racerTicketHandlerWithAuth(keys, secret)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/racer/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Ticket    string `json:"ticket"`
		ExpiresAt int64  `json:"expires_at"`
		PlayerID  string `json:"player_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PlayerID != playerID.String() {
		t.Errorf("player_id = %q, want %q", resp.PlayerID, playerID.String())
	}

	raw, err := hex.DecodeString(resp.Ticket)
	if err != nil {
		t.Fatalf("ticket not valid hex: %v", err)
	}
	if len(raw) != 36 {
		t.Fatalf("ticket length = %d, want 36 (16 player_id + 4 expiry + 16 truncated mac)", len(raw))
	}

	payload := raw[:20]
	gotMAC := raw[20:36]

	// player_id bytes must round-trip to the same UUID.
	var gotID uuid.UUID
	copy(gotID[:], payload[:16])
	if gotID != playerID {
		t.Errorf("ticket player_id bytes = %s, want %s", gotID, playerID)
	}

	gotExpiry := binary.LittleEndian.Uint32(payload[16:20])
	if int64(gotExpiry) != resp.ExpiresAt {
		t.Errorf("ticket expiry = %d, want %d (from JSON)", gotExpiry, resp.ExpiresAt)
	}
	wantExpiryWindow := time.Now().Add(handlers.RacerTicketTTL)
	if int64(gotExpiry) > wantExpiryWindow.Unix()+5 || int64(gotExpiry) < wantExpiryWindow.Unix()-5 {
		t.Errorf("ticket expiry %d not within 5s of expected TTL window %d", gotExpiry, wantExpiryWindow.Unix())
	}

	// Recompute the MAC independently and check it matches — proves the
	// handler actually signs with the configured secret, not a hardcoded
	// or wrong value.
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	wantMAC := mac.Sum(nil)[:16]
	if !hmac.Equal(gotMAC, wantMAC) {
		t.Errorf("ticket MAC does not match independently recomputed HMAC-SHA256(secret, payload)")
	}
}

func TestRacerTicket_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h := racerTicketHandlerWithAuth(keys, []byte("secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/racer/ticket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token presented)", rec.Code)
	}
}

func TestRacerTicket_RejectsNonUUIDSubject(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, "EMILY_PRIME", nil) // agent-style subject, not a player UUID

	h := racerTicketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/racer/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (non-UUID subject)", rec.Code)
	}
}

func TestRacerTicket_RequiresSecretConfigured(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, uuid.New().String(), nil)

	h := racerTicketHandlerWithAuth(keys, nil) // no RACER_TICKET_SECRET set
	req := httptest.NewRequest(http.MethodPost, "/api/v1/racer/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (secret not configured)", rec.Code)
	}
}
