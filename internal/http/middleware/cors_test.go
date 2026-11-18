package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func corsServe(t *testing.T, origins string, method, path, origin string, hdr map[string]string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSAllowlist(ParseOrigins(origins))(next)
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, called
}

func TestParseOrigins(t *testing.T) {
	got := ParseOrigins(" http://127.0.0.1:5500/ ,,HTTP://LocalHost:8090,")
	want := []string{"http://127.0.0.1:5500", "http://localhost:8090"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseOrigins = %q, want %q", got, want)
	}
	if ParseOrigins("") != nil {
		t.Fatalf("empty IDUNA_CORS_ORIGINS must give no origins")
	}
}

func TestCORSUnsetChangesNothing(t *testing.T) {
	rec, called := corsServe(t, "", http.MethodGet, "/health", "http://127.0.0.1:5500", nil)
	if !called || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Vary") != "" {
		t.Fatalf("unset: called=%v headers=%v", called, rec.Header())
	}
}

func TestCORSListedOriginReadsHealthAndAPI(t *testing.T) {
	for _, p := range []string{"/health", "/api/v1/health", "/api/v1/hs/me", "/.well-known/jwks.json"} {
		rec, called := corsServe(t, "http://127.0.0.1:5500", http.MethodGet, p, "http://127.0.0.1:5500", nil)
		if !called {
			t.Fatalf("%s: handler not called", p)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5500" {
			t.Fatalf("%s: Access-Control-Allow-Origin = %q", p, got)
		}
		if rec.Header().Get("Vary") != "Origin" {
			t.Fatalf("%s: Vary = %q", p, rec.Header().Get("Vary"))
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s: credentials must never be allowed", p)
		}
	}
}

func TestCORSOtherOriginAndOtherPathsUntouched(t *testing.T) {
	rec, called := corsServe(t, "http://127.0.0.1:5500", http.MethodGet, "/health", "https://evil.example", nil)
	if !called || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unlisted origin got CORS headers: %v", rec.Header())
	}
	rec, called = corsServe(t, "http://127.0.0.1:5500", http.MethodGet, "/blog/", "http://127.0.0.1:5500", nil)
	if !called || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("a page path got CORS headers: %v", rec.Header())
	}
}

func TestCORSPreflightIncludingLocalNetwork(t *testing.T) {
	rec, called := corsServe(t, "http://127.0.0.1:5500", http.MethodOptions, "/api/v1/hs/me", "http://127.0.0.1:5500",
		map[string]string{"Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization",
			"Access-Control-Request-Private-Network": "true"})
	if called {
		t.Fatalf("a preflight must be answered by the middleware, not the handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin":          "http://127.0.0.1:5500",
		"Access-Control-Allow-Headers":         "Authorization, Content-Type, Accept",
		"Access-Control-Allow-Private-Network": "true",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	// an unlisted origin's preflight goes to the handler as before
	_, called = corsServe(t, "http://127.0.0.1:5500", http.MethodOptions, "/api/v1/hs/me", "https://evil.example",
		map[string]string{"Access-Control-Request-Method": "GET"})
	if !called {
		t.Fatalf("an unlisted preflight must pass through")
	}
}

func TestCORSStar(t *testing.T) {
	rec, _ := corsServe(t, "*", http.MethodGet, "/health", "http://anything.test:1234", nil)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("* must allow any origin: %v", rec.Header())
	}
}
