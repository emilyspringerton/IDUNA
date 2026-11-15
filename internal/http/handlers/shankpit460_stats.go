package handlers

// shankpit460_stats.go -- shankpit-460 web's results: the recorder the matchmaker (internal/shankpit460) hands every match
// that went live to, and the public reads WOTAN's ladder, match and player pages use. Founder real-time, 2026-11-15: "we
// need esports and stats up asap ... start tracking and training bots asap for our various esports leagues".
//
//	GET /api/v1/shankpit460/matches               ?limit=&player_id=   recent matches with everyone in them, finishing order
//	GET /api/v1/shankpit460/matches/{id}          one match
//	GET /api/v1/shankpit460/players/{player_id}   rating, rank, record, totals and recent matches of one person
//	GET /api/v1/shankpit460/leaders               ?stat=kills|wins|kd|best&limit=
//
// The ladder itself is the generic one (GET /api/v1/games/shankpit460/leaderboard: game_player_stats for 'shankpit460').
//
// The rating: a free-for-all of up to ten is scored as every pair of people in it, by finishing order (ahead = 1, equal
// = 1/2, behind = 0), with K/(n-1) per pairing, so one match moves a rating no further than a two-seat match would. A
// guest has no rating of their own and counts as the starting rating, as Bedrock Racers counts its bots (so a match
// against guests still moves the ladder; a rating far above the start gains almost nothing from one). Leaving a live
// match is placed below everyone who stayed and recorded as "left", which is a loss; the last person standing in an
// abandoned match wins by forfeit. An abandoned match counts only after internal/shankpit460.MinCounted of play.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iduna/internal/shankpit460"
)

// ---- the recorder -----------------------------------------------------------------------------------------------------

type sp460Recorder struct{ db *sql.DB }

// NewShankpit460Recorder is the recorder NewShankpit460Play installs, for anything else that plays shankpit-460 matches
// against the same ladder (and for tests).
func NewShankpit460Recorder(db *sql.DB) shankpit460.Recorder { return &sp460Recorder{db: db} }

// sp460Line is one person in a match as the API (and the roster column) shows them.
type sp460Line struct {
	Slot         int     `json:"slot"`
	PlayerID     string  `json:"player_id,omitempty"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"` // human | guest
	Kills        int     `json:"kills"`
	Deaths       int     `json:"deaths"`
	Place        int     `json:"place"`
	Left         bool    `json:"left,omitempty"`
	LeftAfterMS  int64   `json:"left_after_ms,omitempty"`
	Result       string  `json:"result"`
	RatingBefore float64 `json:"rating_before,omitempty"`
	RatingAfter  float64 `json:"rating_after,omitempty"`
}

func sp460Result(p shankpit460.MatchPlayer, sharedFirst bool) string {
	switch {
	case p.Left:
		return "left"
	case p.Winner:
		return "win"
	case p.Place == 1 && sharedFirst:
		return "draw"
	}
	return "loss"
}

// sp460Deltas is each person's rating change: every pair by finishing order, K/(n-1) per pairing.
func sp460Deltas(ratings []float64, places []int) []float64 {
	n := len(ratings)
	out := make([]float64, n)
	if n < 2 {
		return out
	}
	k := gameEloK / float64(n-1)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			s := 0.5
			switch {
			case places[i] < places[j]:
				s = 1
			case places[i] > places[j]:
				s = 0
			}
			e := 1.0 / (1.0 + math.Pow(10, (ratings[j]-ratings[i])/400.0))
			out[i] += k * (s - e)
		}
	}
	return out
}

