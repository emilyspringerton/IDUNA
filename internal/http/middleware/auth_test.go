package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/middleware"
)

func TestRequireAuth_NoHeader(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	handler := middleware.RequireAuth(k)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_ValidToken(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	claims := map[string]any{
		"sub": "u1",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	}
	token, _ := jwt.Sign(k, claims)

	handler := middleware.RequireAuth(k)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := middleware.SubjectFromContext(r.Context())
		if sub != "u1" {
			t.Errorf("sub: got %q, want u1", sub)
		}
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequirePermission_Missing(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	claims := map[string]any{
		"sub":         "u1",
		"permissions": []any{"iduna.me.read"},
		"exp":         float64(time.Now().Add(time.Hour).Unix()),
	}
	token, _ := jwt.Sign(k, claims)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := middleware.RequireAuth(k)(middleware.RequirePermission("iduna.admin")(inner))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestRequireCookieAuth_NoRefreshWhenFresh(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	sessionTTL := time.Hour
	claims := map[string]any{
		"sub": "u1",
		"exp": float64(time.Now().Add(sessionTTL).Unix()), // fresh: full TTL remaining
	}
	token, _ := jwt.Sign(k, claims)

	handler := middleware.RequireCookieAuth(k, "/admin/login", sessionTTL)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "iduna_session", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Result().Cookies(); len(got) != 0 {
		t.Errorf("expected no refreshed cookie for a fresh session, got %d Set-Cookie header(s)", len(got))
	}
}

func TestRequireCookieAuth_RefreshesWhenStale(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	sessionTTL := time.Hour
	origExp := time.Now().Add(10 * time.Minute) // within the < TTL/2 refresh window
	claims := map[string]any{
		"sub": "u1",
		"exp": float64(origExp.Unix()),
	}
	token, _ := jwt.Sign(k, claims)

	handler := middleware.RequireCookieAuth(k, "/admin/login", sessionTTL)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "iduna_session", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200 (request in flight should succeed even while refreshing), got %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "iduna_session" {
		t.Fatalf("expected exactly one refreshed iduna_session cookie, got %+v", cookies)
	}
	newClaims, err := jwt.Verify(k, cookies[0].Value)
	if err != nil {
		t.Fatalf("refreshed cookie did not verify: %v", err)
	}
	newExp := int64(newClaims["exp"].(float64))
	if newExp <= origExp.Unix() {
		t.Errorf("refreshed exp %d should be later than original exp %d", newExp, origExp.Unix())
	}
	if sub, _ := newClaims["sub"].(string); sub != "u1" {
		t.Errorf("refreshed token lost claims: sub=%q", sub)
	}
}

func TestRequireCookieAuth_NoRefreshWhenTTLZero(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	claims := map[string]any{
		"sub": "u1",
		"exp": float64(time.Now().Add(time.Minute).Unix()), // about to expire
	}
	token, _ := jwt.Sign(k, claims)

	handler := middleware.RequireCookieAuth(k, "/admin/login", 0)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "iduna_session", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Result().Cookies(); len(got) != 0 {
		t.Errorf("sessionTTL=0 should disable refresh entirely, got %d Set-Cookie header(s)", len(got))
	}
}

func TestRequireCookieAuth_ExpiredRedirects(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	claims := map[string]any{
		"sub": "u1",
		"exp": float64(time.Now().Add(-time.Minute).Unix()), // already expired
	}
	token, _ := jwt.Sign(k, claims)

	handler := middleware.RequireCookieAuth(k, "/admin/login", time.Hour)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: "iduna_session", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for expired session, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/login?next=%2Fadmin" {
		t.Errorf("unexpected redirect location: %q", loc)
	}
}

func TestRequirePermission_Present(t *testing.T) {
	k, _ := jwt.GenerateKeys()
	claims := map[string]any{
		"sub":         "u1",
		"permissions": []any{"iduna.admin", "iduna.me.read"},
		"exp":         float64(time.Now().Add(time.Hour).Unix()),
	}
	token, _ := jwt.Sign(k, claims)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := middleware.RequireAuth(k)(middleware.RequirePermission("iduna.admin")(inner))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
