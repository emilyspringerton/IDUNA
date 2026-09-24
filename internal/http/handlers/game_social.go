package handlers

// game_social.go -- S536: friends, public profiles, and friendly-challenge (duel) primitives,
// layered onto the same per-game online-services surface as game_online.go (routes under
// /api/v1/games/{game}/...). Founder real-time, 2026-09-24: "add iduna online accounts / add
// social features / profiles / friends / friendly challenges (duels) / for DEADWEIGHT / WOTAN".
// Dispatched from GameOnlineHandler.ServeHTTP's own switch (game_online.go). Reuses
// draftPlayerClaims (already resolves "authenticated human player for this game" for the
// draft-run routes) rather than inventing a second auth helper.
//
// V0 scope: the friend-request and duel-challenge lifecycles are real and complete (send/accept/
// decline/list/remove). Turning an accepted duel into a live match instance is real, named,
// deferred follow-up work -- it needs a real per-game match-start mechanism (e.g. DEADWEIGHT's
// own ticket/queue system), not invented here -- see DEADWEIGHT/NORTHSTAR.md's social-features
// section. An accepted duel record is enough today for two friends to know it's on and connect
// manually, same "correct primitive, no consumer yet" pattern as BIG_O's lab centrifuge.
//
// Friendship has no separate "friendships" table: an accepted friend_requests row IS the
// friendship (a friends list is a query over status='accepted', either direction).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"iduna/internal/games"
)

type friendRequest struct {
	ID          int64  `json:"id"`
	RequesterID string `json:"requester_id"`
	RecipientID string `json:"recipient_id"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type friendSummary struct {
	PlayerID    string  `json:"player_id"`
	DisplayName string  `json:"display_name"`
	Rating      float64 `json:"rating"`
}

type duelChallenge struct {
	ID           int64  `json:"id"`
	ChallengerID string `json:"challenger_id"`
	ChallengedID string `json:"challenged_id"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

// profile is GET /api/v1/games/{game}/players/{id}/profile -- public, no auth, matching
// leaderboard/stats' own public-read convention. Adds friend_count on top of stats().
func (h *GameOnlineHandler) profile(w http.ResponseWriter, r *http.Request, cfg games.Config, pid string) {
	var s playerStats
	var provider string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT p.player_id, p.display_name, p.provider, COALESCE(st.rating,?), COALESCE(st.wins,0), COALESCE(st.losses,0), COALESCE(st.draws,0), COALESCE(st.matches,0)
		 FROM players p LEFT JOIN game_player_stats st ON st.player_id = p.player_id AND st.game = p.game
		 WHERE p.player_id = ? AND p.game = ?`, gameEloStart, pid, cfg.Slug).
		Scan(&s.PlayerID, &s.DisplayName, &provider, &s.Rating, &s.Wins, &s.Losses, &s.Draws, &s.Matches)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, "player not found")
		return
	}
	s.Kind = kindOf(provider)
	var friendCount int
	_ = h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM friend_requests WHERE game = ? AND status = 'accepted' AND (requester_id = ? OR recipient_id = ?)`,
		cfg.Slug, pid, pid).Scan(&friendCount)
	writeJSON(w, http.StatusOK, map[string]any{
		"player_id": s.PlayerID, "display_name": s.DisplayName, "kind": s.Kind,
		"rating": s.Rating, "wins": s.Wins, "losses": s.Losses, "draws": s.Draws, "matches": s.Matches,
		"friend_count": friendCount,
	})
}

