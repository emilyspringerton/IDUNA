package handlers

// admin_clubs.go -- the server-rendered IDUNA Back Office console for WOTAN clubs
// (WOTAN/CLUBS_NORTHSTAR.md section 7.3 + 4.4). Mounted under /admin/clubs by AdminHandler.Init, so it inherits
// the existing /admin/ gate in main.go (RequireCookieAuth + RequirePermission("iduna.admin")); the operator's
// identity is the session subject.
//
//	GET  /admin/clubs                         index: search, filters, sort, paging (Secret clubs included)
//	GET  /admin/clubs/settings | POST         platform caps (club_settings)
//	GET  /admin/clubs/catalog                 read-only wotan.club.* nodes, role bundles, platform permissions
//	GET  /admin/clubs/{id}                    detail (the view itself is audit-logged as admin.view)
//	POST /admin/clubs/{id}/{action}           suspend | unsuspend | tier | exclusive | transfer | remove-member |
//	                                          revoke-invites | revoke-invite | delete | restore
//
// Every POST requires a non-empty `reason` (3-300 chars, same rule as the JSON admin API), writes club_audit_log
// with platform_admin=1 and the operator's sub, and mirrors to the unified event log as
// iduna:wotan.club.admin.* through the same clubTx plumbing the public clubs handler uses. Shared mutation
// cores (suspend/transfer/delete) live in clubs_admin.go next to the JSON surface; the rest are here.
//
// CSRF: the Back Office has no per-form token anywhere; the session cookie is SameSite=Lax (admin_login.go),
// which is what protects these POSTs, same as every other /admin/ form.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"iduna/internal/clubs"
	"iduna/internal/http/middleware"
)

const clubsAdminPageSize = 50

func (h *AdminHandler) clubsCore() *ClubsHandler {
	return &ClubsHandler{DB: h.DB, EventLog: h.EventLog}
}

// clubSettingDef describes one platform cap on /admin/clubs/settings.
type clubSettingDef struct {
	Key, Label, Help string
	Def, Min, Max    int
}

var clubSettingDefs = []clubSettingDef{
	{"clubs_per_user", "Clubs a player may create", "Per-user cap on owned, non-deleted clubs.", clubDefClubsPerUser, 1, 100},
	{"members_per_club", "Members per club", "Join and invite redemption are refused at this size.", clubDefMembersPerClub, 1, 5000},
	{"invite_ttl_max_hours", "Invite TTL ceiling (hours)", "Longest lifetime a manager/coach may give an invite. The server hard-caps this at 168 (7 days).", clubDefInviteTTLMax, 1, 168},
	{"pending_invites_per_club", "Pending invites per club", "Outstanding (unused, unrevoked) invites a club may hold.", clubDefPendingInvites, 1, 1000},
	{"featured_decks_cap", "Featured decks per club", "How many decks a club may feature at once.", clubFeaturedCap, 1, 100},
}

// ---- routing ----------------------------------------------------------------------------------------------

func (h *AdminHandler) clubsIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if h.DB == nil {
		http.Error(w, "clubs console unavailable: no database handle", http.StatusServiceUnavailable)
		return
	}
	h.clubsIndexPage(w, r)
}

// clubsRouter handles everything under /admin/clubs/.
func (h *AdminHandler) clubsRouter(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "clubs console unavailable: no database handle", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/clubs/"), "/")
	p := strings.Split(rest, "/")
	switch {
	case rest == "":
		h.clubsIndexPage(w, r)
	case rest == "settings" && r.Method == http.MethodGet:
		h.clubsSettingsPage(w, r, "", "", http.StatusOK)
	case rest == "settings" && r.Method == http.MethodPost:
		h.clubsSettingsSave(w, r)
	case rest == "catalog" && r.Method == http.MethodGet:
		h.clubsCatalogPage(w, r)
	case len(p) == 1 && r.Method == http.MethodGet:
		h.clubsDetailPage(w, r, p[0], "", http.StatusOK)
	case len(p) == 2 && r.Method == http.MethodPost:
		h.clubsAction(w, r, p[0], p[1])
	default:
		http.NotFound(w, r)
	}
}

// ---- index ------------------------------------------------------------------------------------------------

type clubIndexRow struct {
	ID, Slug, Name, Tier, OwnerSub, OwnerHandle, Created string
	Exclusive, Suspended, Deleted                        bool
	Members                                              int
}

var clubIndexSorts = map[string]string{
	"new":     "c.created_at DESC, c.rowid DESC",
	"old":     "c.created_at ASC, c.rowid ASC",
	"members": "members DESC, c.created_at DESC",
	"name":    "c.name COLLATE NOCASE ASC",
	"tier":    "c.privacy_tier ASC, c.created_at DESC",
}

