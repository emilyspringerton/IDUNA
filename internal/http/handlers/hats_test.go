package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func seedHat(t *testing.T, db *sql.DB, hatID, name string, flowCost int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO hats (hat_id, name, description, flow_cost, image_asset) VALUES (?, ?, '', ?, '')`,
		hatID, name, flowCost,
	); err != nil {
		t.Fatalf("seed hat: %v", err)
	}
}

func TestListHatCatalog(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedHat(t, db, "hat-1", "Top Hat", 250)
	seedHat(t, db, "hat-2", "Joystick Cap", 150)

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 hats, got %d", len(out))
	}
	// ORDER BY flow_cost ASC -- cheaper hat first.
	if out[0]["hat_id"] != "hat-2" {
		t.Errorf("expected cheapest hat (hat-2) first, got %v", out[0]["hat_id"])
	}
}

func TestBuyHat_Success(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-1")
	seedHat(t, db, "hat-1", "Top Hat", 250)
	if _, err := db.Exec(`UPDATE characters SET gold_balance = 500 WHERE character_id = 'char-buy-1'`); err != nil {
		t.Fatalf("seed gold: %v", err)
	}

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-1/hats/buy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var balance int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-buy-1'`).Scan(&balance)
	if balance != 250 {
		t.Errorf("expected balance 250 (500-250), got %d", balance)
	}
	var owned int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE character_id='char-buy-1' AND hat_id='hat-1'`).Scan(&owned)
	if owned != 1 {
		t.Errorf("expected the hat to be recorded as owned, got count=%d", owned)
	}
}

func TestBuyHat_InsufficientFlow(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-2")
	seedHat(t, db, "hat-1", "Top Hat", 250)
	// gold_balance defaults to 0 -- not enough.

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-2/hats/buy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var owned int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE character_id='char-buy-2'`).Scan(&owned)
	if owned != 0 {
		t.Errorf("expected no hat granted on a rejected purchase, got count=%d", owned)
	}
}

func TestBuyHat_AlreadyOwned(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-3")
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`UPDATE characters SET gold_balance = 1000 WHERE character_id = 'char-buy-3'`)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES ('char-buy-3','hat-1','now',0)`)

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-3/hats/buy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for an already-owned hat, got %d: %s", rec.Code, rec.Body.String())
	}
	// Real, important assertion: the failed purchase's own Flow deduction must be rolled back
	// entirely (balance stays at the real starting 1000), not left half-applied -- the whole
	// point of wrapping the deduct+insert in one transaction.
	var balance int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-buy-3'`).Scan(&balance)
	if balance != 1000 {
		t.Errorf("expected the rejected duplicate purchase's own Flow deduction to roll back entirely (balance should stay 1000), got balance=%d", balance)
	}
}

func TestBuyHat_UnknownHat(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-4")

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "does-not-exist"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-4/hats/buy", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown hat, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListCharacterHats(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-owned-1")
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES ('char-owned-1','hat-1','2026-09-03T00:00:00Z',1)`)

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-owned-1/hats", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) != 1 {
		t.Fatalf("expected 1 owned hat, got %d", len(out))
	}
	if out[0]["equipped"] != true {
		t.Errorf("expected equipped=true, got %v", out[0]["equipped"])
	}
}

func TestEquipHat_SwapsExclusively(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-equip-1")
	seedHat(t, db, "hat-1", "Top Hat", 100)
	seedHat(t, db, "hat-2", "Joystick Cap", 150)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES ('char-equip-1','hat-1','now',1)`)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES ('char-equip-1','hat-2','now',0)`)

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "hat-2"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-equip-1/hats/equip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var equippedCount int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE character_id='char-equip-1' AND equipped=1`).Scan(&equippedCount)
	if equippedCount != 1 {
		t.Fatalf("expected exactly 1 equipped hat after a swap, got %d", equippedCount)
	}
	var equippedHat string
	db.QueryRow(`SELECT hat_id FROM character_hats WHERE character_id='char-equip-1' AND equipped=1`).Scan(&equippedHat)
	if equippedHat != "hat-2" {
		t.Errorf("expected hat-2 to be the equipped hat, got %s", equippedHat)
	}
}

// Real, found-live security gap fix (2026-09-04, WOTAN Phase 2 prep): handleBuyHat/
// handleEquipHat had no ownership check at all before this -- any authenticated player JWT
// could buy/equip against ANY character_id, spending someone else's own Flow. These tests
// mirror mmo_position_test.go's own already-established agent/owning-player/non-owning-player
// coverage for handleUpdatePosition's identical ownership-check pattern.

func TestBuyHat_NonOwningPlayerJWTRejected(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-5") // player_id = "player-1"
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`UPDATE characters SET gold_balance = 1000 WHERE character_id = 'char-buy-5'`)

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "some-other-player")

	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-5/hats/buy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owning player JWT: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	var balance int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-buy-5'`).Scan(&balance)
	if balance != 1000 {
		t.Errorf("expected no Flow spent on a rejected buy, balance should stay 1000, got %d", balance)
	}
}

func TestBuyHat_OwningPlayerJWTSucceeds(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-6") // player_id = "player-1"
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`UPDATE characters SET gold_balance = 1000 WHERE character_id = 'char-buy-6'`)

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "player-1")

	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-6/hats/buy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("owning player JWT: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBuyHat_AgentJWTCanBuyForAnyCharacter(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-buy-7") // player_id = "player-1"
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`UPDATE characters SET gold_balance = 1000 WHERE character_id = 'char-buy-7'`)

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-1", "DRAGONSNSHIT-MUD")

	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/char-buy-7/hats/buy", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("agent JWT: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEquipHat_NonOwningPlayerJWTRejected(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-equip-3") // player_id = "player-1"
	seedHat(t, db, "hat-1", "Top Hat", 100)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES ('char-equip-3','hat-1','now',0)`)

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "some-other-player")

	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-equip-3/hats/equip", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owning player JWT: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	var equipped int
	db.QueryRow(`SELECT equipped FROM character_hats WHERE character_id='char-equip-3' AND hat_id='hat-1'`).Scan(&equipped)
	if equipped != 0 {
		t.Errorf("expected the rejected equip to leave the hat unequipped, got equipped=%d", equipped)
	}
}

func TestEquipHat_NotOwned(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-equip-2")
	seedHat(t, db, "hat-1", "Top Hat", 100)

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"hat_id": "hat-1"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-equip-2/hats/equip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for equipping an unowned hat, got %d: %s", rec.Code, rec.Body.String())
	}
}
