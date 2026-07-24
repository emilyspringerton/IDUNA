package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"iduna/internal/auth"
	googleverify "iduna/internal/auth/google"
	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/honorcode"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// WebCeremonyHandler implements the VS0 browser ceremony's actual contract --
// the endpoints app.js has always called (/auth/google/start,
// /auth/google/callback, /me, /honor-code/accept, /gamertag/check,
// /me/handle) but that were never registered anywhere in main.go
// (VS0_IDENTITY_GATE.md "known divergence #2": stale web bindings, ceremony
// unverified end-to-end). This handler closes that gap.
//
// Routes are bare (no /api/v1 prefix), matching the existing device-auth
// bridge convention: browser/ceremony-facing endpoints live outside the
// versioned machine API.
//
//	GET  /auth/google/start     -> {url, state}         (public)
//	POST /auth/google/callback  -> {token, honor_code}  (public; needs code+state)
//	GET  /me                    -> {user, honor_code}   (bearer JWT required)
//	POST /honor-code/accept     -> {ok:true}             (bearer JWT required)
//	GET  /gamertag/check        -> {available, reason}   (public)
//	POST /me/handle             -> {user}                (bearer JWT required)
type WebCeremonyHandler struct {
	GoogleClientID     string
	GoogleClientSecret string
	RedirectURI        string // the ceremony page's own URL; must match Google Cloud Console
	Keys               *authjwt.Keys
	Store              store.IAMStore
	Issuer             string
}

// Register wires the public routes onto mux. /me, /honor-code/accept, and
// /me/handle must additionally be wrapped in middleware.RequireAuth by the
// caller (main.go) since this handler has no Keys-independent way to do so
// itself for a bare http.HandlerFunc registration.
func (h *WebCeremonyHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/auth/google/start", h.HandleStart)
	mux.HandleFunc("/auth/google/callback", h.HandleCallback)
	mux.HandleFunc("/gamertag/check", h.HandleGamertagCheck)
}

const ceremonyStateCookie = "iduna_ceremony_state"

var handleFormatRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

var reservedHandles = map[string]bool{
	"admin": true, "moderator": true, "system": true,
	"root": true, "support": true, "iduna": true,
}

// HandleStart handles GET /auth/google/start.
func (h *WebCeremonyHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if h.GoogleClientID == "" || h.GoogleClientSecret == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code": "NOT_CONFIGURED", "message": "Google OAuth not configured",
		})
		return
	}

	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := base64.URLEncoding.EncodeToString(stateBytes)

	http.SetCookie(w, &http.Cookie{
		Name:     ceremonyStateCookie,
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
		url.QueryEscape(h.GoogleClientID),
		url.QueryEscape(h.RedirectURI),
		url.QueryEscape("openid email profile"),
		url.QueryEscape(state),
	)
	writeJSON(w, http.StatusOK, map[string]any{"url": authURL, "state": state})
}

type ceremonyCallbackRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
	State       string `json:"state"`
}

// HandleCallback handles POST /auth/google/callback.
func (h *WebCeremonyHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var req ceremonyCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "code is required"})
		return
	}

	stateCookie, err := r.Cookie(ceremonyStateCookie)
	if err != nil || req.State == "" || stateCookie.Value != req.State {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_STATE", "message": "state mismatch — possible CSRF, restart login"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: ceremonyStateCookie, Value: "", Path: "/", MaxAge: -1})

	redirectURI := req.RedirectURI
	if redirectURI == "" {
		redirectURI = h.RedirectURI
	}

	idToken, err := h.exchangeCodeForIDToken(r.Context(), req.Code, redirectURI)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "TOKEN_EXCHANGE_FAILED", "message": err.Error()})
		return
	}

	gClaims, err := googleverify.Verify(r.Context(), idToken, h.GoogleClientID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "ID_TOKEN_INVALID", "message": err.Error()})
		return
	}

	user, _, err := h.Store.GetOrCreateUserByGoogleSubject(r.Context(), gClaims.Sub, gClaims.Email)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to resolve identity"})
		return
	}
	if user.Status == "SUSPENDED" || user.Status == "BANNED" {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "IDENTITY_SUSPENDED", "message": "identity is suspended or banned"})
		return
	}

	token, err := h.signJWT(r.Context(), user.IDString, user.Email, user.Handle)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to sign token"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"honor_code": honorCodeStatus(user),
	})
}

// HandleMe handles GET /me.
func (h *WebCeremonyHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	sub := middleware.SubjectFromContext(r.Context())
	if sub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED", "message": "missing subject"})
		return
	}
	user, err := h.Store.GetUserByID(r.Context(), sub)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "identity not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":     user.IDString,
			"email":  user.Email,
			"handle": nullableString(user.Handle),
		},
		"honor_code": honorCodeStatus(user),
	})
}

