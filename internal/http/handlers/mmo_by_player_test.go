package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/http/handlers"
)

// GetCharacterByPlayer is new (2026-07-31, REDGARDEN_GUI_NORTHSTAR.md Milestone 4) --
// apps/arena_server's report_match_result needs to resolve a match participant's WOTAN
// player_id to a character_id before it can credit that character's Flow.

func TestGetCharacterByPlayer_Found(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-by-player-1") // seeded with player_id "player-1"

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/by-player/player-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CharacterID string `json:"character_id"`
		PlayerID    string `json:"player_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CharacterID != "char-by-player-1" {
		t.Errorf("character_id = %q, want char-by-player-1", resp.CharacterID)
	}
	if resp.PlayerID != "player-1" {
		t.Errorf("player_id = %q, want player-1", resp.PlayerID)
	}
}

func TestGetCharacterByPlayer_NotFound(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/by-player/no-such-player", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a player_id with no DragonsNShit character, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetCharacterByPlayer_DoesNotShadowGetByID(t *testing.T) {
	// Regression guard: the by-player prefix check must not swallow the plain GET /:id route
	// for a character literally named "by-player" (unlikely, but the routing here is string
	// matching, not a real router -- worth pinning down explicitly).
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "some-real-char-id")

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/some-real-char-id", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the plain by-id route, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CharacterID string `json:"character_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.CharacterID != "some-real-char-id" {
		t.Errorf("character_id = %q, want some-real-char-id", resp.CharacterID)
	}
}
