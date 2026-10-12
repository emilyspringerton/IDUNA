package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	googleverify "iduna/internal/auth/google"

	"github.com/google/uuid"
)

// SSOGoogleHandler is the "Sign in with Google" half of the hosted SSO login page
// (sso_login.go). Founder real-time, 2026-10-12: "we need to add SIGN IN WITH GOOGLE button to
// IDUNA IAM".
//
//	GET /api/v1/auth/sso/google/start?redirect_uri=<caller return URL>
//	      -> validates redirect_uri against the SAME allowlist the login page uses, sets a
//	         short-lived state cookie, 302s to Google's consent screen.
//	GET /api/v1/auth/sso/google/callback?code=...&state=...
//	      -> verifies state, exchanges the code, verifies the id_token, resolves/creates the
//	         player, issues the same JWT shape email/password login issues, then serves a tiny
//	         page that remembers the session in this origin's localStorage (so the login page's
//	         "Continue as <name>" works next time) and navigates to redirect_uri#sso_token=...
//
// The token comes back as a URL fragment, exactly like the password path (never a query string).
//
// Player resolution: an existing google-provider player by sub; else an email/password account
// whose address Google has verified (so "one IDUNA account, every app" holds across both ways of
// signing in); else a brand-new player with no game scope. An unverified Google email is never
// used to link to an existing account.
//
// SSO_GOOGLE_REDIRECT_URI (default BASE_URL + /api/v1/auth/sso/google/callback) must be listed
// under "Authorized redirect URIs" on the OAuth client in Google Cloud Console — a human-only
// step; until it is, Google rejects the start redirect with redirect_uri_mismatch.
type SSOGoogleHandler struct {
	GoogleClientID     string
	GoogleClientSecret string
	RedirectURI        string
	AllowedHosts       []string
	DB                 *sql.DB
	Players            *PlayerEmailAuthHandler // reused for JWT issuance so both paths mint identical claims

	// Test seam: swap the two network calls. Nil means the real Google endpoints.
	ExchangeCode func(ctx context.Context, code, redirectURI string) (idToken string, err error)
	VerifyToken  func(ctx context.Context, idToken, clientID string) (*googleverify.GoogleClaims, error)
}

const (
	ssoGoogleStateCookie    = "iduna_sso_google_state"
	ssoGoogleRedirectCookie = "iduna_sso_google_redirect"
	ssoGoogleCookiePath     = "/api/v1/auth/sso/google"
)

func (h *SSOGoogleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/callback") {
		h.callback(w, r)
		return
	}
	h.start(w, r)
}

func (h *SSOGoogleHandler) hostAllowed(host string) bool {
	return (&SSOLoginHandler{AllowedHosts: h.AllowedHosts}).hostAllowed(host)
}

// validRedirect applies the same rule SSOLoginHandler enforces: absolute URL, allowlisted host.
func (h *SSOGoogleHandler) validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.IsAbs() && h.hostAllowed(u.Hostname())
}

