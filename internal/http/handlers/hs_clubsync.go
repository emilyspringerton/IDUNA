package handlers

// hs_clubsync.go -- WOTAN Phase 2 Clubs, chunk P2-B (WOTAN/CLUBS_NORTHSTAR.md sections 6, 7.4, 0-R4/R9):
// the telemetry sync target (where a player's finished games are filed), ingest routing with membership
// re-verification at insert time, owner-only re-attribution, and the self-declared player rank.
//
// The uplink never names a club. The server resolves hs_sync_targets and re-checks the live membership,
// the club's deleted/suspended flags and the club.telemetry.contribute node for every single game.

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"

	"iduna/internal/auth/jwt"
	"iduna/internal/clubs"
)

const (
	hsVisPersonal = "personal"
	hsVisClub     = "club"
	hsVisPrivate  = "private"
)

type hsSyncTarget struct {
	Kind     string // personal | club | none
	ClubID   string
	ClubName string
	Valid    bool
	Reason   string // machine reason when !Valid
}

// Label is the human display string the uplink prints ("Syncing to: <label>").
func (t hsSyncTarget) Label() string {
	switch {
	case t.Kind == "club" && t.Valid:
		return "Club " + t.ClubName
	case t.Kind == "club":
		return "Private only (club target no longer valid)"
	case t.Kind == "none":
		return "Private only"
	}
	return "Personal wall"
}

// qualifyClub reports whether sub CURRENTLY may contribute telemetry to clubID: a live membership in a club
// that is neither deleted nor suspended, whose role carries club.telemetry.contribute.
func (h *HSHandler) qualifyClub(sub, clubID string) (name string, ok bool, reason string) {
	var role string
	var deleted, suspended int
	err := h.DB.QueryRow(`SELECT c.name, c.deleted, c.suspended, m.role FROM clubs c
		JOIN club_memberships m ON m.club_id=c.id AND m.sub=? WHERE c.id=?`, sub, clubID).Scan(&name, &deleted, &suspended, &role)
	switch {
	case err != nil:
		return "", false, "not_member"
	case deleted == 1:
		return "", false, "not_member"
	case suspended == 1:
		return name, false, "club_suspended"
	case !clubs.Can(clubs.Role(role), clubs.NodeTelemetryContrib):
		return name, false, "forbidden"
	}
	return name, true, ""
}

// syncTargetOf resolves a subject's stored preference (default personal) and validates it right now.
func (h *HSHandler) syncTargetOf(sub string) hsSyncTarget {
	t := hsSyncTarget{Kind: "personal", Valid: true}
	var kind, clubID string
	if sub == "" || h.DB.QueryRow(`SELECT kind, club_id FROM hs_sync_targets WHERE sub=?`, sub).Scan(&kind, &clubID) != nil {
		return t
	}
	t.Kind = kind
	switch kind {
	case "club":
		t.ClubID = clubID
		name, ok, reason := h.qualifyClub(sub, clubID)
		t.ClubName, t.Valid, t.Reason = name, ok, reason
	case "none":
	default:
		t.Kind = "personal"
	}
	return t
}

// routeFor decides how one finished game is filed. An invalid club target falls back to private and is
// flagged; it is never rerouted to another club or to the public pool.
func (h *HSHandler) routeFor(sub string) (clubID, visibility string, invalid bool) {
	t := h.syncTargetOf(sub)
	switch t.Kind {
	case "club":
		if t.Valid {
			return t.ClubID, hsVisClub, false
		}
		return "", hsVisPrivate, true
	case "none":
		return "", hsVisPrivate, false
	}
	return "", hsVisPersonal, false
}