func (h *AdminHandler) clubsIndexPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortKey := q.Get("sort")
	if _, ok := clubIndexSorts[sortKey]; !ok {
		sortKey = "new"
	}
	search := strings.TrimSpace(q.Get("q"))
	tier, exclusive, suspended, deleted := q.Get("tier"), q.Get("exclusive"), q.Get("suspended"), q.Get("deleted")
	where, args := []string{"1=1"}, []any{}
	switch deleted {
	case "1": // include deleted
	case "only":
		where = append(where, "c.deleted=1")
	default:
		deleted = ""
		where = append(where, "c.deleted=0")
	}
	if search != "" {
		where = append(where, `(c.name LIKE ? ESCAPE '\' OR c.slug LIKE ? ESCAPE '\' OR c.id=? OR c.owner_sub=?)`)
		args = append(args, hsLike(search), hsLike(search), search, search)
	}
	if t, err := strconv.Atoi(tier); err == nil && t >= 1 && t <= 3 {
		where = append(where, "c.privacy_tier=?")
		args = append(args, t)
	} else {
		tier = ""
	}
	if exclusive == "1" || exclusive == "0" {
		where = append(where, "c.is_exclusive=?")
		args = append(args, exclusive)
	} else {
		exclusive = ""
	}
	if suspended == "1" || suspended == "0" {
		where = append(where, "c.suspended=?")
		args = append(args, suspended)
	} else {
		suspended = ""
	}
	offset := atoiDefault(q.Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	from := ` FROM clubs c LEFT JOIN hs_profiles o ON o.sub=c.owner_sub WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(`SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	rows, err := h.DB.Query(`SELECT c.id, c.slug, c.name, c.privacy_tier, c.is_exclusive, c.suspended, c.deleted, c.owner_sub,
		COALESCE(o.handle,''), c.created_at, (SELECT COUNT(*) FROM club_memberships m WHERE m.club_id=c.id) AS members`+from+
		` ORDER BY `+clubIndexSorts[sortKey]+` LIMIT ? OFFSET ?`, append(args, clubsAdminPageSize, offset)...)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	var items []clubIndexRow
	for rows.Next() {
		var it clubIndexRow
		var t, ex, sus, del int
		if rows.Scan(&it.ID, &it.Slug, &it.Name, &t, &ex, &sus, &del, &it.OwnerSub, &it.OwnerHandle, &it.Created, &it.Members) != nil {
			continue
		}
		it.Tier, it.Exclusive, it.Suspended, it.Deleted = clubTier(t), ex == 1, sus == 1, del == 1
		items = append(items, it)
	}
	rows.Close()
	// Same rule as the JSON admin list: listing Secret clubs is itself a platform_admin audit row.
	core := h.clubsCore()
	_ = core.adminAuditOnly(r, clubViewer{sub: middleware.SubjectFromContext(r.Context())}, "", "admin.list",
		map[string]any{"reason": "back office index", "returned": len(items)})

	link := func(off int) string {
		v := url.Values{}
		for k, val := range map[string]string{"q": search, "tier": tier, "exclusive": exclusive, "suspended": suspended, "deleted": deleted, "sort": sortKey} {
			if val != "" {
				v.Set(k, val)
			}
		}
		v.Set("offset", strconv.Itoa(off))
		return "/admin/clubs?" + v.Encode()
	}
	data := map[string]any{
		"Title": "WOTAN Clubs", "Items": items, "Total": total, "Q": search, "Tier": tier, "Exclusive": exclusive,
		"Suspended": suspended, "Deleted": deleted, "Sort": sortKey, "From": offset + 1, "To": offset + len(items),
	}
	if offset > 0 {
		prev := offset - clubsAdminPageSize
		if prev < 0 {
			prev = 0
		}
		data["PrevLink"] = link(prev)
	}
	if offset+len(items) < total {
		data["NextLink"] = link(offset + clubsAdminPageSize)
	}
	renderHTML(w, adminClubsIndexTmpl, data)
}

// ---- detail -----------------------------------------------------------------------------------------------

type clubMemberView struct {
	Sub, Handle, Role, Joined string
	Owner, PublicVisible      bool
	Cells                     []bool // one per clubs.AllNodes
	PermCount                 int
}

type clubInviteView struct {
	ID, Role, CreatedBy, BoundSub, Expires, UsedBy, State string
	Revocable                                             bool
}

type clubRequestView struct{ Sub, Handle, Message, Status, Created string }
type clubDeckView struct {
	DeckID                int64
	Title, FeaturedBy, At string
}
type clubAuditView struct {
	ID                              int64
	At, Actor, Action, Target, Meta string
	Platform                        bool
}
type clubReportView struct {
	ID                                 int64
	Kind, Target, Reporter, Reason, At string
}

var clubNotices = map[string]string{
	"suspend": "Club suspended.", "unsuspend": "Club unsuspended.", "tier": "Privacy tier changed.",
	"exclusive": "Exclusivity updated.", "transfer": "Ownership transferred.", "remove-member": "Member removed.",
	"revoke-invites": "All pending invites revoked.", "revoke-invite": "Invite revoked.", "delete": "Club deleted.",
	"restore": "Club restored.",
}

