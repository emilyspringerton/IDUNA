package handlers_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
)

func newInventoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE characters (
			character_id TEXT PRIMARY KEY,
			player_id    TEXT NOT NULL,
			name         TEXT NOT NULL,
			scene_id     INTEGER NOT NULL DEFAULT 1,
			pos_x        REAL NOT NULL DEFAULT 0,
			pos_y        REAL NOT NULL DEFAULT 0,
			pos_z        REAL NOT NULL DEFAULT 0,
			gold_balance INTEGER NOT NULL DEFAULT 0,
			level        INTEGER NOT NULL DEFAULT 1,
			current_xp   INTEGER NOT NULL DEFAULT 0,
			job_main     TEXT NOT NULL DEFAULT 'WAR',
			job_sub      TEXT NOT NULL DEFAULT '',
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		);
		CREATE TABLE items (
			item_id            TEXT PRIMARY KEY,
			owner_character_id TEXT,
			item_type          TEXT NOT NULL DEFAULT '',
			name               TEXT NOT NULL DEFAULT '',
			item_level         INTEGER NOT NULL DEFAULT 0,
			quantity           INTEGER NOT NULL DEFAULT 1,
			provenance_chain   TEXT NOT NULL DEFAULT '[]',
			def_id             INTEGER NOT NULL DEFAULT 0,
			flags              INTEGER NOT NULL DEFAULT 0,
			created_at         TEXT NOT NULL,
			updated_at         TEXT NOT NULL,
			destroyed_at       TEXT
		);
		CREATE TABLE character_inventory (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			character_id TEXT NOT NULL,
			bag          TEXT NOT NULL,
			slot_index   INTEGER NOT NULL,
			item_id      TEXT NOT NULL,
			def_id       INTEGER NOT NULL,
			quantity     INTEGER NOT NULL DEFAULT 1,
			UNIQUE(character_id, bag, slot_index)
		);
		CREATE TABLE character_equipment (
			character_id TEXT NOT NULL,
			slot         TEXT NOT NULL,
			item_id      TEXT,
			PRIMARY KEY (character_id, slot)
		);
		CREATE TABLE character_bag_capacity (
			character_id TEXT NOT NULL,
			bag          TEXT NOT NULL,
			capacity     INTEGER NOT NULL DEFAULT 30,
			PRIMARY KEY (character_id, bag)
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return db
}

func seedCharacterForInv(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO characters (character_id, player_id, name, created_at, updated_at) VALUES (?,?,?,'now','now')`,
		id, "player-1", "Tester",
	)
	if err != nil {
		t.Fatalf("seed character: %v", err)
	}
}

func TestGetInventoryEmpty(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-1")

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-1/inventory", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bags     map[string][]interface{} `json:"bags"`
		Capacity map[string]int           `json:"capacity"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Bags["inventory"]) != 0 {
		t.Errorf("expected empty inventory bag, got %d items", len(resp.Bags["inventory"]))
	}
	if resp.Capacity["inventory"] != 30 {
		t.Errorf("expected default capacity 30, got %d", resp.Capacity["inventory"])
	}
}

func TestGetInventoryWithItems(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-2")

	db.Exec(`INSERT INTO items (item_id,owner_character_id,name,created_at,updated_at) VALUES ('item-1','char-2','Iron Sword','now','now')`)
	db.Exec(`INSERT INTO character_inventory (character_id,bag,slot_index,item_id,def_id,quantity) VALUES ('char-2','inventory',0,'item-1',5,1)`)
	db.Exec(`INSERT INTO character_bag_capacity (character_id,bag,capacity) VALUES ('char-2','inventory',40)`)

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-2/inventory", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]json.RawMessage
	json.NewDecoder(rec.Body).Decode(&resp)

	var bags map[string][]map[string]interface{}
	json.Unmarshal(resp["bags"], &bags)
	if len(bags["inventory"]) != 1 {
		t.Errorf("expected 1 inventory item, got %d", len(bags["inventory"]))
	}
	if bags["inventory"][0]["item_id"] != "item-1" {
		t.Errorf("expected item-1, got %v", bags["inventory"][0]["item_id"])
	}

	var cap map[string]int
	json.Unmarshal(resp["capacity"], &cap)
	if cap["inventory"] != 40 {
		t.Errorf("expected capacity 40, got %d", cap["inventory"])
	}
}

func TestGetEquipmentEmpty(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-3")

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-3/equipment", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Equipment []interface{} `json:"equipment"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Equipment) != 0 {
		t.Errorf("expected empty equipment, got %d", len(resp.Equipment))
	}
}

func TestGetEquipmentWithSlots(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-4")

	db.Exec(`INSERT INTO items (item_id,owner_character_id,name,created_at,updated_at) VALUES ('sword-1','char-4','Sword','now','now')`)
	db.Exec(`INSERT INTO character_equipment (character_id,slot,item_id) VALUES ('char-4','main_hand','sword-1')`)
	db.Exec(`INSERT INTO character_equipment (character_id,slot,item_id) VALUES ('char-4','head',NULL)`)

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-4/equipment", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Equipment []map[string]interface{} `json:"equipment"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Equipment) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(resp.Equipment))
	}
	found := false
	for _, s := range resp.Equipment {
		if s["slot"] == "main_hand" && s["item_id"] == "sword-1" {
			found = true
		}
	}
	if !found {
		t.Error("expected main_hand slot with sword-1")
	}
}
