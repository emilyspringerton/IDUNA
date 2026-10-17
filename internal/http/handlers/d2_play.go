package handlers

// d2_play.go -- the WebSocket front of D2 web (internal/d2play), WOTAN's /d2/play.html.
//
// Founder real-time, 2026-10-17: "now DEADWEIGHT_2 same affordances WOTAN ESPORTS WASM TS PARENA" (after Red Garden web: full
// match and player tracking, guest play, IDUNA SSO, the existing bot infrastructure). The browser (a PARENA wasm client) keeps
// one socket here; the gateway puts it on dw2_server's queue under the player's IDUNA name, relays the player's orders and
// the server's frames, and records the finished match against the account.
//
//	GET /api/v1/d2/play                 WebSocket (the match server's own frames, see internal/d2play/proto.go)
//	GET /api/v1/d2/status               {configured, online, searching, playing}
//	GET /api/v1/d2/web-matches          ?limit=&player_id=  recent matches played in the browser (public)
//	GET /api/v1/d2/web-matches/{id}     one match with the packed ship and the round-break log (public)
//	GET /api/v1/d2/players/{player_id}  rating, rank, record and the recent matches of one player (public)
//
// Identity: the first frame carries the visitor's IDUNA bearer token -- a D2 game token (guest-register / guest-login /
// email-login / sso-exchange on /api/v1/games/d2) or the generic SSO JWT WOTAN holds, which resolves through ssoGamePlayer
// exactly like sso-exchange and Redeem-a-code. Guests are real (named) guest accounts the page creates first.
//
// The ladder is the generic one (GET /api/v1/games/d2/leaderboard): bots are real player rows there (kind "bot"), a match
// against one is rated like any other, and the gateway is what reports because dw2_server runs --no-auth (it has no idea who
// anyone is). If the server is ever given IDUNA agent credentials it would report too: switch this off first.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/d2play"
)

// D2Play serves the gateway.
type D2Play struct {
	Hub  *d2play.Hub
	game *GameOnlineHandler
	db   *sql.DB
	ws   websocket.Server
}

// NewD2Play builds the gateway around the games handler's database and signing keys.
func NewD2Play(g *GameOnlineHandler) *D2Play {
	p := &D2Play{game: g, db: g.DB}
	cfg := d2play.DefaultConfig()
	if a := strings.TrimSpace(os.Getenv("D2_SERVER_ADDR")); a != "" {
		cfg.ServerAddr = a
	}
	hub, err := d2play.NewHub(cfg, p.resolve, &d2Recorder{g: g})
	if err != nil {
		log.Printf("d2 web: gateway disabled: %v", err)
	} else {
		p.Hub = hub
	}
	p.ws = websocket.Server{
		// the token travels in the first frame, not a cookie, so there is nothing for an Origin check to protect
		Handshake: func(c *websocket.Config, r *http.Request) error { return nil },
		Handler:   p.serveConn,
	}
	return p
}

// resolve is the Hub's Resolver: bearer token -> the D2 player it plays as.
func (p *D2Play) resolve(ctx context.Context, token string) (d2play.Identity, error) {
	cfg, ok := p.game.games()["d2"]
	if !ok {
		return d2play.Identity{}, errors.New("d2 is not registered")
	}
	claims, err := authjwt.Verify(p.game.Keys, token)
	if err != nil {
		return d2play.Identity{}, err
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		return d2play.Identity{}, errors.New("an agent token is not a player")
	}
	c := accountCaller{claims: claims}
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		return d2play.Identity{}, errors.New("token does not identify a player")
	}
	pid, name, err := p.game.accountPlayer(ctx, c, cfg)
	if err != nil {
		return d2play.Identity{}, err
	}
	guest := c.playerID != "" && p.game.accountState(ctx, pid) != "base"
	return d2play.Identity{ID: pid, Name: name, Guest: guest}, nil
}

func (p *D2Play) serveConn(ws *websocket.Conn) {
	ws.MaxPayloadBytes = 4096
	c := p.Hub.Connect(&wsTransport{ws: ws})
	defer c.Close()
	for {
		var msg []byte
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			return
		}
		c.Handle(msg)
	}
}

