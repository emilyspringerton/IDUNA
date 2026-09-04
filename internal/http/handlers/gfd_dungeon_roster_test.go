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

func newDungeonRosterHandler(t *testing.T, seed string) *GfdDungeonRosterHandler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dungeon_roster.json")
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return &GfdDungeonRosterHandler{RosterJSONPath: path}
}

const seedDungeonRoster = `[
  {"name":"The Sealed Archive","boss":"ARENA_HERO_CART","elite":["ARENA_HERO_NOOR1"]},
  {"name":"The Proving Grounds","boss":"ARENA_HERO_WARRIOR","elite":[]}
]`

func TestGfdDungeonRoster_List_PreservesOrder(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	req := httptest.NewRequest(http.MethodGet, "/admin/gfd-dungeon-roster/api/dungeons", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var roster []GfdDungeonAssignment
	json.Unmarshal(w.Body.Bytes(), &roster)
	if len(roster) != 2 || roster[0].Name != "The Sealed Archive" || roster[1].Name != "The Proving Grounds" {
		t.Fatalf("order not preserved: %+v", roster)
	}
}

func TestGfdDungeonRoster_Create_AppendsAtEnd(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	body, _ := json.Marshal(GfdDungeonAssignment{Name: "New Dungeon", Boss: "ARENA_HERO_TEST"})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-dungeon-roster/api/dungeons", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	roster, _ := h.readAll()
	if len(roster) != 3 || roster[2].Name != "New Dungeon" {
		t.Fatalf("expected new dungeon appended at index 2, got %+v", roster)
	}
	// Real, load-bearing check: appending must never reorder existing rows, since
	// GenerateDungeonSpawns' own dungeonIndex % len(DungeonRoster) depends on array position.
	if roster[0].Name != "The Sealed Archive" || roster[1].Name != "The Proving Grounds" {
		t.Fatalf("existing rows were reordered by create: %+v", roster)
	}
}

func TestGfdDungeonRoster_Create_RejectsMissingBoss(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	body, _ := json.Marshal(GfdDungeonAssignment{Name: "No Boss Dungeon"})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-dungeon-roster/api/dungeons", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGfdDungeonRoster_Update_ReplacesRowInPlace(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	body, _ := json.Marshal(GfdDungeonAssignment{Name: "The Sealed Archive", Boss: "ARENA_HERO_DIFFERENT", Elite: []string{}})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-dungeon-roster/api/dungeons/0", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	roster, _ := h.readAll()
	if roster[0].Boss != "ARENA_HERO_DIFFERENT" {
		t.Fatalf("expected row 0's boss updated, got %+v", roster[0])
	}
	if roster[1].Name != "The Proving Grounds" {
		t.Fatalf("expected row 1 untouched, got %+v", roster[1])
	}
}

func TestGfdDungeonRoster_Update_OutOfRange(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	body, _ := json.Marshal(GfdDungeonAssignment{Name: "X", Boss: "Y"})
	req := httptest.NewRequest(http.MethodPatch, "/admin/gfd-dungeon-roster/api/dungeons/99", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGfdDungeonRoster_Delete_PreservesRemainingOrder(t *testing.T) {
	h := newDungeonRosterHandler(t, `[
		{"name":"A","boss":"ARENA_HERO_A","elite":[]},
		{"name":"B","boss":"ARENA_HERO_B","elite":[]},
		{"name":"C","boss":"ARENA_HERO_C","elite":[]}
	]`)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-dungeon-roster/api/dungeons/1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	roster, _ := h.readAll()
	if len(roster) != 2 || roster[0].Name != "A" || roster[1].Name != "C" {
		t.Fatalf("expected [A, C] after deleting index 1, got %+v", roster)
	}
}

func TestGfdDungeonRoster_Delete_OutOfRange(t *testing.T) {
	h := newDungeonRosterHandler(t, seedDungeonRoster)
	req := httptest.NewRequest(http.MethodDelete, "/admin/gfd-dungeon-roster/api/dungeons/99", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGfdDungeonRoster_RoundTripPreservesRealFieldsApps2MudExpects confirms the JSON shape
// matches server/mob.DungeonBossAssignment's own real field tags.
func TestGfdDungeonRoster_RoundTripPreservesRealFieldsApps2MudExpects(t *testing.T) {
	h := newDungeonRosterHandler(t, `[]`)
	body, _ := json.Marshal(GfdDungeonAssignment{Name: "Test Dungeon", Boss: "ARENA_HERO_TEST", Elite: []string{"ARENA_HERO_E1"}})
	req := httptest.NewRequest(http.MethodPost, "/admin/gfd-dungeon-roster/api/dungeons", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(h.RosterJSONPath)
	var generic []map[string]interface{}
	json.Unmarshal(raw, &generic)
	row := generic[0]
	if row["name"] != "Test Dungeon" || row["boss"] != "ARENA_HERO_TEST" {
		t.Fatalf("row shape wrong: %+v", row)
	}
	elite, ok := row["elite"].([]interface{})
	if !ok || len(elite) != 1 || elite[0] != "ARENA_HERO_E1" {
		t.Fatalf("elite field wrong shape: %+v", row)
	}
}
