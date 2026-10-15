package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/shankpit"
)

func dReq(t *testing.T, h *handlers.ShankpitEditSessionsHandler, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A player joining a live session must load the editor's UNSAVED working level, not the saved row.
func TestEditSessions_DraftLevelServedUnsaved(t *testing.T) {
	store := newShankpitLevelsTestStore(t)
	saved, err := store.CreateLevel(context.Background(), "Town", 100, 50, 100, true, 2,
		[]shankpit.Wall{{ID: 1, X: 0, Y: 0, Z: 0, SX: 4, SY: 4, SZ: 4, R: 1, G: 1, B: 1}}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &handlers.ShankpitEditSessionsHandler{Store: store}
	_, c := dReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions",
		`{"level_id":`+strconv.FormatInt(saved.ID, 10)+`,"level_name":"Town"}`)
	base := "/api/v1/shankpit-edit-sessions/" + c["id"].(string)

	// before any draft: the saved level (1 wall)
	if code, doc := dReq(t, h, http.MethodGet, base+"/level", ""); code != 200 || len(doc["walls"].([]any)) != 1 {
		t.Fatalf("saved fallback: %d %v", code, doc)
	}
	// the editor deletes the building (0 walls) and adds a rotated cube, without saving
	draft := `{"width":100,"height":50,"depth":100,"ground_plane_enabled":true,"ground_plane_squares":2,` +
		`"walls":[{"id":7,"x":10,"y":0,"z":0,"sx":2,"sy":2,"sz":2,"r":1,"g":0,"b":0,"rot_y":30},` +
		`{"id":8,"x":20,"y":0,"z":0,"sx":2,"sy":2,"sz":2,"r":0,"g":1,"b":0}]}`
	if code, r := dReq(t, h, http.MethodPut, base+"/level", draft); code != 200 || r["rev"].(float64) != 1 {
		t.Fatalf("put draft: %d %v", code, r)
	}
	code, doc := dReq(t, h, http.MethodGet, base+"/level", "")
	ws, _ := doc["walls"].([]any)
	if code != 200 || len(ws) != 2 || ws[0].(map[string]any)["rot_y"].(float64) != 30 {
		t.Fatalf("draft not served: %d %v", code, doc)
	}
	// the saved row is untouched
	if got, _ := store.GetLevel(context.Background(), saved.ID); len(got.Walls) != 1 {
		t.Fatalf("draft leaked into the saved level: %d walls", len(got.Walls))
	}
	// pollers see the rev + a level event
	_, p := dReq(t, h, http.MethodGet, base+"?since=0", "")
	if p["level_rev"].(float64) != 1 {
		t.Fatalf("level_rev missing: %v", p)
	}
}
