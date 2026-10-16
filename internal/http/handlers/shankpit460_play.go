package handlers

// shankpit460_play.go -- the WebSocket front of shankpit-460 web's matchmaker (internal/shankpit460).
//
// Founder real-time, 2026-10-16: "ADD SHANKPIT 460 as a first class citizen to WOTAN, full esports treatment, full
// IDUNA and WOTAN integration ... queue them into a bot match ... if there is at least 1 other human in matchmaking
// put them in a ffa lobby". The browser (WOTAN/shankpit-460/) plays a bot match locally and keeps one socket here,
// asking to be paired with another human.
//
//	GET /api/v1/shankpit460/play     WebSocket (binary frames, see internal/shankpit460/proto.go)
//	GET /api/v1/shankpit460/status   {online, searching, playing, rooms[], kill_limit, minutes}
//
// Identity: the HELLO frame carries the visitor's IDUNA bearer token, if they have one -- either the generic SSO JWT
// WOTAN holds (resolved with ssoGamePlayer, the same rule sso-exchange and Redeem-a-code use, so the lobby player IS
// the shankpit460 player row the stats pass will attach results to) or a shankpit460 game token. No token is a
// guest; a rejected token is a guest too, and the page is told so it can say "sign in again".

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/games"
	"iduna/internal/shankpit460"
)

// Shankpit460Play serves the matchmaker.
type Shankpit460Play struct {
	Hub  *shankpit460.Hub
	game *GameOnlineHandler
	ws   websocket.Server
}

// NewShankpit460Play builds the matchmaker around the games handler's database and signing keys.
func NewShankpit460Play(g *GameOnlineHandler) (*Shankpit460Play, error) {
	p := &Shankpit460Play{game: g}
	hub, err := shankpit460.NewHub(shankpit460ConfigFromEnv(), p.resolve)
	if err != nil {
		return nil, err
	}
	p.Hub = hub
	p.ws = websocket.Server{
		// the token travels in the first frame, not a cookie, so there is nothing for an Origin check to protect
		Handshake: func(c *websocket.Config, r *http.Request) error { return nil },
		Handler:   p.serveConn,
	}
	return p, nil
}

// shankpit460ConfigFromEnv is the human-match format, 16 kills / six minutes, with test and ops knobs:
// SHANKPIT460_KILL_LIMIT, SHANKPIT460_MATCH_SECONDS, SHANKPIT460_COUNTDOWN_SECONDS. A bad value is ignored.
func shankpit460ConfigFromEnv() shankpit460.Config {
	cfg := shankpit460.DefaultConfig()
	if n, err := strconv.Atoi(os.Getenv("SHANKPIT460_KILL_LIMIT")); err == nil && n >= 1 && n <= 255 {
		cfg.KillLimit = n
	}
	if n, err := strconv.Atoi(os.Getenv("SHANKPIT460_MATCH_SECONDS")); err == nil && n >= 10 && n <= 3600 {
		cfg.Duration = time.Duration(n) * time.Second
		cfg.LateJoinCutoff = cfg.Duration * 3 / 4
	}
	if n, err := strconv.Atoi(os.Getenv("SHANKPIT460_COUNTDOWN_SECONDS")); err == nil && n >= 1 && n <= 60 {
		cfg.Countdown = time.Duration(n) * time.Second
	}
	return cfg
}

// resolve is the Hub's Resolver: bearer token -> the shankpit460 player it plays as.
func (p *Shankpit460Play) resolve(ctx context.Context, token string) (shankpit460.Identity, error) {
	cfg, ok := p.game.games()["shankpit460"]
	if !ok {
		return shankpit460.Identity{}, errors.New("shankpit460 is not registered")
	}
	claims, err := authjwt.Verify(p.game.Keys, token)
	if err != nil {
		return shankpit460.Identity{}, err
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		return shankpit460.Identity{}, errors.New("an agent token is not a player")
	}
	c := accountCaller{claims: claims}
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		return shankpit460.Identity{}, errors.New("token does not identify a player")
	}
	pid, name, err := p.game.accountPlayer(ctx, c, cfg)
	if err != nil {
		return shankpit460.Identity{}, err
	}
	return shankpit460.Identity{ID: pid, Name: name}, nil
}

// ServeHTTP routes /api/v1/shankpit460/play and /status.
func (p *Shankpit460Play) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/shankpit460/status":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(p.Hub.Stats())
	case "/api/v1/shankpit460/play":
		p.ws.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

type wsTransport struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (t *wsTransport) Send(b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return websocket.Message.Send(t.ws, b)
}
func (t *wsTransport) Close() { _ = t.ws.Close() }

func (p *Shankpit460Play) serveConn(ws *websocket.Conn) {
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

var _ = games.Registry
