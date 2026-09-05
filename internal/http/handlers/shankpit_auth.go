package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/store"

	"github.com/google/uuid"
)

// ShankpitAuthHandler implements the browser OAuth flow for SHANKPIT players.
//
//	GET  /api/v1/auth/google/shankpit             → redirect to Google consent page
//	GET  /api/v1/auth/google/shankpit/callback    → exchange code, register player,
//	                                                 redirect to shankpit://auth?token=...
//
// Env vars required:
//
//	GOOGLE_CLIENT_ID
//	GOOGLE_CLIENT_SECRET
//	SHANKPIT_OAUTH_REDIRECT_URI — public URL of the callback endpoint (e.g. https://iduna.farthq.com/api/v1/auth/google/shankpit/callback)
type ShankpitAuthHandler struct {
	GoogleClientID     string
	GoogleClientSecret string
	RedirectURI        string // must match what's registered in Google Cloud Console
	Keys               *authjwt.Keys
	Store              store.IAMStore
	DB                 *sql.DB
	Issuer             string
	BaseURL            string
}

func (h *ShankpitAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/callback"):
		h.handleCallback(w, r)
	default:
		h.handleInitiate(w, r)
	}
}

func (h *ShankpitAuthHandler) handleInitiate(w http.ResponseWriter, r *http.Request) {
	if h.GoogleClientID == "" || h.GoogleClientSecret == "" {
		http.Error(w, "Google OAuth not configured (GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET missing)", http.StatusServiceUnavailable)
		return
	}

	// Generate CSRF state token.
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := base64.URLEncoding.EncodeToString(stateBytes)

	// Store state in a short-lived cookie so we can verify it on callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "shankpit_oauth_state",
		Value:    state,
		Path:     "/api/v1/auth/google/shankpit",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	redirectURI := h.RedirectURI
	if redirectURI == "" {
		redirectURI = h.BaseURL + "/api/v1/auth/google/shankpit/callback"
	}

	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s&access_type=offline",
		url.QueryEscape(h.GoogleClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape("openid profile email"),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *ShankpitAuthHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Verify CSRF state.
	stateCookie, err := r.Cookie("shankpit_oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state — possible CSRF", http.StatusBadRequest)
		return
	}
	// Clear state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   "shankpit_oauth_state",
		Value:  "",
		Path:   "/api/v1/auth/google/shankpit",
		MaxAge: -1,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Error(w, "OAuth error: "+errParam, http.StatusBadRequest)
		return
	}

	redirectURI := h.RedirectURI
	if redirectURI == "" {
		redirectURI = h.BaseURL + "/api/v1/auth/google/shankpit/callback"
	}

	// Exchange code for tokens.
	tokenResp, err := h.exchangeCode(r.Context(), code, redirectURI)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Fetch user info.
	userInfo, err := h.fetchUserInfo(r.Context(), tokenResp.AccessToken)
	if err != nil {
		http.Error(w, "userinfo fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Register or update player in IDUNA DB.
	playerID, displayName, game, err := h.upsertPlayer(r.Context(), userInfo)
	if err != nil {
		http.Error(w, "player registration failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Issue IDUNA JWT with player_id as sub + display_name claim. game (S241-01) reflects
	// whatever is actually stored on the player row -- "shankpit" for a brand-new player (this
	// endpoint is unambiguously SHANKPIT's own OAuth flow), or whatever an existing row already
	// had (never silently overwritten on login -- see upsertPlayer's own doc comment).
	expiresAt := time.Now().Add(72 * time.Hour)
	claims := map[string]any{
		"sub":          playerID,
		"display_name": displayName,
		"email":        userInfo.Email,
		"iss":          h.Issuer,
		"aud":          "shankpit",
		"iat":          time.Now().Unix(),
		"exp":          expiresAt.Unix(),
	}
	if game != "" {
		claims["game"] = game
	}
	token, err := authjwt.Sign(h.Keys, claims)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}

	// Redirect client app: shankpit://auth?token=JWT&player_id=UUID&display_name=NAME
	redirectTarget := fmt.Sprintf("shankpit://auth?token=%s&player_id=%s&display_name=%s",
		url.QueryEscape(token),
		url.QueryEscape(playerID),
		url.QueryEscape(displayName),
	)
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

type googleUserInfo struct {
	Sub         string `json:"sub"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	GivenName   string `json:"given_name"`
	PictureURL  string `json:"picture"`
}

func (h *ShankpitAuthHandler) exchangeCode(ctx context.Context, code, redirectURI string) (*oauthTokenResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	body := url.Values{
		"code":          {code},
		"client_id":     {h.GoogleClientID},
		"client_secret": {h.GoogleClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	resp, err := client.PostForm("https://oauth2.googleapis.com/token", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, raw)
	}
	var tok oauthTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func (h *ShankpitAuthHandler) fetchUserInfo(ctx context.Context, accessToken string) (*googleUserInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var ui googleUserInfo
	if err := json.Unmarshal(raw, &ui); err != nil {
		return nil, err
	}
	return &ui, nil
}

// upsertPlayer returns the player's stored game scope (S241-01) alongside the existing
// id/display-name pair. A brand-new player gets "shankpit" (this endpoint is unambiguously
// SHANKPIT's own OAuth flow); an existing player's stored game is read back as-is and never
// overwritten here -- an account that predates this column, or was somehow created scoped to
// something else, keeps that state rather than being silently reassigned on a routine login.
func (h *ShankpitAuthHandler) upsertPlayer(ctx context.Context, ui *googleUserInfo) (playerID, displayName, game string, err error) {
	name := ui.Name
	if name == "" {
		name = ui.GivenName
	}
	if name == "" && ui.Email != "" {
		at := strings.Index(ui.Email, "@")
		if at > 0 {
			name = ui.Email[:at]
		} else {
			name = ui.Email
		}
	}

	db := h.DB
	var gameCol sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT player_id, display_name, game FROM players WHERE provider='google' AND provider_sub=?`,
		ui.Sub,
	).Scan(&playerID, &displayName, &gameCol)

	if err == nil {
		// Update display_name + last_seen.
		displayName = name
		_, _ = db.ExecContext(ctx,
			`UPDATE players SET display_name=?, email=?, last_seen=CURRENT_TIMESTAMP WHERE player_id=?`,
			displayName, ui.Email, playerID,
		)
		return playerID, displayName, gameCol.String, nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", err
	}

	// New player.
	playerID = uuid.New().String()
	displayName = name
	_, err = db.ExecContext(ctx,
		`INSERT INTO players (player_id, display_name, provider, provider_sub, email, game) VALUES (?,?,?,?,?,?)`,
		playerID, displayName, "google", ui.Sub, ui.Email, "shankpit",
	)
	return playerID, displayName, "shankpit", err
}