func (h *HSHandler) getSyncTarget(w http.ResponseWriter, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	t := h.syncTargetOf(v.sub)
	out := map[string]any{"kind": t.Kind, "club_id": t.ClubID, "valid": t.Valid, "sync_target_invalid": !t.Valid,
		"invalid": !t.Valid, "label": t.Label()}
	if t.Kind == "club" {
		out["club_name"] = t.ClubName
		if !t.Valid {
			out["reason"] = t.Reason
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type hsSyncTargetReq struct {
	Kind   string `json:"kind"`
	ClubID string `json:"club_id"`
}

// validTarget checks a requested target against what v currently qualifies for.
func (h *HSHandler) validTarget(w http.ResponseWriter, sub string, in hsSyncTargetReq) (ok bool, name string) {
	switch in.Kind {
	case "personal", "none":
		return true, ""
	case "club":
		name, qok, reason := h.qualifyClub(sub, strings.TrimSpace(in.ClubID))
		if qok {
			return true, name
		}
		switch reason {
		case "club_suspended":
			clubErr(w, http.StatusConflict, "club_suspended", "that club is suspended")
		case "forbidden":
			clubErr(w, http.StatusForbidden, "forbidden", "your role cannot contribute match data to that club")
		default:
			clubErr(w, http.StatusForbidden, "not_member", "you are not a member of that club")
		}
		return false, ""
	}
	clubErr(w, http.StatusUnprocessableEntity, "bad_kind", "kind must be personal, club or none")
	return false, ""
}

func (h *HSHandler) putSyncTarget(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in hsSyncTargetReq
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	ok, name := h.validTarget(w, v.sub, in)
	if !ok {
		return
	}
	clubID := ""
	if in.Kind == "club" {
		clubID = strings.TrimSpace(in.ClubID)
	}
	if _, err := h.DB.Exec(`INSERT INTO hs_sync_targets (sub, kind, club_id, updated_at) VALUES (?,?,?,datetime('now'))
		ON CONFLICT(sub) DO UPDATE SET kind=excluded.kind, club_id=excluded.club_id, updated_at=excluded.updated_at`,
		v.sub, in.Kind, clubID); err != nil {
		hsErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	out := map[string]any{"kind": in.Kind, "club_id": clubID, "valid": true, "sync_target_invalid": false, "invalid": false}
	if in.Kind == "club" {
		out["club_name"] = name
	}
	writeJSON(w, http.StatusOK, out)
}

// liveSyncTarget serves GET /live/sync-target. It is the ONE route the upload-scoped token may call besides
// POST /live/lines (a normal IDUNA token works too). Read-only and display-only: it never returns a club id.
func (h *HSHandler) liveSyncTarget(w http.ResponseWriter, r *http.Request) {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") || h.Keys == nil {
		hsErr(w, http.StatusUnauthorized, "missing upload token")
		return
	}
	claims, err := jwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	sub, _ := claims["sub"].(string)
	if aud, _ := claims["aud"].(string); err != nil || sub == "" || aud == clubTokenAud {
		hsErr(w, http.StatusUnauthorized, "invalid or expired upload token")
		return
	}
	t := h.syncTargetOf(sub)
	out := map[string]any{"kind": t.Kind, "valid": t.Valid, "sync_target_invalid": !t.Valid, "label": t.Label()}
	if t.Kind == "club" {
		out["club_name"] = t.ClubName
	}
	writeJSON(w, http.StatusOK, out)
}

// retargetGame: PATCH /games/{id}/target. Owner only (a club manager can never pull a member's game out of
// their personal history), and only onto targets the owner currently qualifies for.
func (h *HSHandler) retargetGame(w http.ResponseWriter, r *http.Request, v hsViewer, id int64) {
	var in hsSyncTargetReq
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	var owner string
	if err := h.DB.QueryRow(`SELECT owner_sub FROM hs_games WHERE id=?`, id).Scan(&owner); err == sql.ErrNoRows || (err == nil && owner != v.sub) {
		hsErr(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		hsErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if ok, _ := h.validTarget(w, v.sub, in); !ok {
		return
	}
	clubID, vis := "", hsVisPersonal
	switch in.Kind {
	case "club":
		clubID, vis = strings.TrimSpace(in.ClubID), hsVisClub
	case "none":
		vis = hsVisPrivate
	}
	if _, err := h.DB.Exec(`UPDATE hs_games SET club_id=?, visibility=? WHERE id=? AND owner_sub=?`, clubID, vis, id, v.sub); err != nil {
		hsErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "club_id": clubID, "visibility": vis})
}

// ---- rank (self-declared; rank_verified is always false in this phase, section 0-R4) ----------------------

var hsBattleTagRe = regexp.MustCompile(`^[^\s#]{2,24}#[0-9]{3,8}$`)
var hsRegions = map[string]bool{"": true, "US": true, "EU": true, "AP": true}

// battletagVerified: the tag counts as owned only if it appears as my_name (the uploader's own slot, as
// stored at ingest) in this user's own tracked games.
func (h *HSHandler) battletagVerified(sub, tag string) bool {
	if tag == "" {
		return false
	}
	var one int
	return h.DB.QueryRow(`SELECT 1 FROM hs_games WHERE owner_sub=? AND my_name=? LIMIT 1`, sub, tag).Scan(&one) == nil
}

type hsRankRow struct {
	battletag, region string
	btVerified        bool
	legend            int
}

func (h *HSHandler) loadRank(sub string) hsRankRow {
	var r hsRankRow
	var bv int
	_ = h.DB.QueryRow(`SELECT battletag, battletag_verified, region, legend_rank FROM hs_player_rank WHERE sub=?`, sub).
		Scan(&r.battletag, &bv, &r.region, &r.legend)
	r.btVerified = bv == 1
	return r
}

func rankJSON(r hsRankRow) map[string]any {
	return map[string]any{"battletag": r.battletag, "battletag_verified": r.btVerified, "region": r.region,
		"legend_rank": r.legend, "rank_verified": false, "source": "self"}
}

func (h *HSHandler) getRank(w http.ResponseWriter, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	r := h.loadRank(v.sub)
	// refresh on read: games may have arrived since the tag was declared
	if now := h.battletagVerified(v.sub, r.battletag); now != r.btVerified {
		r.btVerified = now
		_, _ = h.DB.Exec(`UPDATE hs_player_rank SET battletag_verified=?, updated_at=datetime('now') WHERE sub=?`, boolInt(now), v.sub)
	}
	writeJSON(w, http.StatusOK, rankJSON(r))
}

func (h *HSHandler) putRank(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in struct {
		Battletag  *string `json:"battletag"`
		Region     *string `json:"region"`
		LegendRank *int    `json:"legend_rank"`
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	cur := h.loadRank(v.sub)
	if in.Battletag != nil {
		bt := strings.TrimSpace(*in.Battletag)
		if bt != "" && (len(bt) > 40 || !hsBattleTagRe.MatchString(bt)) {
			hsErr(w, http.StatusUnprocessableEntity, "BattleTag must look like Name#1234")
			return
		}
		cur.battletag = bt
	}
	if in.Region != nil {
		rg := strings.ToUpper(strings.TrimSpace(*in.Region))
		if !hsRegions[rg] {
			hsErr(w, http.StatusUnprocessableEntity, "region must be US, EU or AP")
			return
		}
		cur.region = rg
	}
	if in.LegendRank != nil {
		if *in.LegendRank < 0 || *in.LegendRank > 99999 {
			hsErr(w, http.StatusUnprocessableEntity, "legend rank must be between 1 and 99999 (0 clears it)")
			return
		}
		cur.legend = *in.LegendRank
	}
	cur.btVerified = h.battletagVerified(v.sub, cur.battletag)
	// rank_verified is server-set and stays 0: there is no verification source yet (R4).
	if _, err := h.DB.Exec(`INSERT INTO hs_player_rank (sub, battletag, battletag_verified, region, legend_rank, rank_verified, source, updated_at)
		VALUES (?,?,?,?,?,0,'self',datetime('now'))
		ON CONFLICT(sub) DO UPDATE SET battletag=excluded.battletag, battletag_verified=excluded.battletag_verified,
		  region=excluded.region, legend_rank=excluded.legend_rank, rank_verified=0, source='self', updated_at=excluded.updated_at`,
		v.sub, cur.battletag, boolInt(cur.btVerified), cur.region, cur.legend); err != nil {
		hsErr(w, http.StatusInternalServerError, "could not save")
		return
	}
	writeJSON(w, http.StatusOK, rankJSON(cur))
}
