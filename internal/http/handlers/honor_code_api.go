package handlers

// honor_code_api.go -- THE_HONOR_CODE under /api/v1 (2026-11-13, founder: "do the wotan IDUNA update ...
// including the old honor code"). WOTAN's Valhöll oath page reaches IDUNA only through its own /api/ proxy, so
// it shows, checks and seals the honor code here, same-origin:
//
//	GET  /api/v1/honor-code         -> {honor_code}                (public; WebCeremonyHandler.HandleHonorCode)
//	POST /api/v1/honor-code/accept  {sha256} -> {ok, sealed}       (bearer JWT; HonorCodeAPIHandler.HandleAccept)
//	GET  /api/v1/me/honor-code      -> {honor_code, sealed|null}   (bearer JWT; HonorCodeAPIHandler.HandleMe)
//
// Why not just mount the ceremony's own HandleHonorAccept/HandleMe here: they record acceptance on the users row
// (the Google ceremony identity). A signed-in WOTAN visitor holds the generic IDUNA SSO JWT instead, whose sub is
// a players row (see account_redeem.go), and `UPDATE users ... WHERE id=<player id>` matches nothing -- the
// ceremony route would answer {ok:true} and record nothing, and /me would 404. So these routes resolve the caller
// first:
//   - a players row (the SSO identity) seals into player_honor_code: one row per (player, version); the first
//     seal of a version stands and is never rewritten ("This cannot be unmade"), a repeat is idempotent;
//   - a users row (the ceremony identity) goes through the ceremony's own store call, unchanged;
//   - an agent token or a game-scoped player token is refused (the oath is sworn by a person's IDUNA account);
//   - a disabled or suspended identity is refused; an unknown subject is a 404 -- never a false ok.
// Re-acceptance follows the ceremony's rule: a seal is owed again only when honorcode.CurrentVersion goes up.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"iduna/internal/auth"
	"iduna/internal/honorcode"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// HonorCodeAPIHandler serves the authenticated /api/v1 honor-code routes. main.go wraps both methods in
// middleware.RequireAuth.
type HonorCodeAPIHandler struct {
	IAM store.IAMStore // users rows: the web ceremony's Google identities
	DB  *sql.DB        // players rows (the generic SSO identity) and player_honor_code
}

// honorSeal is one recorded acceptance. AcceptedAt is RFC 3339 UTC; empty when the store does not keep it.
type honorSeal struct {
	Version    int    `json:"version"`
	SHA256     string `json:"sha256"`
	AcceptedAt string `json:"accepted_at,omitempty"`
}

// honorCaller is who is sealing: exactly one of playerID / user is set.
type honorCaller struct {
	playerID string
	user     *auth.User
}

func honorErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg})
}

func (h *HonorCodeAPIHandler) caller(w http.ResponseWriter, r *http.Request) (honorCaller, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	sub, _ := claims["sub"].(string)
	if sub == "" {
		honorErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing subject")
		return honorCaller{}, false
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		honorErr(w, http.StatusForbidden, "AGENT_TOKEN", "the honor code is sealed by a person's IDUNA account, not an agent token")
		return honorCaller{}, false
	}
	if pid, _ := claims["player_id"].(string); pid != "" {
		honorErr(w, http.StatusForbidden, "GAME_TOKEN", "seal the honor code with your IDUNA sign-in, not a game token")
		return honorCaller{}, false
	}
	if h.DB != nil {
		var active int
		err := h.DB.QueryRowContext(r.Context(),
			`SELECT CASE WHEN disabled_at IS NULL THEN 1 ELSE 0 END FROM players WHERE player_id = ?`, sub).Scan(&active)
		switch {
		case err == nil && active == 1:
			return honorCaller{playerID: sub}, true
		case err == nil:
			honorErr(w, http.StatusForbidden, "IDENTITY_SUSPENDED", "identity is suspended or banned")
			return honorCaller{}, false
		case !errors.Is(err, sql.ErrNoRows):
			honorErr(w, http.StatusInternalServerError, "INTERNAL", "cannot resolve identity right now")
			return honorCaller{}, false
		}
	}
	if h.IAM != nil {
		if u, err := h.IAM.GetUserByID(r.Context(), sub); err == nil && u != nil {
			if u.Status == "SUSPENDED" || u.Status == "BANNED" {
				honorErr(w, http.StatusForbidden, "IDENTITY_SUSPENDED", "identity is suspended or banned")
				return honorCaller{}, false
			}
			return honorCaller{user: u}, true
		}
	}
	honorErr(w, http.StatusNotFound, "NOT_FOUND", "identity not found")
	return honorCaller{}, false
}

