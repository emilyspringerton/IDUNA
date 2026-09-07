package handlers_test

import (
	"bytes"
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
			home_scene_id INTEGER NOT NULL DEFAULT 0,
			home_pos_x   REAL NOT NULL DEFAULT 0,
			home_pos_y   REAL NOT NULL DEFAULT 0,
			home_pos_z   REAL NOT NULL DEFAULT 0,
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
		CREATE TABLE hats (
			hat_id                    TEXT PRIMARY KEY,
			name                      TEXT NOT NULL,
			description               TEXT NOT NULL DEFAULT '',
			flow_cost                 INTEGER NOT NULL,
			image_asset               TEXT NOT NULL DEFAULT '',
			user_generated            INTEGER NOT NULL DEFAULT 0,
			generated_by_character_id TEXT,
			created_at                TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE character_hats (
			character_id TEXT NOT NULL,
			hat_id       TEXT NOT NULL,
			acquired_at  TEXT NOT NULL,
			equipped     INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (character_id, hat_id)
		);
		CREATE TABLE gfd_stackable_items (
			character_id TEXT NOT NULL,
			item_id      TEXT NOT NULL,
			quantity     INTEGER NOT NULL DEFAULT 0,
			updated_at   TEXT NOT NULL DEFAULT 'now',
			PRIMARY KEY (character_id, item_id)
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

// TestGetMaterialsEmpty -- S252-00's real starting state: a fresh character
// with no stackable items yet returns an empty map, not an error.
func TestGetMaterialsEmpty(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-mat-1")

	h := &handlers.MMOHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-mat-1/materials", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Materials map[string]int `json:"materials"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Materials) != 0 {
		t.Errorf("expected empty materials map, got %+v", resp.Materials)
	}
}

// TestSetMaterialsThenGet_Roundtrips -- the real S252-00/01 guarantee: a
// whole-map upsert followed by a GET returns exactly what was set.
func TestSetMaterialsThenGet_Roundtrips(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-mat-2")

	h := &handlers.MMOHandler{DB: db}
	putBody, _ := json.Marshal(map[string]any{"materials": map[string]int{"earth-crystal": 3, "worm-sinew": 1}})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/characters/char-mat-2/materials", bytes.NewReader(putBody))
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-mat-2/materials", nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	var resp struct {
		Materials map[string]int `json:"materials"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Materials["earth-crystal"] != 3 || resp.Materials["worm-sinew"] != 1 || len(resp.Materials) != 2 {
		t.Fatalf("unexpected materials after roundtrip: %+v", resp.Materials)
	}
}

// TestSetMaterials_ReplacesWholeMapAndDropsZeroes -- a second PUT with a
// different map must fully replace the first (not merge), and a
// zero-quantity entry (a fully-consumed material) must not linger as a row.
func TestSetMaterials_ReplacesWholeMapAndDropsZeroes(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-mat-3")

	h := &handlers.MMOHandler{DB: db}
	first, _ := json.Marshal(map[string]any{"materials": map[string]int{"earth-crystal": 5}})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/api/v1/characters/char-mat-3/materials", bytes.NewReader(first)))

	second, _ := json.Marshal(map[string]any{"materials": map[string]int{"worm-sinew": 2, "earth-crystal": 0}})
	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, httptest.NewRequest(http.MethodPut, "/api/v1/characters/char-mat-3/materials", bytes.NewReader(second)))
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/characters/char-mat-3/materials", nil))
	var resp struct {
		Materials map[string]int `json:"materials"`
	}
	json.NewDecoder(getRec.Body).Decode(&resp)
	if len(resp.Materials) != 1 || resp.Materials["worm-sinew"] != 2 {
		t.Fatalf("expected only worm-sinew=2 after replace, got %+v", resp.Materials)
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
