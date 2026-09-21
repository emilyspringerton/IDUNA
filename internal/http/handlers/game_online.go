package handlers

// game_online.go -- S503-06. Generic, game-scoped online services: name-only guest accounts, token
// verification for game servers, authoritative match results with Elo, and stats reads. One handler
// for every game in internal/games.Registry; routes are /api/v1/games/{game}/... . See
// DEADWEIGHT/docs/IDUNA_CONTRACT.md for the wire contract.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/games"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

const guestTokenTTL = 24 * time.Hour
const gameEloK = 32.0
const gameEloStart = 1500.0

var botNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

// GameOnlineHandler serves /api/v1/games/{game}/{action}.
type GameOnlineHandler struct {
	DB      *sql.DB
	Keys    *authjwt.Keys
	Issuer  string
	Games   map[string]games.Config   // nil = games.Registry
	Limiter *middleware.IPRateLimiter // guest-register / guest-login; nil = unlimited (tests)
}

func (h *GameOnlineHandler) games() map[string]games.Config {
	if h.Games != nil {
		return h.Games
	}
	return games.Registry
}

func (h *GameOnlineHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/games/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	cfg, ok := h.games()[parts[0]]
	if !ok {
		mmoWriteError(w, http.StatusNotFound, "unknown game")
		return
	}
	switch {
	case len(parts) == 2 && parts[1] == "guest-register" && r.Method == http.MethodPost:
		if !h.allow(w, r) {
			return
		}
		h.guestRegister(w, r, cfg)
	case len(parts) == 2 && parts[1] == "guest-login" && r.Method == http.MethodPost:
		if !h.allow(w, r) {
			return
		}
		h.guestLogin(w, r, cfg)
	case len(parts) == 2 && parts[1] == "steam-login" && r.Method == http.MethodPost:
		if !h.allow(w, r) {
			return
		}
		h.steamLogin(w, r, cfg)
	case len(parts) == 4 && parts[1] == "players" && parts[3] == "tickets" && r.Method == http.MethodGet:
		h.ticketsRead(w, r, cfg, parts[2])
	case len(parts) == 3 && parts[1] == "tickets" && parts[2] == "consume" && r.Method == http.MethodPost:
		h.ticketsConsume(w, r, cfg)
	case len(parts) == 2 && parts[1] == "verify" && r.Method == http.MethodPost:
		h.verify(w, r, cfg)
	case len(parts) == 2 && parts[1] == "match-result" && r.Method == http.MethodPost:
		h.matchResult(w, r, cfg)
	case len(parts) == 2 && parts[1] == "leaderboard" && r.Method == http.MethodGet:
		h.leaderboard(w, r, cfg)
	case len(parts) == 4 && parts[1] == "players" && parts[3] == "stats" && r.Method == http.MethodGet:
		h.stats(w, r, cfg, parts[2])
	default:
		http.NotFound(w, r)
	}
}

func (h *GameOnlineHandler) allow(w http.ResponseWriter, r *http.Request) bool {
	if h.Limiter == nil {
		return true
	}
	ip := r.Header.Get("X-Forwarded-For")
	if i := strings.IndexByte(ip, ','); i >= 0 {
		ip = ip[:i]
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		ip = r.RemoteAddr
		if i := strings.LastIndexByte(ip, ':'); i >= 0 {
			ip = ip[:i]
		}
	}
	if !h.Limiter.Allow(ip) {
		w.Header().Set("Retry-After", "60")
		mmoWriteError(w, http.StatusTooManyRequests, "too many requests")
		return false
	}
	return true
}

func cleanDisplayName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := 0
	for _, c := range s {
		n++
		if unicode.IsControl(c) {
			return "", false
		}
	}
	if n < 1 || n > 16 {
		return "", false
	}
	return s, true
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (h *GameOnlineHandler) issuer() string {
	if h.Issuer == "" {
		return "https://iam.farthq.internal"
	}
	return h.Issuer
}

func (h *GameOnlineHandler) guestToken(cfg games.Config, playerID, name string) (string, int64, error) {
	return h.playerToken(cfg, "guest", playerID, name)
}

