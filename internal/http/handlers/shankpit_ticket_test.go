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

func ticketHandlerWithAuth(keys *jwt.Keys, secret []byte) http.Handler {
	h := &handlers.ShankpitTicketHandler{Secret: secret, Game: "shankpit"}
	return middleware.RequireAuth(keys)(h)
}

// TestShankpitTicket_RejectsMismatchedGameClaim -- S241-01's real fix: an
// account scoped to a different game must not mint a SHANKPIT connect
// ticket -- the exact founder-named scenario ("make sure that account only
// for papercraft"), checked from the other direction.
func TestShankpitTicket_RejectsMismatchedGameClaim(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makePlayerTokenWithGame(t, keys, uuid.New().String(), "papercraft")

	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (game claim mismatch)", rec.Code)
	}
}

// TestShankpitTicket_AllowsAbsentGameClaim -- backward compatibility: every
// account that predates S241-01 keeps working with SHANKPIT exactly as
// before.
func TestShankpitTicket_AllowsAbsentGameClaim(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makePlayerTokenWithGame(t, keys, uuid.New().String(), "")

	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (absent game claim stays unscoped), body = %s", rec.Code, rec.Body.String())
	}
}

func TestShankpitTicket_MintsValidTicket(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("test-shared-secret")
	playerID := uuid.New()
	token := makeAgentToken(t, keys, playerID.String(), nil)

	h := ticketHandlerWithAuth(keys, secret)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
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
	wantExpiryWindow := time.Now().Add(handlers.ShankpitTicketTTL)
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

func TestShankpitTicket_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h := ticketHandlerWithAuth(keys, []byte("secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token presented)", rec.Code)
	}
}

func TestShankpitTicket_RejectsNonUUIDSubject(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, "EMILY_PRIME", nil) // agent-style subject, not a player UUID

	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (non-UUID subject)", rec.Code)
	}
}

func TestShankpitTicket_RequiresSecretConfigured(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	token := makeAgentToken(t, keys, uuid.New().String(), nil)

	h := ticketHandlerWithAuth(keys, nil) // no SHANKPIT_TICKET_SECRET set
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (secret not configured)", rec.Code)
	}
}

// gamePlayerToken is the token shape /api/v1/games/{game}/guest-register actually returns
// (game_online.go's playerToken): subject "<provider>:<uuid>", identity in the player_id claim.
func gamePlayerToken(t *testing.T, keys *jwt.Keys, playerID, game, provider string) string {
	t.Helper()
	tok, err := jwt.Sign(keys, map[string]any{
		"sub":       provider + ":" + playerID,
		"player_id": playerID,
		"game":      game,
		"iss":       "https://iam.farthq.internal",
		"aud":       "farthq-ecosystem",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign game player token: %v", err)
	}
	return tok
}

// TestShankpitTicket_MintsForGamePlayerToken -- the real Roblox 460 login path. A guest account is
// the only sign-in a Roblox player can perform (no OAuth and no password form inside the engine),
// and before this it minted a token this endpoint refused, because the handler only read `sub`
// while a game token's subject is "guest:<uuid>".
func TestShankpitTicket_MintsForGamePlayerToken(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	playerID := uuid.New().String()
	token := gamePlayerToken(t, keys, playerID, "shankpit", "guest")

	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a guest player token, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ticket   string `json:"ticket"`
		PlayerID string `json:"player_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The ticket must be for the CLAIM's player, not for the literal string "guest:<uuid>".
	if resp.PlayerID != playerID {
		t.Errorf("player_id = %q, want the claim's %q", resp.PlayerID, playerID)
	}
	raw, err := hex.DecodeString(resp.Ticket)
	if err != nil || len(raw) != 36 {
		t.Fatalf("ticket = %q (len %d) not 36 hex-encoded bytes: %v", resp.Ticket, len(raw), err)
	}
	var gotID uuid.UUID
	copy(gotID[:], raw[:16])
	if gotID.String() != playerID {
		t.Errorf("ticket payload uuid = %s, want %s", gotID, playerID)
	}
}

// TestShankpitTicket_RejectsLocalAccountSubject -- "local:7" is a real IDUNA account but not a
// player: /api/v1/auth/local's tokens must not buy a game seat.
func TestShankpitTicket_RejectsLocalAccountSubject(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	tok, err := jwt.Sign(keys, map[string]any{
		"sub": "local:7", "local_uid": 7, "iss": "https://iam.farthq.internal",
		"aud": "farthq-ecosystem", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-player account, body = %s", rec.Code, rec.Body.String())
	}
}

// TestShankpitTicket_PlayerIDClaimDisagreesWithNothing -- a provider-prefixed subject with NO
// player_id claim still resolves through its subject, so nothing already working breaks.
func TestShankpitTicket_ProviderPrefixedSubjectWithoutClaim(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	playerID := uuid.New().String()
	tok, err := jwt.Sign(keys, map[string]any{
		"sub": "email:" + playerID, "game": "shankpit",
		"iss": "https://iam.farthq.internal", "aud": "farthq-ecosystem",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	h := ticketHandlerWithAuth(keys, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/ticket", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an email-prefixed subject, body = %s", rec.Code, rec.Body.String())
	}
}
