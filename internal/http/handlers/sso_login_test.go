package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
)

func newTestSSOLoginHandler() *handlers.SSOLoginHandler {
	return &handlers.SSOLoginHandler{AllowedHosts: []string{"wotan.okemily.com", "localhost"}}
}

func TestSSOLogin_MissingRedirectURI(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSSOLogin_DisallowedHost(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login?redirect_uri=https://evil.example.com/steal", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for disallowed host, got %d", rec.Code)
	}
	// The whole point of the allowlist check: an unrecognized redirect_uri must never get a
	// rendered login form (that would be a phishing page collecting a real password on IDUNA's
	// own domain and handing it to an arbitrary redirect target).
	if strings.Contains(rec.Body.String(), "type=\"password\"") {
		t.Fatalf("disallowed redirect_uri must not render a login form, got: %s", rec.Body.String())
	}
}

func TestSSOLogin_MalformedRedirectURI(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login?redirect_uri=not-a-url", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative/malformed redirect_uri, got %d", rec.Code)
	}
}

func TestSSOLogin_MethodNotAllowed(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sso/login?redirect_uri=https://wotan.okemily.com/store.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestSSOLogin_ValidRedirectRendersTwoPaneForm(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login?redirect_uri=https://wotan.okemily.com/store.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="form-pane"`, `class="visual-pane"`, // the two panes
		`type="email"`, `type="password"`, // the actual form
		`IDUNA`, // brand wordmark
		`var redirectURI = "https://wotan.okemily.com/store.html";`, // correctly escaped as a JS string literal
		`var registering =  false ;`, // html/template pads bool literals in JS context defensively
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected response body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestSSOLogin_SignupParamSetsRegisteringTrue(t *testing.T) {
	h := newTestSSOLoginHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login?redirect_uri=https://wotan.okemily.com/store.html&signup=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "var registering =  true ;") {
		t.Errorf("expected registering=true with signup=1, got:\n%s", rec.Body.String())
	}
}

// A redirect_uri whose query string carries something JS-dangerous must come back safely
// escaped inside the <script> block -- html/template's contextual auto-escaping is the actual
// XSS defense here (the allowlist check is only about WHERE the browser ends up, not what's
// safe to print), so this is the test that actually exercises it.
func TestSSOLogin_RedirectURIIsJSEscaped(t *testing.T) {
	h := newTestSSOLoginHandler()
	malicious := `https://wotan.okemily.com/store.html?x=</script><script>alert(1)</script>`
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login", nil)
	req.URL.RawQuery = url.Values{"redirect_uri": {malicious}}.Encode()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Fatalf("redirect_uri was not safely escaped, raw payload leaked into response:\n%s", rec.Body.String())
	}
}