func (h *GameOnlineHandler) friendRequestCreate(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	var req struct {
		ToPlayerID string `json:"to_player_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ToPlayerID == "" {
		mmoWriteError(w, http.StatusBadRequest, "to_player_id required")
		return
	}
	if req.ToPlayerID == pid {
		mmoWriteError(w, http.StatusBadRequest, "cannot friend yourself")
		return
	}
	var exists int
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM players WHERE player_id = ? AND game = ?`, req.ToPlayerID, cfg.Slug).Scan(&exists)
	if exists == 0 {
		mmoWriteError(w, http.StatusNotFound, "player not found")
		return
	}

	var already int
	_ = h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM friend_requests WHERE game=? AND status='accepted' AND
		 ((requester_id=? AND recipient_id=?) OR (requester_id=? AND recipient_id=?))`,
		cfg.Slug, pid, req.ToPlayerID, req.ToPlayerID, pid).Scan(&already)
	if already > 0 {
		mmoWriteError(w, http.StatusConflict, "already friends")
		return
	}

	// A reverse pending request already exists -- treat this as mutual acceptance instead of a
	// duplicate/crossed-wires request: two people who both hit "add friend" on each other end up
	// friends immediately, not stuck with two dangling pending rows pointed at each other.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if res, err := h.DB.ExecContext(r.Context(),
		`UPDATE friend_requests SET status='accepted', responded_at=? WHERE game=? AND requester_id=? AND recipient_id=? AND status='pending'`,
		now, cfg.Slug, req.ToPlayerID, pid); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"status": "accepted"})
			return
		}
	}

	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO friend_requests (game, requester_id, recipient_id, status) VALUES (?,?,?,'pending')`,
		cfg.Slug, pid, req.ToPlayerID); err != nil {
		mmoWriteError(w, http.StatusConflict, "request already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "pending"})
}

func (h *GameOnlineHandler) friendRequestsList(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, requester_id, recipient_id, status, created_at FROM friend_requests
		 WHERE game = ? AND status = 'pending' AND (requester_id = ? OR recipient_id = ?)
		 ORDER BY created_at DESC`, cfg.Slug, pid, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	incoming := []friendRequest{}
	outgoing := []friendRequest{}
	for rows.Next() {
		var fr friendRequest
		if err := rows.Scan(&fr.ID, &fr.RequesterID, &fr.RecipientID, &fr.Status, &fr.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if fr.RecipientID == pid {
			incoming = append(incoming, fr)
		} else {
			outgoing = append(outgoing, fr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"incoming": incoming, "outgoing": outgoing})
}

func (h *GameOnlineHandler) friendRequestRespond(w http.ResponseWriter, r *http.Request, cfg games.Config, idStr string, accept bool) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid request id")
		return
	}
	status := "declined"
	if accept {
		status = "accepted"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE friend_requests SET status=?, responded_at=? WHERE id=? AND game=? AND recipient_id=? AND status='pending'`,
		status, now, id, cfg.Slug, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "request not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (h *GameOnlineHandler) friendsList(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT p.player_id, p.display_name, COALESCE(st.rating, ?)
		 FROM friend_requests fr
		 JOIN players p ON p.game = ? AND p.player_id = (CASE WHEN fr.requester_id = ? THEN fr.recipient_id ELSE fr.requester_id END)
		 LEFT JOIN game_player_stats st ON st.player_id = p.player_id AND st.game = p.game
		 WHERE fr.game = ? AND fr.status = 'accepted' AND (fr.requester_id = ? OR fr.recipient_id = ?)`,
		gameEloStart, cfg.Slug, pid, cfg.Slug, pid, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []friendSummary{}
	for rows.Next() {
		var fs friendSummary
		if err := rows.Scan(&fs.PlayerID, &fs.DisplayName, &fs.Rating); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, fs)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *GameOnlineHandler) friendRemove(w http.ResponseWriter, r *http.Request, cfg games.Config, otherID string) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM friend_requests WHERE game=? AND status='accepted' AND
		 ((requester_id=? AND recipient_id=?) OR (requester_id=? AND recipient_id=?))`,
		cfg.Slug, pid, otherID, otherID, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "not friends")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed"})
}

func (h *GameOnlineHandler) duelCreate(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	var req struct {
		ToPlayerID string `json:"to_player_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ToPlayerID == "" {
		mmoWriteError(w, http.StatusBadRequest, "to_player_id required")
		return
	}
	if req.ToPlayerID == pid {
		mmoWriteError(w, http.StatusBadRequest, "cannot duel yourself")
		return
	}
	// Friendly challenges are, per the name, between friends -- the founder's own pairing of
	// "friends" and "friendly challenges (duels)" in the same ask.
	var friends int
	_ = h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM friend_requests WHERE game=? AND status='accepted' AND
		 ((requester_id=? AND recipient_id=?) OR (requester_id=? AND recipient_id=?))`,
		cfg.Slug, pid, req.ToPlayerID, req.ToPlayerID, pid).Scan(&friends)
	if friends == 0 {
		mmoWriteError(w, http.StatusForbidden, "can only duel a friend")
		return
	}
	var pendingExisting int
	_ = h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM duel_challenges WHERE game=? AND status='pending' AND challenger_id=? AND challenged_id=?`,
		cfg.Slug, pid, req.ToPlayerID).Scan(&pendingExisting)
	if pendingExisting > 0 {
		mmoWriteError(w, http.StatusConflict, "duel already pending")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO duel_challenges (game, challenger_id, challenged_id, status) VALUES (?,?,?,'pending')`,
		cfg.Slug, pid, req.ToPlayerID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "pending"})
}

func (h *GameOnlineHandler) duelsList(w http.ResponseWriter, r *http.Request, cfg games.Config) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, challenger_id, challenged_id, status, created_at FROM duel_challenges
		 WHERE game = ? AND (challenger_id = ? OR challenged_id = ?)
		 ORDER BY created_at DESC LIMIT 50`, cfg.Slug, pid, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []duelChallenge{}
	for rows.Next() {
		var d duelChallenge
		if err := rows.Scan(&d.ID, &d.ChallengerID, &d.ChallengedID, &d.Status, &d.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *GameOnlineHandler) duelRespond(w http.ResponseWriter, r *http.Request, cfg games.Config, idStr string, accept bool) {
	claims := h.bearerClaims(w, r)
	if claims == nil {
		return
	}
	pid, ok := draftPlayerClaims(claims, cfg)
	if !ok {
		mmoWriteError(w, http.StatusForbidden, "token is not valid for this game")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid duel id")
		return
	}
	status := "declined"
	if accept {
		status = "accepted"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE duel_challenges SET status=?, responded_at=? WHERE id=? AND game=? AND challenged_id=? AND status='pending'`,
		status, now, id, cfg.Slug, pid)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "duel not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
}
