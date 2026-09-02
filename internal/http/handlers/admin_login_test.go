package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

// TestAdminLoginHandler_EmitsEvents -- S226-03: a real Back Office login success and two real,
// distinct failure reasons (bad credentials, and valid credentials missing iduna.admin) all land
// in the unified log with the right event Type and reason.
func TestAdminLoginHandler_EmitsEvents(t *testing.T) {
	keys, err := jwt.GenerateKeys()
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	store := &stubAgentStore{agents: map[string]*auth.Agent{
		"EMILY":      {ID: "agent-1", Name: "EMILY", Type: "LLM_AGENT", Status: "ACTIVE", Permissions: []string{"iduna.admin"}},
		"NOT-ADMIN":  {ID: "agent-2", Name: "NOT-ADMIN", Type: "LLM_AGENT", Status: "ACTIVE", Permissions: []string{"read.only"}},
	}}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminLoginHandler{Store: store, Keys: keys, EventLog: eventLog}

	post := func(agentName, agentSecret string) int {
		form := url.Values{"agent_name": {agentName}, "agent_secret": {agentSecret}}
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post("EMILY", "sk-emily"); code != http.StatusSeeOther {
		t.Fatalf("success login: status = %d, want 303", code)
	}
	// stubAgentStore.AuthenticateAgent only checks agent_name presence, not the secret value
	// (see agent_auth_test.go's own definition) -- an unknown agent_name is the real way to
	// trigger its own "invalid credentials" error path.
	if code := post("GHOST", "whatever"); code != http.StatusOK {
		t.Fatalf("bad-credentials login: status = %d, want 200 (re-rendered login page)", code)
	}
	if code := post("NOT-ADMIN", "sk-not-admin"); code != http.StatusOK {
		t.Fatalf("missing-permission login: status = %d, want 200 (re-rendered login page)", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:auth.admin_login.success" {
		t.Errorf("event 0 Type = %q, want iduna:auth.admin_login.success", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:auth.admin_login.failure" || !strings.Contains(string(recs[1].Event.Data), "invalid_credentials") {
		t.Errorf("event 1 = %+v, want a failure with reason=invalid_credentials", recs[1].Event)
	}
	if recs[2].Event.Type != "iduna:auth.admin_login.failure" || !strings.Contains(string(recs[2].Event.Data), "missing_iduna_admin_permission") {
		t.Errorf("event 2 = %+v, want a failure with reason=missing_iduna_admin_permission", recs[2].Event)
	}
}
