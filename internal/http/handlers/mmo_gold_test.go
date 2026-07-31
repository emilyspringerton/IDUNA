package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/http/handlers"
)

func TestCreditGoldSuccess(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-gold-1")
	if _, err := db.Exec(`UPDATE characters SET gold_balance = 500 WHERE character_id = 'char-gold-1'`); err != nil {
		t.Fatalf("seed gold: %v", err)
	}

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]int{"credit": 200})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-gold-1/gold/credit", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var balance int
	if err := db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-gold-1'`).Scan(&balance); err != nil {
		t.Fatalf("query balance: %v", err)
	}
	if balance != 700 {
		t.Errorf("expected balance 700 (500 + 200), got %d", balance)
	}
}

func TestCreditGoldRejectsNonPositive(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-gold-2")

	h := &handlers.MMOHandler{DB: db}
	for _, credit := range []int{0, -50} {
		body, _ := json.Marshal(map[string]int{"credit": credit})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-gold-2/gold/credit", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("credit=%d: expected 400, got %d: %s", credit, rec.Code, rec.Body.String())
		}
	}
}

func TestCreditGoldRejectsOverCap(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-gold-3")

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]int{"credit": 10001}) // maxGoldCreditPerCall + 1
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-gold-3/gold/credit", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-cap credit, got %d: %s", rec.Code, rec.Body.String())
	}

	var balance int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-gold-3'`).Scan(&balance)
	if balance != 0 {
		t.Errorf("expected balance unchanged at 0 after a rejected over-cap credit, got %d", balance)
	}
}

func TestCreditGoldCharacterNotFound(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]int{"credit": 100})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/does-not-exist/gold/credit", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown character, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeductGoldStillWorksAlongsideCredit(t *testing.T) {
	// Regression guard: adding the credit endpoint's routing check must not shadow or break
	// the existing deduct endpoint's own route match.
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-gold-4")
	if _, err := db.Exec(`UPDATE characters SET gold_balance = 300 WHERE character_id = 'char-gold-4'`); err != nil {
		t.Fatalf("seed gold: %v", err)
	}

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]int{"deduct": 100})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/char-gold-4/gold", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	var balance int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id = 'char-gold-4'`).Scan(&balance)
	if balance != 200 {
		t.Errorf("expected balance 200 (300 - 100), got %d", balance)
	}
}
