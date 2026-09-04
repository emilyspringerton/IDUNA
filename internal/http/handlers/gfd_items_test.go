package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"iduna/internal/http/handlers"
)

func newGfdItemsTestFile(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatalf("write test items.json: %v", err)
	}
	return path
}

func TestGfdItems_List(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":1,"name":"Willow Wand","category":"weapon","stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	req := httptest.NewRequest(http.MethodGet, "/admin/gfd-items/api/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var items []handlers.GfdItemDef
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Willow Wand" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestGfdItems_Create_AutoAssignsID(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":5,"name":"Dagger","category":"weapon","stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{"name": "Max Potion", "category": "consumable", "stack_size": 12})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-items/api/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created handlers.GfdItemDef
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID != 6 {
		t.Errorf("expected auto-assigned id 6 (one above existing max 5), got %d", created.ID)
	}

	// Real, direct confirmation the write actually landed in the file, not just the response.
	raw, _ := os.ReadFile(path)
	var all []handlers.GfdItemDef
	json.Unmarshal(raw, &all)
	if len(all) != 2 {
		t.Fatalf("expected 2 items persisted to disk, got %d", len(all))
	}
}

func TestGfdItems_Create_RejectsDuplicateID(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":1,"name":"Willow Wand","category":"weapon","stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{"id": 1, "name": "Duplicate", "category": "weapon"})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-items/api/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate id, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGfdItems_Create_RejectsInvalidCategory(t *testing.T) {
	path := newGfdItemsTestFile(t, `[]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{"name": "Mystery Box", "category": "not-a-real-category"})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-items/api/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid category, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGfdItems_Update(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":1,"name":"Willow Wand","category":"weapon","level":1,"stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{"name": "Willow Wand+1", "category": "weapon", "level": 5, "stack_size": 1})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-items/api/items/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	raw, _ := os.ReadFile(path)
	var all []handlers.GfdItemDef
	json.Unmarshal(raw, &all)
	if len(all) != 1 || all[0].Name != "Willow Wand+1" || all[0].Level != 5 {
		t.Fatalf("expected the row updated in place, got %+v", all)
	}
}

func TestGfdItems_Update_IgnoresBodyIDMismatch(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":1,"name":"Willow Wand","category":"weapon","stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	// Body claims id 99 -- the URL's id (1) must win, so this can't be used to silently rename
	// a different row's real identity.
	body, _ := json.Marshal(map[string]any{"id": 99, "name": "Renamed", "category": "weapon", "stack_size": 1})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-items/api/items/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	raw, _ := os.ReadFile(path)
	var all []handlers.GfdItemDef
	json.Unmarshal(raw, &all)
	if len(all) != 1 || all[0].ID != 1 {
		t.Fatalf("expected the row to keep id 1 (URL wins over body), got %+v", all)
	}
}

func TestGfdItems_Update_NotFound(t *testing.T) {
	path := newGfdItemsTestFile(t, `[]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{"name": "Ghost", "category": "weapon"})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-items/api/items/404", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGfdItems_Delete(t *testing.T) {
	path := newGfdItemsTestFile(t, `[{"id":1,"name":"Willow Wand","category":"weapon","stack_size":1},{"id":2,"name":"Dagger","category":"weapon","stack_size":1}]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-items/api/items/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	raw, _ := os.ReadFile(path)
	var all []handlers.GfdItemDef
	json.Unmarshal(raw, &all)
	if len(all) != 1 || all[0].ID != 2 {
		t.Fatalf("expected only id 2 to remain, got %+v", all)
	}
}

func TestGfdItems_Delete_NotFound(t *testing.T) {
	path := newGfdItemsTestFile(t, `[]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-items/api/items/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestGfdItems_RoundTripPreservesRealFieldsApps2MudExpects confirms the written JSON keeps the
// exact real field names server/itemdef.ItemDef expects (checked against that file directly) --
// a silent field-name drift here would mean apps2/mud's own itemdefReg.LoadFile parses every
// entry this page ever writes into a zero-valued ItemDef.
func TestGfdItems_RoundTripPreservesRealFieldsApps2MudExpects(t *testing.T) {
	path := newGfdItemsTestFile(t, `[]`)
	h := &handlers.GfdItemsHandler{ItemsJSONPath: path}

	body, _ := json.Marshal(map[string]any{
		"name": "Beginner Sword", "category": "weapon", "level": 1,
		"equip_slots": []string{"main"}, "jobs": []string{"WAR"},
		"stats": map[string]int{"attack": 5}, "model_id": "sword_beginner",
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-items/api/items", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	raw, _ := os.ReadFile(path)
	var generic []map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode raw JSON: %v", err)
	}
	row := generic[0]
	for _, field := range []string{"id", "name", "category", "equip_slots", "jobs", "level", "stats", "stack_size", "model_id"} {
		if _, ok := row[field]; !ok {
			t.Errorf("expected real field %q in the written JSON, got keys: %v", field, row)
		}
	}
}
