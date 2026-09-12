package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/http/handlers"
)

// TestHandleGetCharacterByName_CaseInsensitiveMatch guards the real, founder-reported live gap
// (2026-09-12): "it should not allow the guest to login as EMILY thats my character on the ssh
// also Emily should be taken too." A caller asking about "emily" (or "EMILY", or "eMiLy") must
// find the same real character regardless of the exact case it was created with.
func TestHandleGetCharacterByName_CaseInsensitiveMatch(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-emily-1")
	if _, err := db.Exec(`UPDATE characters SET name = 'EMILY' WHERE character_id = 'char-emily-1'`); err != nil {
		t.Fatalf("set real name: %v", err)
	}

	h := &handlers.MMOHandler{DB: db}
	for _, tryName := range []string{"EMILY", "emily", "Emily", "eMiLy"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/by-name/"+tryName, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("by-name/%s: got %d, want 200: %s", tryName, rec.Code, rec.Body.String())
			continue
		}
		var got struct {
			CharacterID string `json:"character_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.CharacterID != "char-emily-1" {
			t.Errorf("by-name/%s resolved to %q, want char-emily-1", tryName, got.CharacterID)
		}
	}
}

func TestHandleGetCharacterByName_UnclaimedNameIs404(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/by-name/NobodyHasThis", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleCreateCharacter_RefusesCaseInsensitiveDuplicate guards the other real half of the
// same founder report: SQLite's own default BINARY collation makes the characters.name UNIQUE
// constraint case-SENSITIVE, so creating "Emily" when "EMILY" already exists would otherwise
// succeed at the DB level despite looking identical to a human.
func TestHandleCreateCharacter_RefusesCaseInsensitiveDuplicate(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-emily-2")
	if _, err := db.Exec(`UPDATE characters SET name = 'EMILY' WHERE character_id = 'char-emily-2'`); err != nil {
		t.Fatalf("set real name: %v", err)
	}

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"player_id": "player-2", "name": "Emily"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("creating \"Emily\" while \"EMILY\" exists: got %d, want 409: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM characters WHERE LOWER(name) = 'emily'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 character named emily (case-insensitive) to exist, got %d", count)
	}
}

func TestHandleCreateCharacter_DifferentNameStillSucceeds(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()

	h := &handlers.MMOHandler{DB: db}
	body, _ := json.Marshal(map[string]string{"player_id": "player-3", "name": "BrandNewName"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("got %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
