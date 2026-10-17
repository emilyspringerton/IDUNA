package handlers

// empire_play.go -- the WebSocket front of EMPIRE web (internal/empire), WOTAN's /empire/play.html, and the public stats around it.
//
// Founder real-time, 2026-10-17: "build the whole vs0 mvp of EMPIRE: ESPORTS WOTAN IDUNA SSO PARENA WASM TS RIGHT INTO WOTAN STATS ALL OF IT AIs".
// The game is one wasm module (PARENA); this is the referee that steps it. It pairs people (or seats one of the eight AIs), runs the
// authoritative match, and records the finished one against the accounts: the ladder, the card statistics, and the replay.
//
//	GET /api/v1/empire/play                 WebSocket (the frames of internal/empire/proto.go)
//	GET /api/v1/empire/status               {configured, version, online, searching, playing, rooms, watching}
//	GET /api/v1/empire/live                 matches being played now (watch one with the WATCH frame)
//	GET /api/v1/empire/bots                 the eight AIs: faction, deck, rating, record
//	GET /api/v1/empire/matches              ?limit=&player_id=&mode=   recent matches (public)
//	GET /api/v1/empire/matches/{id}         one match, with what the board counted for each side
//	GET /api/v1/empire/replays/{id}         the replay file (binary; WOTAN's viewer plays it through the same wasm)
//	GET /api/v1/empire/players/{player_id}  rating, rank, record, favourite cards, recent matches (public)
//	GET /api/v1/empire/cards                how each card fares across every counted match
//
// The ladder itself is the generic one (GET /api/v1/games/empire/leaderboard); the AIs are real player rows there (kind "bot"). Identity: the
// first frame carries the visitor's IDUNA bearer token -- an EMPIRE game token (guest-register / guest-login / sso-exchange on
// /api/v1/games/empire) or the generic SSO JWT WOTAN holds, resolved through ssoGamePlayer exactly like Redeem-a-code does.

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
	"iduna/internal/empire"
)

// EmpirePlay serves the gateway and the stats.
type EmpirePlay struct {
	Hub  *empire.Hub
	eng  *empire.Engine
	game *GameOnlineHandler
	db   *sql.DB
	ws   websocket.Server
	bots [9]empireBotInfo
	rec  *empireRecorder
}

type empireBotInfo struct {
	Name    string `json:"name"`
	Faction int    `json:"faction"`
	Deck    []int  `json:"deck"`
}

// NewEmpirePlay builds the gateway around the games handler's database and signing keys.
func NewEmpirePlay(g *GameOnlineHandler) *EmpirePlay {
	cfg := empire.DefaultConfig()
	if v := strings.TrimSpace(os.Getenv("EMPIRE_LEAGUE_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.LeagueEvery = time.Duration(n) * time.Minute
		}
	}
	return NewEmpirePlayWith(g, cfg)
}

// NewEmpirePlayWith is NewEmpirePlay with explicit timers (tests).
func NewEmpirePlayWith(g *GameOnlineHandler, cfg empire.Config) *EmpirePlay {
	p := &EmpirePlay{game: g, db: g.DB}
	p.rec = &empireRecorder{g: g}
	eng, err := empire.NewEngine(context.Background())
	if err != nil {
		log.Printf("empire: gateway disabled: %v", err)
	} else {
		p.eng = eng
		p.Hub = empire.NewHub(context.Background(), cfg, eng, p.resolve, p.rec)
		if s, err := eng.New(context.Background()); err == nil {
			for n := 1; n <= 8; n++ {
				d := s.BotDeck(n)
				ids := make([]int, 8)
				for k := range ids {
					ids[k] = int(d >> (4 * uint(k)) & 15)
				}
				p.bots[n] = empireBotInfo{Name: empire.BotNames[n], Faction: s.BotFaction(n), Deck: ids}
			}
			s.Close()
		}
	}
	p.ws = websocket.Server{
		// the token travels in the first frame, not a cookie, so there is nothing for an Origin check to protect
		Handshake: func(c *websocket.Config, r *http.Request) error { return nil },
		Handler:   p.serveConn,
	}
	return p
}