type honorAcceptRequest struct {
	SHA256 string `json:"sha256"`
}

// HandleHonorAccept handles POST /honor-code/accept.
func (h *WebCeremonyHandler) HandleHonorAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	sub := middleware.SubjectFromContext(r.Context())
	if sub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED", "message": "missing subject"})
		return
	}

	var req honorAcceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "sha256 is required"})
		return
	}
	if req.SHA256 != honorcode.CurrentSHA256 {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code": "HONOR_CODE_STALE", "message": "honor code has changed, re-review before accepting",
			"honor_code": currentHonorCodePayload(true),
		})
		return
	}

	if err := h.Store.AcceptHonorCode(r.Context(), sub, honorcode.CurrentVersion, honorcode.CurrentSHA256, honorcode.CurrentText, sub); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to record acceptance"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleGamertagCheck handles GET /gamertag/check?handle=....
func (h *WebCeremonyHandler) HandleGamertagCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	handle := r.URL.Query().Get("handle")
	if reason := validateHandleFormat(handle); reason != "" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": reason})
		return
	}
	available, err := h.Store.IsHandleAvailable(r.Context(), handle)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "cannot verify right now"})
		return
	}
	reason := ""
	if !available {
		reason = "Already taken."
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": available, "reason": reason})
}

type handleClaimRequest struct {
	Handle string `json:"handle"`
}

// HandleMeHandle handles POST /me/handle.
func (h *WebCeremonyHandler) HandleMeHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	sub := middleware.SubjectFromContext(r.Context())
	if sub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED", "message": "missing subject"})
		return
	}

	user, err := h.Store.GetUserByID(r.Context(), sub)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND", "message": "identity not found"})
		return
	}
	if !user.HonorAccepted || user.HonorCurrentVer < honorcode.CurrentVersion {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code": "HONOR_CODE_REQUIRED", "message": "accept the honor code before claiming a gamertag",
			"honor_code": honorCodeStatus(user),
		})
		return
	}

	var req handleClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "message": "handle is required"})
		return
	}
	if reason := validateHandleFormat(req.Handle); reason != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_HANDLE", "message": reason})
		return
	}

	if err := h.Store.ClaimHandle(r.Context(), sub, req.Handle, sub); err != nil {
		switch {
		case errors.Is(err, store.ErrHandleAlreadySet):
			writeJSON(w, http.StatusConflict, map[string]any{"code": "HANDLE_ALREADY_SET", "message": "gamertag is permanent and already claimed"})
		case errors.Is(err, store.ErrHandleTaken):
			writeJSON(w, http.StatusConflict, map[string]any{"code": "HANDLE_TAKEN", "message": "that gamertag is already taken"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "message": "failed to claim gamertag"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]any{"handle": req.Handle}})
}

func validateHandleFormat(h string) string {
	if h == "" {
		return "Enter a gamertag."
	}
	if !handleFormatRe.MatchString(h) {
		return "3-16 chars, letters/numbers/underscore only."
	}
	if reservedHandles[strings.ToLower(h)] {
		return "Reserved word."
	}
	return ""
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// honorCodeStatus reports whether the given user still needs to (re-)accept
// the current honor code.
func honorCodeStatus(u *auth.User) map[string]any {
	return currentHonorCodePayload(!u.HonorAccepted || u.HonorCurrentVer < honorcode.CurrentVersion)
}

func currentHonorCodePayload(required bool) map[string]any {
	return map[string]any{
		"required":      required,
		"sha256":        honorcode.CurrentSHA256,
		"version":       honorcode.CurrentVersion,
		"body_markdown": honorcode.CurrentText,
	}
}

// exchangeCodeForIDToken exchanges an authorization code for Google tokens
// and returns the id_token (verified separately by the caller).
func (h *WebCeremonyHandler) exchangeCodeForIDToken(ctx context.Context, code, redirectURI string) (string, error) {
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
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, raw)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", err
	}
	if tok.IDToken == "" {
		return "", fmt.Errorf("token response missing id_token")
	}
	return tok.IDToken, nil
}

// signJWT issues an IDUNA JWT with the same claim shape GoogleAuthHandler uses.
func (h *WebCeremonyHandler) signJWT(ctx context.Context, userID, email, handle string) (string, error) {
	perms, err := h.Store.GetEffectivePermissions(ctx, userID)
	if err != nil {
		return "", err
	}
	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}
	exp := time.Now().UTC().Add(time.Hour)
	return authjwt.Sign(h.Keys, map[string]any{
		"sub":         userID,
		"email":       email,
		"gamertag":    handle,
		"permissions": perms,
		"iss":         issuer,
		"aud":         "farthq-ecosystem",
		"exp":         exp.Unix(),
	})
}
