package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
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
