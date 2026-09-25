package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"iduna/internal/agentsecrets"
	"iduna/internal/auth"
	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

// stubStoreWithAgent overrides stubApplesStore's always-nil GetAgentByID so these tests can
// exercise the reveal-secret/rotate-write-through paths, which need a real agent Name to derive
// the agent-secrets.env key.
type stubStoreWithAgent struct {
	*stubApplesStore
	agent *auth.Agent
}

func (s *stubStoreWithAgent) GetAgentByID(context.Context, string) (*auth.Agent, error) {
	return s.agent, nil
}

// TestAdminHandler_RevealSecret_NotConfigured -- AgentSecretsPath unset (the default on any
// deployment that hasn't wired it in main.go) must 404, not panic, and never claim success.
func TestAdminHandler_RevealSecret_NotConfigured(t *testing.T) {
	h := &handlers.AdminHandler{Store: &stubApplesStore{}}
	h.Init()

	req := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/reveal-secret", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestAdminHandler_RevealSecret_NoRecordedPlaintext -- an agent that HasCredential but was never
// provisioned/rotated through a path that writes agent-secrets.env (e.g. bootstrapped before this
// feature existed, then never rotated) must 404 with a real, actionable message, not a panic or a
// silently empty success.
func TestAdminHandler_RevealSecret_NoRecordedPlaintext(t *testing.T) {
	store := &stubStoreWithAgent{stubApplesStore: &stubApplesStore{}, agent: &auth.Agent{ID: "agent-7", Name: "GHOST-AGENT"}}
	h := &handlers.AdminHandler{Store: store, AgentSecretsPath: filepath.Join(t.TempDir(), "agent-secrets.env")}
	h.Init()

	req := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/reveal-secret", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", rr.Code, rr.Body.String())
	}
}

// TestAdminHandler_RevealSecret_ReturnsRecordedPlaintext_EmitsEvent -- the actual ask (founder
// real-time: "it needs to reveal them to me"): a pre-recorded plaintext for a real agent name
// comes back in the response, and the reveal itself (never the plaintext value) is audit-logged.
func TestAdminHandler_RevealSecret_ReturnsRecordedPlaintext_EmitsEvent(t *testing.T) {
	secretsPath := filepath.Join(t.TempDir(), "agent-secrets.env")
	if err := agentsecrets.WriteMerged(secretsPath, map[string]string{"SHANKPIT-RL": "the-real-training-key"}); err != nil {
		t.Fatalf("WriteMerged: %v", err)
	}
	store := &stubStoreWithAgent{stubApplesStore: &stubApplesStore{}, agent: &auth.Agent{ID: "agent-7", Name: "SHANKPIT-RL"}}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{Store: store, EventLog: eventLog, AgentSecretsPath: secretsPath}
	h.Init()

	req := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/reveal-secret", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "the-real-training-key") {
		t.Fatalf("response should contain the recorded plaintext, got: %s", rr.Body.String())
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 1 || recs[0].Event.Type != "iduna:admin.agent.secret_reveal" {
		t.Fatalf("expected exactly one iduna:admin.agent.secret_reveal event, got: %+v", recs)
	}
	if strings.Contains(string(recs[0].Event.Data), "the-real-training-key") {
		t.Errorf("event must never contain the plaintext secret")
	}
}

// TestAdminHandler_SecretRotate_WriteThrough_MakesItRevealable -- SECTION 551 follow-up: rotating
// a secret through the normal admin UI must also make it revealable afterward, not just shown
// once, when AgentSecretsPath is configured.
func TestAdminHandler_SecretRotate_WriteThrough_MakesItRevealable(t *testing.T) {
	secretsPath := filepath.Join(t.TempDir(), "agent-secrets.env")
	store := &stubStoreWithAgent{stubApplesStore: &stubApplesStore{}, agent: &auth.Agent{ID: "agent-7", Name: "NEWLY-ROTATED"}}
	h := &handlers.AdminHandler{Store: store, AgentSecretsPath: secretsPath}
	h.Init()

	rotateReq := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/secret", nil)
	rotateRR := httptest.NewRecorder()
	h.ServeHTTP(rotateRR, rotateReq)
	if rotateRR.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200", rotateRR.Code)
	}
	rotatedPlaintext := regexp.MustCompile(`[0-9a-f]{64}`).FindString(rotateRR.Body.String())
	if rotatedPlaintext == "" {
		t.Fatalf("could not find the rotated plaintext in the response: %s", rotateRR.Body.String())
	}

	revealReq := httptest.NewRequest(http.MethodPost, "/admin/agents/agent-7/reveal-secret", nil)
	revealRR := httptest.NewRecorder()
	h.ServeHTTP(revealRR, revealReq)
	if revealRR.Code != http.StatusOK {
		t.Fatalf("reveal status = %d, want 200, body: %s", revealRR.Code, revealRR.Body.String())
	}
	if !strings.Contains(revealRR.Body.String(), rotatedPlaintext) {
		t.Fatalf("reveal should return the just-rotated plaintext (%s), got: %s", rotatedPlaintext, revealRR.Body.String())
	}
}