// Record writes one match in one transaction: the row, each signed-in person's line, and (for a match that counts) the
// ladder. A match already recorded (same uid) changes nothing.
func (r *sp460Recorder) Record(ctx context.Context, m *shankpit460.Match) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shankpit460_matches WHERE match_uid = ?`, m.UID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	type acct struct {
		before                      float64
		wins, losses, draws, played int
	}
	accts := make([]*acct, len(m.Players))
	ratings := make([]float64, len(m.Players))
	places := make([]int, len(m.Players))
	signedIn := 0
	firsts := 0
	for i, p := range m.Players {
		ratings[i], places[i] = gameEloStart, p.Place
		if p.Place == 1 && !p.Left {
			firsts++
		}
		if p.Guest || p.ID == "" {
			continue
		}
		// a row in players for this game is what makes someone rated; anything else is treated as a guest
		var ok int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM players WHERE player_id = ? AND game = 'shankpit460'`, p.ID).Scan(&ok); err != nil {
			return err
		}
		if ok == 0 {
			continue
		}
		_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_player_stats (player_id, game) VALUES (?, 'shankpit460')`, p.ID)
		a := &acct{}
		if err := tx.QueryRowContext(ctx, `SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = 'shankpit460'`, p.ID).
			Scan(&a.before, &a.wins, &a.losses, &a.draws, &a.played); err != nil {
			return err
		}
		accts[i], ratings[i] = a, a.before
		signedIn++
	}
	var deltas []float64
	if m.Counted && signedIn > 0 {
		deltas = sp460Deltas(ratings, places)
	}
	lines := make([]sp460Line, len(m.Players))
	winnerName, winnerID, topKills := "", "", 0
	for i, p := range m.Players {
		l := sp460Line{Slot: p.Slot, Name: p.Name, Kind: "guest", Kills: p.Kills, Deaths: p.Deaths, Place: p.Place, Left: p.Left,
			LeftAfterMS: p.LeftAt.Milliseconds(), Result: sp460Result(p, firsts > 1)}
		if a := accts[i]; a != nil {
			l.PlayerID, l.Kind = p.ID, "human"
			l.RatingBefore, l.RatingAfter = a.before, a.before
			if deltas != nil {
				l.RatingAfter = a.before + deltas[i]
			}
		}
		if p.Winner {
			winnerName, winnerID = p.Name, l.PlayerID
		}
		if p.Kills > topKills {
			topKills = p.Kills
		}
		lines[i] = l
	}
	roster, _ := json.Marshal(lines)
	counted := 0
	if m.Counted {
		counted = 1
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO shankpit460_matches
		(match_uid, room_id, reason, kill_limit, live_ms, players, signed_in, winner_name, winner_id, top_kills, counted, roster)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.UID, int64(m.RoomID), m.Reason, m.KillLimit, m.EndedAt.Sub(m.LiveAt).Milliseconds(), len(m.Players), signedIn,
		winnerName, winnerID, topKills, counted, string(roster))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	for i, a := range accts {
		if a == nil {
			continue
		}
		l := lines[i]
		if _, err := tx.ExecContext(ctx, `INSERT INTO shankpit460_player_matches
			(match_id, player_id, slot, place, players, result, kills, deaths, rating_before, rating_after, counted)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, l.PlayerID, l.Slot, l.Place, len(m.Players), l.Result, l.Kills, l.Deaths, l.RatingBefore, l.RatingAfter, counted); err != nil {
			return err
		}
		if !m.Counted {
			continue
		}
		wins, losses, draws := a.wins, a.losses, a.draws
		switch l.Result {
		case "win":
			wins++
		case "draw":
			draws++
		default: // a loss, or leaving, which is one
			losses++
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_player_stats SET rating=?, wins=?, losses=?, draws=?, matches=?, last_match_at=CURRENT_TIMESTAMP
			WHERE player_id=? AND game='shankpit460'`, l.RatingAfter, wins, losses, draws, a.played+1, l.PlayerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- reads ----------------------------------------------------------------------------------------------------------------

type sp460Match struct {
	ID         int64       `json:"id"`
	Reason     string      `json:"reason"` // kills | time | abandoned
	KillLimit  int         `json:"kill_limit"`
	LiveMS     int64       `json:"live_ms"`
	Players    int         `json:"players"`
	SignedIn   int         `json:"signed_in"`
	WinnerName string      `json:"winner_name,omitempty"`
	WinnerID   string      `json:"winner_id,omitempty"`
	TopKills   int         `json:"top_kills"`
	Counted    bool        `json:"counted"`
	PlayedAt   string      `json:"played_at"`
	Roster     []sp460Line `json:"roster"`
}

const sp460MatchCols = `id, reason, kill_limit, live_ms, players, signed_in, winner_name, winner_id, top_kills, counted, played_at, roster`

func sp460ReasonName(n int) string {
	switch n {
	case shankpit460.EndKills:
		return "kills"
	case shankpit460.EndTime:
		return "time"
	case shankpit460.EndAbandoned:
		return "abandoned"
	}
	return "unknown"
}

func scanSP460Match(row interface{ Scan(...any) error }) (sp460Match, error) {
	var m sp460Match
	var reason, counted int
	var roster string
	if err := row.Scan(&m.ID, &reason, &m.KillLimit, &m.LiveMS, &m.Players, &m.SignedIn, &m.WinnerName, &m.WinnerID, &m.TopKills,
		&counted, &m.PlayedAt, &roster); err != nil {
		return m, err
	}
	m.Reason, m.Counted = sp460ReasonName(reason), counted == 1
	_ = json.Unmarshal([]byte(roster), &m.Roster)
	if m.Roster == nil {
		m.Roster = []sp460Line{}
	}
	return m, nil
}

func (p *Shankpit460Play) serveStats(w http.ResponseWriter, r *http.Request, path string) bool {
	if p.db == nil {
		return false
	}
	switch {
	case path == "matches":
		p.sp460Matches(w, r)
	case strings.HasPrefix(path, "matches/"):
		p.sp460Match(w, r, strings.TrimPrefix(path, "matches/"))
	case strings.HasPrefix(path, "players/"):
		p.sp460Player(w, r, strings.TrimPrefix(path, "players/"))
	case path == "leaders":
		p.sp460Leaders(w, r)
	default:
		return false
	}
	return true
}

func (p *Shankpit460Play) sp460Matches(w http.ResponseWriter, r *http.Request) {
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
		rows, err = p.db.QueryContext(ctx, `SELECT `+sp460MatchCols+` FROM shankpit460_matches ORDER BY id DESC LIMIT ?`, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `SELECT `+sp460MatchCols+` FROM shankpit460_matches WHERE id IN
			(SELECT match_id FROM shankpit460_player_matches WHERE player_id = ?) ORDER BY id DESC LIMIT ?`, pid, limit)
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []sp460Match{}
	for rows.Next() {
		m, err := scanSP460Match(rows)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	writeBRJSON(w, map[string]any{"matches": out})
}

func (p *Shankpit460Play) sp460Match(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	m, err := scanSP460Match(p.db.QueryRowContext(ctx, `SELECT `+sp460MatchCols+` FROM shankpit460_matches WHERE id = ?`, n))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeBRJSON(w, m)
}

type sp460Totals struct {
	Matches   int `json:"matches"`
	Kills     int `json:"kills"`
	Deaths    int `json:"deaths"`
	Wins      int `json:"wins"`
	Left      int `json:"left"`
	BestKills int `json:"best_kills"`
}

func (p *Shankpit460Play) sp460Player(w http.ResponseWriter, r *http.Request, pid string) {
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
		 WHERE p.player_id = ? AND p.game = 'shankpit460'`, gameEloStart, pid).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	s.Kind = kindOf(provider)
	rank := 0
	if s.Matches > 0 {
		_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM game_player_stats WHERE game='shankpit460' AND matches > 0 AND rating > ?`, s.Rating).Scan(&rank)
	}
	var t sp460Totals
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(kills),0), COALESCE(SUM(deaths),0),
		COALESCE(SUM(CASE WHEN result = 'win' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN result = 'left' THEN 1 ELSE 0 END),0), COALESCE(MAX(kills),0)
		FROM shankpit460_player_matches WHERE player_id = ?`, pid).Scan(&t.Matches, &t.Kills, &t.Deaths, &t.Wins, &t.Left, &t.BestKills)
	type recentRow struct {
		sp460Match
		Place        int     `json:"place"`
		Result       string  `json:"result"`
		Kills        int     `json:"kills"`
		Deaths       int     `json:"deaths"`
		RatingBefore float64 `json:"rating_before"`
		RatingAfter  float64 `json:"rating_after"`
	}
	recent := []recentRow{}
	rows, err := p.db.QueryContext(ctx, `SELECT m.id, m.reason, m.kill_limit, m.live_ms, m.players, m.signed_in, m.winner_name, m.winner_id, m.top_kills,
		m.counted, m.played_at, m.roster, pm.place, pm.result, pm.kills, pm.deaths, pm.rating_before, pm.rating_after
		FROM shankpit460_player_matches pm JOIN shankpit460_matches m ON m.id = pm.match_id WHERE pm.player_id = ? ORDER BY m.id DESC LIMIT 20`, pid)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var x recentRow
			var reason, counted int
			var roster string
			if rows.Scan(&x.ID, &reason, &x.KillLimit, &x.LiveMS, &x.Players, &x.SignedIn, &x.WinnerName, &x.WinnerID, &x.TopKills, &counted, &x.PlayedAt,
				&roster, &x.Place, &x.Result, &x.Kills, &x.Deaths, &x.RatingBefore, &x.RatingAfter) != nil {
				continue
			}
			x.Reason, x.Counted = sp460ReasonName(reason), counted == 1
			_ = json.Unmarshal([]byte(roster), &x.Roster)
			if x.Roster == nil {
				x.Roster = []sp460Line{}
			}
			recent = append(recent, x)
		}
	}
	writeBRJSON(w, map[string]any{"player": s, "rank": rank, "totals": t, "recent": recent})
}