func (h *AdminHandler) clubsDetailPage(w http.ResponseWriter, r *http.Request, id, errMsg string, status int) {
	c, err := loadClub(h.DB, "id", id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	core := h.clubsCore()
	actor := middleware.SubjectFromContext(r.Context())
	// Viewing a club (Secret ones above all) is itself a platform_admin audit row. Re-rendering after a failed
	// action (status != 200) is not a fresh view.
	if status == http.StatusOK {
		_ = core.adminAuditOnly(r, clubViewer{sub: actor}, c.ID, "admin.view", map[string]any{"reason": "back office detail", "tier": clubTier(c.Tier)})
	}
	d := map[string]any{"Title": "Club: " + c.Name, "Error": errMsg, "Notice": clubNotices[r.URL.Query().Get("notice")]}
	d["Club"] = c
	d["TierName"] = clubTier(c.Tier)
	d["OwnerHandle"] = core.handleOf(h.DB, c.OwnerSub)
	d["Created"], d["Updated"] = hsTime(c.CreatedAt), hsTime(c.UpdatedAt)
	if c.PrevTier.Valid {
		d["PrevTier"] = clubTier(int(c.PrevTier.Int64))
	}
	d["Brand"] = brandOf(c.BrandJSON)

	nodes := make([]string, len(clubs.AllNodes))
	for i, n := range clubs.AllNodes {
		nodes[i] = string(n)
	}
	d["Nodes"] = nodes

	var members []clubMemberView
	if rows, err := h.DB.Query(`SELECT m.sub, COALESCE(p.handle,''), m.role, m.public_visible, m.joined_at
		FROM club_memberships m LEFT JOIN hs_profiles p ON p.sub=m.sub WHERE m.club_id=?
		ORDER BY CASE m.role WHEN 'manager' THEN 0 WHEN 'coach' THEN 1 ELSE 2 END, p.handle, m.sub`, c.ID); err == nil {
		for rows.Next() {
			var mv clubMemberView
			var pv int
			if rows.Scan(&mv.Sub, &mv.Handle, &mv.Role, &pv, &mv.Joined) != nil {
				continue
			}
			mv.Owner, mv.PublicVisible, mv.Joined = mv.Sub == c.OwnerSub, pv == 1, hsTime(mv.Joined)
			mv.Cells = make([]bool, len(clubs.AllNodes))
			for i, n := range clubs.AllNodes {
				if clubs.CanOwner(clubs.Role(mv.Role), mv.Owner, n) {
					mv.Cells[i] = true
					mv.PermCount++
				}
			}
			members = append(members, mv)
		}
		rows.Close()
	}
	d["Members"] = members

	var invites []clubInviteView
	now := clubNow()
	if rows, err := h.DB.Query(`SELECT i.id, i.role, i.created_by, COALESCE(pc.handle,''), i.bound_sub, i.expires_at, i.used_by, i.used_at, i.revoked_at
		FROM club_invites i LEFT JOIN hs_profiles pc ON pc.sub=i.created_by WHERE i.club_id=? ORDER BY i.created_at DESC, i.rowid DESC LIMIT 100`, c.ID); err == nil {
		for rows.Next() {
			var iv clubInviteView
			var handle string
			var usedAt, revokedAt sql.NullString
			var exp string
			if rows.Scan(&iv.ID, &iv.Role, &iv.CreatedBy, &handle, &iv.BoundSub, &exp, &iv.UsedBy, &usedAt, &revokedAt) != nil {
				continue
			}
			if handle != "" {
				iv.CreatedBy = handle + " (" + iv.CreatedBy + ")"
			}
			iv.Expires = hsTime(exp)
			switch {
			case usedAt.Valid:
				iv.State = "used"
			case revokedAt.Valid:
				iv.State = "revoked"
			case exp <= now:
				iv.State = "expired"
			default:
				iv.State, iv.Revocable = "pending", true
			}
			invites = append(invites, iv)
		}
		rows.Close()
	}
	d["Invites"] = invites

	var reqs []clubRequestView
	if rows, err := h.DB.Query(`SELECT jr.sub, COALESCE(p.handle,''), jr.message, jr.status, jr.created_at FROM club_join_requests jr
		LEFT JOIN hs_profiles p ON p.sub=jr.sub WHERE jr.club_id=? ORDER BY (jr.status='pending') DESC, jr.created_at DESC LIMIT 50`, c.ID); err == nil {
		for rows.Next() {
			var rv clubRequestView
			if rows.Scan(&rv.Sub, &rv.Handle, &rv.Message, &rv.Status, &rv.Created) == nil {
				rv.Created = hsTime(rv.Created)
				reqs = append(reqs, rv)
			}
		}
		rows.Close()
	}
	d["Requests"] = reqs

	var decks []clubDeckView
	if rows, err := h.DB.Query(`SELECT f.deck_id, COALESCE(d.title,''), f.featured_by, f.created_at FROM club_featured_decks f
		LEFT JOIN hs_decks d ON d.id=f.deck_id WHERE f.club_id=? ORDER BY f.created_at DESC`, c.ID); err == nil {
		for rows.Next() {
			var dv clubDeckView
			if rows.Scan(&dv.DeckID, &dv.Title, &dv.FeaturedBy, &dv.At) == nil {
				dv.At = hsTime(dv.At)
				decks = append(decks, dv)
			}
		}
		rows.Close()
	}
	d["Decks"] = decks

	// Audit log: newest first, keyset-paged by id. Unlike the member-facing viewer, operators see the actor sub.
	const auditN = 50
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	aq, aargs := `SELECT a.id, a.actor_sub, COALESCE(pa.handle,''), a.action, a.target_sub, COALESCE(pt.handle,''), a.meta_json, a.platform_admin, a.at
		FROM club_audit_log a LEFT JOIN hs_profiles pa ON pa.sub=a.actor_sub LEFT JOIN hs_profiles pt ON pt.sub=a.target_sub AND a.target_sub<>''
		WHERE a.club_id=?`, []any{c.ID}
	if before > 0 {
		aq += ` AND a.id<?`
		aargs = append(aargs, before)
	}
	var audit []clubAuditView
	var lastID int64
	if rows, err := h.DB.Query(aq+` ORDER BY a.id DESC LIMIT ?`, append(aargs, auditN)...); err == nil {
		for rows.Next() {
			var av clubAuditView
			var actor, ah, target, th string
			var pa int
			if rows.Scan(&av.ID, &actor, &ah, &av.Action, &target, &th, &av.Meta, &pa, &av.At) != nil {
				continue
			}
			av.Platform, av.At = pa == 1, hsTime(av.At)
			av.Actor, av.Target = actor, target
			if ah != "" {
				av.Actor = ah + " (" + actor + ")"
			}
			if th != "" {
				av.Target = th + " (" + target + ")"
			}
			audit = append(audit, av)
			lastID = av.ID
		}
		rows.Close()
	}
	d["Audit"] = audit
	if len(audit) == auditN {
		d["OlderLink"] = "/admin/clubs/" + url.PathEscape(c.ID) + "?before=" + strconv.FormatInt(lastID, 10)
	}

	var reports []clubReportView
	if rows, err := h.DB.Query(`SELECT r.id, r.kind, r.target_id, r.reporter_sub, r.reason, r.created_at FROM hs_reports r
		WHERE (r.kind='club' AND r.target_id IN (?, ?))
		   OR (r.kind='club_post' AND r.target_id IN (SELECT CAST(p.id AS TEXT) FROM club_posts p WHERE p.club_id=?))
		ORDER BY r.id DESC LIMIT 50`, c.ID, c.Slug, c.ID); err == nil {
		for rows.Next() {
			var rv clubReportView
			if rows.Scan(&rv.ID, &rv.Kind, &rv.Target, &rv.Reporter, &rv.Reason, &rv.At) == nil {
				rv.At = hsTime(rv.At)
				reports = append(reports, rv)
			}
		}
		rows.Close()
	}
	d["Reports"] = reports

	w.WriteHeader(status)
	renderHTML(w, adminClubDetailTmpl, d)
}

// ---- actions ----------------------------------------------------------------------------------------------

func (h *AdminHandler) clubsAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	known := false
	for k := range clubNotices {
		if k == action {
			known = true
		}
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	c, err := loadClub(h.DB, "id", id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	actor := middleware.SubjectFromContext(r.Context())
	if actor == "" {
		http.Error(w, "no operator identity on this session", http.StatusForbidden)
		return
	}
	reason, ok := clubReason(r.FormValue("reason"))
	if !ok {
		h.clubsDetailPage(w, r, id, "A reason of 3-300 characters is required for every platform action; nothing was changed.", http.StatusUnprocessableEntity)
		return
	}
	core := h.clubsCore()
	var opErr *clubOpErr
	switch action {
	case "suspend":
		opErr = core.adminSuspendCore(r, actor, c, reason, true)
	case "unsuspend":
		opErr = core.adminSuspendCore(r, actor, c, reason, false)
	case "delete":
		opErr = core.adminDeleteCore(r, actor, c, reason)
	case "transfer":
		_, opErr = core.adminTransferCore(r, actor, c, strings.TrimSpace(r.FormValue("to_sub")), reason)
	case "tier":
		t, convErr := strconv.Atoi(r.FormValue("tier"))
		if convErr != nil {
			opErr = &clubOpErr{http.StatusUnprocessableEntity, "tier_forbidden", "tier must be 1 (Secret), 2 (Private) or 3 (Social)"}
		} else {
			opErr = core.adminTierCore(r, actor, c, t, reason)
		}
	case "exclusive":
		opErr = core.adminExclusiveCore(r, actor, c, r.FormValue("exclusive") == "1", reason)
	case "remove-member":
		opErr = core.adminRemoveMemberCore(r, actor, c, strings.TrimSpace(r.FormValue("sub")), reason)
	case "revoke-invites":
		opErr = core.adminRevokeInvitesCore(r, actor, c, "", reason)
	case "revoke-invite":
		iid := strings.TrimSpace(r.FormValue("invite_id"))
		if iid == "" {
			opErr = &clubOpErr{http.StatusBadRequest, "", "invite_id required"}
		} else {
			opErr = core.adminRevokeInvitesCore(r, actor, c, iid, reason)
		}
	case "restore":
		opErr = core.adminRestoreCore(r, actor, c, reason)
	}
	if opErr != nil {
		h.clubsDetailPage(w, r, id, opErr.msg, opErr.status)
		return
	}
	http.Redirect(w, r, "/admin/clubs/"+url.PathEscape(id)+"?notice="+url.QueryEscape(action), http.StatusSeeOther)
}

func (h *ClubsHandler) adminLive(c clubRow) *clubOpErr {
	if c.Deleted {
		return &clubOpErr{http.StatusConflict, "deleted", "club is deleted; restore it first"}
	}
	return nil
}

// adminTierCore forces a club's privacy tier. Same side effects as the manager-facing visibility setter: going
// Secret remembers the previous tier (for Reveal), and leaving Social drops a "request" join policy and cancels
// pending requests.
func (h *ClubsHandler) adminTierCore(r *http.Request, actor string, c clubRow, tier int, reason string) *clubOpErr {
	if e := h.adminLive(c); e != nil {
		return e
	}
	if tier < 1 || tier > 3 {
		return &clubOpErr{http.StatusUnprocessableEntity, "tier_forbidden", "tier must be 1 (Secret), 2 (Private) or 3 (Social)"}
	}
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	cur, err := loadClub(tx.tx, "id", c.ID) // re-read inside the transaction
	if err != nil || cur.Deleted {
		return &clubOpErr{http.StatusNotFound, "", "not found"}
	}
	if cur.Tier == tier {
		return &clubOpErr{http.StatusUnprocessableEntity, "no_change", "the club is already at that tier"}
	}
	prev := cur.PrevTier
	if tier == 1 {
		prev = sql.NullInt64{Int64: int64(cur.Tier), Valid: true}
	}
	jp, reset := cur.JoinPolicy, false
	if jp == "request" && tier != 3 {
		jp, reset = "invite", true
		_, _ = tx.tx.Exec(`UPDATE club_join_requests SET status='cancelled', decided_at=? WHERE club_id=? AND status='pending'`, clubNow(), c.ID)
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET privacy_tier=?, prev_tier=?, join_policy=?, updated_at=? WHERE id=?`, tier, prev, jp, clubNow(), c.ID); err != nil {
		return errClubDB
	}
	tx.audit(c.ID, actor, "admin.tier", "", map[string]any{"reason": reason, "from": cur.Tier, "to": tier, "join_policy_reset": reset}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

// adminExclusiveCore turns exclusivity on/off. Turning it on is refused (409) while any member also belongs to
// another live club, exactly like the manager-facing toggle.
func (h *ClubsHandler) adminExclusiveCore(r *http.Request, actor string, c clubRow, on bool, reason string) *clubOpErr {
	if e := h.adminLive(c); e != nil {
		return e
	}
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	cur, err := loadClub(tx.tx, "id", c.ID)
	if err != nil || cur.Deleted {
		return &clubOpErr{http.StatusNotFound, "", "not found"}
	}
	if cur.Exclusive == on {
		return &clubOpErr{http.StatusUnprocessableEntity, "no_change", "exclusivity is already in that state"}
	}
	if on {
		conflicts, ok := h.exclusiveConflicts(tx.tx, c.ID)
		if !ok {
			return errClubDB
		}
		if len(conflicts) > 0 {
			names := make([]string, 0, len(conflicts))
			for _, m := range conflicts {
				n := m["handle"]
				if n == "" {
					n = m["sub"]
				}
				names = append(names, n)
			}
			return &clubOpErr{http.StatusConflict, "exclusive_conflict", "members also in other clubs: " + strings.Join(names, ", ")}
		}
	}
	v := 0
	if on {
		v = 1
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET is_exclusive=?, updated_at=? WHERE id=?`, v, clubNow(), c.ID); err != nil {
		return errClubDB
	}
	tx.audit(c.ID, actor, "admin.exclusive", "", map[string]any{"reason": reason, "from": cur.Exclusive, "to": on}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

// adminRemoveMemberCore removes a member as a platform operator. The hierarchy invariants still apply through
// clubs.CanActOn (acting as a manager): the owner cannot be removed (transfer first) and the last manager
// cannot be removed.
func (h *ClubsHandler) adminRemoveMemberCore(r *http.Request, actor string, c clubRow, target, reason string) *clubOpErr {
	if e := h.adminLive(c); e != nil {
		return e
	}
	if target == "" {
		return &clubOpErr{http.StatusBadRequest, "", "member sub required"}
	}
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	cur, err := loadClub(tx.tx, "id", c.ID)
	if err != nil || cur.Deleted {
		return &clubOpErr{http.StatusNotFound, "", "not found"}
	}
	trole, ok := h.memberRole(tx.tx, c.ID, target)
	if !ok {
		return &clubOpErr{http.StatusNotFound, "", "that person is not a member of this club"}
	}
	if d := clubs.CanActOn(clubs.RoleManager, false, trole, cur.OwnerSub == target,
		clubs.Action{Kind: clubs.KindRemove, ManagerCount: h.managerCount(tx.tx, c.ID)}); d != nil {
		return &clubOpErr{http.StatusConflict, d.Code, d.Msg + " (transfer ownership first if this is the owner)"}
	}
	if _, err := tx.tx.Exec(`DELETE FROM club_memberships WHERE club_id=? AND sub=?`, c.ID, target); err != nil {
		return errClubDB
	}
	now := clubNow()
	_, _ = tx.tx.Exec(`UPDATE club_invites SET revoked_at=? WHERE club_id=? AND created_by=? AND used_at IS NULL AND revoked_at IS NULL`, now, c.ID, target)
	tx.audit(c.ID, actor, "admin.member_remove", target, map[string]any{"reason": reason, "role": string(trole)}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

// adminRevokeInvitesCore revokes one pending invite (inviteID != "") or all of them.
func (h *ClubsHandler) adminRevokeInvitesCore(r *http.Request, actor string, c clubRow, inviteID, reason string) *clubOpErr {
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	q := `UPDATE club_invites SET revoked_at=? WHERE club_id=? AND used_at IS NULL AND revoked_at IS NULL`
	args := []any{clubNow(), c.ID}
	action, meta := "admin.invites_revoke_all", map[string]any{"reason": reason}
	if inviteID != "" {
		q += ` AND id=?`
		args = append(args, inviteID)
		action = "admin.invite_revoke"
		meta["invite_id"] = inviteID
	}
	res, err := tx.tx.Exec(q, args...)
	if err != nil {
		return errClubDB
	}
	n, _ := res.RowsAffected()
	if inviteID != "" && n == 0 {
		return &clubOpErr{http.StatusNotFound, "", "no such pending invite"}
	}
	meta["revoked"] = n
	tx.audit(c.ID, actor, action, "", meta, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

// adminRestoreCore undeletes a soft-deleted club. Invites and join requests killed by the delete stay dead.
// An exclusive club whose members have since joined elsewhere is refused (same conflict rule as the toggle).
func (h *ClubsHandler) adminRestoreCore(r *http.Request, actor string, c clubRow, reason string) *clubOpErr {
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	cur, err := loadClub(tx.tx, "id", c.ID)
	if err != nil {
		return &clubOpErr{http.StatusNotFound, "", "not found"}
	}
	if !cur.Deleted {
		return &clubOpErr{http.StatusConflict, "not_deleted", "club is not deleted"}
	}
	if cur.Exclusive {
		conflicts, ok := h.exclusiveConflicts(tx.tx, c.ID)
		if !ok {
			return errClubDB
		}
		if len(conflicts) > 0 {
			return &clubOpErr{http.StatusConflict, "exclusive_conflict", "cannot restore an exclusive club while some members belong to other clubs; clear exclusivity or remove them first"}
		}
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET deleted=0, updated_at=? WHERE id=?`, clubNow(), c.ID); err != nil {
		return errClubDB
	}
	tx.audit(c.ID, actor, "admin.restore", "", map[string]any{"reason": reason, "slug": cur.Slug}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

// ---- settings ---------------------------------------------------------------------------------------------

type clubSettingView struct {
	clubSettingDef
	Stored    string
	Effective int
}

func (h *AdminHandler) clubsSettingsPage(w http.ResponseWriter, r *http.Request, errMsg, notice string, status int) {
	core := h.clubsCore()
	var views []clubSettingView
	for _, d := range clubSettingDefs {
		sv := clubSettingView{clubSettingDef: d, Effective: core.setting(h.DB, d.Key, d.Def)}
		_ = h.DB.QueryRow(`SELECT value FROM club_settings WHERE key=?`, d.Key).Scan(&sv.Stored)
		views = append(views, sv)
	}
	var changes []clubAuditView
	if rows, err := h.DB.Query(`SELECT id, actor_sub, target_sub, meta_json, at FROM club_audit_log WHERE action='admin.setting' ORDER BY id DESC LIMIT 20`); err == nil {
		for rows.Next() {
			var av clubAuditView
			if rows.Scan(&av.ID, &av.Actor, &av.Target, &av.Meta, &av.At) == nil {
				av.Action, av.Platform, av.At = "admin.setting", true, hsTime(av.At)
				changes = append(changes, av)
			}
		}
		rows.Close()
	}
	if r.URL.Query().Get("notice") == "saved" {
		notice = "Settings saved."
	}
	w.WriteHeader(status)
	renderHTML(w, adminClubsSettingsTmpl, map[string]any{"Title": "Club Settings", "Settings": views, "Changes": changes, "Error": errMsg, "Notice": notice})
}

func (h *AdminHandler) clubsSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	actor := middleware.SubjectFromContext(r.Context())
	if actor == "" {
		http.Error(w, "no operator identity on this session", http.StatusForbidden)
		return
	}
	reason, ok := clubReason(r.FormValue("reason"))
	if !ok {
		h.clubsSettingsPage(w, r, "A reason of 3-300 characters is required; nothing was changed.", "", http.StatusUnprocessableEntity)
		return
	}
	core := h.clubsCore()
	type change struct {
		key      string
		from, to int
	}
	var changes []change
	for _, d := range clubSettingDefs {
		raw := strings.TrimSpace(r.FormValue(d.Key))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < d.Min || n > d.Max {
			h.clubsSettingsPage(w, r, d.Label+" must be a whole number from "+strconv.Itoa(d.Min)+" to "+strconv.Itoa(d.Max)+"; nothing was changed.", "", http.StatusUnprocessableEntity)
			return
		}
		if cur := core.setting(h.DB, d.Key, d.Def); cur != n {
			changes = append(changes, change{d.Key, cur, n})
		}
	}
	if len(changes) == 0 {
		h.clubsSettingsPage(w, r, "", "No changes: every value already matches.", http.StatusOK)
		return
	}
	tx, err := core.begin(r)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer tx.rollback()
	for _, c := range changes {
		if _, err := tx.tx.Exec(`INSERT INTO club_settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, c.key, strconv.Itoa(c.to)); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		tx.audit("", actor, "admin.setting", c.key, map[string]any{"reason": reason, "key": c.key, "from": c.from, "to": c.to}, true)
	}
	if err := tx.commit(); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/clubs/settings?notice=saved", http.StatusSeeOther)
}

// ---- catalog ----------------------------------------------------------------------------------------------

type clubCatalogNode struct {
	Node                          string
	Player, Coach, Manager, Owner bool
}

type clubPlatformPerm struct {
	Name, Description string
	Roles             []string
}

func (h *AdminHandler) clubsCatalogPage(w http.ResponseWriter, r *http.Request) {
	var nodes []clubCatalogNode
	for _, n := range clubs.AllNodes {
		nodes = append(nodes, clubCatalogNode{
			Node:    string(n),
			Player:  clubs.CanOwner(clubs.RolePlayer, false, n),
			Coach:   clubs.CanOwner(clubs.RoleCoach, false, n),
			Manager: clubs.CanOwner(clubs.RoleManager, false, n),
			Owner:   clubs.CanOwner(clubs.RoleManager, true, n),
		})
	}
	bundles := map[string][]string{}
	for _, role := range []clubs.Role{clubs.RolePlayer, clubs.RoleCoach, clubs.RoleManager} {
		bundles[string(role)] = clubs.Perms(role, false)
	}
	var plats []clubPlatformPerm
	if rows, err := h.DB.Query(`SELECT p.name, p.description, COALESCE(r.name,'') FROM permissions p
		LEFT JOIN role_permissions rp ON rp.permission_id=p.id LEFT JOIN roles r ON r.id=rp.role_id
		WHERE p.name IN ('wotan.club.create','wotan.clubs.admin','wotan.clubs.moderate') ORDER BY p.name, r.name`); err == nil {
		idx := map[string]int{}
		for rows.Next() {
			var name, desc, role string
			if rows.Scan(&name, &desc, &role) != nil {
				continue
			}
			i, seen := idx[name]
			if !seen {
				plats = append(plats, clubPlatformPerm{Name: name, Description: desc})
				i = len(plats) - 1
				idx[name] = i
			}
			if role != "" {
				plats[i].Roles = append(plats[i].Roles, role)
			}
		}
		rows.Close()
	}
	renderHTML(w, adminClubsCatalogTmpl, map[string]any{"Title": "Club Permission Catalog", "Nodes": nodes, "Bundles": bundles, "Platform": plats,
		"Now": time.Now().UTC().Format("2006-01-02 15:04 UTC")})
}

// ---- templates --------------------------------------------------------------------------------------------

const adminClubsSubnav = `<p class="meta"><a href="/admin/clubs">Clubs</a> &middot; <a href="/admin/clubs/settings">Platform settings</a> &middot; <a href="/admin/clubs/catalog">Permission catalog</a></p>`

var adminClubsIndexTmpl = mustParseTmpl("clubs-index", `
{{define "body"}}
<h1>WOTAN Clubs</h1>
`+adminClubsSubnav+`
<p class="meta">Operators are the trust root: Secret clubs are listed here. Listing and opening a club is audit-logged.</p>
<form method="GET" action="/admin/clubs" class="section-card">
  <input type="text" name="q" value="{{.Q}}" placeholder="name, slug, club id or owner sub" style="width:260px">
  <select name="tier"><option value="">any tier</option>
    <option value="1"{{if eq .Tier "1"}} selected{{end}}>Secret</option>
    <option value="2"{{if eq .Tier "2"}} selected{{end}}>Private</option>
    <option value="3"{{if eq .Tier "3"}} selected{{end}}>Social</option></select>
  <select name="exclusive"><option value="">exclusive: any</option>
    <option value="1"{{if eq .Exclusive "1"}} selected{{end}}>exclusive</option>
    <option value="0"{{if eq .Exclusive "0"}} selected{{end}}>not exclusive</option></select>
  <select name="suspended"><option value="">suspended: any</option>
    <option value="1"{{if eq .Suspended "1"}} selected{{end}}>suspended</option>
    <option value="0"{{if eq .Suspended "0"}} selected{{end}}>not suspended</option></select>
  <select name="deleted">
    <option value="">hide deleted</option>
    <option value="1"{{if eq .Deleted "1"}} selected{{end}}>include deleted</option>
    <option value="only"{{if eq .Deleted "only"}} selected{{end}}>only deleted</option></select>
  <select name="sort">
    <option value="new"{{if eq .Sort "new"}} selected{{end}}>newest</option>
    <option value="old"{{if eq .Sort "old"}} selected{{end}}>oldest</option>
    <option value="members"{{if eq .Sort "members"}} selected{{end}}>most members</option>
    <option value="name"{{if eq .Sort "name"}} selected{{end}}>name</option>
    <option value="tier"{{if eq .Sort "tier"}} selected{{end}}>tier</option></select>
  <input type="submit" value="Filter">
</form>
{{if .Items}}
<p class="meta">{{.From}}-{{.To}} of {{.Total}}</p>
<table>
<tr><th>Club</th><th>Slug</th><th>Tier</th><th>Members</th><th>Owner</th><th>Flags</th><th>Created</th></tr>
{{range .Items}}
<tr>
  <td><a href="/admin/clubs/{{.ID}}">{{.Name}}</a></td>
  <td class="meta">{{.Slug}}</td>
  <td>{{.Tier}}</td>
  <td>{{.Members}}</td>
  <td class="meta">{{if .OwnerHandle}}{{.OwnerHandle}}{{else}}{{.OwnerSub}}{{end}}</td>
  <td>{{if .Exclusive}}<span class="badge badge-pending">exclusive</span> {{end}}{{if .Suspended}}<span class="badge badge-suspended">suspended</span> {{end}}{{if .Deleted}}<span class="badge badge-decommissioned">deleted</span>{{end}}</td>
  <td class="meta">{{.Created}}</td>
</tr>
{{end}}
</table>
<p>{{if .PrevLink}}<a href="{{.PrevLink}}">&larr; previous</a>{{end}} {{if .NextLink}}<a href="{{.NextLink}}">next &rarr;</a>{{end}}</p>
{{else}}<p class="empty">No clubs match.</p>{{end}}
{{end}}`)

var adminClubDetailTmpl = mustParseTmpl("club-detail", `
{{define "body"}}
<h1>{{.Club.Name}} {{if .Club.Suspended}}<span class="badge badge-suspended">suspended</span>{{end}} {{if .Club.Deleted}}<span class="badge badge-decommissioned">deleted</span>{{end}}</h1>
`+adminClubsSubnav+`
{{if .Error}}<div class="err section-card" style="border-color:#b85c5c;color:#7a1a1a">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="section-card" style="border-color:#1a5c2a;color:#1a5c2a">{{.Notice}}</div>{{end}}

<div class="section-card">
<h2>Settings</h2>
<table>
<tr><td>Club id</td><td class="meta">{{.Club.ID}}</td></tr>
<tr><td>Slug</td><td class="meta">{{.Club.Slug}}</td></tr>
<tr><td>Tier</td><td>{{.TierName}}{{if .PrevTier}} <span class="meta">(Reveal returns to {{.PrevTier}})</span>{{end}}</td></tr>
<tr><td>Exclusive</td><td>{{.Club.Exclusive}}</td></tr>
<tr><td>Join policy</td><td>{{.Club.JoinPolicy}}</td></tr>
<tr><td>Contributor board</td><td>{{.Club.ShowContrib}}</td></tr>
<tr><td>Owner</td><td class="meta">{{if .OwnerHandle}}{{.OwnerHandle}} {{end}}({{.Club.OwnerSub}})</td></tr>
<tr><td>Tagline</td><td>{{.Club.Tagline}}</td></tr>
<tr><td>Description</td><td>{{.Club.Description}}</td></tr>
<tr><td>Branding</td><td class="meta">accent {{.Brand.Accent}} &middot; banner {{.Brand.BannerText}}</td></tr>
{{if .Club.Suspended}}<tr><td>Suspended because</td><td>{{.Club.SuspendedReason}}</td></tr>{{end}}
<tr><td>Created / updated</td><td class="meta">{{.Created}} / {{.Updated}}</td></tr>
</table>
</div>

<div class="section-card">
<h2>Platform actions</h2>
<p class="meta">Every action needs a reason. Each is written to this club's audit log as a platform action under your identity.</p>
<table>
{{if .Club.Deleted}}
<tr><td>Restore</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/restore">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" value="Restore club"></form></td></tr>
{{else}}
<tr><td>{{if .Club.Suspended}}Unsuspend{{else}}Suspend{{end}}</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/{{if .Club.Suspended}}unsuspend{{else}}suspend{{end}}">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px">
  <input type="submit" class="{{if not .Club.Suspended}}danger{{end}}" value="{{if .Club.Suspended}}Unsuspend{{else}}Suspend{{end}}"></form></td></tr>
<tr><td>Force tier</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/tier">
  <select name="tier"><option value="1">Secret</option><option value="2">Private</option><option value="3">Social</option></select>
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" value="Set tier"></form></td></tr>
<tr><td>Exclusive</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/exclusive">
  <select name="exclusive"><option value="1"{{if not .Club.Exclusive}} selected{{end}}>exclusive</option><option value="0"{{if .Club.Exclusive}} selected{{end}}>not exclusive</option></select>
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" value="Apply"></form></td></tr>
<tr><td>Transfer ownership</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/transfer">
  <select name="to_sub">{{range .Members}}{{if not .Owner}}<option value="{{.Sub}}">{{if .Handle}}{{.Handle}}{{else}}{{.Sub}}{{end}} ({{.Role}})</option>{{end}}{{end}}</select>
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" value="Transfer (auto-promotes to manager)"></form></td></tr>
<tr><td>Revoke all pending invites</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/revoke-invites">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" class="danger" value="Revoke all"></form></td></tr>
<tr><td>Delete</td><td><form method="POST" action="/admin/clubs/{{.Club.ID}}/delete">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:280px"> <input type="submit" class="danger" value="Delete club" onclick="return confirm('Soft-delete this club?')"></form></td></tr>
{{end}}
</table>
</div>

<div class="section-card">
<h2>Members ({{len .Members}}) and effective permissions</h2>
{{if .Members}}
<div style="overflow-x:auto">
<table style="font-size:11px">
<tr><th>Member</th><th>Role</th>{{range .Nodes}}<th title="{{.}}" style="writing-mode:vertical-rl;padding:4px">{{.}}</th>{{end}}<th>#</th><th>Remove</th></tr>
{{$cid := .Club.ID}}{{$dead := .Club.Deleted}}
{{range .Members}}
<tr>
  <td>{{if .Handle}}{{.Handle}}<br>{{end}}<span class="meta">{{.Sub}}</span></td>
  <td>{{.Role}}{{if .Owner}} <span class="badge badge-pending">owner</span>{{end}}</td>
  {{range .Cells}}<td style="text-align:center">{{if .}}&#10003;{{else}}&middot;{{end}}</td>{{end}}
  <td>{{.PermCount}}</td>
  <td>{{if and (not .Owner) (not $dead)}}<form method="POST" action="/admin/clubs/{{$cid}}/remove-member">
    <input type="hidden" name="sub" value="{{.Sub}}">
    <input type="text" name="reason" placeholder="reason" required minlength="3" maxlength="300" style="width:130px">
    <input type="submit" class="danger" value="Remove"></form>{{end}}</td>
</tr>
{{end}}
</table>
</div>
<p class="meta">Computed from internal/clubs (read-only). Owner-only nodes (club.owner.transfer, club.delete) are held by the owner flag alone.</p>
{{else}}<p class="empty">No members.</p>{{end}}
</div>

<div class="section-card">
<h2>Invites</h2>
{{if .Invites}}
<table>
<tr><th>State</th><th>Role</th><th>Created by</th><th>Bound to</th><th>Expires</th><th>Used by</th><th></th></tr>
{{range .Invites}}
<tr><td>{{.State}}</td><td>{{.Role}}</td><td class="meta">{{.CreatedBy}}</td><td class="meta">{{.BoundSub}}</td><td class="meta">{{.Expires}}</td><td class="meta">{{.UsedBy}}</td>
<td>{{if .Revocable}}<form method="POST" action="/admin/clubs/{{$.Club.ID}}/revoke-invite">
  <input type="hidden" name="invite_id" value="{{.ID}}">
  <input type="text" name="reason" placeholder="reason" required minlength="3" maxlength="300" style="width:130px">
  <input type="submit" value="Revoke"></form>{{end}}</td></tr>
{{end}}
</table>
{{else}}<p class="empty">No invites.</p>{{end}}
</div>

<div class="section-card">
<h2>Join requests</h2>
{{if .Requests}}
<table><tr><th>From</th><th>Status</th><th>Message</th><th>When</th></tr>
{{range .Requests}}<tr><td class="meta">{{if .Handle}}{{.Handle}} {{end}}({{.Sub}})</td><td>{{.Status}}</td><td>{{.Message}}</td><td class="meta">{{.Created}}</td></tr>{{end}}
</table>
{{else}}<p class="empty">No join requests.</p>{{end}}
</div>

<div class="section-card">
<h2>Featured decks</h2>
{{if .Decks}}
<table><tr><th>Deck</th><th>Title</th><th>Featured by</th><th>When</th></tr>
{{range .Decks}}<tr><td class="meta">{{.DeckID}}</td><td>{{.Title}}</td><td class="meta">{{.FeaturedBy}}</td><td class="meta">{{.At}}</td></tr>{{end}}
</table>
{{else}}<p class="empty">No featured decks.</p>{{end}}
</div>

<div class="section-card">
<h2>Linked reports</h2>
{{if .Reports}}
<table><tr><th>#</th><th>Kind</th><th>Target</th><th>Reporter</th><th>Reason</th><th>When</th></tr>
{{range .Reports}}<tr><td>{{.ID}}</td><td>{{.Kind}}</td><td class="meta">{{.Target}}</td><td class="meta">{{.Reporter}}</td><td>{{.Reason}}</td><td class="meta">{{.At}}</td></tr>{{end}}
</table>
{{else}}<p class="empty">No reports filed against this club or its posts.</p>{{end}}
</div>

<div class="section-card">
<h2>Audit log</h2>
{{if .Audit}}
<table><tr><th>#</th><th>When</th><th>Actor</th><th>Action</th><th>Target</th><th>Meta</th></tr>
{{range .Audit}}<tr><td>{{.ID}}</td><td class="meta">{{.At}}</td><td class="meta">{{.Actor}}{{if .Platform}} <span class="badge badge-pending">platform</span>{{end}}</td><td>{{.Action}}</td><td class="meta">{{.Target}}</td><td class="meta">{{.Meta}}</td></tr>{{end}}
</table>
{{if .OlderLink}}<p><a href="{{.OlderLink}}">older entries &rarr;</a></p>{{end}}
{{else}}<p class="empty">No audit entries.</p>{{end}}
</div>
{{end}}`)

var adminClubsSettingsTmpl = mustParseTmpl("clubs-settings", `
{{define "body"}}
<h1>Club platform settings</h1>
`+adminClubsSubnav+`
{{if .Error}}<div class="err section-card" style="border-color:#b85c5c;color:#7a1a1a">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="section-card" style="border-color:#1a5c2a;color:#1a5c2a">{{.Notice}}</div>{{end}}
<form method="POST" action="/admin/clubs/settings" class="section-card">
<table>
<tr><th>Cap</th><th>Effective</th><th>Default</th><th>Range</th><th>New value</th></tr>
{{range .Settings}}
<tr><td>{{.Label}}<br><span class="meta">{{.Key}} &mdash; {{.Help}}</span></td>
<td>{{.Effective}}{{if .Stored}} <span class="meta">(stored: {{.Stored}})</span>{{end}}</td><td>{{.Def}}</td><td class="meta">{{.Min}}&ndash;{{.Max}}</td>
<td><input type="text" name="{{.Key}}" value="{{.Effective}}" style="width:80px"></td></tr>
{{end}}
</table>
<p><input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:320px"> <input type="submit" value="Save"></p>
</form>
<div class="section-card">
<h2>Recent changes</h2>
{{if .Changes}}
<table><tr><th>#</th><th>When</th><th>Operator</th><th>Key</th><th>Detail</th></tr>
{{range .Changes}}<tr><td>{{.ID}}</td><td class="meta">{{.At}}</td><td class="meta">{{.Actor}}</td><td class="meta">{{.Target}}</td><td class="meta">{{.Meta}}</td></tr>{{end}}
</table>
{{else}}<p class="empty">No changes yet; defaults apply.</p>{{end}}
</div>
{{end}}`)

var adminClubsCatalogTmpl = mustParseTmpl("clubs-catalog", `
{{define "body"}}
<h1>Club permission catalog</h1>
`+adminClubsSubnav+`
<p class="meta">Read-only. Source of truth is internal/clubs/authz.go plus migration 202610120001_wotan_clubs.sql; shown here so operators can audit. Roles are enforced server-side from club_memberships on every request.</p>
<div class="section-card">
<h2>Club nodes</h2>
<table>
<tr><th>Node</th><th>player</th><th>coach</th><th>manager</th><th>owner</th></tr>
{{range .Nodes}}<tr><td class="meta">{{.Node}}</td>
<td>{{if .Player}}&#10003;{{end}}</td><td>{{if .Coach}}&#10003;{{end}}</td><td>{{if .Manager}}&#10003;{{end}}</td><td>{{if .Owner}}&#10003;{{end}}</td></tr>{{end}}
</table>
</div>
<div class="section-card">
<h2>Role bundles</h2>
<table>
<tr><th>Role</th><th>Nodes</th></tr>
<tr><td>player</td><td class="meta">{{join (index .Bundles "player") ", "}}</td></tr>
<tr><td>coach</td><td class="meta">{{join (index .Bundles "coach") ", "}}</td></tr>
<tr><td>manager</td><td class="meta">{{join (index .Bundles "manager") ", "}}</td></tr>
</table>
</div>
<div class="section-card">
<h2>Platform permissions</h2>
<table>
<tr><th>Permission</th><th>Description</th><th>Held by IAM roles</th></tr>
{{range .Platform}}<tr><td class="meta">{{.Name}}</td><td>{{.Description}}</td><td class="meta">{{if .Roles}}{{join .Roles ", "}}{{else}}no role (implicit / unassigned){{end}}</td></tr>{{end}}
</table>
</div>
{{end}}`)
