package handlers

// identity_oauth.go -- Battle.net SSO (login + link) and Twitch SSO (login + link, IQ-2 resolved
// 2026-10-12) (IDENTITY_NORTHSTAR sections 2 and 4.1/4.2). Modeled on sso_google.go: same redirect
// allowlist (SSOLoginHandler.hostAllowed), injectable network seams, tokens handed back in a URL
// fragment, never a query string. Twitch still requests zero scopes either way (I8): identity only,
// never the stream key, never email.
//
//	GET  /api/v1/auth/sso/{battlenet,twitch}/start?redirect_uri=...[&mode=link][&region=us|eu|kr|tw]
//	        no mode (or mode=login): a plain sign-in, same shape as sso_google.go -- mints an IDUNA
//	        session JWT via finishLogin. mode=link needs the player session: Authorization: Bearer
//	        <token> (fetch -> 200 {"authorize_url": ...}, the page then navigates there) or, for a
//	        plain full-page navigation, ?access_token=<token> (302 straight to the provider). Both
//	        set the signed flow cookie. Link results come back as fragment keys:
//	        #linked=<provider>&identity_id=N or #link_error=<code>.
//	GET  /api/v1/auth/sso/{battlenet,twitch}/callback
//	GET|POST /api/v1/hs/me/identities/{battlenet|twitch}/start   (link mode, via HSHandler, bearer required)
//
// CSRF / link-hijack defence: the flow cookie is an ES256 JWT (aud iduna-oauth-state, deliberately no `sub`)
// minted at start carrying provider, mode, nonce, the authenticated player_id (link mode) and the
// redirect_uri. The `state` query parameter is the nonce. The callback requires cookie AND state to agree,
// so a callback can only complete in the same browser that began the flow, as the same authenticated player.
// Nothing is ever auto-linked by email or BattleTag string.
//
// Disabled (404 provider_not_configured) unless BNET_CLIENT_ID/BNET_CLIENT_SECRET (resp. TWITCH_*) are set --
// registering those applications is a human-only step (develop.battle.net, dev.twitch.tv).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"iduna/internal/auth/jwt"
	"iduna/internal/userlog"
)

const (
	oauthStateAud   = "iduna-oauth-state"
	oauthStateTTL   = 10 * time.Minute
	bnetAuthorize   = "https://oauth.battle.net/authorize"
	bnetToken       = "https://oauth.battle.net/token"
	bnetUserinfo    = "https://oauth.battle.net/userinfo"
	twitchAuthorize = "https://id.twitch.tv/oauth2/authorize"
	twitchToken     = "https://id.twitch.tv/oauth2/token"
	twitchUsersURL  = "https://api.twitch.tv/helix/users"
)

var bnetRegions = map[string]bool{"us": true, "eu": true, "kr": true, "tw": true}

// OAuthUser is what a provider's userinfo call yields.
type OAuthUser struct {
	Sub     string
	Display string // BattleTag (battlenet) or Twitch login
	Extra   map[string]any
}

// IdentityOAuthHandler serves the Battle.net / Twitch OAuth start+callback routes.
type IdentityOAuthHandler struct {
	DB           *sql.DB
	Keys         *jwt.Keys
	Players      *PlayerEmailAuthHandler // reused for JWT issuance so every login path mints identical claims
	AllowedHosts []string
	EventLog     userlog.EventLog

	BnetClientID, BnetClientSecret, BnetRedirectURI       string
	TwitchClientID, TwitchClientSecret, TwitchRedirectURI string

	// Test seams: swap the network calls. Nil means the real endpoints.
	BnetExchange   func(ctx context.Context, code, redirectURI string) (accessToken string, err error)
	BnetUserInfo   func(ctx context.Context, accessToken string) (*OAuthUser, error)
	TwitchExchange func(ctx context.Context, code, redirectURI string) (accessToken string, err error)
	TwitchUserInfo func(ctx context.Context, accessToken string) (*OAuthUser, error)
}

func (h *IdentityOAuthHandler) configured(prov string) bool {
	switch prov {
	case idProvBattlenet:
		return h.BnetClientID != "" && h.BnetClientSecret != ""
	case idProvTwitch:
		return h.TwitchClientID != "" && h.TwitchClientSecret != ""
	}
	return false
}