// sp460Leaders is the top of one statistic across everyone with an account who has played. kd needs five matches.
func (p *Shankpit460Play) sp460Leaders(w http.ResponseWriter, r *http.Request) {
	agg := map[string]string{
		"kills": "SUM(pm.kills)", "wins": "SUM(CASE WHEN pm.result = 'win' THEN 1 ELSE 0 END)", "best": "MAX(pm.kills)",
		"kd": "CAST(SUM(pm.kills) AS REAL) / MAX(1, SUM(pm.deaths))",
	}
	stat := r.URL.Query().Get("stat")
	expr, ok := agg[stat]
	if !ok {
		stat, expr = "kills", agg["kills"]
	}
	having := "v > 0"
	if stat == "kd" {
		having = "v > 0 AND n >= 5"
	}
	limit := 10
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 50)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT pm.player_id, p.display_name, p.provider, `+expr+` AS v, COUNT(*) AS n
		FROM shankpit460_player_matches pm JOIN players p ON p.player_id = pm.player_id
		GROUP BY pm.player_id HAVING `+having+` ORDER BY v DESC, n ASC LIMIT ?`, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type row struct {
		PlayerID string  `json:"player_id"`
		Name     string  `json:"display_name"`
		Kind     string  `json:"kind"`
		Value    float64 `json:"value"`
		Matches  int     `json:"matches"`
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