// playerSeal is the player's most recent seal, or nil when they have never sealed.
func (h *HonorCodeAPIHandler) playerSeal(ctx context.Context, playerID string) (*honorSeal, error) {
	var s honorSeal
	err := h.DB.QueryRowContext(ctx,
		`SELECT version, sha256, accepted_at FROM player_honor_code WHERE player_id = ? ORDER BY version DESC LIMIT 1`,
		playerID).Scan(&s.Version, &s.SHA256, &s.AcceptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func userSeal(u *auth.User) *honorSeal {
	if !u.HonorAccepted {
		return nil
	}
	s := &honorSeal{Version: u.HonorCurrentVer, SHA256: u.HonorCurrentSHA}
	if u.HonorAcceptedAtUTC != nil {
		s.AcceptedAt = u.HonorAcceptedAtUTC.UTC().Format(time.RFC3339)
	}
	return s
}

// HandleAccept handles POST /api/v1/honor-code/accept {sha256}. The sha256 must be the current text's: a client
// that showed an older text gets HONOR_CODE_STALE with the current code to show instead.
func (h *HonorCodeAPIHandler) HandleAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	var req honorAcceptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		honorErr(w, http.StatusBadRequest, "BAD_REQUEST", "sha256 is required")
		return
	}
	if req.SHA256 != honorcode.CurrentSHA256 {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code": "HONOR_CODE_STALE", "message": "honor code has changed, re-review before accepting",
			"honor_code": currentHonorCodePayload(true),
		})
		return
	}
	if c.user != nil {
		if err := h.IAM.AcceptHonorCode(r.Context(), c.user.IDString, honorcode.CurrentVersion, honorcode.CurrentSHA256, honorcode.CurrentText, c.user.IDString); err != nil {
			honorErr(w, http.StatusInternalServerError, "INTERNAL", "failed to record acceptance")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true,
			"sealed": honorSeal{Version: honorcode.CurrentVersion, SHA256: honorcode.CurrentSHA256}})
		return
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT OR IGNORE INTO player_honor_code (player_id, version, sha256, accepted_at) VALUES (?, ?, ?, ?)`,
		c.playerID, honorcode.CurrentVersion, honorcode.CurrentSHA256, time.Now().UTC().Format(time.RFC3339)); err != nil {
		honorErr(w, http.StatusInternalServerError, "INTERNAL", "failed to record acceptance")
		return
	}
	seal, err := h.playerSeal(r.Context(), c.playerID)
	if err != nil || seal == nil {
		honorErr(w, http.StatusInternalServerError, "INTERNAL", "failed to read the recorded acceptance")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sealed": seal})
}

// HandleMe handles GET /api/v1/me/honor-code: the current code, whether the caller still owes it (required), and
// their most recent seal (null if none).
func (h *HonorCodeAPIHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	c, ok := h.caller(w, r)
	if !ok {
		return
	}
	var seal *honorSeal
	if c.user != nil {
		seal = userSeal(c.user)
	} else {
		var err error
		if seal, err = h.playerSeal(r.Context(), c.playerID); err != nil {
			honorErr(w, http.StatusInternalServerError, "INTERNAL", "cannot read the honor code record right now")
			return
		}
	}
	required := seal == nil || seal.Version < honorcode.CurrentVersion
	writeJSON(w, http.StatusOK, map[string]any{"honor_code": currentHonorCodePayload(required), "sealed": seal})
}