// resolve is the Hub's Resolver: bearer token -> the EMPIRE player it plays as.
func (p *EmpirePlay) resolve(ctx context.Context, token string) (empire.Identity, error) {
	cfg, ok := p.game.games()["empire"]
	if !ok {
		return empire.Identity{}, errors.New("empire is not registered")
	}
	claims, err := authjwt.Verify(p.game.Keys, token)
	if err != nil {
		return empire.Identity{}, err
	}
	if name, _ := claims["agent_name"].(string); name != "" {
		return empire.Identity{}, errors.New("an agent token is not a player")
	}
	c := accountCaller{claims: claims}
	c.playerID, _ = claims["player_id"].(string)
	c.game, _ = claims["game"].(string)
	c.sub, _ = claims["sub"].(string)
	if c.ident() == "" {
		return empire.Identity{}, errors.New("token does not identify a player")
	}
	pid, name, err := p.game.accountPlayer(ctx, c, cfg)
	if err != nil {
		return empire.Identity{}, err
	}
	guest := c.playerID != "" && p.game.accountState(ctx, pid) != "base"
	return empire.Identity{ID: pid, Name: name, Guest: guest}, nil
}

func (p *EmpirePlay) serveConn(ws *websocket.Conn) {
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

func writeEmpireJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP routes the /api/v1/empire/ paths.
func (p *EmpirePlay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/empire/"), "/")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case path == "status":
		if p.Hub == nil {
			writeEmpireJSON(w, map[string]any{"configured": false})
			return
		}
		st := p.Hub.Stats()
		writeEmpireJSON(w, map[string]any{"configured": true, "version": p.Hub.Version(), "online": st.Online, "searching": st.Searching,
			"playing": st.Playing, "rooms": st.Rooms, "watching": st.Watching})
	case path == "live":
		if p.Hub == nil {
			writeEmpireJSON(w, map[string]any{"live": []any{}})
			return
		}
		writeEmpireJSON(w, map[string]any{"live": p.Hub.Live()})
	case path == "bots":
		p.botsList(w, r)
	case path == "matches":
		p.matchList(w, r)
	case strings.HasPrefix(path, "matches/"):
		p.matchOne(w, r, strings.TrimPrefix(path, "matches/"))
	case strings.HasPrefix(path, "replays/"):
		p.replay(w, r, strings.TrimPrefix(path, "replays/"))
	case strings.HasPrefix(path, "players/"):
		p.player(w, r, strings.TrimPrefix(path, "players/"))
	case path == "cards":
		p.cardStats(w, r)
	case path == "play":
		if p.Hub == nil {
			http.Error(w, "the EMPIRE gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		p.ws.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ---- reading matches ------------------------------------------------------------------------------------------------------------------------

type empireSeat struct {
	PlayerID string  `json:"player_id"`
	Name     string  `json:"name"`
	Bot      int     `json:"bot"`
	Kind     string  `json:"kind"`
	Deck     []int   `json:"deck"`
	Faction  int     `json:"faction"`
	Before   float64 `json:"rating_before"`
	After    float64 `json:"rating_after"`
	Crowns   int     `json:"crowns"`
	TowerHP  int     `json:"tower_hp"`
	Towers   int     `json:"towers"`
}

type empireMatchRow struct {
	ID       int64               `json:"id"`
	Mode     string              `json:"mode"`
	Seats    [2]empireSeat       `json:"seats"`
	Winner   int                 `json:"winner"`
	Reason   int                 `json:"reason"`
	Ticks    int                 `json:"ticks"`
	Counted  bool                `json:"counted"`
	Version  int                 `json:"sim_version"`
	PlayedAt string              `json:"played_at"`
	Stats    *[2]json.RawMessage `json:"stats,omitempty"`
	Plays    *[2]json.RawMessage `json:"plays,omitempty"`
}

func deckIDs(d uint32) []int {
	ids := make([]int, 8)
	for k := range ids {
		ids[k] = int(d >> (4 * uint(k)) & 15)
	}
	return ids
}

const empireMatchCols = `id, mode, seat0_player, seat1_player, seat0_name, seat1_name, seat0_bot, seat1_bot, winner, reason, ticks,
	crowns0, crowns1, tower_hp0, tower_hp1, towers0, towers1, deck0, deck1, faction0, faction1,
	rating0_before, rating0_after, rating1_before, rating1_after, counted, sim_version, played_at, stats0, stats1, plays0, plays1`

func scanEmpireMatch(rows interface{ Scan(...any) error }, full bool) (empireMatchRow, error) {
	var m empireMatchRow
	var d0, d1 int64
	var counted int
	var s0, s1, p0, p1 string
	a, b := &m.Seats[0], &m.Seats[1]
	if err := rows.Scan(&m.ID, &m.Mode, &a.PlayerID, &b.PlayerID, &a.Name, &b.Name, &a.Bot, &b.Bot, &m.Winner, &m.Reason, &m.Ticks,
		&a.Crowns, &b.Crowns, &a.TowerHP, &b.TowerHP, &a.Towers, &b.Towers, &d0, &d1, &a.Faction, &b.Faction,
		&a.Before, &a.After, &b.Before, &b.After, &counted, &m.Version, &m.PlayedAt, &s0, &s1, &p0, &p1); err != nil {
		return m, err
	}
	m.Counted = counted == 1
	a.Deck, b.Deck = deckIDs(uint32(d0)), deckIDs(uint32(d1))
	for _, s := range []*empireSeat{a, b} {
		s.Kind = "human"
		if s.Bot > 0 {
			s.Kind = "bot"
		}
	}
	if full {
		m.Stats = &[2]json.RawMessage{json.RawMessage(s0), json.RawMessage(s1)}
		m.Plays = &[2]json.RawMessage{json.RawMessage(p0), json.RawMessage(p1)}
	}
	return m, nil
}

func (p *EmpirePlay) matchList(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 100)
	}
	pid := strings.TrimSpace(r.URL.Query().Get("player_id"))
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode != "ranked" && mode != "league" {
		mode = ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT `+empireMatchCols+` FROM empire_matches
		WHERE (? = '' OR seat0_player = ? OR seat1_player = ?) AND (? = '' OR mode = ?) ORDER BY id DESC LIMIT ?`, pid, pid, pid, mode, mode, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []empireMatchRow{}
	for rows.Next() {
		m, err := scanEmpireMatch(rows, false)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	writeEmpireJSON(w, map[string]any{"matches": out})
}

func (p *EmpirePlay) matchOne(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	m, err := scanEmpireMatch(p.db.QueryRowContext(ctx, `SELECT `+empireMatchCols+` FROM empire_matches WHERE id = ?`, n), true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeEmpireJSON(w, m)
}

func (p *EmpirePlay) replay(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var b []byte
	if err := p.db.QueryRowContext(ctx, `SELECT replay FROM empire_matches WHERE id = ?`, n).Scan(&b); err != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}

// ---- the AIs ----------------------------------------------------------------------------------------------------------------------------------

func (p *EmpirePlay) botsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	type row struct {
		N        int     `json:"n"`
		PlayerID string  `json:"player_id,omitempty"`
		Name     string  `json:"name"`
		Faction  int     `json:"faction"`
		Deck     []int   `json:"deck"`
		Rating   float64 `json:"rating"`
		Wins     int     `json:"wins"`
		Losses   int     `json:"losses"`
		Draws    int     `json:"draws"`
		Matches  int     `json:"matches"`
	}
	out := []row{}
	for n := 1; n <= 8; n++ {
		b := p.bots[n]
		x := row{N: n, Name: empire.BotNames[n], Faction: b.Faction, Deck: b.Deck, Rating: gameEloStart}
		var pid string
		err := p.db.QueryRowContext(ctx, `SELECT player_id FROM players WHERE provider = 'deadweight_bot' AND provider_sub = ? AND game = 'empire'`, "empire:"+x.Name).Scan(&pid)
		if err == nil {
			x.PlayerID = pid
			_ = p.db.QueryRowContext(ctx, `SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'empire'`, pid).
				Scan(&x.Rating, &x.Wins, &x.Losses, &x.Draws, &x.Matches)
		}
		out = append(out, x)
	}
	writeEmpireJSON(w, map[string]any{"bots": out})
}

// ---- players and cards --------------------------------------------------------------------------------------------------------------------------

type empireCardRow struct {
	Card  int     `json:"card"`
	Games int     `json:"games"`
	Wins  int     `json:"wins"`
	Plays int     `json:"plays"`
	Rate  float64 `json:"win_rate"`
}

func (p *EmpirePlay) cardRows(ctx context.Context, q string, args ...any) []empireCardRow {
	out := make([]empireCardRow, 16)
	for i := range out {
		out[i].Card = i
	}
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c empireCardRow
		if rows.Scan(&c.Card, &c.Games, &c.Wins, &c.Plays) == nil && c.Card >= 0 && c.Card < 16 {
			if c.Games > 0 {
				c.Rate = float64(c.Wins) / float64(c.Games)
			}
			out[c.Card] = c
		}
	}
	return out
}

func (p *EmpirePlay) cardStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var total int
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM empire_matches WHERE counted = 1`).Scan(&total)
	writeEmpireJSON(w, map[string]any{"matches": total,
		"cards": p.cardRows(ctx, `SELECT card, games, wins, plays FROM empire_card_stats ORDER BY card`)})
}

// player is the public profile: the ladder numbers (the same rows the leaderboard shows), the rank, how their cards fare, recent matches.
func (p *EmpirePlay) player(w http.ResponseWriter, r *http.Request, pid string) {
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
		 WHERE p.player_id = ? AND p.game = 'empire'`, gameEloStart, pid).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.Kind = kindOf(provider)
	rank := 0
	if s.Matches > 0 {
		_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM game_player_stats WHERE game='empire' AND matches > 0 AND rating > ?`, s.Rating).Scan(&rank)
	}
	var resigned int
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM empire_matches WHERE reason = 6 AND ((seat0_player = ? AND winner = 1) OR (seat1_player = ? AND winner = 0))`, pid, pid).Scan(&resigned)
	rows, err := p.db.QueryContext(ctx, `SELECT `+empireMatchCols+` FROM empire_matches WHERE seat0_player = ? OR seat1_player = ? ORDER BY id DESC LIMIT 20`, pid, pid)
	recent := []empireMatchRow{}
	streak := 0
	if err == nil {
		defer rows.Close()
		first := true
		counting := true
		for rows.Next() {
			m, err := scanEmpireMatch(rows, false)
			if err != nil {
				continue
			}
			recent = append(recent, m)
			seat := 0
			if m.Seats[1].PlayerID == pid {
				seat = 1
			}
			res := 0 // 1 win, -1 loss, 0 draw
			if m.Winner == seat {
				res = 1
			} else if m.Winner == 1-seat {
				res = -1
			}
			if counting {
				if first {
					streak = res
					first = false
				} else if (streak > 0 && res > 0) || (streak < 0 && res < 0) {
					streak += res
				} else {
					counting = false
				}
			}
		}
	}
	// the deck they last played
	var lastDeck []int
	var lastFac int
	if len(recent) > 0 {
		seat := 0
		if recent[0].Seats[1].PlayerID == pid {
			seat = 1
		}
		lastDeck, lastFac = recent[0].Seats[seat].Deck, recent[0].Seats[seat].Faction
	}
	writeEmpireJSON(w, map[string]any{"player": s, "rank": rank, "resigned": resigned, "streak": streak, "recent": recent,
		"cards":     p.cardRows(ctx, `SELECT card, games, wins, plays FROM empire_player_cards WHERE player_id = ? ORDER BY card`, pid),
		"last_deck": lastDeck, "last_faction": lastFac})
}

// ---- the recorder ---------------------------------------------------------------------------------------------------------------------------------

type empireRecorder struct{ g *GameOnlineHandler }

// Rating is the player's rating in the generic ladder (the starting rating for someone who has not played).
func (r *empireRecorder) Rating(ctx context.Context, playerID string) (float64, error) {
	var rt float64
	err := r.g.DB.QueryRowContext(ctx, `SELECT rating FROM game_player_stats WHERE player_id = ? AND game = 'empire'`, playerID).Scan(&rt)
	if errors.Is(err, sql.ErrNoRows) {
		return gameEloStart, nil
	}
	return rt, err
}

// BotPlayer finds or creates the empire player row an AI stands for (the same shape upsertBot gives DEADWEIGHT's bots, so the generic
// leaderboard marks it kind "bot").
func (r *empireRecorder) BotPlayer(ctx context.Context, n int, name string) (empire.Player, error) {
	var pid string
	err := r.g.DB.QueryRowContext(ctx,
		`SELECT player_id FROM players WHERE provider = 'deadweight_bot' AND provider_sub = ? AND game = 'empire'`, "empire:"+name).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		pid = uuid.New().String()
		_, err = r.g.DB.ExecContext(ctx,
			`INSERT INTO players (player_id, display_name, provider, provider_sub, game) VALUES (?,?,?,?,?)`,
			pid, name, "deadweight_bot", "empire:"+name, "empire")
	}
	if err != nil {
		return empire.Player{}, err
	}
	rt, _ := r.Rating(ctx, pid)
	return empire.Player{ID: pid, Name: name, Bot: n, Rating: rt}, nil
}

var empireReasons = map[int]string{1: "king", 2: "crowns", 3: "overtime", 4: "health", 5: "level", 6: "resigned"}

func statsJSON(st [16]int) string {
	names := []string{"elixir_spent", "elixir_wasted_milli", "plays", "mythic", "spells", "deployed", "lost", "kills", "tower_damage", "damage",
		"cancelled_ticks", "lowest_standing", "first_play_tick", "elixir_pumped", "healed", "refused"}
	m := map[string]int{}
	for i, n := range names {
		m[n] = st[i]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func playsJSON(p [16]int) string {
	b, _ := json.Marshal(p[:])
	return string(b)
}

// Record writes one finished match in one transaction: the row, the ladder (Elo, wins, losses) and the card statistics.
func (r *empireRecorder) Record(ctx context.Context, m *empire.Match) (empire.Outcome, error) {
	var out empire.Outcome
	a, b := m.Seats[0], m.Seats[1]
	if a.PlayerID == "" || b.PlayerID == "" || a.PlayerID == b.PlayerID {
		return out, errors.New("a match needs two different players")
	}
	tx, err := r.g.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	res := m.Result
	res0, err := tx.ExecContext(ctx, `
		INSERT INTO empire_matches
		  (mode, seat0_player, seat1_player, seat0_name, seat1_name, seat0_bot, seat1_bot, seed, sim_version, winner, reason, ticks,
		   crowns0, crowns1, tower_hp0, tower_hp1, towers0, towers1, deck0, deck1, faction0, faction1, stats0, stats1, plays0, plays1, replay)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.Mode, a.PlayerID, b.PlayerID, a.Name, b.Name, a.Bot, b.Bot, int64(m.Seed), int64(m.Version), res.Winner, res.Reason, res.Ticks,
		res.Crowns[0], res.Crowns[1], res.TowerHP[0], res.TowerHP[1], res.Towers[0], res.Towers[1], int64(a.Deck), int64(b.Deck), a.Faction, b.Faction,
		statsJSON(res.Stats[0]), statsJSON(res.Stats[1]), playsJSON(res.Plays[0]), playsJSON(res.Plays[1]), m.Replay)
	if err != nil {
		return out, err
	}
	id, _ := res0.LastInsertId()
	out.MatchID = id
	if m.Counted {
		reason := empireReasons[res.Reason]
		gm, err := applyGameMatch(ctx, tx, "empire", gameMatchReq{MatchID: id, Seed: int64(m.Seed), Seat0: a.PlayerID, Seat1: b.PlayerID,
			Winner: res.Winner, Rounds: res.Ticks, Reason: reason})
		if err != nil {
			return out, err
		}
		out.Counted = !gm.Dup
		out.Before = [2]float64{gm.ABefore.Rating, gm.BBefore.Rating}
		out.After = [2]float64{gm.A.Rating, gm.B.Rating}
		if _, err := tx.ExecContext(ctx, `UPDATE empire_matches SET counted = 1, rating0_before = ?, rating0_after = ?, rating1_before = ?, rating1_after = ? WHERE id = ?`,
			out.Before[0], out.After[0], out.Before[1], out.After[1], id); err != nil {
			return out, err
		}
		for seat, s := range m.Seats {
			won := 0
			if res.Winner == seat {
				won = 1
			}
			for k := 0; k < 8; k++ {
				c := int(s.Deck >> (4 * uint(k)) & 15)
				plays := res.Plays[seat][c]
				if _, err := tx.ExecContext(ctx, `INSERT INTO empire_card_stats (card, games, wins, plays) VALUES (?,1,?,?)
					ON CONFLICT(card) DO UPDATE SET games = games + 1, wins = wins + excluded.wins, plays = plays + excluded.plays`, c, won, plays); err != nil {
					return out, err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO empire_player_cards (player_id, card, games, wins, plays) VALUES (?,?,1,?,?)
					ON CONFLICT(player_id, card) DO UPDATE SET games = games + 1, wins = wins + excluded.wins, plays = plays + excluded.plays`, s.PlayerID, c, won, plays); err != nil {
					return out, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
