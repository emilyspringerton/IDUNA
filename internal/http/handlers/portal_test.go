package handlers_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

// Portal's own auth gating (RequireCookieAuth + RequirePermission) is
// wired in main.go and already covered by
// internal/http/middleware/auth_test.go's own suite -- these tests just
// cover the handler's own rendering, the same split every other handler
// in this package uses.

func TestPortalHandler_Login_RendersSignInWidget(t *testing.T) {
	h := &handlers.PortalHandler{GoogleClientID: "test-client-id.apps.googleusercontent.com"}
	req := httptest.NewRequest(http.MethodGet, "/portal/login?next=%2Fportal", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "test-client-id.apps.googleusercontent.com") {
		t.Errorf("expected the configured GoogleClientID in the rendered page, got: %s", body)
	}
	if !strings.Contains(body, "/api/v1/auth/google") {
		t.Errorf("expected the login page to POST credentials to /api/v1/auth/google, got: %s", body)
	}
}

func TestPortalHandler_Login_NoClientIDShowsFallback(t *testing.T) {
	h := &handlers.PortalHandler{} // GoogleClientID unset
	req := httptest.NewRequest(http.MethodGet, "/portal/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not yet configured") {
		t.Errorf("expected an honest fallback message when GOOGLE_CLIENT_ID is unset, got: %s", rr.Body.String())
	}
}

func TestPortalHandler_Login_WrongMethod(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodPost, "/portal/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-GET, got %d", rr.Code)
	}
}

// TestPortalHandler_LocalLogin_EmitsEvents -- S226-03: a real success and a real failure both
// land in the unified log with the right event Type, distinct from LocalAuthHandler's own
// iduna:auth.local.* events (this is the cookie-based /portal/login surface, a real, separate
// login path).
func TestPortalHandler_LocalLogin_EmitsEvents(t *testing.T) {
	keys, err := jwt.GenerateKeys()
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	proj := &stubUserProjector{byEmail: map[string]*userlog.LocalUser{
		"alice@example.com": {LocalUID: 1, Email: "alice@example.com", Status: "active",
			PasswordHash: mustHash(t, "correct-horse-battery-staple")},
	}}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.PortalHandler{Keys: keys, Proj: proj, EventLog: eventLog}

	post := func(email, password string) int {
		form := url.Values{"email": {email}, "password": {password}}
		req := httptest.NewRequest(http.MethodPost, "/portal/login", bytes.NewBufferString(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.LocalLogin(rr, req)
		return rr.Code
	}

	if code := post("alice@example.com", "correct-horse-battery-staple"); code != http.StatusSeeOther {
		t.Fatalf("success login: status = %d, want 303", code)
	}
	if code := post("alice@example.com", "wrong-password"); code != http.StatusOK {
		// LocalLogin re-renders the login page (200) with an error, rather than a redirect.
		t.Fatalf("failed login: status = %d, want 200 (re-rendered login page)", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:auth.portal.success" {
		t.Errorf("event 0 Type = %q, want iduna:auth.portal.success", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:auth.portal.failure" {
		t.Errorf("event 1 Type = %q, want iduna:auth.portal.failure", recs[1].Event.Type)
	}
}

func TestPortalHandler_Home_ListsTools(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodGet, "/portal", nil)
	rr := httptest.NewRecorder()
	h.Home(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Jupyter") {
		t.Errorf("expected Jupyter listed on the portal, got: %s", body)
	}
	if !strings.Contains(body, "SARENA_NOTEBOOK") {
		t.Errorf("expected SARENA_NOTEBOOK listed on the portal, got: %s", body)
	}
}
