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
