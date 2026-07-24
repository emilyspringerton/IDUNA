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

func newTestRedgardenDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE players (
		player_id    TEXT PRIMARY KEY,
		display_name TEXT NOT NULL DEFAULT '',
		provider     TEXT NOT NULL DEFAULT '',
		provider_sub TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create players table: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE player_game_stats (
		player_id      TEXT NOT NULL,
		game           TEXT NOT NULL,
		wins           INTEGER NOT NULL DEFAULT 0,
		losses         INTEGER NOT NULL DEFAULT 0,
		matches_played INTEGER NOT NULL DEFAULT 0,
		last_played_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(player_id, game)
	)`)
	if err != nil {
		t.Fatalf("create player_game_stats table: %v", err)
	}
	return db
}

func insertPlayer(t *testing.T, db *sql.DB, playerID, provider string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub) VALUES (?, ?, ?, ?)`,
		playerID, "test-player", provider, "sub-"+playerID)
	if err != nil {
		t.Fatalf("insert player: %v", err)
	}
}

func redgardenTicketHandlerWithAuth(keys *jwt.Keys, db *sql.DB, secret []byte) http.Handler {
	h := &handlers.RedgardenTicketHandler{DB: db, Secret: secret}
	return middleware.RequireAuth(keys)(middleware.RequirePermission("redgarden.ticket.mint")(h))
}

func TestRedgardenTicket_MintsValidTicketForRegisteredBotPlayer(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("test-shared-secret")
	db := newTestRedgardenDB(t)
	playerID := uuid.New()
	insertPlayer(t, db, playerID.String(), "redgarden_bot")

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.ticket.mint"})
	h := redgardenTicketHandlerWithAuth(keys, db, secret)

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/ticket", bytes.NewReader(body))
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
		t.Fatalf("ticket length = %d, want 36 (matches shankpit's wire format, verified by the same C server code)", len(raw))
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

func TestRedgardenTicket_RejectsNonBotProvider(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New()
	insertPlayer(t, db, playerID.String(), "google") // a real human player, not a bot

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.ticket.mint"})
	h := redgardenTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (must not mint a ticket impersonating a real human player)", rec.Code)
	}
}

func TestRedgardenTicket_RejectsUnregisteredPlayer(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.ticket.mint"})
	h := redgardenTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": uuid.New().String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (player never registered)", rec.Code)
	}
}

func TestRedgardenTicket_RequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New()
	insertPlayer(t, db, playerID.String(), "redgarden_bot")

	// No redgarden.ticket.mint permission on this token.
	token := makeAgentToken(t, keys, "SOME-OTHER-AGENT", []string{"blog.write"})
	h := redgardenTicketHandlerWithAuth(keys, db, []byte("secret"))

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing redgarden.ticket.mint permission)", rec.Code)
	}
}

func TestRedgardenTicket_RequiresSecretConfigured(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New()
	insertPlayer(t, db, playerID.String(), "redgarden_bot")

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.ticket.mint"})
	h := redgardenTicketHandlerWithAuth(keys, db, nil) // no REDGARDEN_TICKET_SECRET set

	body, _ := json.Marshal(map[string]string{"player_id": playerID.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/ticket", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (secret not configured)", rec.Code)
	}
}

func redgardenResultHandlerWithAuth(keys *jwt.Keys, db *sql.DB) http.Handler {
	h := &handlers.RedgardenGameResultHandler{DB: db}
	return middleware.RequireAuth(keys)(middleware.RequirePermission("redgarden.match.write")(h))
}

func TestRedgardenGameResult_RecordsWinAndLoss(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New().String()
	insertPlayer(t, db, playerID, "redgarden_bot")

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenResultHandlerWithAuth(keys, db)

	post := func(result string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"player_id": playerID, "game": "redgarden", "result": result})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/game-result", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post("win")
	if rec.Code != http.StatusOK {
		t.Fatalf("first post (win) status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = post("loss")
	if rec.Code != http.StatusOK {
		t.Fatalf("second post (loss) status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Wins          int `json:"wins"`
		Losses        int `json:"losses"`
		MatchesPlayed int `json:"matches_played"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Wins != 1 || resp.Losses != 1 || resp.MatchesPlayed != 2 {
		t.Errorf("stats = %+v, want wins=1 losses=1 matches_played=2 (accumulated across both posts)", resp)
	}
}

func TestRedgardenGameResult_RequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New().String()
	insertPlayer(t, db, playerID, "redgarden_bot")

	token := makeAgentToken(t, keys, "SOME-OTHER-AGENT", nil) // no redgarden.match.write
	h := redgardenResultHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{"player_id": playerID, "game": "redgarden", "result": "win"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/game-result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (missing redgarden.match.write permission)", rec.Code)
	}
}

func TestRedgardenGameResult_RejectsInvalidResult(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestRedgardenDB(t)
	playerID := uuid.New().String()
	insertPlayer(t, db, playerID, "redgarden_bot")

	token := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	h := redgardenResultHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{"player_id": playerID, "game": "redgarden", "result": "draw"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/game-result", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (\"draw\" is not a valid result)", rec.Code)
	}
}

func TestRedgardenLeaderboard_OrdersByWins(t *testing.T) {
	db := newTestRedgardenDB(t)
	p1, p2 := uuid.New().String(), uuid.New().String()
	insertPlayer(t, db, p1, "redgarden_bot")
	insertPlayer(t, db, p2, "redgarden_bot")
	_, err := db.Exec(`INSERT INTO player_game_stats (player_id, game, wins, losses, matches_played) VALUES
		(?, 'redgarden', 2, 1, 3), (?, 'redgarden', 5, 0, 5)`, p1, p2)
	if err != nil {
		t.Fatalf("seed stats: %v", err)
	}

	h := &handlers.RedgardenLeaderboardHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/leaderboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Leaderboard []struct {
			PlayerID string `json:"player_id"`
			Wins     int    `json:"wins"`
		} `json:"leaderboard"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Leaderboard) != 2 {
		t.Fatalf("leaderboard len = %d, want 2", len(resp.Leaderboard))
	}
	if resp.Leaderboard[0].PlayerID != p2 {
		t.Errorf("leaderboard[0] = %s (wins=%d), want %s (5 wins) first", resp.Leaderboard[0].PlayerID, resp.Leaderboard[0].Wins, p2)
	}
}
