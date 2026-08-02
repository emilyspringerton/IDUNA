package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func makePlayerToken(t *testing.T, keys *jwt.Keys, sub string) string {
	t.Helper()
	token, err := jwt.Sign(keys, map[string]any{
		"sub": sub,
		"iss": "https://test.internal",
		"aud": "farthq-ecosystem",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign player token: %v", err)
	}
	return token
}

func makeAgentTokenWithName(t *testing.T, keys *jwt.Keys, agentID, agentName string) string {
	t.Helper()
	token, err := jwt.Sign(keys, map[string]any{
		"sub":        agentID,
		"agent_name": agentName,
		"iss":        "https://test.internal",
		"aud":        "farthq-ecosystem",
		"exp":        time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign agent token: %v", err)
	}
	return token
}

func postPosition(t *testing.T, h http.Handler, token, characterID string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]float64{"pos_x": 5, "pos_y": 0, "pos_z": 9})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/"+characterID+"/position", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpdatePosition_AgentJWTCanMoveAnyCharacter(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-pos-1") // player_id = "player-1"

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-1", "GFD_MUD_AGENT")

	rec := postPosition(t, h, token, "char-pos-1")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("agent JWT: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdatePosition_OwningPlayerJWTSucceeds(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-pos-2") // player_id = "player-1"

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "player-1")

	rec := postPosition(t, h, token, "char-pos-2")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owning player JWT: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var x float64
	if err := db.QueryRow(`SELECT pos_x FROM characters WHERE character_id = 'char-pos-2'`).Scan(&x); err != nil {
		t.Fatalf("query pos_x: %v", err)
	}
	if x != 5 {
		t.Errorf("expected pos_x=5, got %v", x)
	}
}

func TestUpdatePosition_NonOwningPlayerJWTRejected(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-pos-3") // player_id = "player-1"

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "some-other-player")

	rec := postPosition(t, h, token, "char-pos-3")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owning player JWT: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var x float64
	db.QueryRow(`SELECT pos_x FROM characters WHERE character_id = 'char-pos-3'`).Scan(&x)
	if x != 0 {
		t.Errorf("expected pos_x unchanged at 0 after a rejected update, got %v", x)
	}
}

func TestUpdatePosition_NoMiddlewareStillWorks(t *testing.T) {
	// Regression guard: existing callers that hit MMOHandler directly (no RequireAuth wrapping,
	// e.g. the pre-existing gold tests in this same package) must keep working -- claims are nil
	// in that case, and the ownership check is a no-op when there's nothing to check.
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-pos-4")

	h := &handlers.MMOHandler{DB: db}
	rec := postPosition(t, h, "", "char-pos-4")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}