// ProviderStatus is shown in the identities list so the UI can hide unconfigured buttons.
func (h *IdentityOAuthHandler) ProviderStatus() map[string]any {
	if h == nil {
		return map[string]any{"battlenet": map[string]any{"configured": false}, "twitch": map[string]any{"configured": false}}
	}
	return map[string]any{
		"battlenet": map[string]any{"configured": h.configured(idProvBattlenet)},
		"twitch":    map[string]any{"configured": h.configured(idProvTwitch)},
	}
}

func (h *IdentityOAuthHandler) validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.IsAbs() && (&SSOLoginHandler{AllowedHosts: h.AllowedHosts}).hostAllowed(u.Hostname())
}

func (h *IdentityOAuthHandler) redirectURIFor(prov string) string {
	if prov == idProvTwitch {
		return h.TwitchRedirectURI
	}
	return h.BnetRedirectURI
}

func oauthCookieName(prov string) string { return "iduna_oauth_" + prov }
func oauthCookiePath(prov string) string { return "/api/v1/auth/sso/" + prov }

func notConfigured(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "this sign-in provider is not configured", "code": "provider_not_configured"})
}

// ServeHTTP routes /api/v1/auth/sso/{battlenet,twitch}/{start,callback}.
func (h *IdentityOAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/auth/sso/"), "/")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || (parts[0] != idProvBattlenet && parts[0] != idProvTwitch) {
		http.NotFound(w, r)
		return
	}
	prov := parts[0]
	if !h.configured(prov) {
		notConfigured(w)
		return
	}
	switch parts[1] {
	case "start":
		h.start(w, r, prov, "")
	case "callback":
		h.callback(w, r, prov)
	default:
		http.NotFound(w, r)
	}
}

// start begins a flow. linkSub is the authenticated player (link mode) when called from the hs route; for
// the public start route it is resolved from the bearer when mode=link.
func (h *IdentityOAuthHandler) start(w http.ResponseWriter, r *http.Request, prov, linkSub string) {
	q := r.URL.Query()
	mode := "login"
	if q.Get("mode") == "link" || linkSub != "" {
		mode = "link"
	}
	redir := q.Get("redirect_uri")
	if redir == "" && r.Method == http.MethodPost {
		var in struct {
			RedirectURI string `json:"redirect_uri"`
			Region      string `json:"region"`
		}
		if hsDecode(r, &in) == nil {
			redir = in.RedirectURI
			if q.Get("region") == "" && in.Region != "" {
				q.Set("region", in.Region)
			}
		}
	}
	if !h.validRedirect(redir) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "redirect_uri is missing or its host is not allowed", "code": "bad_redirect"})
		return
	}
	pid := ""
	if mode == "link" {
		pid = linkSub
		if pid == "" {
			pid = h.sessionSub(r)
		}
		if pid == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in with IDUNA first", "code": "unauthenticated"})
			return
		}
	}
	region := strings.ToLower(q.Get("region"))
	if region != "" && !bnetRegions[region] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "region must be us, eu, kr or tw", "code": "bad_region"})
		return
	}
	if h.Keys == nil {
		http.Error(w, "signing keys unavailable", http.StatusServiceUnavailable)
		return
	}
	nb := make([]byte, 18)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	now := time.Now().UTC()
	cookie, err := jwt.Sign(h.Keys, map[string]any{
		"aud": oauthStateAud, "iss": "iduna", "iat": now.Unix(), "exp": now.Add(oauthStateTTL).Unix(),
		"prov": prov, "mode": mode, "nonce": nonce, "pid": pid, "redir": redir, "region": region,
	})
	if err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthCookieName(prov), Value: cookie, Path: oauthCookiePath(prov),
		MaxAge: int(oauthStateTTL.Seconds()), HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
	var authURL string
	switch prov {
	case idProvBattlenet:
		authURL = bnetAuthorize + "?" + url.Values{
			"client_id": {h.BnetClientID}, "redirect_uri": {h.BnetRedirectURI}, "response_type": {"code"},
			"scope": {"openid"}, "state": {nonce},
		}.Encode()
	case idProvTwitch:
		// No scopes at all: identity only. Never the stream key, never email (I8).
		authURL = twitchAuthorize + "?" + url.Values{
			"client_id": {h.TwitchClientID}, "redirect_uri": {h.TwitchRedirectURI}, "response_type": {"code"},
			"scope": {""}, "state": {nonce}, "force_verify": {"true"},
		}.Encode()
	}
	// A fetch (Authorization header, POST, or Accept: application/json) gets the URL as JSON; a plain browser
	// navigation (e.g. carrying access_token=) is redirected straight to the provider.
	if r.Method == http.MethodPost || r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"authorize_url": authURL, "mode": mode, "expires_in": int(oauthStateTTL.Seconds())})
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// sessionSub resolves the authenticated player for a link-mode start: the Authorization header, or (for a
// full-page navigation, which cannot carry one) an access_token query parameter. The token is only ever
// verified here and its subject read; it is never stored, logged or echoed.
func (h *IdentityOAuthHandler) sessionSub(r *http.Request) string {
	hv := &HSHandler{Keys: h.Keys}
	if s := hv.viewer(r).sub; s != "" {
		return s
	}
	if t := r.URL.Query().Get("access_token"); t != "" {
		r2 := r.Clone(r.Context())
		r2.Header = r.Header.Clone()
		r2.Header.Set("Authorization", "Bearer "+t)
		return hv.viewer(r2).sub
	}
	return ""
}

