package handlers

// redgarden_play.go -- the WebSocket front of REDGARDEN web (internal/redgardenplay), WOTAN's /redgarden/play.html.
//
// Founder real-time, 2026-10-17: "publish redgarden web ... it is an esport so it needs FULL WOTAN MATCH AND PLAYER
// TRACKING ... it should allow for guest play ... it should integrate with iduna sso ... it should use existing bot
// match infrastructure and bot pool". The browser (a PARENA wasm client) keeps one socket here; the gateway queues it
// with REDGARDEN's matchmaker, plays the match on the real arena server against the persistent bot pool, and records the
// result against the player's IDUNA account.
//
//	GET /api/v1/redgarden/play         WebSocket (binary frames, see internal/redgardenplay/proto.go)
//	GET /api/v1/redgarden/status       {configured, online, searching, playing, pools[]}
//	GET /api/v1/redgarden/web-matches  ?limit=&player_id=   recent matches played through the web client (public)
//
// Identity: the HELLO frame carries the visitor's IDUNA bearer token -- a REDGARDEN game token (guest-register /
// guest-login / email-login / sso-exchange on /api/v1/games/redgarden) or the generic SSO JWT WOTAN holds, which
// resolves through ssoGamePlayer exactly like sso-exchange and Redeem-a-code (so the SSO identity IS the redgarden
// player row the leaderboard shows). Guests are real (named) guest accounts the page creates first; a token that does
// not resolve is refused, never silently turned into somebody else.

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

	"golang.org/x/net/websocket"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/redgardenplay"
)

// RedgardenPlay serves the gateway.
type RedgardenPlay struct {
	Hub  *redgardenplay.Hub // nil when REDGARDEN_TICKET_SECRET is not set
	game *GameOnlineHandler
	db   *sql.DB
	ws   websocket.Server
	why  string
}

// NewRedgardenPlay builds the gateway around the games handler's database and signing keys. A missing ticket secret
// does not stop IDUNA: the play route then answers 503 and /status says configured:false.
func NewRedgardenPlay(g *GameOnlineHandler) *RedgardenPlay {
	p := &RedgardenPlay{game: g, db: g.DB}
	cfg := redgardenplay.DefaultConfig()
	cfg.Secret = []byte(os.Getenv("REDGARDEN_TICKET_SECRET"))
	if pools := redgardenPoolsFromEnv(os.Getenv("REDGARDEN_POOLS")); len(pools) > 0 {
		cfg.Pools = pools
	}
	hub, err := redgardenplay.NewHub(cfg, p.resolve, &redgardenplay.DBRecorder{DB: g.DB})
	if err != nil {
		p.why = err.Error()
		log.Printf("redgarden web: gateway disabled: %v", err)
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

// redgardenPoolsFromEnv: REDGARDEN_POOLS="name|mode|host:port|lobby;..." (mode 3v3 or 10v10), for ops and tests.
func redgardenPoolsFromEnv(s string) []redgardenplay.Pool {
	var out []redgardenplay.Pool
	for _, part := range strings.Split(s, ";") {
		f := strings.Split(strings.TrimSpace(part), "|")
		if len(f) != 4 {
			continue
		}
		lobby, err := strconv.Atoi(f[3])
		if err != nil || lobby < 2 || lobby > 20 {
			continue
		}
		mode := 1
		if f[1] == "3v3" {
			mode = 0
		}
		out = append(out, redgardenplay.Pool{Name: f[0], Mode: mode, Addr: f[2], Lobby: lobby})
	}
	return out
}

// resolve is the Hub's Resolver: bearer token -> the REDGARDEN player it plays as.
func (p *RedgardenPlay) resolve(ctx context.Context, token string) (redgardenplay.Identity, error) {
	cfg, ok := p.game.games()["redgarden"]
	if !ok {
		return redgardenplay.Identity{}, errors.New("redgarden is not registered")
	}
	claims, err := authjwt.Verify(p.game.Keys, token)
	if err != nil {
		return redgardenplay.Identity{}, err
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		return redgardenplay.Identity{}, errors.New("an agent token is not a player")
	}
	c := accountCaller{claims: claims}
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		return redgardenplay.Identity{}, errors.New("token does not identify a player")
	}
	pid, name, err := p.game.accountPlayer(ctx, c, cfg)
	if err != nil {
		return redgardenplay.Identity{}, err
	}
	guest := c.playerID != "" && p.game.accountState(ctx, pid) != "base"
	return redgardenplay.Identity{ID: pid, Name: name, Guest: guest}, nil
}

// ServeHTTP routes /api/v1/redgarden/play, /status and /web-matches.
func (p *RedgardenPlay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/redgarden/status":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if p.Hub == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"configured": false, "online": 0, "searching": 0, "playing": 0, "pools": []any{}})
			return
		}
		st := p.Hub.Stats()
		_ = json.NewEncoder(w).Encode(map[string]any{"configured": true, "online": st.Online, "searching": st.Searching, "playing": st.Playing, "pools": st.Pools})
	case "/api/v1/redgarden/web-matches":
		p.webMatches(w, r)
	case "/api/v1/redgarden/play":
		if p.Hub == nil {
			http.Error(w, "the arena gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		p.ws.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *RedgardenPlay) serveConn(ws *websocket.Conn) {
	ws.MaxPayloadBytes = 8192
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

// webMatches is the public feed of matches played through the web client: one row per (match, human). No tokens, no
// emails -- a display name, a hero and numbers, the same trust level as the leaderboard.
func (p *RedgardenPlay) webMatches(w http.ResponseWriter, r *http.Request) {
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
	rows, err := p.db.QueryContext(ctx, `
		SELECT m.id, m.player_id, COALESCE(pl.display_name,'Player'), m.mode, m.lobby, m.team, m.hero_id, m.result,
		       m.winner_team, m.kills, m.deaths, m.flow_earned, m.seconds, m.counted, m.roster, m.played_at
		FROM redgarden_web_matches m LEFT JOIN players pl ON pl.player_id = m.player_id
		WHERE (? = '' OR m.player_id = ?) AND m.result IN ('win','loss')
		ORDER BY m.id DESC LIMIT ?`, pid, pid, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type row struct {
		ID         int             `json:"id"`
		PlayerID   string          `json:"player_id"`
		Name       string          `json:"display_name"`
		Mode       string          `json:"mode"`
		Lobby      int             `json:"lobby"`
		Team       int             `json:"team"`
		HeroID     int             `json:"hero_id"`
		Result     string          `json:"result"`
		WinnerTeam int             `json:"winner_team"`
		Kills      int             `json:"kills"`
		Deaths     int             `json:"deaths"`
		Flow       int             `json:"flow_earned"`
		Seconds    int             `json:"seconds"`
		Counted    bool            `json:"counted"`
		Roster     json.RawMessage `json:"roster"`
		PlayedAt   string          `json:"played_at"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		var counted int
		var roster string
		if err := rows.Scan(&x.ID, &x.PlayerID, &x.Name, &x.Mode, &x.Lobby, &x.Team, &x.HeroID, &x.Result, &x.WinnerTeam,
			&x.Kills, &x.Deaths, &x.Flow, &x.Seconds, &counted, &roster, &x.PlayedAt); err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		x.Counted = counted == 1
		x.Roster = json.RawMessage(roster)
		out = append(out, x)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"matches": out})
}
