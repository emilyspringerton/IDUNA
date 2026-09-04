package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newMobDropsHandler(t *testing.T, seed string) *GfdMobDropsHandler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mob_drops.json")
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return &GfdMobDropsHandler{DropsJSONPath: path}
}

const seedMobDrops = `[
  {"kind":"worm","items":[{"id":"worm-sinew","name":"Worm Sinew"},{"id":"earth-crystal","name":"Earth Crystal"}]},
  {"kind":"leech","items":[{"id":"leech-blood","name":"Leech Blood"}]}
]`

func TestGfdMobDrops_List(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	req := httptest.NewRequest(http.MethodGet, "/admin/gfd-mob-drops/api/tables", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var tables []GfdMobDropTable
	if err := json.Unmarshal(w.Body.Bytes(), &tables); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
}

func TestGfdMobDrops_Create(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	body, _ := json.Marshal(GfdMobDropTable{Kind: "slime", Items: []GfdMobDropItem{{ID: "slime-oil", Name: "Slime Oil"}}})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-drops/api/tables", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	tables, _ := h.readAll()
	if len(tables) != 3 {
		t.Fatalf("expected 3 tables after create, got %d", len(tables))
	}
}

func TestGfdMobDrops_Create_RejectsDuplicateKindCaseInsensitive(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	body, _ := json.Marshal(GfdMobDropTable{Kind: "WORM", Items: []GfdMobDropItem{{ID: "x", Name: "X"}}})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-drops/api/tables", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGfdMobDrops_Create_RejectsEmptyKind(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	body, _ := json.Marshal(GfdMobDropTable{Items: []GfdMobDropItem{{ID: "x", Name: "X"}}})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-drops/api/tables", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGfdMobDrops_Update(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	body, _ := json.Marshal(GfdMobDropTable{Items: []GfdMobDropItem{{ID: "worm-sinew-hq", Name: "Worm Sinew (HQ)"}}})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-drops/api/tables/worm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	tables, _ := h.readAll()
	for _, tbl := range tables {
		if tbl.Kind == "worm" {
			if len(tbl.Items) != 1 || tbl.Items[0].ID != "worm-sinew-hq" {
				t.Fatalf("update did not apply: %+v", tbl)
			}
			return
		}
	}
	t.Fatal("worm table missing after update")
}

func TestGfdMobDrops_Update_URLKindIsAuthoritative(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	// Body claims a different kind -- the URL segment must win, matching gfd_items.go's own
	// "URL id wins over body id" contract.
	body, _ := json.Marshal(GfdMobDropTable{Kind: "something-else", Items: []GfdMobDropItem{{ID: "x", Name: "X"}}})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-drops/api/tables/worm", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	tables, _ := h.readAll()
	for _, tbl := range tables {
		if tbl.Kind == "something-else" {
			t.Fatalf("body kind was allowed to rename the row: %+v", tables)
		}
	}
}

func TestGfdMobDrops_Update_NotFound(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	body, _ := json.Marshal(GfdMobDropTable{Items: []GfdMobDropItem{{ID: "x", Name: "X"}}})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-drops/api/tables/nonexistent", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGfdMobDrops_Delete(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-mob-drops/api/tables/leech", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	tables, _ := h.readAll()
	if len(tables) != 1 {
		t.Fatalf("expected 1 table after delete, got %d", len(tables))
	}
}

func TestGfdMobDrops_Delete_NotFound(t *testing.T) {
	h := newMobDropsHandler(t, seedMobDrops)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-mob-drops/api/tables/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGfdMobDrops_RoundTripPreservesRealFieldsApps2MudExpects confirms the JSON this handler
// writes is byte-shape-compatible with server/mobdrop.Registry.LoadJSON's own real field tags
// (checked directly against that file), the same real guarantee gfd_items.go's own equivalent
// test gives for items.json.
func TestGfdMobDrops_RoundTripPreservesRealFieldsApps2MudExpects(t *testing.T) {
	h := newMobDropsHandler(t, `[]`)
	body, _ := json.Marshal(GfdMobDropTable{
		Kind: "cave-bat",
		Items: []GfdMobDropItem{
			{ID: "bat-wing", Name: "Bat Wing"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-drops/api/tables", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(h.DropsJSONPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var generic []map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	row := generic[0]
	if row["kind"] != "cave-bat" {
		t.Fatalf("kind field wrong shape: %+v", row)
	}
	items, ok := row["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("items field wrong shape: %+v", row)
	}
	item := items[0].(map[string]interface{})
	if item["id"] != "bat-wing" || item["name"] != "Bat Wing" {
		t.Fatalf("item fields wrong shape: %+v", item)
	}
}
