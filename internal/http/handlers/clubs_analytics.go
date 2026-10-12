package handlers

// clubs_analytics.go -- WOTAN Phase 2 Clubs, chunk P2-B: members-only matchup matrix and contributor board
// (contract section 7.4) and the public contributor board of section 0-R5. Every query is scoped by the
// club id that came out of guard()/resolvePublic -- never from the request body or query string.

import (
	"net/http"
	"sort"

	"iduna/internal/clubs"
)

const clubMinSample = 5

// clubWindow maps ?window= onto a SQLite datetime modifier ("" = all time).
func clubWindow(w http.ResponseWriter, raw, def string) (name, modifier string, ok bool) {
	if raw == "" {
		raw = def
	}
	switch raw {
	case "week":
		return raw, "-7 days", true
	case "month":
		return raw, "-30 days", true
	case "all":
		return raw, "", true
	}
	clubErr(w, http.StatusUnprocessableEntity, "bad_window", "window must be week, month or all")
	return "", "", false
}

func (h *ClubsHandler) analyticsMatchups(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeAnalyticsView)
	if g == nil {
		return
	}
	win, mod, ok := clubWindow(w, r.URL.Query().Get("window"), "week")
	if !ok {
		return
	}
	q := `SELECT my_class, opp_class, COALESCE(SUM(result=1),0), COALESCE(SUM(result=2),0), COUNT(*)
		FROM hs_games WHERE club_id=? AND visibility='club' AND complete=1 AND result IN (1,2,3) AND my_class!='' AND opp_class!=''`
	args := []any{g.c.ID}
	if mod != "" {
		q += ` AND created_at >= datetime('now', ?)`
		args = append(args, mod)
	}
	q += ` GROUP BY my_class, opp_class`
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	matrix := map[string]map[string]map[string]int{}
	total := 0
	for rows.Next() {
		var mc, oc string
		var wins, losses, games int
		if rows.Scan(&mc, &oc, &wins, &losses, &games) != nil {
			continue
		}
		if matrix[mc] == nil {
			matrix[mc] = map[string]map[string]int{}
		}
		matrix[mc][oc] = map[string]int{"wins": wins, "losses": losses, "games": games}
		total += games
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": win, "min_sample": clubMinSample, "games": total, "matrix": matrix})
}

type clubContrib struct {
	handle        string
	wins, losses  int
	games         int
	former        bool
	publicVisible bool
}

// contributorRows aggregates a club's tagged games per owner. Current members carry their handle; everyone
// else is folded into one de-identified "former member" row (their sub is never emitted).
func (h *ClubsHandler) contributorRows(clubID, mod string) ([]clubContrib, error) {
	q := `SELECT g.owner_sub, COALESCE(p.handle,''), COALESCE(m.public_visible,0), m.sub IS NOT NULL,
		COALESCE(SUM(g.result=1),0), COALESCE(SUM(g.result=2),0), COUNT(*)
		FROM hs_games g
		LEFT JOIN club_memberships m ON m.club_id=g.club_id AND m.sub=g.owner_sub
		LEFT JOIN hs_profiles p ON p.sub=g.owner_sub
		WHERE g.club_id=? AND g.visibility='club' AND g.complete=1`
	args := []any{clubID}
	if mod != "" {
		q += ` AND g.created_at >= datetime('now', ?)`
		args = append(args, mod)
	}
	q += ` GROUP BY g.owner_sub`
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []clubContrib{}
	var former clubContrib
	former.former = true
	for rows.Next() {
		var sub, handle string
		var pv, member, wins, losses, games int
		if rows.Scan(&sub, &handle, &pv, &member, &wins, &losses, &games) != nil {
			continue
		}
		if member == 0 || handle == "" {
			former.wins += wins
			former.losses += losses
			former.games += games
			continue
		}
		out = append(out, clubContrib{handle: handle, wins: wins, losses: losses, games: games, publicVisible: pv == 1})
	}
	if former.games > 0 {
		out = append(out, former)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.wins != b.wins {
			return a.wins > b.wins
		}
		if a.games != b.games {
			return a.games > b.games
		}
		return a.handle < b.handle
	})
	return out, nil
}

func contribItems(rows []clubContrib, publicOnly bool) []map[string]any {
	items := []map[string]any{}
	for _, c := range rows {
		if publicOnly && (c.former || !c.publicVisible) {
			continue
		}
		it := map[string]any{"handle": c.handle, "wins": c.wins, "losses": c.losses, "games": c.games}
		if c.former {
			it["handle"] = ""
			it["former_member"] = true
			it["label"] = "former member"
		}
		items = append(items, it)
		if len(items) >= 50 {
			break
		}
	}
	return items
}

func (h *ClubsHandler) analyticsContributors(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeAnalyticsView)
	if g == nil {
		return
	}
	win, mod, ok := clubWindow(w, r.URL.Query().Get("window"), "week")
	if !ok {
		return
	}
	rows, err := h.contributorRows(g.c.ID, mod)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	items := contribItems(rows, false)
	writeJSON(w, http.StatusOK, map[string]any{"window": win, "total": len(items), "items": items})
}

// publicContributors (section 0-R5): public only for Social clubs whose manager enabled the board, and only
// members who opted into public visibility. Former members and hidden members are omitted entirely.
func (h *ClubsHandler) publicContributors(w http.ResponseWriter, r *http.Request, v clubViewer, slug string) {
	c := h.resolvePublic(w, v, slug)
	if c == nil {
		return
	}
	if c.tier != 3 {
		clubErr(w, http.StatusForbidden, "tier_forbidden", "this club does not publish a contributor board")
		return
	}
	if !c.showContrib {
		clubErr(w, http.StatusForbidden, "contributors_hidden", "this club has not enabled its contributor board")
		return
	}
	win, mod, ok := clubWindow(w, r.URL.Query().Get("window"), "week")
	if !ok {
		return
	}
	rows, err := h.contributorRows(c.id, mod)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	items := contribItems(rows, true)
	writeJSON(w, http.StatusOK, map[string]any{"window": win, "total": len(items), "items": items})
}
