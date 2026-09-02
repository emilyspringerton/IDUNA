package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

// TestAdminHandler_UserSuspendUnsuspend_EmitsEvents -- S226-03: the founder's own explicitly
// named case ("admin events like suspend/un suspend"). A real suspend and a real unsuspend
// ("activate" in this handler's own action name) both land in the unified log with the right
// event Type and the acted-on user_id.
func TestAdminHandler_UserSuspendUnsuspend_EmitsEvents(t *testing.T) {
	store := &stubApplesStore{}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog}
	h.Init()

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post("/admin/users/user-42/suspend"); code != http.StatusSeeOther {
		t.Fatalf("suspend: status = %d, want 303", code)
	}
	if code := post("/admin/users/user-42/activate"); code != http.StatusSeeOther {
		t.Fatalf("activate: status = %d, want 303", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.user.suspend" {
		t.Errorf("event 0 Type = %q, want iduna:admin.user.suspend", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:admin.user.unsuspend" {
		t.Errorf("event 1 Type = %q, want iduna:admin.user.unsuspend", recs[1].Event.Type)
	}
	for _, rec := range recs {
		if !strings.Contains(string(rec.Event.Data), "user-42") {
			t.Errorf("event should record the acted-on user_id, got: %s", rec.Event.Data)
		}
	}
}

// TestAdminHandler_AgentSuspendUnsuspend_EmitsEvents -- the same real check for agents.
func TestAdminHandler_AgentSuspendUnsuspend_EmitsEvents(t *testing.T) {
	store := &stubApplesStore{}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog}
	h.Init()

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post("/admin/agents/agent-7/suspend"); code != http.StatusSeeOther {
		t.Fatalf("suspend: status = %d, want 303", code)
	}
	if code := post("/admin/agents/agent-7/activate"); code != http.StatusSeeOther {
		t.Fatalf("activate: status = %d, want 303", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.agent.suspend" {
		t.Errorf("event 0 Type = %q, want iduna:admin.agent.suspend", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:admin.agent.unsuspend" {
		t.Errorf("event 1 Type = %q, want iduna:admin.agent.unsuspend", recs[1].Event.Type)
	}
}