func (h *SSOGoogleHandler) start(w http.ResponseWriter, r *http.Request) {
	if h.GoogleClientID == "" || h.GoogleClientSecret == "" {
		http.Error(w, "Google sign-in is not configured", http.StatusServiceUnavailable)
		return
	}
	raw := r.URL.Query().Get("redirect_uri")
	if !h.validRedirect(raw) {
		http.Error(w, "redirect_uri is missing or its host is not allowed", http.StatusBadRequest)
		return
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := base64.RawURLEncoding.EncodeToString(b)
	for name, val := range map[string]string{ssoGoogleStateCookie: state, ssoGoogleRedirectCookie: raw} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: url.QueryEscape(val), Path: ssoGoogleCookiePath,
			MaxAge: 600, HttpOnly: true, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			SameSite: http.SameSiteLaxMode,
		})
	}

	authURL := "https://accounts.google.com/o/oauth2/v2/auth?" + url.Values{
		"client_id":     {h.GoogleClientID},
		"redirect_uri":  {h.RedirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"prompt":        {"select_account"},
	}.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *SSOGoogleHandler) callback(w http.ResponseWriter, r *http.Request) {
	clearSSOGoogleCookies(w)

	stateCookie, err := r.Cookie(ssoGoogleStateCookie)
	if err != nil || stateCookie.Value == "" || url.QueryEscape(r.URL.Query().Get("state")) != stateCookie.Value {
		http.Error(w, "invalid state — restart sign-in", http.StatusBadRequest)
		return
	}
	redirCookie, err := r.Cookie(ssoGoogleRedirectCookie)
	if err != nil {
		http.Error(w, "sign-in session expired — restart sign-in", http.StatusBadRequest)
		return
	}
	redirectURI, err := url.QueryUnescape(redirCookie.Value)
	if err != nil || !h.validRedirect(redirectURI) {
		http.Error(w, "redirect_uri is not allowed", http.StatusBadRequest)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		// User hit "cancel" on Google's screen — send them back to the login page, not a dead end.
		http.Redirect(w, r, "/api/v1/auth/sso/login?redirect_uri="+url.QueryEscape(redirectURI), http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	exchange := h.ExchangeCode
	if exchange == nil {
		exchange = h.exchangeCode
	}
	verify := h.VerifyToken
	if verify == nil {
		verify = googleverify.Verify
	}
	idToken, err := exchange(r.Context(), code, h.RedirectURI)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	claims, err := verify(r.Context(), idToken, h.GoogleClientID)
	if err != nil {
		http.Error(w, "Google id_token invalid", http.StatusUnauthorized)
		return
	}

	playerID, displayName, game, disabled, err := h.resolvePlayer(r.Context(), claims)
	if err != nil {
		http.Error(w, "could not resolve account", http.StatusInternalServerError)
		return
	}
	if disabled {
		http.Error(w, "this account has been disabled", http.StatusForbidden)
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE players SET last_seen=CURRENT_TIMESTAMP WHERE player_id=?`, playerID)

	token, err := h.Players.issueJWT(playerID, displayName, claims.Email, game)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = ssoGoogleDoneTemplate.Execute(w, map[string]string{
		"Token": token, "PlayerID": playerID, "DisplayName": displayName, "RedirectURI": redirectURI,
	})
}

func clearSSOGoogleCookies(w http.ResponseWriter) {
	for _, n := range []string{ssoGoogleStateCookie, ssoGoogleRedirectCookie} {
		http.SetCookie(w, &http.Cookie{Name: n, Value: "", Path: ssoGoogleCookiePath, MaxAge: -1})
	}
}

func (h *SSOGoogleHandler) resolvePlayer(ctx context.Context, c *googleverify.GoogleClaims) (id, name, game string, disabled bool, err error) {
	var gameCol, disabledAt sql.NullString
	display := c.Name
	if display == "" {
		display = c.Email
		if at := strings.Index(display, "@"); at > 0 {
			display = display[:at]
		}
	}

	// 1. Already signed in with this Google account before.
	err = h.DB.QueryRowContext(ctx,
		`SELECT player_id, display_name, game, disabled_at FROM players WHERE provider='google' AND provider_sub=?`, c.Sub,
	).Scan(&id, &name, &gameCol, &disabledAt)
	if err == nil {
		return id, name, gameCol.String, disabledAt.Valid, nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", false, err
	}

	// 2. Same person already registered with email+password — only when Google vouches for the address.
	if c.EmailVerified && c.Email != "" {
		err = h.DB.QueryRowContext(ctx,
			`SELECT p.player_id, p.display_name, p.game, p.disabled_at
			   FROM player_credentials pc JOIN players p ON p.player_id = pc.player_id
			  WHERE pc.email=?`, strings.ToLower(c.Email),
		).Scan(&id, &name, &gameCol, &disabledAt)
		if err == nil {
			return id, name, gameCol.String, disabledAt.Valid, nil
		}
		if err != sql.ErrNoRows {
			return "", "", "", false, err
		}
	}

	// 3. New player, unscoped (no game) — like an email/password signup with no game.
	id = uuid.New().String()
	_, err = h.DB.ExecContext(ctx,
		`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES (?,?,?,?,?)`,
		id, display, "google", c.Sub, c.Email,
	)
	return id, display, "", false, err
}

func (h *SSOGoogleHandler) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token",
		strings.NewReader(url.Values{
			"code":          {code},
			"client_id":     {h.GoogleClientID},
			"client_secret": {h.GoogleClientSecret},
			"redirect_uri":  {redirectURI},
			"grant_type":    {"authorization_code"},
		}.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.IDToken == "" {
		return "", fmt.Errorf("token endpoint returned no id_token")
	}
	return tok.IDToken, nil
}

// html/template escapes each value for its JS-string context; the page only ever navigates to a
// redirect_uri that already passed the allowlist above.
var ssoGoogleDoneTemplate = template.Must(template.New("sso_google_done").Parse(`<!doctype html>
<html lang="en"><head><meta charset="UTF-8"><meta name="robots" content="noindex, nofollow">
<title>Signing in &mdash; IDUNA</title></head>
<body><p>Signing you in&hellip;</p>
<script>
  var s = { token: {{.Token}}, player_id: {{.PlayerID}}, display_name: {{.DisplayName}} };
  try { localStorage.setItem('iduna_sso_session', JSON.stringify(s)); } catch (e) {}
  window.location.replace({{.RedirectURI}} + '#sso_token=' + encodeURIComponent(s.token) +
    '&player_id=' + encodeURIComponent(s.player_id) +
    '&display_name=' + encodeURIComponent(s.display_name));
</script></body></html>
`))