func (h *IdentityOAuthHandler) clearCookie(w http.ResponseWriter, prov string) {
	http.SetCookie(w, &http.Cookie{Name: oauthCookieName(prov), Value: "", Path: oauthCookiePath(prov), MaxAge: -1})
}

func fragmentRedirect(w http.ResponseWriter, r *http.Request, base string, kv map[string]string) {
	u, err := url.Parse(base)
	if err != nil {
		http.Error(w, "bad redirect", http.StatusBadRequest)
		return
	}
	f := url.Values{}
	for k, v := range kv {
		f.Set(k, v)
	}
	u.Fragment = f.Encode()
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *IdentityOAuthHandler) callback(w http.ResponseWriter, r *http.Request, prov string) {
	h.clearCookie(w, prov)
	ck, err := r.Cookie(oauthCookieName(prov))
	if err != nil || ck.Value == "" || h.Keys == nil {
		http.Error(w, "sign-in session expired - restart sign-in", http.StatusBadRequest)
		return
	}
	claims, err := jwt.Verify(h.Keys, ck.Value)
	if err != nil || claims["aud"] != oauthStateAud || claims["prov"] != prov {
		http.Error(w, "invalid state - restart sign-in", http.StatusBadRequest)
		return
	}
	nonce, _ := claims["nonce"].(string)
	state := r.URL.Query().Get("state")
	if nonce == "" || state != nonce {
		http.Error(w, "invalid state - restart sign-in", http.StatusBadRequest)
		return
	}
	redir, _ := claims["redir"].(string)
	if !h.validRedirect(redir) {
		http.Error(w, "redirect_uri is not allowed", http.StatusBadRequest)
		return
	}
	mode, _ := claims["mode"].(string)
	pid, _ := claims["pid"].(string)
	region, _ := claims["region"].(string)
	if mode != "login" && mode != "link" || (mode == "link" && pid == "") {
		http.Error(w, "invalid state - restart sign-in", http.StatusBadRequest)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		if mode == "link" {
			fragmentRedirect(w, r, redir, map[string]string{"link_error": "cancelled", "provider": prov})
			return
		}
		http.Redirect(w, r, "/api/v1/auth/sso/login?redirect_uri="+url.QueryEscape(redir), http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	user, err := h.fetchUser(r.Context(), prov, code)
	if err != nil || user == nil || user.Sub == "" {
		if mode == "link" {
			fragmentRedirect(w, r, redir, map[string]string{"link_error": "provider_failed", "provider": prov})
			return
		}
		http.Error(w, "sign-in with "+prov+" failed", http.StatusBadGateway)
		return
	}
	extra := map[string]any{}
	for k, v := range user.Extra {
		extra[k] = v
	}
	if region != "" {
		extra["region_hint"] = region
	}
	extraJSON, _ := json.Marshal(extra)

	if mode == "link" {
		h.finishLink(w, r, prov, redir, pid, user, string(extraJSON))
		return
	}
	if !loginDoorProviders[prov] {
		http.Error(w, "this provider cannot be used to sign in", http.StatusBadRequest)
		return
	}
	h.finishLogin(w, r, prov, redir, user, string(extraJSON))
}

func (h *IdentityOAuthHandler) fetchUser(ctx context.Context, prov, code string) (*OAuthUser, error) {
	var exch func(context.Context, string, string) (string, error)
	var info func(context.Context, string) (*OAuthUser, error)
	switch prov {
	case idProvBattlenet:
		exch, info = h.BnetExchange, h.BnetUserInfo
		if exch == nil {
			exch = h.bnetExchange
		}
		if info == nil {
			info = bnetUserInfo
		}
	case idProvTwitch:
		exch, info = h.TwitchExchange, h.TwitchUserInfo
		if exch == nil {
			exch = h.twitchExchange
		}
		if info == nil {
			info = h.twitchUserInfo
		}
	default:
		return nil, fmt.Errorf("unknown provider")
	}
	tok, err := exch(ctx, code, h.redirectURIFor(prov))
	if err != nil {
		return nil, err
	}
	return info(ctx, tok)
}

func (h *IdentityOAuthHandler) finishLink(w http.ResponseWriter, r *http.Request, prov, redir, pid string, u *OAuthUser, extra string) {
	id, out, err := linkIdentity(h.DB, pid, prov, u.Sub, u.Display, extra, true)
	switch {
	case errors.Is(err, errIdentityTaken):
		// Non-leaking: the user is told only that it is taken. Operators get the dispute in the audit log.
		var owner string
		_ = h.DB.QueryRow(`SELECT player_id FROM player_identities WHERE provider=? AND provider_sub=?`, prov, u.Sub).Scan(&owner)
		identityAudit(r.Context(), h.DB, h.EventLog, pid, pid, "link.identity_taken",
			map[string]any{"provider": prov, "identity_id": id, "owner_player_id": owner}, false)
		fragmentRedirect(w, r, redir, map[string]string{"link_error": "identity_taken", "provider": prov})
	case errors.Is(err, errTwitchAlreadyLinked):
		fragmentRedirect(w, r, redir, map[string]string{"link_error": "twitch_already_linked", "provider": prov})
	case err != nil:
		fragmentRedirect(w, r, redir, map[string]string{"link_error": "server_error", "provider": prov})
	default:
		act := "link"
		if out == linkRefreshed {
			act = "link.refresh"
		}
		identityAudit(r.Context(), h.DB, h.EventLog, pid, pid, act, map[string]any{"provider": prov, "identity_id": id}, false)
		fragmentRedirect(w, r, redir, map[string]string{"linked": prov, "identity_id": fmt.Sprint(id)})
	}
}

func (h *IdentityOAuthHandler) finishLogin(w http.ResponseWriter, r *http.Request, prov, redir string, u *OAuthUser, extra string) {
	playerID, display, game, disabled, err := h.resolveLogin(r.Context(), prov, u, extra)
	if err != nil {
		http.Error(w, "could not resolve account", http.StatusInternalServerError)
		return
	}
	if disabled {
		http.Error(w, "this account has been disabled", http.StatusForbidden)
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE players SET last_seen=CURRENT_TIMESTAMP WHERE player_id=?`, playerID)
	if h.Players == nil {
		http.Error(w, "token issuer unavailable", http.StatusServiceUnavailable)
		return
	}
	token, err := h.Players.issueJWT(playerID, display, "", game)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}
	identityAudit(r.Context(), h.DB, h.EventLog, playerID, playerID, "login", map[string]any{"provider": prov}, false)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = ssoGoogleDoneTemplate.Execute(w, map[string]string{
		"Token": token, "PlayerID": playerID, "DisplayName": display, "RedirectURI": redir,
	})
}

// resolveLogin: an active identity resolves to its player_id (whatever provider linked it); an unknown one
// creates a brand-new player with no game scope and no email.
func (h *IdentityOAuthHandler) resolveLogin(ctx context.Context, prov string, u *OAuthUser, extra string) (id, name, game string, disabled bool, err error) {
	var status string
	err = h.DB.QueryRowContext(ctx, `SELECT player_id, status FROM player_identities WHERE provider=? AND provider_sub=?`, prov, u.Sub).Scan(&id, &status)
	if err == nil && status == "active" {
		var gameCol, disabledAt sql.NullString
		if e := h.DB.QueryRowContext(ctx, `SELECT display_name, game, disabled_at FROM players WHERE player_id=?`, id).Scan(&name, &gameCol, &disabledAt); e != nil {
			return "", "", "", false, e
		}
		_, _ = h.DB.ExecContext(ctx, `UPDATE player_identities SET display=?, extra_json=?, last_login_at=?, last_refreshed_at=? WHERE provider=? AND provider_sub=?`,
			u.Display, extra, sqlNowUTC(), sqlNowUTC(), prov, u.Sub)
		return id, name, gameCol.String, disabledAt.Valid, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", "", "", false, err
	}
	id = uuid.New().String()
	name = battletagName(u.Display)
	if name == "" {
		name = "Player"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	// players has UNIQUE(provider, provider_sub): a Battle.net account that was unlinked from an earlier player
	// leaves that old row behind, so the new player's row gets a distinct (informational) provider_sub. The
	// identity table is the authority for who owns the account, not this column.
	psub := u.Sub
	var taken int
	if h.DB.QueryRowContext(ctx, `SELECT 1 FROM players WHERE provider=? AND provider_sub=?`, prov, psub).Scan(&taken) == nil {
		psub = u.Sub + "#" + id[:8]
	}
	if _, err = h.DB.ExecContext(ctx, `INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES (?,?,?,?,?)`,
		id, name, prov, psub, ""); err != nil {
		return "", "", "", false, err
	}
	if _, _, err = linkIdentity(h.DB, id, prov, u.Sub, u.Display, extra, true); err != nil {
		return "", "", "", false, err
	}
	_, _ = h.DB.ExecContext(ctx, `UPDATE player_identities SET last_login_at=? WHERE provider=? AND provider_sub=?`, sqlNowUTC(), prov, u.Sub)
	return id, name, "", false, nil
}

// ---- real network calls ---------------------------------------------------------------------------------------

func oauthPostToken(ctx context.Context, endpoint string, form url.Values, basicUser, basicPass string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basicUser != "" {
		req.SetBasicAuth(basicUser, basicPass)
	}
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
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(raw, &tok) != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("token endpoint returned no access_token")
	}
	return tok.AccessToken, nil
}

func (h *IdentityOAuthHandler) bnetExchange(ctx context.Context, code, redirectURI string) (string, error) {
	return oauthPostToken(ctx, bnetToken, url.Values{
		"code": {code}, "redirect_uri": {redirectURI}, "grant_type": {"authorization_code"}, "scope": {"openid"},
	}, h.BnetClientID, h.BnetClientSecret)
}

// bnetUserInfo calls https://oauth.battle.net/userinfo -> {"sub","id","battletag"}.
func bnetUserInfo(ctx context.Context, accessToken string) (*OAuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bnetUserinfo, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo %d", resp.StatusCode)
	}
	var ui struct {
		Sub       string `json:"sub"`
		ID        any    `json:"id"`
		BattleTag string `json:"battletag"`
	}
	if err := json.Unmarshal(raw, &ui); err != nil {
		return nil, err
	}
	if ui.Sub == "" && ui.ID != nil {
		ui.Sub = fmt.Sprint(ui.ID)
	}
	return &OAuthUser{Sub: ui.Sub, Display: ui.BattleTag}, nil
}

func (h *IdentityOAuthHandler) twitchExchange(ctx context.Context, code, redirectURI string) (string, error) {
	return oauthPostToken(ctx, twitchToken, url.Values{
		"client_id": {h.TwitchClientID}, "client_secret": {h.TwitchClientSecret},
		"code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {redirectURI},
	}, "", "")
}

// twitchUserInfo uses Helix GET /users with the user token (no scopes needed for the caller's own profile).
func (h *IdentityOAuthHandler) twitchUserInfo(ctx context.Context, accessToken string) (*OAuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, twitchUsersURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Client-Id", h.TwitchClientID)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("helix users %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID              string `json:"id"`
			Login           string `json:"login"`
			DisplayName     string `json:"display_name"`
			BroadcasterType string `json:"broadcaster_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Data) == 0 {
		return nil, fmt.Errorf("helix users: no user")
	}
	d := out.Data[0]
	return &OAuthUser{Sub: d.ID, Display: d.Login, Extra: map[string]any{"display_name": d.DisplayName, "broadcaster_type": d.BroadcasterType}}, nil
}
