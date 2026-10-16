package handlers

// account_redeem.go -- founder real-time (2026-10-16): "we need an affordance under the user profile
// for redeem a code ... we need it to enable founder mode in DEADWEIGHT web ... make it nice when a
// code is redeemed it should say what it did", modelled on Battle.net's Redeem a Code page and its
// account overview (recent redemptions, per-game status).
//
// The per-game route (POST /api/v1/games/{game}/redeem, game_online.go) needs the caller to hold a
// GAME player token -- the native client does. WOTAN does not: a signed-in WOTAN visitor holds the
// generic IDUNA SSO JWT (sub = their players row, no game claims), and a redeem box that asked
// "which game is this code for?" would be wrong anyway -- Battle.net doesn't ask, the code knows.
// So these routes sit one level up, on the same handler (same claim-code table, same grant
// transaction, applyClaimCode) and work out the rest themselves:
//
//	POST /api/v1/redeem          {code}  -> find the game the code belongs to, find the player the caller
//	                                        plays it as (ssoGamePlayer -- the exact rule sso-exchange uses,
//	                                        so a code lands on the player the game client signs in as),
//	                                        spend it, answer with what it did
//	GET  /api/v1/me/entitlements         -> the caller's per-game status (founder, tickets) and their
//	                                        redemption history, for WOTAN's account page
//
// Either a generic SSO token or a game player token is accepted; an agent token never is.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"iduna/internal/games"
)

// ServeAccount dispatches the two account-level routes above. main.go mounts it at /api/v1/redeem
// and /api/v1/me/entitlements (exact paths).
func (h *GameOnlineHandler) ServeAccount(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/redeem":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			redeemError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
			return
		}
		if !h.allow(w, r) {
			return
		}
		h.accountRedeem(w, r)
	case "/api/v1/me/entitlements":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			redeemError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
			return
		}
		h.accountEntitlements(w, r)
	default:
		http.NotFound(w, r)
	}
}

// redeemError answers {error: <prose for a human>, code: <stable machine code>}. The page picks its
// own wording from code; error is the fallback for a code it has not heard of.
func redeemError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// accountCaller is who is calling an account-level route. Exactly one of sub / playerID is set:
// playerID for a game-scoped player token (the native client's, a guest's), sub for the generic SSO
// JWT WOTAN holds.
type accountCaller struct {
	sub      string
	playerID string
	game     string
	claims   map[string]any
}

// ident is the identity the caller's linked game accounts hang off: the SSO subject, or for a game
// token the player itself.
func (c accountCaller) ident() string {
	if c.playerID != "" {
		return c.playerID
	}
	return c.sub
}

func (h *GameOnlineHandler) accountCaller(w http.ResponseWriter, r *http.Request) (accountCaller, bool) {
	var c accountCaller
	claims := h.bearerClaims(w, r) // writes the 401 itself
	if claims == nil {
		return c, false
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		redeemError(w, http.StatusForbidden, "agent_token", "Redeeming needs a player account, not an agent token.")
		return c, false
	}
	c.claims = claims
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		redeemError(w, http.StatusForbidden, "no_subject", "This token does not identify a player.")
		return c, false
	}
	return c, true
}

// errWrongGameToken: the caller holds a game-scoped token for a different game than the code's.
var errWrongGameToken = errors.New("game token is for a different game")

// accountPlayer resolves the player the caller plays cfg's game as. A game token already names it; a
// generic SSO token goes through ssoGamePlayer (linked guest if any, else the identity's own row,
// claimed for the game on first use -- see claimGamePlayer for why that is safe).
func (h *GameOnlineHandler) accountPlayer(ctx context.Context, c accountCaller, cfg games.Config) (pid, name string, err error) {
	if c.playerID != "" {
		if c.game != cfg.Slug || !hasPerm(c.claims, cfg.PlayPerm) {
			return "", "", errWrongGameToken
		}
		err = h.DB.QueryRowContext(ctx,
			`SELECT display_name FROM players WHERE player_id = ? AND game = ? AND disabled_at IS NULL`,
			c.playerID, cfg.Slug).Scan(&name)
		return c.playerID, name, err
	}
	return h.ssoGamePlayer(ctx, c.sub, cfg)
}

// gameForCode finds which game an unspent code belongs to. (code, game) is the table's key, so in
// principle two games could hold the same string; at 25 random characters that does not happen, and
// if it ever did the alphabetically first registered game wins, deterministically.
func (h *GameOnlineHandler) gameForCode(ctx context.Context, code string) (games.Config, bool) {
	rows, err := h.DB.QueryContext(ctx,
		`SELECT game FROM game_claim_codes WHERE code = ? AND used_by_player_id IS NULL ORDER BY game`, code)
	if err != nil {
		return games.Config{}, false
	}
	var slugs []string
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil {
			slugs = append(slugs, g)
		}
	}
	rows.Close()
	for _, g := range slugs {
		if cfg, ok := h.games()[g]; ok {
			return cfg, true
		}
	}
	return games.Config{}, false
}

// gameDisplayName is the name a player sees. Every registry slug is its game's name in lower case
// (deadweight, brawlpit, big_o), so upper-casing it is the whole table.
func gameDisplayName(slug string) string { return strings.ToUpper(slug) }

