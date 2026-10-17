package redgardenplay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DBRecorder writes a finished match where WOTAN's REDGARDEN pages already read it from: player_game_stats (the
// leaderboard and profile totals), redgarden_player_matches (the per-player hero / recent-match log) and
// redgarden_hero_stats (the public hero win rates) -- the same three aggregates apps/arena_server reports through
// /api/v1/redgarden/game-result and /hero-result for native players -- plus redgarden_web_matches, the full record of
// the match (mode, kills, deaths, duration, the whole roster) that the web client's own history reads.
//
// One transaction, and nothing else touches the database inside it: IDUNA's SQLite pool is a single connection.
type DBRecorder struct {
	DB *sql.DB
}

// ArenaGame is the player_game_stats.game value the arena has always reported under.
const ArenaGame = "redgarden-arena"

type rosterEntry struct {
	Slot   int  `json:"slot"`
	Team   int  `json:"team"`
	Hero   int  `json:"hero"`
	Kills  int  `json:"kills"`
	Deaths int  `json:"deaths"`
	You    bool `json:"you,omitempty"`
}

// Record implements Recorder.
func (r *DBRecorder) Record(ctx context.Context, m MatchRecord) error {
	if r == nil || r.DB == nil {
		return errors.New("no database")
	}
	if m.PlayerID == "" {
		return errors.New("no player")
	}
	roster := make([]rosterEntry, 0, len(m.Roster))
	for _, h := range m.Roster {
		roster = append(roster, rosterEntry{Slot: h.Slot, Team: h.Team, Hero: h.HeroID, Kills: h.Kills, Deaths: h.Deaths, You: h.Slot == m.Slot})
	}
	rj, _ := json.Marshal(roster)
	result := "abandoned"
	switch m.Result {
	case ResWin:
		result = "win"
	case ResLoss:
		result = "loss"
	case ResLost:
		result = "aborted"
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	counted := 0
	if m.Counted {
		counted = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO redgarden_web_matches
		  (player_id, pool, mode, lobby, slot, team, hero_id, result, winner_team, kills, deaths, flow_earned, xp, seconds, counted, roster)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.PlayerID, m.Pool, modeName(m.Mode), m.Lobby, m.Slot, m.Team, m.HeroID, result, m.WinnerTeam, m.Kills, m.Deaths,
		m.FlowEarned, m.XP, m.Seconds, counted, string(rj)); err != nil {
		return fmt.Errorf("web match: %w", err)
	}
	if m.Counted {
		win, loss := 0, 0
		if m.Result == ResWin {
			win = 1
		} else {
			loss = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO player_game_stats (player_id, game, wins, losses, matches_played, last_played_at)
			VALUES (?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
			ON CONFLICT(player_id, game) DO UPDATE SET
				wins = wins + excluded.wins,
				losses = losses + excluded.losses,
				matches_played = matches_played + 1,
				last_played_at = CURRENT_TIMESTAMP`, m.PlayerID, ArenaGame, win, loss); err != nil {
			return fmt.Errorf("stats: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO redgarden_player_matches (player_id, hero_id, result) VALUES (?, ?, ?)`,
			m.PlayerID, m.HeroID, result); err != nil {
			return fmt.Errorf("player match: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO redgarden_hero_stats (hero_id, wins, losses, matches_played, last_played_at)
			VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
			ON CONFLICT(hero_id) DO UPDATE SET
				wins = wins + excluded.wins,
				losses = losses + excluded.losses,
				matches_played = matches_played + 1,
				last_played_at = CURRENT_TIMESTAMP`, m.HeroID, win, loss); err != nil {
			return fmt.Errorf("hero stats: %w", err)
		}
	}
	return tx.Commit()
}
