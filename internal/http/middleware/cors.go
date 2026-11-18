package middleware

// cors.go -- let pages on other origins read IDUNA's machine endpoints, for the origins IDUNA_CORS_ORIGINS lists.
//
// Founder real-time, 2026-11-17: "we need a local iduna to test against it needs to work so that it checks to see if
// there is a local iduna and connects to that and if not it attempts to use cloud iduna". A page served by IDUNA itself
// (WOTAN_SITE_DIR) or by nginx sending /api/ to IDUNA talks to it on its own origin and needs nothing here. A page served
// from anywhere else (a dev server on another port, a static host) can only find and use an IDUNA that answers its
// origin's CORS checks, which is what this adds, and only for the origins listed.
//
// IDUNA_CORS_ORIGINS: comma-separated exact origins (scheme://host[:port]), or "*" for any. Unset or empty: nothing
// changes, every response is exactly what it was before this file existed. Applies to /api/, /health and
// /.well-known/ only. Access-Control-Allow-Credentials is never sent: WOTAN pages carry IDUNA tokens as Bearer headers,
// and IDUNA's cookie-authenticated routes stay closed to other origins.
// A preflight from a listed origin is answered here (204), including Chrome's local-network check
// (Access-Control-Request-Private-Network), so handlers that 404 an OPTIONS request don't block it. A request from an
// origin that is not listed passes through untouched, and the browser keeps blocking it.

import (
	"net/http"
	"strings"
)

// ParseOrigins turns IDUNA_CORS_ORIGINS into the list CORSAllowlist takes: trimmed, lower-cased, no trailing slash,
// empty entries dropped.
func ParseOrigins(csv string) []string {
	var out []string
	for _, o := range strings.Split(csv, ",") {
		o = strings.TrimRight(strings.ToLower(strings.TrimSpace(o)), "/")
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

func corsPath(p string) bool {
	return p == "/health" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/.well-known/")
}

// CORSAllowlist returns middleware that answers CORS for the given origins (see ParseOrigins). With no origins it
// returns next unchanged.
func CORSAllowlist(origins []string) func(http.Handler) http.Handler {
	anyOrigin := false
	allowed := map[string]bool{}
	for _, o := range origins {
		if o == "*" {
			anyOrigin = true
		}
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !corsPath(r.URL.Path) || !(anyOrigin || allowed[strings.TrimRight(strings.ToLower(origin), "/")]) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			if anyOrigin {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
				h.Set("Access-Control-Max-Age", "600")
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					h.Set("Access-Control-Allow-Private-Network", "true")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
