package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	googleverify "iduna/internal/auth/google"
	"iduna/internal/http/handlers"
)

func newSSOGoogle(t *testing.T, claims *googleverify.GoogleClaims) (*handlers.SSOGoogleHandler, func()) {
	t.Helper()
	db := newTestEmailAuthDB(t)
	h := &handlers.SSOGoogleHandler{
		GoogleClientID: "cid", GoogleClientSecret: "sec",
		RedirectURIs: map[string]string{
			"example.com": "https://example.com/api/v1/auth/sso/google/callback",
			"iam.example": "https://iam.example/api/v1/auth/sso/google/callback",
		},
		DefaultHost:  "example.com",
		AllowedHosts: []string{"wotan.okemily.com", "localhost"},
		DB:           db,
		Players:      &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"},
		ExchangeCode: func(context.Context, string, string) (string, error) { return "idtok", nil },
		VerifyToken:  func(context.Context, string, string) (*googleverify.GoogleClaims, error) { return claims, nil },
	}
	return h, func() { db.Close() }
}

// startFlow runs /start and returns the cookies + the state sent to Google.
func startFlow(t *testing.T, h *handlers.SSOGoogleHandler, redirect string) ([]*http.Cookie, string) {
	t.Helper()
	cookies, state, _ := startFlowOnHost(t, h, "", redirect)
	return cookies, state
}

