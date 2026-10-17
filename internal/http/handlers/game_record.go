package handlers

// game_record.go -- the one place a finished 1v1 game match is written to game_matches / game_player_stats (Elo,
// wins, losses, draws). POST /api/v1/games/{game}/match-result (an M2M agent reporting) and the D2 browser gateway
// (internal/d2play, which is the only thing that knows who is playing) both go through it, so there is one rating rule.

import (
	"context"
	"database/sql"
	"math"
)

type gameMatchReq struct {
	MatchID, Seed int64
	Seat0, Seat1  string
	Winner        int // 0 = seat 0 won, 1 = seat 1 won, 2 = draw
	Rounds        int
	Reason        string
	Mode          int
}

type gameMatchResult struct {
	Dup              bool      // this exact match was already recorded: nothing was changed
	A, B             seatStats // after
	ABefore, BBefore seatStats
}

// applyGameMatch inserts the match (idempotent on game, match, seed and seats) and, the first time, moves both players'
// rating and counters. The caller owns the transaction and has already checked that the players exist.
func applyGameMatch(ctx context.Context, tx *sql.Tx, slug string, req gameMatchReq) (gameMatchResult, error) {
	var out gameMatchResult
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO game_matches (game, match_id, seed, seat0_player_id, seat1_player_id, winner, rounds, reason, mode)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		slug, req.MatchID, req.Seed, req.Seat0, req.Seat1, req.Winner, req.Rounds, req.Reason, req.Mode)
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		out.Dup = true
	}
	load := func(pid string) (seatStats, error) {
		_, _ = tx.ExecContext(ctx, `INSERT OR IGNORE INTO game_player_stats (player_id, game) VALUES (?,?)`, pid, slug)
		var s seatStats
		err := tx.QueryRowContext(ctx, `SELECT rating, wins, losses, draws, matches FROM game_player_stats WHERE player_id = ? AND game = ?`,
			pid, slug).Scan(&s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
		return s, err
	}
	a, err := load(req.Seat0)
	if err != nil {
		return out, err
	}
	b, err := load(req.Seat1)
	if err != nil {
		return out, err
	}
	out.ABefore, out.BBefore = a, b
	if !out.Dup {
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
				 WHERE player_id=? AND game=?`, s.Rating, s.Wins, s.Losses, s.Draws, s.Matches, pid, slug); err != nil {
				return out, err
			}
		}
	}
	out.A, out.B = a, b
	return out, nil
}
