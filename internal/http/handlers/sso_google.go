package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	googleverify "iduna/internal/auth/google"
	"iduna/internal/auth/jwt"

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

var errSignInUnlinked = errors.New("sign-in method unlinked")

const (
	ssoGoogleStateCookie    = "iduna_sso_google_state"
	ssoGoogleRedirectCookie = "iduna_sso_google_redirect"
	ssoGoogleLinkCookie     = "iduna_sso_google_link"
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
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	// Link mode (IDENTITY_NORTHSTAR section 2): attach this Google account to the ALREADY signed-in player
	// instead of signing in. The session comes from Authorization: Bearer or, for a full-page navigation,
	// ?access_token=; it is bound into a signed, short-lived cookie together with the state nonce, so the
	// callback can only complete in the same browser, as that player (CSRF / link-hijack defence).
	linkMode := r.URL.Query().Get("mode") == "link"
	if linkMode {
		var keys *jwt.Keys
		if h.Players != nil {
			keys = h.Players.Keys
		}
		pid := (&IdentityOAuthHandler{Keys: keys}).sessionSub(r)
		if pid == "" || keys == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in with IDUNA first", "code": "unauthenticated"})
			return
		}
		now := time.Now().UTC()
		lc, err := jwt.Sign(keys, map[string]any{
			"aud": oauthStateAud, "iss": "iduna", "iat": now.Unix(), "exp": now.Add(oauthStateTTL).Unix(),
			"prov": idProvGoogle, "mode": "link", "nonce": state, "pid": pid,
		})
		if err != nil {
			http.Error(w, "could not start sign-in", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: ssoGoogleLinkCookie, Value: lc, Path: ssoGoogleCookiePath,
			MaxAge: 600, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	}

	for name, val := range map[string]string{ssoGoogleStateCookie: state, ssoGoogleRedirectCookie: raw} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: url.QueryEscape(val), Path: ssoGoogleCookiePath,
			MaxAge: 600, HttpOnly: true, Secure: secure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	authURL := "https://accounts.google.com/o/oauth2/v2/auth?" + url.Values{
		"client_id":     {h.GoogleClientID},
		"redirect_uri":  {h.RedirectURI},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"state":         {state},
		"prompt":        {"select_account"},
	}.Encode()
	if linkMode && (r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Accept"), "application/json")) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"authorize_url": authURL, "mode": "link", "expires_in": 600})
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *SSOGoogleHandler) callback(w http.ResponseWriter, r *http.Request) {
	clearSSOGoogleCookies(w)
	linkPID := h.linkCookiePlayer(r)

	stateCookie, err := r.Cookie(ssoGoogleStateCookie)
	qState := url.QueryEscape(r.URL.Query().Get("state"))
	if err != nil || stateCookie.Value == "" || qState != stateCookie.Value {
		// TEMP DIAGNOSTIC (2026-10-12, remove once the real-world "invalid state" report is root-caused):
		// no PII here, just whether/why the cookie<->query state match failed.
		cookieNames := make([]string, 0, len(r.Cookies()))
		for _, c := range r.Cookies() {
			cookieNames = append(cookieNames, c.Name)
		}
		cookieLen := -1
		if stateCookie != nil {
			cookieLen = len(stateCookie.Value)
		}
		log.Printf("sso_google: state check failed — cookie_err=%v cookie_present=%v cookies_seen=%v qstate_len=%d cookie_len=%d ua=%q",
			err, stateCookie != nil, cookieNames, len(qState), cookieLen, r.UserAgent())
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

	if linkPID != "" {
		h.finishLink(w, r, redirectURI, linkPID, claims)
		return
	}

	playerID, displayName, game, disabled, err := h.resolvePlayer(r.Context(), claims)
	if errors.Is(err, errSignInUnlinked) {
		http.Error(w, "this sign-in method was unlinked from its account; sign in another way and link it again", http.StatusForbidden)
		return
	}
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

// linkCookiePlayer returns the player a link-mode flow was started for ("" = a normal sign-in). The cookie is
// a signed JWT whose nonce must equal this callback's state.
func (h *SSOGoogleHandler) linkCookiePlayer(r *http.Request) string {
	ck, err := r.Cookie(ssoGoogleLinkCookie)
	if err != nil || ck.Value == "" || h.Players == nil || h.Players.Keys == nil {
		return ""
	}
	claims, err := jwt.Verify(h.Players.Keys, ck.Value)
	if err != nil || claims["aud"] != oauthStateAud || claims["prov"] != idProvGoogle || claims["mode"] != "link" {
		return ""
	}
	nonce, _ := claims["nonce"].(string)
	if nonce == "" || nonce != r.URL.Query().Get("state") {
		return ""
	}
	pid, _ := claims["pid"].(string)
	return pid
}

// finishLink attaches the verified Google identity to the signed-in player the flow was started for.
func (h *SSOGoogleHandler) finishLink(w http.ResponseWriter, r *http.Request, redirectURI, pid string, c *googleverify.GoogleClaims) {
	if !c.EmailVerified {
		fragmentRedirect(w, r, redirectURI, map[string]string{"link_error": "email_unverified", "provider": idProvGoogle})
		return
	}
	disp := c.Email
	if disp == "" {
		disp = c.Name
	}
	id, out, err := linkIdentity(h.DB, pid, idProvGoogle, c.Sub, disp, "{}", true)
	switch {
	case errors.Is(err, errIdentityTaken):
		var owner string
		_ = h.DB.QueryRow(`SELECT player_id FROM player_identities WHERE provider='google' AND provider_sub=?`, c.Sub).Scan(&owner)
		identityAudit(r.Context(), h.DB, nil, pid, pid, "link.identity_taken",
			map[string]any{"provider": idProvGoogle, "identity_id": id, "owner_player_id": owner}, false)
		fragmentRedirect(w, r, redirectURI, map[string]string{"link_error": "identity_taken", "provider": idProvGoogle})
	case err != nil:
		fragmentRedirect(w, r, redirectURI, map[string]string{"link_error": "server_error", "provider": idProvGoogle})
	default:
		act := "link"
		if out == linkRefreshed {
			act = "link.refresh"
		}
		identityAudit(r.Context(), h.DB, nil, pid, pid, act, map[string]any{"provider": idProvGoogle, "identity_id": id}, false)
		fragmentRedirect(w, r, redirectURI, map[string]string{"linked": idProvGoogle, "identity_id": fmt.Sprint(id)})
	}
}

func clearSSOGoogleCookies(w http.ResponseWriter) {
	for _, n := range []string{ssoGoogleStateCookie, ssoGoogleRedirectCookie, ssoGoogleLinkCookie} {
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

	// 0. A Google identity linked to some WOTAN player (player_identities, IDENTITY_NORTHSTAR section 3):
	// it resolves to that player whatever provider the player was created with.
	err = h.DB.QueryRowContext(ctx,
		`SELECT p.player_id, p.display_name, p.game, p.disabled_at
		   FROM player_identities i JOIN players p ON p.player_id = i.player_id
		  WHERE i.provider='google' AND i.provider_sub=? AND i.status='active'`, c.Sub,
	).Scan(&id, &name, &gameCol, &disabledAt)
	if err == nil {
		_, _ = h.DB.ExecContext(ctx, `UPDATE player_identities SET last_login_at=? WHERE provider='google' AND provider_sub=?`, sqlNowUTC(), c.Sub)
		return id, name, gameCol.String, disabledAt.Valid, nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", false, err
	}

	// 0b. Unlinked on purpose (tombstone): the owner removed this door, so it must not silently re-open
	// through the legacy players-row match below. Linking it again (from a signed-in session) revives it.
	var tomb int
	if h.DB.QueryRowContext(ctx, `SELECT 1 FROM player_identities WHERE provider='google' AND provider_sub=? AND status='revoked'`, c.Sub).Scan(&tomb) == nil {
		return "", "", "", false, errSignInUnlinked
	}

	// 1. Already signed in with this Google account before.
	err = h.DB.QueryRowContext(ctx,
		`SELECT player_id, display_name, game, disabled_at FROM players WHERE provider='google' AND provider_sub=?`, c.Sub,
	).Scan(&id, &name, &gameCol, &disabledAt)
	if err == nil {
		h.recordGoogleIdentity(ctx, id, c)
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
			h.recordGoogleIdentity(ctx, id, c)
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
	if err == nil {
		h.recordGoogleIdentity(ctx, id, c)
	}
	return id, display, "", false, err
}

// recordGoogleIdentity keeps player_identities in step with every Google login path (best effort: the login
// itself never fails on it, and an identity already owned by another player is left alone).
func (h *SSOGoogleHandler) recordGoogleIdentity(ctx context.Context, playerID string, c *googleverify.GoogleClaims) {
	disp := c.Email
	if disp == "" {
		disp = c.Name
	}
	_, _ = h.DB.ExecContext(ctx, `INSERT OR IGNORE INTO player_identities (player_id, provider, provider_sub, display, verified, linked_at, last_login_at, status)
		VALUES (?, 'google', ?, ?, ?, ?, ?, 'active')`, playerID, c.Sub, disp, boolInt(c.EmailVerified), sqlNowUTC(), sqlNowUTC())
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
