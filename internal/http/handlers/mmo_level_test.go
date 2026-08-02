package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func postLevel(t *testing.T, h http.Handler, token, characterID string, level, currentXP int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]int{"level": level, "current_xp": currentXP})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/"+characterID+"/level", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpdateLevel_AgentJWTSucceeds(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-lvl-1")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-2", "DRAGONSNSHIT-MUD")

	rec := postLevel(t, h, token, "char-lvl-1", 5, 340)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("agent JWT: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var level, xp int
	if err := db.QueryRow(`SELECT level, current_xp FROM characters WHERE character_id = 'char-lvl-1'`).Scan(&level, &xp); err != nil {
		t.Fatalf("query level: %v", err)
	}
	if level != 5 || xp != 340 {
		t.Errorf("expected level=5 current_xp=340, got level=%d current_xp=%d", level, xp)
	}
}

func TestUpdateLevel_PlayerJWTRejectedEvenForOwnCharacter(t *testing.T) {
	// Level/XP is a cheat vector no client should self-report -- unlike position, even the
	// OWNING player's own JWT must be rejected here, not just a non-owner's.
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-lvl-2") // player_id = "player-1"

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "player-1")

	rec := postLevel(t, h, token, "char-lvl-2", 99, 0)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("owning player JWT: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var level int
	db.QueryRow(`SELECT level FROM characters WHERE character_id = 'char-lvl-2'`).Scan(&level)
	if level != 1 {
		t.Errorf("expected level unchanged at 1 after a rejected update, got %d", level)
	}
}

func TestUpdateLevel_RejectsSubOneLevel(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-lvl-3")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-3", "DRAGONSNSHIT-MUD")

	rec := postLevel(t, h, token, "char-lvl-3", 0, 0)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("level=0: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLevel_CharacterNotFound(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-4", "DRAGONSNSHIT-MUD")

	rec := postLevel(t, h, token, "does-not-exist", 5, 0)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown character, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLevel_NoMiddlewareStillWorks(t *testing.T) {
	// Regression guard: existing callers that hit MMOHandler directly (no RequireAuth wrapping)
	// must keep working -- claims are nil in that case, and the agent-only check is a no-op
	// when there's nothing to check (same shape mmo_position_test.go's own equivalent test uses).
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-lvl-5")

	h := &handlers.MMOHandler{DB: db}
	rec := postLevel(t, h, "", "char-lvl-5", 7, 100)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}