// playerToken is guestToken generalized over the sub prefix ("guest" or "steam") -- the token
// shape/claims/permissions/TTL are identical either way, matching the founder's own "IDUNA
// INTEGRATED WOTAN INTEGRATED THE STEAM WILL HANDLE THE ACCOUNTS WE INTEGRATE WITH IT" framing:
// Steam is just another provider feeding the same IDUNA player-token pipeline guest accounts
// already use, not a separate auth system.
func (h *GameOnlineHandler) playerToken(cfg games.Config, subPrefix, playerID, name string) (string, int64, error) {
	exp := time.Now().UTC().Add(guestTokenTTL)
	tok, err := authjwt.Sign(h.Keys, map[string]any{
		"sub":          subPrefix + ":" + playerID,
		"player_id":    playerID,
		"display_name": name,
		"game":         cfg.Slug,
		"permissions":  []string{cfg.PlayPerm},
		"iss":          h.issuer(),
		"aud":          "farthq-ecosystem",
		"exp":          exp.Unix(),
	})
	return tok, exp.Unix(), err
}

func (h *GameOnlineHandler) guestRegister(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	name, ok := cleanDisplayName(req.DisplayName)
	if !ok {
		mmoWriteError(w, http.StatusBadRequest, "display_name must be 1-16 printable characters")
		return
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	secret := hex.EncodeToString(secretBytes)
	playerID := uuid.New().String()

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?,?)`,
		playerID, name, "guest", playerID, cfg.Slug); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO game_guest_credentials (player_id, game, secret_hash) VALUES (?,?,?)`,
		playerID, cfg.Slug, hashSecret(secret)); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	tok, exp, err := h.guestToken(cfg, playerID, name)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"player_id": playerID, "guest_secret": secret, "display_name": name, "token": tok, "expires_at": exp,
	})
}

func (h *GameOnlineHandler) guestLogin(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	var req struct {
		PlayerID    string `json:"player_id"`
		GuestSecret string `json:"guest_secret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Identical work + identical response whether the player is unknown or the secret is wrong.
	stored := hashSecret("no-such-player")
	name := ""
	var g string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT c.secret_hash, c.game, p.display_name FROM game_guest_credentials c
		 JOIN players p ON p.player_id = c.player_id WHERE c.player_id = ?`, req.PlayerID).Scan(&stored, &g, &name)
	found := err == nil && g == cfg.Slug
	match := subtle.ConstantTimeCompare([]byte(hashSecret(req.GuestSecret)), []byte(stored)) == 1
	if !found || !match {
		mmoWriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE players SET last_seen=CURRENT_TIMESTAMP WHERE player_id=?`, req.PlayerID)
	tok, exp, err := h.guestToken(cfg, req.PlayerID, name)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"player_id": req.PlayerID, "display_name": name, "token": tok, "expires_at": exp,
	})
}

// --- Steam auth (S507) -------------------------------------------------------------------
//
// Server-side validation only needs plain HTTPS (Steam's own ISteamUserAuth/AuthenticateUserTicket
// Web API) -- no Steamworks SDK is needed here. The SDK is a client-only dependency (the game
// client calls ISteamUser::GetAuthSessionTicket to produce the hex ticket this endpoint accepts);
// it is proprietary/Valve-partner-gated and genuinely not present in this sandbox, so the client
// side of Phase 1 (DEADWEIGHT/core/iduna.c's dwi_steam_login, wiring the real SDK call) is real,
// scoped, separate work for the founder's own machine with real Steamworks access -- named
// honestly rather than faked, same class of external gap as OpenExecutive's own real GCP project
// requirement.

type SteamAuthResult struct {
	SteamID         string
	VACBanned       bool
	PublisherBanned bool
}

// SteamAuthenticateFn is a package var so tests can substitute a fake Steam Web API response
// without a real network call / real Steam credentials.
var SteamAuthenticateFn = steamAuthenticateReal

func steamAuthenticateReal(apiKey, appID, ticketHex string) (*SteamAuthResult, error) {
	url := fmt.Sprintf(
		"https://api.steampowered.com/ISteamUserAuth/AuthenticateUserTicket/v1/?key=%s&appid=%s&ticket=%s",
		apiKey, appID, ticketHex)
	resp, err := http.Get(url) //nolint:gosec // key/appid/ticket are urlencoded-safe (hex/digits) at the call sites
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Response struct {
			Params *struct {
				Result          string `json:"result"`
				SteamID         string `json:"steamid"`
				VACBanned       bool   `json:"vacbanned"`
				PublisherBanned bool   `json:"publisherbanned"`
			} `json:"params"`
			Error *struct {
				ErrorCode int    `json:"errorcode"`
				ErrorDesc string `json:"errordesc"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("malformed Steam Web API response: %w", err)
	}
	if parsed.Response.Error != nil || parsed.Response.Params == nil || parsed.Response.Params.Result != "OK" {
		return nil, fmt.Errorf("steam ticket rejected")
	}
	return &SteamAuthResult{
		SteamID:         parsed.Response.Params.SteamID,
		VACBanned:       parsed.Response.Params.VACBanned,
		PublisherBanned: parsed.Response.Params.PublisherBanned,
	}, nil
}

