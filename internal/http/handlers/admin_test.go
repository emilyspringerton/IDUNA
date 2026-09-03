package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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

// postForm issues a real application/x-www-form-urlencoded POST, matching what
// AdminHandler.userAction/agentAction's own r.ParseForm() actually expects on the wire (a real
// HTML <form>, not JSON) -- the "roles"/"permissions"/"secret" real admin actions all read
// r.FormValue, unlike the JSON-bodied /api/v1 handlers elsewhere in this package.
func postForm(t *testing.T, h http.Handler, path string, form url.Values) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// TestAdminHandler_RoleAssignRevoke_EmitsEvents -- S226-04's own explicitly named case ("role
// assign/revoke").
func TestAdminHandler_RoleAssignRevoke_EmitsEvents(t *testing.T) {
	store := &stubApplesStore{}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog}
	h.Init()

	if code := postForm(t, h, "/admin/users/user-9/roles", url.Values{"role_id": {"admin"}, "verb": {"assign"}}); code != http.StatusSeeOther {
		t.Fatalf("assign: status = %d, want 303", code)
	}
	if code := postForm(t, h, "/admin/users/user-9/roles", url.Values{"role_id": {"admin"}, "verb": {"revoke"}}); code != http.StatusSeeOther {
		t.Fatalf("revoke: status = %d, want 303", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.role.assign" {
		t.Errorf("event 0 Type = %q, want iduna:admin.role.assign", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:admin.role.revoke" {
		t.Errorf("event 1 Type = %q, want iduna:admin.role.revoke", recs[1].Event.Type)
	}
	for _, rec := range recs {
		if !strings.Contains(string(rec.Event.Data), "user-9") || !strings.Contains(string(rec.Event.Data), "admin") {
			t.Errorf("event should record the real user_id/role_id, got: %s", rec.Event.Data)
		}
	}
}

// TestAdminHandler_AgentPermissionGrantRevoke_EmitsEvents -- S226-04's own explicitly named
// case ("agent permission grants").
func TestAdminHandler_AgentPermissionGrantRevoke_EmitsEvents(t *testing.T) {
	store := &stubApplesStore{}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog}
	h.Init()

	if code := postForm(t, h, "/admin/agents/agent-7/permissions", url.Values{"permission_name": {"fatbaby.read"}, "verb": {"grant"}}); code != http.StatusSeeOther {
		t.Fatalf("grant: status = %d, want 303", code)
	}
	if code := postForm(t, h, "/admin/agents/agent-7/permissions", url.Values{"permission_name": {"fatbaby.read"}, "verb": {"revoke"}}); code != http.StatusSeeOther {
		t.Fatalf("revoke: status = %d, want 303", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.agent_permission.grant" {
		t.Errorf("event 0 Type = %q, want iduna:admin.agent_permission.grant", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:admin.agent_permission.revoke" {
		t.Errorf("event 1 Type = %q, want iduna:admin.agent_permission.revoke", recs[1].Event.Type)
	}
}

// TestAdminHandler_AgentSecretRotate_EmitsEvent_NeverPlaintext -- a real security-audit-trail
// check: the event records that a rotation happened, never the freshly-generated plaintext
// secret itself.
func TestAdminHandler_AgentSecretRotate_EmitsEvent_NeverPlaintext(t *testing.T) {
	store := &stubApplesStore{}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog}
	h.Init()

	req := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/secret", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (one-time reveal page)", rr.Code)
	}
	// generateAgentSecret(32) produces a 64-char hex string -- pull the real, actual secret value
	// out of the one-time-reveal page rather than comparing against the whole HTML body, so the
	// "never logged" assertion below is a real, meaningful check.
	plaintext := regexp.MustCompile(`[0-9a-f]{64}`).FindString(rr.Body.String())
	if plaintext == "" {
		t.Fatalf("could not find the real generated secret in the one-time-reveal page: %s", rr.Body.String())
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.agent.secret_rotate" {
		t.Errorf("event Type = %q, want iduna:admin.agent.secret_rotate", recs[0].Event.Type)
	}
	eventData := string(recs[0].Event.Data)
	if !strings.Contains(eventData, "agent-7") {
		t.Errorf("event should record the real agent_id, got: %s", eventData)
	}
	// The event payload must never contain the one-time-reveal plaintext secret rendered in the
	// real HTTP response above -- a real credential should never end up duplicated into the
	// audit log itself.
	if strings.Contains(eventData, plaintext) {
		t.Errorf("event must never contain the plaintext secret")
	}
}
