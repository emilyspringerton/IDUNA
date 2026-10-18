package handlers

// bedrockracers_play.go -- the WebSocket front of BEDROCK RACERS web (internal/bedrockracers), WOTAN's /bedrockracers/, and the public stats around it.
//
// Founder real-time, 2026-10-18: "SHIP IT TO WOTAN AS AN ESPORT ... STATS ALL OF IT NOT JUST AGAINST BOTS THIS IS MULTIPLAYER". The game is one wasm module (PARENA);
// this is the referee that steps it. People who press PLAY keep a local bot match going while the browser searches here; two or more searching people become a
// lobby, then a lockstep room (the server sequences everyone's commands tick by tick and steps its own copy of the module), and the finished match is recorded against
// the accounts: the rated ladder (four teams, so a pairwise Elo across them), each person's counts, and the replay.
//
//	GET /api/v1/bedrockracers/play                  WebSocket (the frames of internal/bedrockracers/proto.go)
//	GET /api/v1/bedrockracers/status                {configured, version, online, searching, playing, rooms, lobby}
//	GET /api/v1/bedrockracers/matches               ?limit=&player_id=   recent matches with the people in them (public)
//	GET /api/v1/bedrockracers/matches/{id}          one match, all twelve cars (people and bots) and what each did
//	GET /api/v1/bedrockracers/replays/{id}          the replay file (binary: the module plays it back through the same wasm)
//	GET /api/v1/bedrockracers/players/{player_id}   rating, rank, record, totals and recent matches of one person (public)
//	GET /api/v1/bedrockracers/leaders               ?stat=goals|assists|steals|wins|catches|passes|fires|speed&limit=
//
// The ladder itself is the generic one (GET /api/v1/games/bedrockracers/leaderboard). Identity: the first frame carries the visitor's IDUNA bearer token -- a game token
// (guest-register / guest-login / sso-exchange on /api/v1/games/bedrockracers) or the generic SSO JWT WOTAN holds, resolved through ssoGamePlayer exactly like EMPIRE's.
// No token is allowed too: that person plays, and nothing is recorded for them.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/bedrockracers"
)

// BedrockRacersPlay serves the gateway and the stats.
type BedrockRacersPlay struct {
	Hub  *bedrockracers.Hub
	eng  *bedrockracers.Engine
	game *GameOnlineHandler
	db   *sql.DB
	ws   websocket.Server
}

func brEnvInt(name string, lo, hi int) (int, bool) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= lo && n <= hi {
			return n, true
		}
	}
	return 0, false
}

// NewBedrockRacersPlay builds the gateway around the games handler's database and signing keys.
func NewBedrockRacersPlay(g *GameOnlineHandler) *BedrockRacersPlay {
	cfg := bedrockracers.DefaultConfig()
	// operators and the browser tests can change the clock and the lobby: the browsers play whatever ticks they are sent
	if n, ok := brEnvInt("BEDROCKRACERS_TICK_US", 50, 100000); ok {
		cfg.TickEvery = time.Duration(n) * time.Microsecond
	}
	if n, ok := brEnvInt("BEDROCKRACERS_LOBBY_SECONDS", 1, 120); ok {
		cfg.LobbyWindow = time.Duration(n) * time.Second
	}
	if n, ok := brEnvInt("BEDROCKRACERS_MAX_ROOMS", 1, 500); ok {
		cfg.MaxRooms = n
	}
	if n, ok := brEnvInt("BEDROCKRACERS_MAX_TICKS", 300, 100000); ok {
		cfg.MaxTicks = n
	}
	return NewBedrockRacersPlayWith(g, cfg)
}

// NewBedrockRacersPlayWith is NewBedrockRacersPlay with explicit timers (tests).
func NewBedrockRacersPlayWith(g *GameOnlineHandler, cfg bedrockracers.Config) *BedrockRacersPlay {
	p := &BedrockRacersPlay{game: g, db: g.DB}
	eng, err := bedrockracers.NewEngine(context.Background())
	if err != nil {
		log.Printf("bedrockracers: gateway disabled: %v", err)
	} else {
		p.eng = eng
		p.Hub = bedrockracers.NewHub(context.Background(), cfg, eng, p.resolve, &brRecorder{g: g})
	}
	p.ws = websocket.Server{
		// the token travels in the first frame, not a cookie, so there is nothing for an Origin check to protect
		Handshake: func(c *websocket.Config, r *http.Request) error { return nil },
		Handler:   p.serveConn,
	}
	return p
}