const steamStarterTickets = 1 // founder: "grant them 1 free Draft Ticket" on first-ever shadow-account creation

func (h *GameOnlineHandler) steamLogin(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	if cfg.SteamAppID == "" {
		mmoWriteError(w, http.StatusNotFound, "steam auth is not configured for this game yet")
		return
	}
	apiKey := os.Getenv("STEAM_WEB_API_KEY")
	if apiKey == "" {
		mmoWriteError(w, http.StatusNotImplemented, "steam web api key not configured on this server")
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Ticket == "" {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON / missing ticket")
		return
	}
	if _, err := hex.DecodeString(req.Ticket); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "ticket must be hex-encoded")
		return
	}
	result, err := SteamAuthenticateFn(apiKey, cfg.SteamAppID, req.Ticket)
	if err != nil {
		mmoWriteError(w, http.StatusUnauthorized, "steam ticket validation failed")
		return
	}
	if result.VACBanned || result.PublisherBanned {
		mmoWriteError(w, http.StatusForbidden, "banned account")
		return
	}
	providerSub := result.SteamID
	ctx := r.Context()

	var playerID, name string
	err = h.DB.QueryRowContext(ctx,
		`SELECT player_id, display_name FROM players WHERE provider = 'steam' AND provider_sub = ? AND game = ?`,
		providerSub, cfg.Slug).Scan(&playerID, &name)
	isNew := err == sql.ErrNoRows
	if err != nil && !isNew {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if isNew {
		// Shadow-account provisioning -- founder: "If it does not exist, silently create a new
		// user row tied to this SteamID64, grant them 1 free Draft Ticket." No real Steam
		// persona name is fetched here (that's a separate ISteamUser/GetPlayerSummaries call,
		// real, named, not-yet-built gap) -- a placeholder derived from the SteamID64 tail, same
		// spirit as guest accounts' own "no email, no recovery" minimal-identity design; the
		// player can rename later via whatever display-name-update path this game adds.
		playerID = uuid.New().String()
		name = "Steam" + providerSub[max(0, len(providerSub)-4):]
		tx, err := h.DB.BeginTx(ctx, nil)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?,?)`,
			playerID, name, "steam", providerSub, cfg.Slug); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "registration failed")
			return
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO game_player_tickets (player_id, game, tickets) VALUES (?,?,?)`,
			playerID, cfg.Slug, steamStarterTickets); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "registration failed")
			return
		}
		if err := tx.Commit(); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "registration failed")
			return
		}
	} else {
		_, _ = h.DB.ExecContext(ctx, `UPDATE players SET last_seen=CURRENT_TIMESTAMP WHERE player_id=?`, playerID)
	}

	tok, exp, err := h.playerToken(cfg, "steam", playerID, name)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	tickets := 0
	_ = h.DB.QueryRowContext(ctx,
		`SELECT tickets FROM game_player_tickets WHERE player_id = ? AND game = ?`, playerID, cfg.Slug).Scan(&tickets)
	writeJSON(w, http.StatusOK, map[string]any{
		"player_id": playerID, "display_name": name, "token": tok, "expires_at": exp,
		"tickets": tickets, "is_new": isNew,
	})
}

