package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func editReq(t *testing.T, h *ShankpitEditSessionsHandler, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestEditSessions_CreatePostPoll(t *testing.T) {
	h := &ShankpitEditSessionsHandler{}
	code, c := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", `{"level_id":7,"level_name":"nextown"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	id, _ := c["id"].(string)
	if len(id) != 32 {
		t.Fatalf("session id %q should be a 128-bit hex token", id)
	}
	base := "/api/v1/shankpit-edit-sessions/" + id

	// NOCK moves the spawner, SHANKPIT reports its avatar, then flips the spawn mode
	for _, ev := range []string{
		`{"source":"nock","kind":"spawner","data":{"x":1,"y":2,"z":3,"yaw":90}}`,
		`{"source":"shankpit","kind":"avatar","data":{"x":4,"y":5,"z":6,"yaw":0,"pitch":0}}`,
		`{"source":"nock","kind":"spawn_mode","data":{"mode":"crosshair"}}`,
		`{"source":"shankpit","kind":"box_add","data":{"id":9,"x":0,"y":0,"z":0,"sx":2,"sy":2,"sz":2}}`,
	} {
		if code, _ := editReq(t, h, http.MethodPost, base+"/events", ev); code != http.StatusCreated {
			t.Fatalf("post %s: %d", ev, code)
		}
	}
	code, snap := editReq(t, h, http.MethodGet, base+"?since=0", "")
	if code != 200 || int(snap["seq"].(float64)) != 4 || len(snap["events"].([]any)) != 4 {
		t.Fatalf("poll since=0: %d %v", code, snap)
	}
	st := snap["state"].(map[string]any)
	if st["spawn_mode"] != "crosshair" || st["spawner"] == nil || st["avatar"] == nil {
		t.Fatalf("materialised state wrong: %v", st)
	}
	// a late poller only sees what is new
	_, snap = editReq(t, h, http.MethodGet, base+"?since=3", "")
	evs := snap["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["kind"] != "box_add" {
		t.Fatalf("since=3 should return only the box_add: %v", evs)
	}
}

func TestEditSessions_PoseStreamsCoalesce(t *testing.T) {
	h := &ShankpitEditSessionsHandler{}
	_, c := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``)
	base := "/api/v1/shankpit-edit-sessions/" + c["id"].(string)
	for i := 0; i < 50; i++ {
		editReq(t, h, http.MethodPost, base+"/events", `{"source":"shankpit","kind":"avatar","data":{"x":1,"y":1,"z":1}}`)
	}
	_, snap := editReq(t, h, http.MethodGet, base+"?since=0", "")
	if n := len(snap["events"].([]any)); n != 1 {
		t.Fatalf("50 avatar poses should coalesce to the latest, log has %d", n)
	}
	if int(snap["seq"].(float64)) != 50 {
		t.Fatalf("seq must keep counting: %v", snap["seq"])
	}
}

func TestEditSessions_Validation(t *testing.T) {
	h := &ShankpitEditSessionsHandler{}
	_, c := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``)
	base := "/api/v1/shankpit-edit-sessions/" + c["id"].(string)
	for name, body := range map[string]string{
		"unknown kind": `{"source":"nock","kind":"rm_rf","data":{}}`,
		"bad source":   `{"source":"evil","kind":"spawner","data":{}}`,
		"bad mode":     `{"source":"nock","kind":"spawn_mode","data":{"mode":"wherever"}}`,
		"not json":     `nope`,
	} {
		if code, _ := editReq(t, h, http.MethodPost, base+"/events", body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, code)
		}
	}
	if code, _ := editReq(t, h, http.MethodGet, "/api/v1/shankpit-edit-sessions/deadbeef", ""); code != http.StatusNotFound {
		t.Errorf("unknown session id: want 404, got %d", code)
	}
}

func TestEditSessions_LongPollWakesOnEvent(t *testing.T) {
	h := &ShankpitEditSessionsHandler{}
	_, c := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``)
	base := "/api/v1/shankpit-edit-sessions/" + c["id"].(string)
	done := make(chan map[string]any, 1)
	go func() {
		_, snap := editReq(t, h, http.MethodGet, base+"?since=0&wait=10", "")
		done <- snap
	}()
	time.Sleep(150 * time.Millisecond)
	editReq(t, h, http.MethodPost, base+"/events", `{"source":"nock","kind":"spawner","data":{"x":1,"y":1,"z":1}}`)
	select {
	case snap := <-done:
		if len(snap["events"].([]any)) != 1 {
			t.Fatalf("long poll should return the new event: %v", snap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not wake on a new event")
	}
}

func TestEditSessions_ExpiryAndCap(t *testing.T) {
	now := time.Now()
	h := &ShankpitEditSessionsHandler{Now: func() time.Time { return now }}
	_, c := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``)
	id := c["id"].(string)
	now = now.Add(editSessionTTL + time.Minute)
	if code, _ := editReq(t, h, http.MethodGet, "/api/v1/shankpit-edit-sessions/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("a session past its TTL must 404, got %d", code)
	}
	for i := 0; i < editSessionMaxCount; i++ {
		if code, _ := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``); code != http.StatusCreated {
			t.Fatalf("create %d: %d", i, code)
		}
	}
	if code, _ := editReq(t, h, http.MethodPost, "/api/v1/shankpit-edit-sessions", ``); code != http.StatusServiceUnavailable {
		t.Fatalf("session cap should answer 503, got %d", code)
	}
}
