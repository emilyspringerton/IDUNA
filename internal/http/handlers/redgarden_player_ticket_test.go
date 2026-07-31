package handlers_test

import (
	"bytes"
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

func newTestCharactersDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE characters (
		character_id TEXT PRIMARY KEY,
		player_id    TEXT NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		job_main     TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create characters table: %v", err)
	}
	return db
}

func insertCharacter(t *testing.T, db *sql.DB, characterID, playerID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO characters (character_id, player_id, name, job_main) VALUES (?, ?, ?, ?)`,
		characterID, playerID, "test-warrior", "WAR")
	if err != nil {
		t.Fatalf("insert character: %v", err)
	}
}

func redgardenPlayerTicketHandlerWithAuth(keys *jwt.Keys, db *sql.DB, secret []byte) http.Handler {
	h := &handlers.RedgardenPlayerTicketHandler{DB: db, Secret: secret}
	return middleware.RequireAuth(keys)(middleware.RequirePermission("redgarden.player-ticket.mint")(h))
}

func TestRedgardenPlayerTicket_MintsValidTicketForRegisteredCharacter(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("test-shared-secret")
	db := newTestCharactersDB(t)
	playerID := uuid.New()
	insertCharacter(t, db, uuid.New().String(), playerID.String())

	token := makeAgentToken(t, keys, "DRAGONSNSHIT-MUD", []string{"redgarden.player-ticket.mint"})
	h := redgardenPlayerTicketHandlerWithAuth(keys, db, secret)

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/player-ticket", bytes.NewReader(body))
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
		t.Fatalf("ticket length = %d, want 36 (same wire format as RedgardenTicketHandler)", len(raw))
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

func TestRedgardenPlayerTicket_RejectsPlayerWithNoCharacter(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)

	token := makeAgentToken(t, keys, "DRAGONSNSHIT-MUD", []string{"redgarden.player-ticket.mint"})
	h := redgardenPlayerTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": uuid.New().String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/player-ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (player_id has no registered DragonsNShit character)", rec.Code)
	}
}

func TestRedgardenPlayerTicket_RequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	playerID := uuid.New()
	insertCharacter(t, db, uuid.New().String(), playerID.String())

	// No redgarden.player-ticket.mint permission on this token -- crucially, also NOT
	// redgarden.ticket.mint, proving this is a genuinely separate permission gate, not an
	// accidental alias of the bot-only one.
	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.ticket.mint"})
	h := redgardenPlayerTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/player-ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (REDGARDEN-BOTS' own redgarden.ticket.mint permission must not satisfy this gate)", rec.Code)
	}
}

func TestRedgardenPlayerTicket_RequiresSecretConfigured(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)
	playerID := uuid.New()
	insertCharacter(t, db, uuid.New().String(), playerID.String())

	token := makeAgentToken(t, keys, "DRAGONSNSHIT-MUD", []string{"redgarden.player-ticket.mint"})
	h := redgardenPlayerTicketHandlerWithAuth(keys, db, nil) // no REDGARDEN_TICKET_SECRET set

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/player-ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (secret not configured)", rec.Code)
	}
}

func TestRedgardenPlayerTicket_RejectsInvalidPlayerID(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestCharactersDB(t)

	token := makeAgentToken(t, keys, "DRAGONSNSHIT-MUD", []string{"redgarden.player-ticket.mint"})
	h := redgardenPlayerTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": "not-a-uuid"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/player-ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (player_id must be a valid UUID)", rec.Code)
	}
}