func (h *GameOnlineHandler) ticketsRead(w http.ResponseWriter, r *http.Request, cfg games.Config, playerID string) {
	tickets := 0
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT tickets FROM game_player_tickets WHERE player_id = ? AND game = ?`, playerID, cfg.Slug).Scan(&tickets)
	if err != nil && err != sql.ErrNoRows {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// A player with no row yet (never granted/spent any) legitimately has 0, not a 404 -- same
	// COALESCE-to-default convention game_player_stats' own stats() handler already uses.
	writeJSON(w, http.StatusOK, map[string]any{"player_id": playerID, "tickets": tickets})
}

func (h *GameOnlineHandler) ticketsConsume(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	if !hasPerm(claims, cfg.TicketsWritePerm) {
		mmoWriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	var req struct {
		PlayerID string `json:"player_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.PlayerID == "" {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON / missing player_id")
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	// Atomic decrement gated in the WHERE clause -- no read-then-write race between two
	// concurrent draft-queue requests for the same player (unlike LeagueManager's own Elo
	// read-modify-write, which accepted that race as low-probability; a ticket is real spendable
	// inventory, so this path doesn't get to accept it).
	res, err := tx.ExecContext(ctx,
		`UPDATE game_player_tickets SET tickets = tickets - 1, updated_at = CURRENT_TIMESTAMP
		 WHERE player_id = ? AND game = ? AND tickets > 0`, req.PlayerID, cfg.Slug)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusPaymentRequired, "insufficient tickets")
		return
	}
	var remaining int
	if err := tx.QueryRowContext(ctx,
		`SELECT tickets FROM game_player_tickets WHERE player_id = ? AND game = ?`, req.PlayerID, cfg.Slug).
		Scan(&remaining); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tickets": remaining})
}

func hasPerm(claims map[string]any, perm string) bool {
	arr, _ := claims["permissions"].([]any)
	for _, p := range arr {
		if s, _ := p.(string); s == perm {
			return true
		}
	}
	return false
}

func (h *GameOnlineHandler) bearerClaims(w http.ResponseWriter, r *http.Request) map[string]any {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") {
		mmoWriteError(w, http.StatusUnauthorized, "missing bearer token")
		return nil
	}
	claims, err := authjwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	if err != nil {
		mmoWriteError(w, http.StatusUnauthorized, "invalid token")
		return nil
	}
	return claims
}

func (h *GameOnlineHandler) verify(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	// Guest (human) token: must be scoped to THIS game and carry its play permission.
	if pid, _ := claims["player_id"].(string); pid != "" {
		if g, _ := claims["game"].(string); g != cfg.Slug || !hasPerm(claims, cfg.PlayPerm) {
			mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
			return
		}
		var name string
		if err := h.DB.QueryRowContext(r.Context(),
			`SELECT display_name FROM players WHERE player_id = ? AND game = ? AND disabled_at IS NULL`, pid, cfg.Slug).Scan(&name); err != nil {
			mmoWriteError(w, http.StatusUnauthorized, "unknown player")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"player_id": pid, "display_name": name, "kind": "human", "game": cfg.Slug})
		return
	}
	// Bot agent token.
	if hasPerm(claims, cfg.BotPerm) {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
		if !botNameRe.MatchString(req.Name) {
			mmoWriteError(w, http.StatusBadRequest, "bot name required ([A-Za-z0-9_-]{1,16})")
			return
		}
		pid, err := h.upsertBot(r, cfg, req.Name)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"player_id": pid, "display_name": req.Name, "kind": "bot", "game": cfg.Slug})
		return
	}
	mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
}

func (h *GameOnlineHandler) upsertBot(r *http.Request, cfg games.Config, name string) (string, error) {
	var pid string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT player_id FROM players WHERE provider = 'deadweight_bot' AND provider_sub = ? AND game = ?`,
		cfg.Slug+":"+name, cfg.Slug).Scan(&pid)
	if err == nil {
		return pid, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	pid = uuid.New().String()
	_, err = h.DB.ExecContext(r.Context(),
		`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?,?)`,
		pid, name, "deadweight_bot", cfg.Slug+":"+name, cfg.Slug)
	return pid, err
}

type playerStats struct {
	PlayerID    string  `json:"player_id"`
	DisplayName string  `json:"display_name"`
	Kind        string  `json:"kind"`
	Rating      float64 `json:"rating"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Draws       int     `json:"draws"`
	Matches     int     `json:"matches"`
}