// startFlowOnHost is startFlow with an explicit request Host (defaults to httptest's "example.com"
// when host == ""), also returning the redirect_uri Google was actually sent.
func startFlowOnHost(t *testing.T, h *handlers.SSOGoogleHandler, host, redirect string) ([]*http.Cookie, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/start?redirect_uri="+url.QueryEscape(redirect), nil)
	if host != "" {
		req.Host = host
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("start: status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Host != "accounts.google.com" {
		t.Fatalf("start redirected to %q", loc.Host)
	}
	return w.Result().Cookies(), loc.Query().Get("state"), loc.Query().Get("redirect_uri")
}

func callback(h *handlers.SSOGoogleHandler, cookies []*http.Cookie, state string) *httptest.ResponseRecorder {
	return callbackOnHost(h, "", cookies, state)
}

func callbackOnHost(h *handlers.SSOGoogleHandler, host string, cookies []*http.Cookie, state string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/callback?code=abc&state="+url.QueryEscape(state), nil)
	if host != "" {
		req.Host = host
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSSOGoogle_StartRejectsUnallowlistedRedirect(t *testing.T) {
	h, done := newSSOGoogle(t, &googleverify.GoogleClaims{})
	defer done()
	for _, bad := range []string{"", "https://evil.example/x", "/relative"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/google/start?redirect_uri="+url.QueryEscape(bad), nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("redirect_uri %q: want 400, got %d", bad, w.Code)
		}
	}
}

func TestSSOGoogle_HappyPathCreatesPlayerAndReturnsToken(t *testing.T) {
	h, done := newSSOGoogle(t, &googleverify.GoogleClaims{Sub: "g-1", Email: "a@example.com", EmailVerified: true, Name: "Ada"})
	defer done()
	cookies, state := startFlow(t, h, "https://wotan.okemily.com/store.html")
	w := callback(h, cookies, state)
	if w.Code != http.StatusOK {
		t.Fatalf("callback: status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "iduna_sso_session") || !strings.Contains(body, "wotan.okemily.com") {
		t.Fatalf("done page missing session/redirect: %s", body)
	}
	var n int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM players WHERE provider='google' AND provider_sub='g-1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 google player, got %d", n)
	}
	// Signing in again must reuse the same player, not create a second.
	cookies, state = startFlow(t, h, "https://wotan.okemily.com/store.html")
	if w := callback(h, cookies, state); w.Code != http.StatusOK {
		t.Fatalf("second callback: %d", w.Code)
	}
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&n)
	if n != 1 {
		t.Fatalf("second sign-in created a duplicate player (%d rows)", n)
	}
}

// TestSSOGoogle_RedirectURIMatchesLoginHost regression-covers the 2026-10-12 live incident: the
// login page is reachable on more than one registered host (e.g. the dedicated iam.okemily.com
// domain), and a *fixed* redirect_uri sent every Google callback back to a different host than
// the one that set the state/redirect cookies -- the cookies are host-scoped, so the browser had
// nothing to send back, and every real sign-in failed "invalid state" before the code exchange
// ever ran. The fix picks the registered redirect_uri matching the request's own Host, for both
// legs, so the callback always lands on the same origin that started the flow.
func TestSSOGoogle_RedirectURIMatchesLoginHost(t *testing.T) {
	h, done := newSSOGoogle(t, &googleverify.GoogleClaims{Sub: "g-1", Email: "a@example.com", EmailVerified: true})
	defer done()

	cookies, state, redirectURI := startFlowOnHost(t, h, "iam.example", "https://wotan.okemily.com/")
	if redirectURI != "https://iam.example/api/v1/auth/sso/google/callback" {
		t.Fatalf("authorize redirect_uri = %q, want the iam.example entry", redirectURI)
	}
	// Google always calls back on the exact redirect_uri it was given above -- same host.
	w := callbackOnHost(h, "iam.example", cookies, state)
	if w.Code != http.StatusOK {
		t.Fatalf("callback on the same host that started the flow: status=%d body=%s", w.Code, w.Body.String())
	}

	// A second flow started on the OTHER registered host must get ITS OWN redirect_uri, not the
	// first one's -- this is the exact bug: a single fixed value would have passed the first
	// assertion above by coincidence and only broken here.
	cookies2, state2, redirectURI2 := startFlowOnHost(t, h, "example.com", "https://wotan.okemily.com/")
	if redirectURI2 != "https://example.com/api/v1/auth/sso/google/callback" {
		t.Fatalf("authorize redirect_uri = %q, want the example.com entry", redirectURI2)
	}
	if w := callbackOnHost(h, "example.com", cookies2, state2); w.Code != http.StatusOK {
		t.Fatalf("callback on example.com: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSSOGoogle_StateMismatchRejected(t *testing.T) {
	h, done := newSSOGoogle(t, &googleverify.GoogleClaims{Sub: "g-1", Email: "a@example.com", EmailVerified: true})
	defer done()
	cookies, _ := startFlow(t, h, "https://wotan.okemily.com/")
	if w := callback(h, cookies, "forged"); w.Code != http.StatusBadRequest {
		t.Fatalf("forged state: want 400, got %d", w.Code)
	}
	if w := callback(h, nil, "anything"); w.Code != http.StatusBadRequest {
		t.Fatalf("no cookies: want 400, got %d", w.Code)
	}
}

func TestSSOGoogle_DisabledAccountRejected(t *testing.T) {
	h, done := newSSOGoogle(t, &googleverify.GoogleClaims{Sub: "g-2", Email: "b@example.com", EmailVerified: true, Name: "B"})
	defer done()
	if _, err := h.DB.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, email, disabled_at)
		VALUES ('p-dis','B','google','g-2','b@example.com','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	cookies, state := startFlow(t, h, "https://wotan.okemily.com/")
	if w := callback(h, cookies, state); w.Code != http.StatusForbidden {
		t.Fatalf("disabled: want 403, got %d", w.Code)
	}
}

func TestSSOGoogle_LinksByVerifiedEmailOnly(t *testing.T) {
	for _, verified := range []bool{true, false} {
		h, done := newSSOGoogle(t, &googleverify.GoogleClaims{Sub: "g-3", Email: "c@example.com", EmailVerified: verified, Name: "C"})
		_, _ = h.DB.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES ('p-email','C','email','c@example.com','c@example.com')`)
		_, _ = h.DB.Exec(`INSERT INTO player_credentials (player_id, email, password_hash) VALUES ('p-email','c@example.com','x')`)
		cookies, state := startFlow(t, h, "https://wotan.okemily.com/")
		if w := callback(h, cookies, state); w.Code != http.StatusOK {
			t.Fatalf("verified=%v callback: %d", verified, w.Code)
		}
		var n int
		_ = h.DB.QueryRow(`SELECT COUNT(*) FROM players`).Scan(&n)
		if verified && n != 1 {
			t.Errorf("verified email should link to existing account, got %d players", n)
		}
		if !verified && n != 2 {
			t.Errorf("unverified email must NOT link to existing account, got %d players", n)
		}
		done()
	}
}
