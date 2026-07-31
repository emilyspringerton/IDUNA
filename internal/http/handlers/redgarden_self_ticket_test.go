package handlers_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

func redgardenSelfTicketHandlerWithAuth(keys *jwt.Keys, db *sql.DB, secret []byte) http.Handler {
	h := &handlers.RedgardenSelfTicketHandler{DB: db, Secret: secret}
	return middleware.RequireAuth(keys)(h)
}

func TestRedgardenSelfTicket_MintsValidTicketForOwnPlayerID(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("test-shared-secret")
	db := newTestCharactersDB(t)
	playerID := uuid.New()
	insertCharacter(t, db, uuid.New().String(), playerID.String())

	// The caller's own JWT subject is the player_id -- no request body needed, unlike
	// RedgardenPlayerTicketHandler (which mints on behalf of an agent-supplied player_id).
	token := makeAgentToken(t, keys, playerID.String(), nil)
	h := redgardenSelfTicketHandlerWithAuth(keys, db, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
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
		t.Fatalf("ticket length = %d, want 36", len(raw))
	}
	payload := raw[:20]
	gotMAC := raw[20:36]

	var gotID uuid.UUID
	copy(gotID[:], payload[:16])
	if gotID != playerID {
		t.Errorf("ticket player_id bytes = %s, want %s", gotID, playerID)
	}

	gotExpiry := binary.LittleEndian.Uint32(payload[16:20])
	wantExpiryWindow := time.Now().Add(handlers.RedgardenTicketTTL)
	if int64(gotExpiry) > wantExpiryWindow.Unix()+5 || int64(gotExpiry) < wantExpiryWindow.Unix()-5 {
		t.Errorf("ticket expiry %d not within 5s of expected TTL window %d", gotExpiry, wantExpiryWindow.Unix())
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	wantMAC := mac.Sum(nil)[:16]
	if !hmac.Equal(gotMAC, wantMAC) {
		t.Errorf("ticket MAC does not match independently recomputed HMAC-SHA256(secret, payload)")
	}
}

func TestRedgardenSelfTicket_RejectsPlayerWithNoCharacter(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	playerID := uuid.New()

	token := makeAgentToken(t, keys, playerID.String(), nil)
	h := redgardenSelfTicketHandlerWithAuth(keys, db, []byte("secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no registered DragonsNShit character)", rec.Code)
	}
}

func TestRedgardenSelfTicket_CannotMintForAnotherPlayer(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	callerID := uuid.New()
	otherPlayerID := uuid.New()
	// A character exists, but only for otherPlayerID, not the caller -- proves the handler
	// never reads a player_id from anywhere but the caller's own JWT subject.
	insertCharacter(t, db, uuid.New().String(), otherPlayerID.String())

	token := makeAgentToken(t, keys, callerID.String(), nil)
	h := redgardenSelfTicketHandlerWithAuth(keys, db, []byte("secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (caller has no character even though otherPlayerID does)", rec.Code)
	}
}

func TestRedgardenSelfTicket_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	h := redgardenSelfTicketHandlerWithAuth(keys, db, []byte("secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token presented)", rec.Code)
	}
}

func TestRedgardenSelfTicket_RejectsNonUUIDSubject(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	token := makeAgentToken(t, keys, "EMILY_PRIME", nil) // agent-style subject, not a player UUID

	h := redgardenSelfTicketHandlerWithAuth(keys, db, []byte("secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (non-UUID subject)", rec.Code)
	}
}

func TestRedgardenSelfTicket_RequiresSecretConfigured(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	playerID := uuid.New()
	insertCharacter(t, db, uuid.New().String(), playerID.String())

	token := makeAgentToken(t, keys, playerID.String(), nil)
	h := redgardenSelfTicketHandlerWithAuth(keys, db, nil) // no REDGARDEN_TICKET_SECRET set

	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (secret not configured)", rec.Code)
	}
}
