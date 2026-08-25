package middleware

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"iduna/internal/auth/jwt"
)

type contextKey string

const claimsKey contextKey = "jwt_claims"

// RequireAuth returns middleware that validates an ES256 Bearer token.
// On success it stores the claims map in the request context.
func RequireAuth(keys *jwt.Keys) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeUnauthorized(w)
				return
			}
			token := strings.TrimPrefix(authHeader, "Bearer ")
			claims, err := jwt.Verify(keys, token)
			if err != nil {
				writeUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireCookieAuth is like RequireAuth but also accepts an iduna_session cookie.
// When auth fails for a browser request (Accept: text/html), it redirects to loginURL.
//
// sessionTTL enables sliding-expiration refresh: once less than half of sessionTTL
// remains before the cookie's JWT expires, a fresh cookie with a new full-TTL expiry
// is issued transparently on the response. Without this, a still-active admin session
// hits a silent hard cutoff at exactly sessionTTL after login — the next click just
// bounces to the login page with no explanation, which reads as an unexplained/
// "unexpected" logout during a long working session. Pass 0 to disable refresh.
func RequireCookieAuth(keys *jwt.Keys, loginURL string, sessionTTL time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerOrCookie(r)
			if token == "" {
				redirectOrJSON(w, r, loginURL)
				return
			}
			claims, err := jwt.Verify(keys, token)
			if err != nil {
				redirectOrJSON(w, r, loginURL)
				return
			}
			if sessionTTL > 0 {
				refreshCookieIfStale(w, keys, claims, sessionTTL)
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// refreshCookieIfStale re-signs claims with a fresh sessionTTL expiry and sets a new
// iduna_session cookie when less than half of sessionTTL remains on the current token.
// Silently no-ops on any error — a failed refresh just means the original cookie's own
// (still-valid) expiry applies, never a hard failure for the request in flight.
func refreshCookieIfStale(w http.ResponseWriter, keys *jwt.Keys, claims map[string]any, sessionTTL time.Duration) {
	exp, ok := claims["exp"]
	if !ok {
		return
	}
	var expUnix int64
	switch v := exp.(type) {
	case float64:
		expUnix = int64(v)
	case int64:
		expUnix = v
	default:
		return
	}
	remaining := time.Until(time.Unix(expUnix, 0))
	if remaining <= 0 || remaining > sessionTTL/2 {
		return
	}
	newExp := time.Now().UTC().Add(sessionTTL)
	claims["exp"] = newExp.Unix()
	token, err := jwt.Sign(keys, claims)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "iduna_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func bearerOrCookie(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if c, err := r.Cookie("iduna_session"); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

func redirectOrJSON(w http.ResponseWriter, r *http.Request, loginURL string) {
	if loginURL != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
		target := loginURL
		if path := r.URL.RequestURI(); path != "/" && path != loginURL {
			target += "?next=" + url.QueryEscape(path)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	writeUnauthorized(w)
}

// RequirePermission returns middleware that checks the "permissions" claim
// contains the required permission string. Returns 403 if not present.
func RequirePermission(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if !hasPermission(claims, perm) {
				writeForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFromContext returns the JWT claims stored in the context, or nil.
func ClaimsFromContext(ctx context.Context) map[string]any {
	v, _ := ctx.Value(claimsKey).(map[string]any)
	return v
}

// PermissionsFromContext returns the "permissions" slice from the JWT stored in context.
func PermissionsFromContext(ctx context.Context) []string {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return nil
	}
	perms, _ := claims["permissions"]
	switch v := perms.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, p := range v {
			if s, ok := p.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	}
	return nil
}

// SubjectFromContext returns the "sub" claim from the JWT stored in context.
func SubjectFromContext(ctx context.Context) string {
	claims := ClaimsFromContext(ctx)
	if claims == nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	return sub
}

func hasPermission(claims map[string]any, perm string) bool {
	if claims == nil {
		return false
	}
	perms, ok := claims["permissions"]
	if !ok {
		return false
	}
	switch v := perms.(type) {
	case []any:
		for _, p := range v {
			if s, ok := p.(string); ok && s == perm {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == perm {
				return true
			}
		}
	}
	return false
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"valid Bearer token required"}`))
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"code":"FORBIDDEN","message":"insufficient permissions"}`))
}