// resolve is the Hub's Resolver: bearer token -> the Bedrock Racers player it plays as.
func (p *BedrockRacersPlay) resolve(ctx context.Context, token string) (bedrockracers.Identity, error) {
	cfg, ok := p.game.games()["bedrockracers"]
	if !ok {
		return bedrockracers.Identity{}, errors.New("bedrockracers is not registered")
	}
	claims, err := authjwt.Verify(p.game.Keys, token)
	if err != nil {
		return bedrockracers.Identity{}, err
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		return bedrockracers.Identity{}, errors.New("an agent token is not a player")
	}
	c := accountCaller{claims: claims}
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		return bedrockracers.Identity{}, errors.New("token does not identify a player")
	}
	pid, name, err := p.game.accountPlayer(ctx, c, cfg)
	if err != nil {
		return bedrockracers.Identity{}, err
	}
	guest := c.playerID != "" && p.game.accountState(ctx, pid) != "base"
	return bedrockracers.Identity{ID: pid, Name: name, Guest: guest}, nil
}

func (p *BedrockRacersPlay) serveConn(ws *websocket.Conn) {
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

func writeBRJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP routes the /api/v1/bedrockracers/ paths.
func (p *BedrockRacersPlay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/bedrockracers/"), "/")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case path == "status":
		if p.Hub == nil {
			writeBRJSON(w, map[string]any{"configured": false})
			return
		}
		st := p.Hub.Stats()
		writeBRJSON(w, map[string]any{"configured": true, "version": p.Hub.Version(), "online": st.Online, "searching": st.Searching,
			"playing": st.Playing, "rooms": st.Rooms, "lobby": st.Lobby})
	case path == "matches":
		p.matchList(w, r)
	case strings.HasPrefix(path, "matches/"):
		p.matchOne(w, r, strings.TrimPrefix(path, "matches/"))
	case strings.HasPrefix(path, "replays/"):
		p.replay(w, r, strings.TrimPrefix(path, "replays/"))
	case strings.HasPrefix(path, "players/"):
		p.player(w, r, strings.TrimPrefix(path, "players/"))
	case path == "leaders":
		p.leaders(w, r)
	case path == "play":
		if p.Hub == nil {
			http.Error(w, "the Bedrock Racers gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		p.ws.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ---- reading matches ------------------------------------------------------------------------------------------------------------------------

type brCar struct {
	Slot         int     `json:"slot"`
	Team         int     `json:"team"`
	PlayerID     string  `json:"player_id,omitempty"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Left         bool    `json:"left,omitempty"`
	LeftTick     int     `json:"left_tick,omitempty"`
	Goals        int     `json:"goals"`
	Assists      int     `json:"assists"`
	OwnGoals     int     `json:"own_goals"`
	Steals       int     `json:"steals"`
	Catches      int     `json:"catches"`
	Throws       int     `json:"throws"`
	Passes       int     `json:"passes"`
	Fires        int     `json:"fires"`
	Touches      int     `json:"touches"`
	TopSpeed     int     `json:"top_speed"`
	AirUsed      int     `json:"air_used"`
	Result       string  `json:"result,omitempty"`
	RatingBefore float64 `json:"rating_before,omitempty"`
	RatingAfter  float64 `json:"rating_after,omitempty"`
}

type brMatch struct {
	ID       int64   `json:"id"`
	Mode     string  `json:"mode"`
	Level    int     `json:"level"`
	Humans   int     `json:"humans"`
	Winner   int     `json:"winner"`
	Reason   int     `json:"reason"`
	Ticks    int     `json:"ticks"`
	Overtime bool    `json:"overtime"`
	Scores   [4]int  `json:"scores"`
	Counted  bool    `json:"counted"`
	Desyncs  int     `json:"desyncs"`
	Version  int     `json:"sim_version"`
	PlayedAt string  `json:"played_at"`
	Cars     []brCar `json:"cars"`
}

const brMatchCols = `id, mode, level, humans, winner, reason, ticks, overtime, score0, score1, score2, score3, counted, desyncs, sim_version, played_at, roster`

func scanBRMatch(rows interface{ Scan(...any) error }, all bool) (brMatch, error) {
	var m brMatch
	var ot, counted int
	var roster string
	if err := rows.Scan(&m.ID, &m.Mode, &m.Level, &m.Humans, &m.Winner, &m.Reason, &m.Ticks, &ot, &m.Scores[0], &m.Scores[1], &m.Scores[2], &m.Scores[3],
		&counted, &m.Desyncs, &m.Version, &m.PlayedAt, &roster); err != nil {
		return m, err
	}
	m.Overtime, m.Counted = ot == 1, counted == 1
	var cars []brCar
	_ = json.Unmarshal([]byte(roster), &cars)
	for _, c := range cars {
		if all || c.Kind != "bot" {
			m.Cars = append(m.Cars, c)
		}
	}
	if m.Cars == nil {
		m.Cars = []brCar{}
	}
	return m, nil
}

func (p *BedrockRacersPlay) matchList(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 100)
	}
	pid := strings.TrimSpace(r.URL.Query().Get("player_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var rows *sql.Rows
	var err error
	if pid == "" {
		rows, err = p.db.QueryContext(ctx, `SELECT `+brMatchCols+` FROM bedrockracers_matches ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `SELECT `+brMatchCols+` FROM bedrockracers_matches WHERE id IN
			(SELECT match_id FROM bedrockracers_player_matches WHERE player_id = ?) ORDER BY id DESC LIMIT ?`, pid, limit)
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []brMatch{}
	for rows.Next() {
		m, err := scanBRMatch(rows, false)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	writeBRJSON(w, map[string]any{"matches": out})
}

func (p *BedrockRacersPlay) matchOne(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	m, err := scanBRMatch(p.db.QueryRowContext(ctx, `SELECT `+brMatchCols+` FROM bedrockracers_matches WHERE id = ?`, n), true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeBRJSON(w, m)
}

func (p *BedrockRacersPlay) replay(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var b []byte
	if err := p.db.QueryRowContext(ctx, `SELECT replay FROM bedrockracers_matches WHERE id = ?`, n).Scan(&b); err != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}

// ---- players and leaders ------------------------------------------------------------------------------------------------------------------------

type brTotals struct {
	Matches  int `json:"matches"`
	Goals    int `json:"goals"`
	Assists  int `json:"assists"`
	OwnGoals int `json:"own_goals"`
	Steals   int `json:"steals"`
	Catches  int `json:"catches"`
	Throws   int `json:"throws"`
	Passes   int `json:"passes"`
	Fires    int `json:"fires"`
	Touches  int `json:"touches"`
	TopSpeed int `json:"top_speed"`
	Left     int `json:"left"`
}

// player is the public profile: the ladder numbers (the same rows the leaderboard shows), the rank, what their car has done, and their recent matches.
func (p *BedrockRacersPlay) player(w http.ResponseWriter, r *http.Request, pid string) {
	pid = strings.TrimSpace(strings.Trim(pid, "/"))
	if pid == "" || strings.Contains(pid, "/") {
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
		 WHERE p.player_id = ? AND p.game = 'bedrockracers'`, gameEloStart, pid).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Kind = kindOf(provider)
	rank := 0
	if s.Matches > 0 {
		_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM game_player_stats WHERE game='bedrockracers' AND matches > 0 AND rating > ?`, s.Rating).Scan(&rank)
	}
	var t brTotals
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(goals),0), COALESCE(SUM(assists),0), COALESCE(SUM(own_goals),0), COALESCE(SUM(steals),0),
		COALESCE(SUM(catches),0), COALESCE(SUM(throws),0), COALESCE(SUM(passes),0), COALESCE(SUM(fires),0), COALESCE(SUM(touches),0), COALESCE(MAX(top_speed),0),
		COALESCE(SUM(CASE WHEN result = 'left' THEN 1 ELSE 0 END),0) FROM bedrockracers_player_matches WHERE player_id = ?`, pid).
		Scan(&t.Matches, &t.Goals, &t.Assists, &t.OwnGoals, &t.Steals, &t.Catches, &t.Throws, &t.Passes, &t.Fires, &t.Touches, &t.TopSpeed, &t.Left)
	type recentRow struct {
		brMatch
		Slot         int     `json:"slot"`
		Team         int     `json:"team"`
		Result       string  `json:"result"`
		Goals        int     `json:"goals"`
		Assists      int     `json:"assists"`
		RatingBefore float64 `json:"rating_before"`
		RatingAfter  float64 `json:"rating_after"`
	}
	recent := []recentRow{}
	rows, err := p.db.QueryContext(ctx, `SELECT m.id, m.mode, m.level, m.humans, m.winner, m.reason, m.ticks, m.overtime, m.score0, m.score1, m.score2, m.score3,
		m.counted, m.desyncs, m.sim_version, m.played_at, m.roster, pm.slot, pm.team, pm.result, pm.goals, pm.assists, pm.rating_before, pm.rating_after
		FROM bedrockracers_player_matches pm JOIN bedrockracers_matches m ON m.id = pm.match_id WHERE pm.player_id = ? ORDER BY m.id DESC LIMIT 20`, pid)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var x recentRow
			var ot, counted int
			var roster string
			if rows.Scan(&x.ID, &x.Mode, &x.Level, &x.Humans, &x.Winner, &x.Reason, &x.Ticks, &ot, &x.Scores[0], &x.Scores[1], &x.Scores[2], &x.Scores[3],
				&counted, &x.Desyncs, &x.Version, &x.PlayedAt, &roster, &x.Slot, &x.Team, &x.Result, &x.Goals, &x.Assists, &x.RatingBefore, &x.RatingAfter) != nil {
				continue
			}
			x.Overtime, x.Counted = ot == 1, counted == 1
			x.Cars = []brCar{}
			recent = append(recent, x)
		}
	}
	writeBRJSON(w, map[string]any{"player": s, "rank": rank, "totals": t, "recent": recent})
}

// leaders is the top of one statistic across everyone who has played (a person needs a match to appear).
func (p *BedrockRacersPlay) leaders(w http.ResponseWriter, r *http.Request) {
	agg := map[string]string{
		"goals": "SUM(pm.goals)", "assists": "SUM(pm.assists)", "steals": "SUM(pm.steals)", "catches": "SUM(pm.catches)", "passes": "SUM(pm.passes)",
		"fires": "SUM(pm.fires)", "speed": "MAX(pm.top_speed)", "wins": "SUM(CASE WHEN pm.result = 'win' THEN 1 ELSE 0 END)",
	}
	stat := r.URL.Query().Get("stat")
	expr, ok := agg[stat]
	if !ok {
		stat, expr = "goals", agg["goals"]
	}
	limit := 10
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 50)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT pm.player_id, p.display_name, p.provider, `+expr+` AS v, COUNT(*) AS n
		FROM bedrockracers_player_matches pm JOIN players p ON p.player_id = pm.player_id GROUP BY pm.player_id HAVING v > 0 ORDER BY v DESC, n ASC LIMIT ?`, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type row struct {
		PlayerID string `json:"player_id"`
		Name     string `json:"display_name"`
		Kind     string `json:"kind"`
		Value    int    `json:"value"`
		Matches  int    `json:"matches"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		var provider string
		if rows.Scan(&x.PlayerID, &x.Name, &provider, &x.Value, &x.Matches) == nil {
			x.Kind = kindOf(provider)
			out = append(out, x)
		}
	}
	writeBRJSON(w, map[string]any{"stat": stat, "leaders": out})
}

// ---- the recorder ---------------------------------------------------------------------------------------------------------------------------------

type brRecorder struct{ g *GameOnlineHandler }

// Rating is the player's rating in the generic ladder (the starting rating for someone who has not played).
func (r *brRecorder) Rating(ctx context.Context, playerID string) (float64, error) {
	var rt float64
	err := r.g.DB.QueryRowContext(ctx, `SELECT rating FROM game_player_stats WHERE player_id = ? AND game = 'bedrockracers'`, playerID).Scan(&rt)
	if errors.Is(err, sql.ErrNoRows) {
		return gameEloStart, nil
	}
	return rt, err
}

// brTeamDeltas is the rating change of every team with at least one person: the pairwise Elo of its mean rating against each other team (a team with
// nobody rated counts as the starting rating), K/3 per opponent so the three results together move a rating no further than a two-seat match would.
// scores decide each pairing (equal = half); a person who left before the end is scored as having lost to everyone.
func brTeamDeltas(means [4]float64, has [4]bool, scores [4]int, left bool, team int) float64 {
	var d float64
	for u := 0; u < 4; u++ {
		if u == team {
			continue
		}
		ru := gameEloStart
		if has[u] {
			ru = means[u]
		}
		s := 0.5
		switch {
		case left || scores[team] < scores[u]:
			s = 0
		case scores[team] > scores[u]:
			s = 1
		}
		e := 1.0 / (1.0 + math.Pow(10, (ru-means[team])/400.0))
		d += gameEloK / 3.0 * (s - e)
	}
	return d
}

// Record writes one finished match in one transaction: the row, each person's counts, and (for a match that counts) the ladder.
func (r *brRecorder) Record(ctx context.Context, m *bedrockracers.Match) (bedrockracers.Outcome, error) {
	var out bedrockracers.Outcome
	tx, err := r.g.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	res := m.Result
	winner := res.Winner
	type human struct {
		slot       int
		before     float64
		wins, loss int
		draws      int
		matches    int
	}
	humans := map[int]*human{}
	var sum [4]float64
	var cnt [4]int
	for i, s := range m.Seats {
		if s.Kind != "human" || s.PlayerID == "" {
			continue
		}
		_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_player_stats (player_id, game) VALUES (?, 'bedrockracers')`, s.PlayerID)
		h := &human{slot: i}
		if err := tx.QueryRowContext(ctx, `SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'bedrockracers'`, s.PlayerID).
			Scan(&h.before, &h.wins, &h.loss, &h.draws, &h.matches); err != nil {
			return out, err
		}
		humans[i] = h
		sum[s.Team] += h.before
		cnt[s.Team]++
	}
	var means [4]float64
	var has [4]bool
	for t := 0; t < 4; t++ {
		if cnt[t] > 0 {
			means[t] = sum[t] / float64(cnt[t])
			has[t] = true
		}
	}
	result := func(s bedrockracers.Seat) string {
		switch {
		case s.Left:
			return "left"
		case winner < 0:
			return "draw"
		case winner == s.Team:
			return "win"
		}
		return "loss"
	}
	cars := make([]brCar, 0, 12)
	for i, s := range m.Seats {
		c := brCar{Slot: i, Team: s.Team, PlayerID: s.PlayerID, Name: s.Name, Kind: s.Kind, Left: s.Left, LeftTick: s.LeftTick,
			Goals: s.Stats.Goals, Assists: s.Stats.Assists, OwnGoals: s.Stats.OwnGoals, Steals: s.Stats.Steals, Catches: s.Stats.Catches, Throws: s.Stats.Throws,
			Passes: s.Stats.Passes, Fires: s.Stats.Fires, Touches: s.Stats.Touches, TopSpeed: s.Stats.TopSpeed, AirUsed: s.Stats.AirUsed}
		if s.Kind != "bot" {
			c.Result = result(s)
		}
		if h := humans[i]; h != nil {
			after := h.before
			if m.Counted {
				after = h.before + brTeamDeltas(means, has, res.Scores, s.Left, s.Team)
			}
			c.RatingBefore, c.RatingAfter = h.before, after
			out.Seats[i] = bedrockracers.SeatOutcome{Before: h.before, After: after, Rated: m.Counted}
		}
		cars = append(cars, c)
	}
	rosterJSON, _ := json.Marshal(cars)
	ot, counted := 0, 0
	if res.Overtime {
		ot = 1
	}
	if m.Counted {
		counted = 1
	}
	rs, err := tx.ExecContext(ctx, `INSERT INTO bedrockracers_matches
		(mode, seed, sim_version, level, humans, winner, reason, ticks, overtime, score0, score1, score2, score3, counted, desyncs, roster, replay)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Mode, int64(m.Seed), int64(m.Version), m.Level, m.HumansAtStart, winner, res.Reason, res.Ticks, ot, res.Scores[0], res.Scores[1], res.Scores[2], res.Scores[3],
		counted, m.Desyncs, string(rosterJSON), m.Replay)
	if err != nil {
		return out, err
	}
	id, _ := rs.LastInsertId()
	out.MatchID, out.Counted = id, m.Counted
	for i, h := range humans {
		s := m.Seats[i]
		c := cars[i]
		if _, err := tx.ExecContext(ctx, `INSERT INTO bedrockracers_player_matches
			(match_id, player_id, slot, team, result, goals, assists, own_goals, steals, catches, throws, passes, fires, touches, top_speed, air_used, humans,
			 rating_before, rating_after, counted) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, s.PlayerID, i, s.Team, c.Result, c.Goals, c.Assists, c.OwnGoals, c.Steals, c.Catches, c.Throws, c.Passes, c.Fires, c.Touches, c.TopSpeed, c.AirUsed,
			m.HumansAtStart, c.RatingBefore, c.RatingAfter, counted); err != nil {
			return out, err
		}
		if !m.Counted {
			continue
		}
		wins, losses, draws := h.wins, h.loss, h.draws
		switch c.Result {
		case "win":
			wins++
		case "draw":
			draws++
		default: // a loss, or leaving, which is one
			losses++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_player_stats SET rating=?, wins=?, losses=?, draws=?, matches=?, last_match_at=CURRENT_TIMESTAMP
			WHERE player_id=? AND game='bedrockracers'`, c.RatingAfter, wins, losses, draws, h.matches+1, s.PlayerID); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
