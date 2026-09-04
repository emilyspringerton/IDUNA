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

func newMobSpawnsHandler(t *testing.T, seed string) *GfdMobSpawnsHandler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mob_spawns.json")
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return &GfdMobSpawnsHandler{RulesJSONPath: path}
}

const seedMobSpawns = `[
  {"zone_id":0,"kind":"worm","enabled":true},
  {"zone_id":1,"kind":"rabbit","enabled":true}
]`

func TestGfdMobSpawns_List(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	req := httptest.NewRequest(http.MethodGet, "/admin/gfd-mob-spawns/api/rules", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var rules []GfdMobSpawnRule
	if err := json.Unmarshal(w.Body.Bytes(), &rules); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestGfdMobSpawns_Create(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(GfdMobSpawnRule{ZoneID: 1, Kind: "beetle", Enabled: false})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-spawns/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rules, _ := h.readAll()
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules after create, got %d", len(rules))
	}
}

func TestGfdMobSpawns_Create_RejectsDuplicateZoneKindCaseInsensitive(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(GfdMobSpawnRule{ZoneID: 0, Kind: "WORM", Enabled: false})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-spawns/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGfdMobSpawns_Create_AllowsSameKindInDifferentZone(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(GfdMobSpawnRule{ZoneID: 4, Kind: "worm", Enabled: true})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-spawns/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (different zone, same kind is a distinct real key), got %d: %s", w.Code, w.Body.String())
	}
}

func TestGfdMobSpawns_Create_RejectsEmptyKind(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(GfdMobSpawnRule{ZoneID: 0, Enabled: true})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-spawns/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGfdMobSpawns_Update_TogglesEnabled(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(map[string]bool{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-spawns/api/rules/1/rabbit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rules, _ := h.readAll()
	for _, rule := range rules {
		if rule.ZoneID == 1 && rule.Kind == "rabbit" {
			if rule.Enabled {
				t.Fatal("expected rabbit to be disabled after update")
			}
			return
		}
	}
	t.Fatal("rabbit rule missing after update")
}

func TestGfdMobSpawns_Update_NotFound(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(map[string]bool{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-spawns/api/rules/9/nonexistent", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGfdMobSpawns_Update_MalformedPath(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	body, _ := json.Marshal(map[string]bool{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-mob-spawns/api/rules/notanumber/rabbit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-integer zone id, got %d", w.Code)
	}
}

func TestGfdMobSpawns_Delete(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-mob-spawns/api/rules/0/worm", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	rules, _ := h.readAll()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule after delete, got %d", len(rules))
	}
}

func TestGfdMobSpawns_Delete_NotFound(t *testing.T) {
	h := newMobSpawnsHandler(t, seedMobSpawns)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-mob-spawns/api/rules/0/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGfdMobSpawns_RoundTripPreservesRealFieldsApps2MudExpects confirms the JSON this handler
// writes is byte-shape-compatible with server/spawn.Registry.LoadJSON's own real field tags.
func TestGfdMobSpawns_RoundTripPreservesRealFieldsApps2MudExpects(t *testing.T) {
	h := newMobSpawnsHandler(t, `[]`)
	body, _ := json.Marshal(GfdMobSpawnRule{ZoneID: 2, Kind: "cave-bat", Enabled: false})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-mob-spawns/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(h.RulesJSONPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var generic []map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	row := generic[0]
	if row["zone_id"] != float64(2) || row["kind"] != "cave-bat" || row["enabled"] != false {
		t.Fatalf("row shape wrong: %+v", row)
	}
}