type seatStats struct {
	Rating  float64 `json:"rating"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	Matches int     `json:"matches"`
}

func kindOf(provider string) string {
	if provider == "deadweight_bot" {
		return "bot"
	}
	return "human"
}

func (h *GameOnlineHandler) matchResult(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	if !hasPerm(claims, cfg.MatchWritePerm) {
		mmoWriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	var req struct {
		MatchID int64  `json:"match_id"`
		Seed    int64  `json:"seed"`
		Seat0   string `json:"seat0_player_id"`
		Seat1   string `json:"seat1_player_id"`
		Winner  int    `json:"winner"`
		Rounds  int    `json:"rounds"`
		Reason  string `json:"reason"`
		Mode    int    `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Winner < 0 || req.Winner > 2 || req.Seat0 == "" || req.Seat1 == "" || req.Seat0 == req.Seat1 ||
		req.Rounds < 0 || req.Rounds > 1000 || len(req.Reason) > 16 {
		mmoWriteError(w, http.StatusBadRequest, "invalid match result")
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()

	for _, pid := range []string{req.Seat0, req.Seat1} {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM players WHERE player_id = ? AND game = ?`, pid, cfg.Slug).Scan(&one); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "unknown player for this game")
			return
		}
	}
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO game_matches (game, match_id, seed, seat0_player_id, seat1_player_id, winner, rounds, reason, mode)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		cfg.Slug, req.MatchID, req.Seed, req.Seat0, req.Seat1, req.Winner, req.Rounds, req.Reason, req.Mode)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	dup := false
	if n, _ := res.RowsAffected(); n == 0 {
		dup = true
	}

	load := func(pid string) (seatStats, error) {
		_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_player_stats (player_id, game) VALUES (?,?)`, pid, cfg.Slug)
		var s seatStats
		err := tx.QueryRowContext(ctx, `SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = ?`,
			pid, cfg.Slug).Scan(&s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
		return s, err
	}
	a, err := load(req.Seat0)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	b, err := load(req.Seat1)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !dup {
		scoreA := map[int]float64{0: 1, 1: 0, 2: 0.5}[req.Winner]
		expA := 1.0 / (1.0 + math.Pow(10, (b.Rating-a.Rating)/400.0))
		newA := a.Rating + gameEloK*(scoreA-expA)
		newB := b.Rating + gameEloK*((1-scoreA)-(1-expA))
		a.Rating, b.Rating = newA, newB
		a.Matches++
		b.Matches++
		switch req.Winner {
		case 0:
			a.Wins++
			b.Losses++
		case 1:
			b.Wins++
			a.Losses++
		default:
			a.Draws++
			b.Draws++
		}
		for pid, s := range map[string]seatStats{req.Seat0: a, req.Seat1: b} {
			if _, err := tx.ExecContext(ctx,
				`UPDATE game_player_stats SET rating=?, wins=?, losses=?, draws=?, matches=?, last_match_at=CURRENT_TIMESTAMP
				 WHERE player_id=? AND game=?`, s.Rating, s.Wins, s.Losses, s.Draws, s.Matches, pid, cfg.Slug); err != nil {
				mmoWriteError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": dup, "seat0": a, "seat1": b})
}

func (h *GameOnlineHandler) stats(w http.ResponseWriter, r *http.Request, cfg games.Config, pid string) {
	var s playerStats
	var provider string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT p.player_id, p.display_name, p.provider, COALESCE(s.rating,?), COALESCE(s.wins,0), COALESCE(s.losses,0), COALESCE(s.draws,0), COALESCE(s.matches,0)
		 FROM players p LEFT JOIN game_player_stats s ON s.player_id = p.player_id AND s.game = p.game
		 WHERE p.player_id = ? AND p.game = ?`, gameEloStart, pid, cfg.Slug).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, "player not found")
		return
	}
	s.Kind = kindOf(provider)
	writeJSON(w, http.StatusOK, s)
}

func (h *GameOnlineHandler) leaderboard(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT p.player_id, p.display_name, p.provider, s.rating, s.wins, s.losses, s.draws, s.matches
		 FROM game_player_stats s JOIN players p ON p.player_id = s.player_id
		 WHERE s.game = ? ORDER BY s.rating DESC, s.matches DESC LIMIT ?`, cfg.Slug, limit)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []playerStats{}
	for rows.Next() {
		var s playerStats
		var provider string
		if err := rows.Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.Kind = kindOf(provider)
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, out)
}