// ServeHTTP routes the /api/v1/d2/ paths.
func (p *D2Play) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/d2/")
	switch {
	case path == "status":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if p.Hub == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"configured": false, "online": 0, "searching": 0, "playing": 0})
			return
		}
		st := p.Hub.Stats()
		_ = json.NewEncoder(w).Encode(map[string]any{"configured": true, "online": st.Online, "searching": st.Searching, "playing": st.Playing})
	case path == "web-matches":
		p.webMatches(w, r)
	case strings.HasPrefix(path, "web-matches/"):
		p.webMatch(w, r, strings.TrimPrefix(path, "web-matches/"))
	case strings.HasPrefix(path, "players/"):
		p.player(w, r, strings.TrimPrefix(path, "players/"))
	case path == "play":
		if p.Hub == nil {
			http.Error(w, "the D2 gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		p.ws.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

type d2MatchRow struct {
	ID           int             `json:"id"`
	PlayerID     string          `json:"player_id"`
	Name         string          `json:"display_name"`
	OppName      string          `json:"opp_name"`
	OppKind      string          `json:"opp_kind"`
	OppPlayerID  string          `json:"opp_player_id"`
	Seat         int             `json:"seat"`
	Result       string          `json:"result"`
	Reason       string          `json:"reason"`
	Ticks        int             `json:"ticks"`
	HullYou      int             `json:"hull_you"`
	HullOpp      int             `json:"hull_opp"`
	ArmorYou     int             `json:"armor_you"`
	ArmorOpp     int             `json:"armor_opp"`
	CargoYou     int             `json:"cargo_you"`
	CargoOpp     int             `json:"cargo_opp"`
	Seconds      int             `json:"seconds"`
	RatingBefore float64         `json:"rating_before"`
	RatingAfter  float64         `json:"rating_after"`
	Counted      bool            `json:"counted"`
	PlayedAt     string          `json:"played_at"`
	Loadout      json.RawMessage `json:"loadout,omitempty"`
	Rounds       json.RawMessage `json:"rounds,omitempty"`
}

const d2MatchCols = `m.id, m.player_id, COALESCE(pl.display_name,'Player'), m.opp_name, m.opp_kind, m.opp_player_id, m.seat, m.result, m.reason,
	m.ticks, m.hull_you, m.hull_opp, m.armor_you, m.armor_opp, m.cargo_you, m.cargo_opp, m.seconds, m.rating_before, m.rating_after,
	m.counted, m.played_at, m.loadout, m.rounds`

func scanD2Match(rows interface{ Scan(...any) error }, full bool) (d2MatchRow, error) {
	var x d2MatchRow
	var counted int
	var lo, rd string
	if err := rows.Scan(&x.ID, &x.PlayerID, &x.Name, &x.OppName, &x.OppKind, &x.OppPlayerID, &x.Seat, &x.Result, &x.Reason, &x.Ticks, &x.HullYou,
		&x.HullOpp, &x.ArmorYou, &x.ArmorOpp, &x.CargoYou, &x.CargoOpp, &x.Seconds, &x.RatingBefore, &x.RatingAfter, &counted, &x.PlayedAt, &lo, &rd); err != nil {
		return x, err
	}
	x.Counted = counted == 1
	if full {
		x.Loadout, x.Rounds = json.RawMessage(lo), json.RawMessage(rd)
	}
	return x, nil
}

// webMatches is the public feed of matches played through the web client: one row per (match, human). No tokens, no emails.
func (p *D2Play) webMatches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 100)
	}
	pid := strings.TrimSpace(r.URL.Query().Get("player_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT `+d2MatchCols+`
		FROM d2_web_matches m LEFT JOIN players pl ON pl.player_id = m.player_id
		WHERE (? = '' OR m.player_id = ?) AND m.result <> 'aborted'
		ORDER BY m.id DESC LIMIT ?`, pid, pid, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []d2MatchRow{}
	for rows.Next() {
		x, err := scanD2Match(rows, false)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		out = append(out, x)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"matches": out})
}

func (p *D2Play) webMatch(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.Atoi(id)
	if err != nil || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	x, err := scanD2Match(p.db.QueryRowContext(ctx, `SELECT `+d2MatchCols+`
		FROM d2_web_matches m LEFT JOIN players pl ON pl.player_id = m.player_id WHERE m.id = ?`, n), true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(x)
}

// player is the public profile: the ladder numbers (the same rows the leaderboard shows), the rank, and recent browser matches.
func (p *D2Play) player(w http.ResponseWriter, r *http.Request, pid string) {
	pid = strings.TrimSpace(strings.Trim(pid, "/"))
	if pid == "" || strings.Contains(pid, "/") || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var s playerStats
	var provider string
	err := p.db.QueryRowContext(ctx,
		`SELECT p.player_id, p.display_name, p.provider, COALESCE(s.rating,?), COALESCE(s.wins,0), COALESCE(s.losses,0), COALESCE(s.draws,0), COALESCE(s.matches,0)
		 FROM players p LEFT JOIN game_player_stats s ON s.player_id = p.player_id AND s.game = p.game
		 WHERE p.player_id = ? AND p.game = 'd2'`, gameEloStart, pid).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Kind = kindOf(provider)
	rank := 0
	if s.Matches > 0 {
		_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM game_player_stats WHERE game='d2' AND matches > 0 AND rating > ?`, s.Rating).Scan(&rank)
	}
	var forfeits int
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM d2_web_matches WHERE player_id = ? AND result = 'forfeit'`, pid).Scan(&forfeits)
	rows, err := p.db.QueryContext(ctx, `SELECT `+d2MatchCols+`
		FROM d2_web_matches m LEFT JOIN players pl ON pl.player_id = m.player_id
		WHERE m.player_id = ? AND m.result <> 'aborted' ORDER BY m.id DESC LIMIT 20`, pid)
	recent := []d2MatchRow{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			if x, err := scanD2Match(rows, false); err == nil {
				recent = append(recent, x)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"player": s, "rank": rank, "forfeits": forfeits, "recent": recent})
}

// ---- the recorder ----------------------------------------------------------------------------------------------------

type d2Recorder struct{ g *GameOnlineHandler }

// botPlayer finds or creates the d2 player row a bot's name stands for (the same shape upsertBot gives DEADWEIGHT's bots, so
// the generic leaderboard marks it kind "bot").
func (r *d2Recorder) botPlayer(ctx context.Context, name string) (string, error) {
	var pid string
	err := r.g.DB.QueryRowContext(ctx,
		`SELECT player_id FROM players WHERE provider = 'deadweight_bot' AND provider_sub = ? AND game = 'd2'`, "d2:"+name).Scan(&pid)
	if err == nil {
		return pid, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	pid = uuid.New().String()
	_, err = r.g.DB.ExecContext(ctx,
		`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?,?)`,
		pid, name, "deadweight_bot", "d2:"+name, "d2")
	return pid, err
}

// Record implements d2play.Recorder: one transaction, the full web record plus (for a match that counts) the ladder.
func (r *d2Recorder) Record(ctx context.Context, m d2play.Record) (d2play.Outcome, error) {
	var out d2play.Outcome
	if m.PlayerID == "" {
		return out, errors.New("no player")
	}
	oppID := m.OppPlayerID
	if m.OppKind == "bot" && m.OppName != "" {
		id, err := r.botPlayer(ctx, m.OppName)
		if err != nil {
			return out, err
		}
		oppID = id
	}
	tx, err := r.g.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rated := m.Counted && oppID != "" && oppID != m.PlayerID
	if rated && !m.SkipStats {
		seat0, seat1 := m.PlayerID, oppID
		if m.Seat == 1 {
			seat0, seat1 = oppID, m.PlayerID
		}
		reason := m.Reason
		if len(reason) > 16 {
			reason = reason[:16]
		}
		gm, err := applyGameMatch(ctx, tx, "d2", gameMatchReq{MatchID: int64(m.MatchID), Seed: int64(m.Seed), Seat0: seat0, Seat1: seat1,
			Winner: m.Winner, Rounds: m.Ticks, Reason: reason})
		if err != nil {
			return out, err
		}
		out.Rated = !gm.Dup
		out.Seat[0].Before, out.Seat[0].After = gm.ABefore.Rating, gm.A.Rating
		out.Seat[1].Before, out.Seat[1].After = gm.BBefore.Rating, gm.B.Rating
		out.RatingBefore, out.RatingAfter = out.Seat[m.Seat].Before, out.Seat[m.Seat].After
	}
	if m.SkipStats {
		var cur float64
		_ = tx.QueryRowContext(ctx, `SELECT rating FROM game_player_stats WHERE player_id = ? AND game = 'd2'`, m.PlayerID).Scan(&cur)
		out.RatingAfter = cur
	}
	counted := 0
	if rated {
		counted = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO d2_web_matches
		  (player_id, opp_player_id, opp_name, opp_kind, match_id, seed, seat, result, reason, ticks, hull_you, hull_opp, armor_you, armor_opp,
		   cargo_you, cargo_opp, seconds, loadout, rounds, rating_before, rating_after, counted)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.PlayerID, oppID, m.OppName, m.OppKind, int64(m.MatchID), int64(m.Seed), m.Seat, m.Result, m.Reason, m.Ticks, m.HullYou, m.HullOpp,
		m.ArmorYou, m.ArmorOpp, m.CargoYou, m.CargoOpp, m.Seconds, d2play.MarshalLoadout(m.Loadout), d2play.MarshalRounds(m.Rounds),
		out.RatingBefore, out.RatingAfter, counted); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	if m.SkipStats {
		out.Rated = true
	}
	return out, nil
}