func (h *GameOnlineHandler) accountRedeem(w http.ResponseWriter, r *http.Request) {
	c, ok := h.accountCaller(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		redeemError(w, http.StatusBadRequest, "invalid_json", "Could not read the request.")
		return
	}
	code, ok := normalizeClaimCode(req.Code)
	if !ok {
		redeemError(w, http.StatusBadRequest, "invalid_code_format",
			"That does not look like a code. Codes are five groups of five letters and numbers.")
		return
	}
	ctx := r.Context()
	cfg, found := h.gameForCode(ctx, code)
	if !found {
		// Same answer whether the code never existed or was already spent -- don't help a guesser
		// tell "wrong" from "somebody else got there first."
		redeemError(w, http.StatusBadRequest, "code_unavailable", "That code is not valid, or it has already been used.")
		return
	}
	pid, name, err := h.accountPlayer(ctx, c, cfg)
	switch {
	case err == errWrongGameToken:
		redeemError(w, http.StatusForbidden, "wrong_game_token",
			"You are signed in to a different game than this code is for. Sign in to WOTAN with your IDUNA account and try again.")
		return
	case err == errPlayerScopedToOtherGame:
		redeemError(w, http.StatusForbidden, "other_game",
			"This account already belongs to a different game, so a "+gameDisplayName(cfg.Slug)+" code cannot be applied to it.")
		return
	case err != nil:
		redeemError(w, http.StatusNotFound, "no_account",
			"There is no "+gameDisplayName(cfg.Slug)+" account for this sign-in yet.")
		return
	}
	out, err := h.applyClaimCode(ctx, cfg, pid, code)
	if err == errClaimCodeUnavailable {
		redeemError(w, http.StatusBadRequest, "code_unavailable", "That code is not valid, or it has already been used.")
		return
	}
	if err != nil {
		redeemError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again in a moment.")
		return
	}
	h.emitRedeemEvent(ctx, pid, out)
	resp := out.response()
	resp["game_name"] = gameDisplayName(cfg.Slug)
	resp["player_id"] = pid
	resp["display_name"] = name
	writeJSON(w, http.StatusOK, resp)
}

type entitlementGame struct {
	Game         string `json:"game"`
	Name         string `json:"name"`
	PlayerID     string `json:"player_id"`
	DisplayName  string `json:"display_name"`
	Founder      bool   `json:"founder"`
	Tickets      int    `json:"tickets"`
	AccountState string `json:"account_state"`
}

type entitlementRedemption struct {
	Code       string `json:"code"`
	Game       string `json:"game"`
	GameName   string `json:"game_name"`
	Tickets    int    `json:"tickets"`
	Founder    bool   `json:"founder"`
	Tier       string `json:"tier"`
	RedeemedAt string `json:"redeemed_at"`
}

// accountEntitlements is the account overview's data: which games this identity has an account in
// (with founder status and ticket balance -- Battle.net's "Subscriptions & Game Time") and the codes
// it has redeemed (its "Recent Purchases"). Read-only: it never claims an identity for a game.
func (h *GameOnlineHandler) accountEntitlements(w http.ResponseWriter, r *http.Request) {
	c, ok := h.accountCaller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ident := c.ident()

	// Every player row this identity plays as: itself, plus each guest it was linked to. Read fully
	// before any other query -- the pool is one connection wide, so no nesting inside a row loop.
	links := map[string]string{} // game -> linked player_id
	owned := []string{ident}
	if rows, err := h.DB.QueryContext(ctx, `SELECT game, player_id FROM game_sso_links WHERE sso_player_id = ?`, ident); err == nil {
		for rows.Next() {
			var g, p string
			if rows.Scan(&g, &p) == nil && p != "" {
				links[g] = p
				owned = append(owned, p)
			}
		}
		rows.Close()
	}

	slugs := make([]string, 0, len(h.games()))
	for slug := range h.games() {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	gameList := []entitlementGame{}
	for _, slug := range slugs {
		cfg := h.games()[slug]
		pid := ident
		if p, ok := links[slug]; ok {
			pid = p
		}
		var name string
		var isFounder int
		if err := h.DB.QueryRowContext(ctx,
			`SELECT display_name, is_founder FROM players WHERE player_id = ? AND game = ? AND disabled_at IS NULL`,
			pid, slug).Scan(&name, &isFounder); err != nil {
			continue // no account in this game
		}
		gameList = append(gameList, entitlementGame{
			Game: slug, Name: gameDisplayName(slug), PlayerID: pid, DisplayName: name,
			Founder: isFounder != 0, Tickets: h.ticketBalance(ctx, cfg, pid), AccountState: h.accountState(ctx, pid),
		})
	}

	history := []entitlementRedemption{}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(owned)), ",")
	args := make([]any, len(owned))
	for i, p := range owned {
		args[i] = p
	}
	rows, err := h.DB.QueryContext(ctx,
		`SELECT code, game, tickets, founder_flag, tier, used_at FROM game_claim_codes
		 WHERE used_by_player_id IN (`+marks+`) ORDER BY used_at DESC, code LIMIT 25`, args...)
	if err != nil {
		redeemError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again in a moment.")
		return
	}
	for rows.Next() {
		var e entitlementRedemption
		var founderFlag int
		var tier, usedAt sql.NullString
		if err := rows.Scan(&e.Code, &e.Game, &e.Tickets, &founderFlag, &tier, &usedAt); err != nil {
			rows.Close()
			redeemError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side. Try again in a moment.")
			return
		}
		e.GameName = gameDisplayName(e.Game)
		e.Founder = founderFlag != 0
		e.Tier = tier.String
		e.RedeemedAt = isoTimestamp(usedAt.String)
		history = append(history, e)
	}
	rows.Close()

	writeJSON(w, http.StatusOK, map[string]any{"player_id": ident, "games": gameList, "redemptions": history})
}

// isoTimestamp turns whatever the SQLite driver handed back for a DATETIME column -- RFC 3339 when it
// parsed the column, SQLite's own "YYYY-MM-DD HH:MM:SS" (always UTC) when it did not -- into RFC 3339,
// which is what the browser's Date.parse wants. Anything unrecognised comes back unchanged.
func isoTimestamp(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
