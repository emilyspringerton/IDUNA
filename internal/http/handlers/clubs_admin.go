package handlers

// clubs_admin.go -- the machine admin surface for WOTAN clubs (CLUBS_NORTHSTAR.md 7.3), mounted under
// /api/v1/clubs/admin/*. Bearer permission wotan.clubs.admin (everything) or wotan.clubs.moderate (read-only).
// Platform operators are the trust root, so Secret clubs ARE visible here -- which is exactly why every call,
// reads included, requires a free-text `reason` and is written to club_audit_log with platform_admin=1.
//
//	GET    /clubs/admin/clubs?q=&tier=&suspended=&exclusive=&deleted=&limit=&offset=&reason=
//	GET    /clubs/admin/clubs/{id}?reason=
//	GET    /clubs/admin/clubs/{id}/audit?reason=&limit=&before=
//	POST   /clubs/admin/clubs/{id}/suspend    {reason}
//	POST   /clubs/admin/clubs/{id}/unsuspend  {reason}
//	POST   /clubs/admin/clubs/{id}/transfer   {to_sub, reason}
//	DELETE /clubs/admin/clubs/{id}            {reason} (or ?reason=)

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	clubAdminPerm    = "wotan.clubs.admin"
	clubModeratePerm = "wotan.clubs.moderate"
)

func clubReason(raw string) (string, bool) {
	s, ok := hsText(raw, 3, 300)
	return s, ok
}

func (h *ClubsHandler) admin(w http.ResponseWriter, r *http.Request, v clubViewer, p []string) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	full, read := v.perms[clubAdminPerm], v.perms[clubAdminPerm] || v.perms[clubModeratePerm]
	if !read {
		clubErr(w, http.StatusForbidden, "forbidden", "platform permission required")
		return
	}
	m, n := r.Method, len(p)
	isRead := m == http.MethodGet
	if !isRead && !full {
		clubErr(w, http.StatusForbidden, "forbidden", "wotan.clubs.admin required")
		return
	}
	if n == 0 || p[0] != "clubs" {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	// The reason: query string for reads/deletes, JSON body for the POST actions (query accepted too).
	reason := r.URL.Query().Get("reason")
	var body struct {
		Reason string `json:"reason"`
		ToSub  string `json:"to_sub"`
	}
	if m != http.MethodGet {
		if clubDecodeOpt(r, &body) != nil {
			hsErr(w, http.StatusBadRequest, "bad json")
			return
		}
		if body.Reason != "" {
			reason = body.Reason
		}
	}
	reason, ok := clubReason(reason)
	if !ok {
		clubErr(w, http.StatusUnprocessableEntity, "reason_required", "a reason of 3-300 characters is required for platform actions")
		return
	}
	switch {
	case n == 1 && m == http.MethodGet:
		h.adminList(w, r, v, reason)
	case n == 2 && m == http.MethodGet:
		h.adminGet(w, r, v, p[1], reason)
	case n == 3 && p[2] == "audit" && m == http.MethodGet:
		h.adminAudit(w, r, v, p[1], reason)
	case n == 3 && p[2] == "suspend" && m == http.MethodPost:
		h.adminSuspend(w, r, v, p[1], reason, true)
	case n == 3 && p[2] == "unsuspend" && m == http.MethodPost:
		h.adminSuspend(w, r, v, p[1], reason, false)
	case n == 3 && p[2] == "transfer" && m == http.MethodPost:
		h.adminTransfer(w, r, v, p[1], body.ToSub, reason)
	case n == 2 && m == http.MethodDelete:
		h.adminDelete(w, r, v, p[1], reason)
	default:
		hsErr(w, http.StatusNotFound, "not found")
	}
}

// adminAuditOnly records a platform_admin=1 audit row (used for reads).
func (h *ClubsHandler) adminAuditOnly(r *http.Request, v clubViewer, clubID, action string, meta map[string]any) bool {
	tx, err := h.begin(r)
	if err != nil {
		return false
	}
	defer tx.rollback()
	tx.audit(clubID, v.sub, action, "", meta, true)
	return tx.commit() == nil
}

func (h *ClubsHandler) adminList(w http.ResponseWriter, r *http.Request, v clubViewer, reason string) {
	q := r.URL.Query()
	limit, offset := hsPage(r, 50)
	where := []string{"1=1"}
	args := []any{}
	if q.Get("deleted") != "1" {
		where = append(where, "c.deleted=0")
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		where = append(where, `(c.name LIKE ? ESCAPE '\' OR c.slug LIKE ? ESCAPE '\' OR c.id=?)`)
		args = append(args, hsLike(s), hsLike(s), s)
	}
	if t, err := strconv.Atoi(q.Get("tier")); err == nil && t >= 1 && t <= 3 {
		where = append(where, "c.privacy_tier=?")
		args = append(args, t)
	}
	if q.Get("suspended") == "1" || q.Get("suspended") == "0" {
		where = append(where, "c.suspended=?")
		args = append(args, q.Get("suspended"))
	}
	if q.Get("exclusive") == "1" || q.Get("exclusive") == "0" {
		where = append(where, "c.is_exclusive=?")
		args = append(args, q.Get("exclusive"))
	}
	from := ` FROM clubs c LEFT JOIN hs_profiles o ON o.sub=c.owner_sub WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(`SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	rows, err := h.DB.Query(`SELECT c.id, c.slug, c.name, c.privacy_tier, c.is_exclusive, c.suspended, c.deleted, c.owner_sub,
		COALESCE(o.handle,''), c.created_at, (SELECT COUNT(*) FROM club_memberships m WHERE m.club_id=c.id)`+from+
		` ORDER BY c.created_at DESC, c.rowid DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	items := []map[string]any{}
	for rows.Next() {
		var id, slug, name, owner, ownerHandle, created string
		var tier, ex, sus, del, members int
		if rows.Scan(&id, &slug, &name, &tier, &ex, &sus, &del, &owner, &ownerHandle, &created, &members) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "slug": slug, "name": name, "privacy_tier": tier, "tier": clubTier(tier),
			"is_exclusive": ex == 1, "suspended": sus == 1, "deleted": del == 1, "owner_sub": owner, "owner_handle": ownerHandle,
			"member_count": members, "created_at": hsTime(created)})
	}
	rows.Close()
	if !h.adminAuditOnly(r, v, "", "admin.list", map[string]any{"reason": reason, "returned": len(items)}) {
		hsErr(w, 500, "database error")
		return
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) adminClub(w http.ResponseWriter, id string) (clubRow, bool) {
	c, err := loadClub(h.DB, "id", id)
	if err != nil {
		hsErr(w, http.StatusNotFound, "not found")
		return c, false
	}
	return c, true
}

func (h *ClubsHandler) adminGet(w http.ResponseWriter, r *http.Request, v clubViewer, id, reason string) {
	c, ok := h.adminClub(w, id)
	if !ok {
		return
	}
	members := []map[string]any{}
	rows, err := h.DB.Query(`SELECT m.sub, COALESCE(p.handle,''), m.role, m.public_visible, m.show_badge, m.joined_at
		FROM club_memberships m LEFT JOIN hs_profiles p ON p.sub=m.sub WHERE m.club_id=?
		ORDER BY CASE m.role WHEN 'manager' THEN 0 WHEN 'coach' THEN 1 ELSE 2 END, p.handle`, c.ID)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	for rows.Next() {
		var sub, handle, role, joined string
		var pv, sb int
		if rows.Scan(&sub, &handle, &role, &pv, &sb, &joined) != nil {
			continue
		}
		members = append(members, map[string]any{"sub": sub, "handle": handle, "role": role, "is_owner": sub == c.OwnerSub,
			"public_visible": pv == 1, "show_badge": sb == 1, "joined_at": hsTime(joined),
			"effective_perms": clubsPerms(role, sub == c.OwnerSub)})
	}
	rows.Close()
	var invites, requests, featured, posts int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_invites WHERE club_id=? AND used_at IS NULL AND revoked_at IS NULL AND expires_at>?`, c.ID, clubNow()).Scan(&invites)
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_join_requests WHERE club_id=? AND status='pending'`, c.ID).Scan(&requests)
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_featured_decks WHERE club_id=?`, c.ID).Scan(&featured)
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_posts WHERE club_id=? AND deleted=0`, c.ID).Scan(&posts)
	if !h.adminAuditOnly(r, v, c.ID, "admin.view", map[string]any{"reason": reason}) {
		hsErr(w, 500, "database error")
		return
	}
	var prev any
	if c.PrevTier.Valid {
		prev = c.PrevTier.Int64
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": c.ID, "slug": c.Slug, "name": c.Name, "tagline": c.Tagline, "description": c.Description,
		"privacy_tier": c.Tier, "tier": clubTier(c.Tier), "prev_tier": prev, "is_exclusive": c.Exclusive,
		"join_policy": c.JoinPolicy, "show_contributors": c.ShowContrib, "owner_sub": c.OwnerSub,
		"owner_handle": h.handleOf(h.DB, c.OwnerSub), "brand": brandOf(c.BrandJSON), "suspended": c.Suspended,
		"suspended_reason": c.SuspendedReason, "deleted": c.Deleted, "created_at": hsTime(c.CreatedAt), "updated_at": hsTime(c.UpdatedAt),
		"members": members, "member_count": len(members),
		"counts": map[string]int{"pending_invites": invites, "pending_requests": requests, "featured_decks": featured, "posts": posts},
	})
}

func (h *ClubsHandler) adminAudit(w http.ResponseWriter, r *http.Request, v clubViewer, id, reason string) {
	c, ok := h.adminClub(w, id)
	if !ok {
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, last, ok := h.auditRows(c.ID, limit, before)
	if !ok {
		hsErr(w, 500, "database error")
		return
	}
	if !h.adminAuditOnly(r, v, c.ID, "admin.audit_view", map[string]any{"reason": reason}) {
		hsErr(w, 500, "database error")
		return
	}
	out := map[string]any{"total": len(items), "limit": limit, "offset": 0, "items": items}
	if len(items) == limit {
		out["next_before"] = last
	}
	writeJSON(w, http.StatusOK, out)
}

// clubOpErr is a refusal from one of the shared platform-action cores below. The JSON admin API maps it to
// hsErr/clubErr; the server-rendered Back Office console (admin_clubs.go) shows msg to the operator.
type clubOpErr struct {
	status int
	code   string // empty = plain {"error": msg} like the original hsErr responses
	msg    string
}

func (e *clubOpErr) Error() string { return e.msg }

func writeOpErr(w http.ResponseWriter, e *clubOpErr) {
	if e.code == "" {
		hsErr(w, e.status, e.msg)
		return
	}
	clubErr(w, e.status, e.code, e.msg)
}

var errClubDB = &clubOpErr{status: 500, msg: "database error"}

// adminSuspendCore suspends/unsuspends a club (platform_admin=1 audit row + unified event).
func (h *ClubsHandler) adminSuspendCore(r *http.Request, actor string, c clubRow, reason string, suspend bool) *clubOpErr {
	if c.Deleted {
		return &clubOpErr{http.StatusConflict, "deleted", "club is deleted"}
	}
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	sv, action, why := 0, "admin.unsuspend", ""
	if suspend {
		sv, action, why = 1, "admin.suspend", reason
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET suspended=?, suspended_reason=?, updated_at=? WHERE id=?`, sv, why, clubNow(), c.ID); err != nil {
		return errClubDB
	}
	tx.audit(c.ID, actor, action, "", map[string]any{"reason": reason}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

func (h *ClubsHandler) adminSuspend(w http.ResponseWriter, r *http.Request, v clubViewer, id, reason string, suspend bool) {
	c, ok := h.adminClub(w, id)
	if !ok {
		return
	}
	if e := h.adminSuspendCore(r, v.sub, c, reason, suspend); e != nil {
		writeOpErr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": c.ID, "suspended": suspend})
}

// adminTransferCore reassigns ownership to an existing member, promoting them to manager if needed.
func (h *ClubsHandler) adminTransferCore(r *http.Request, actor string, c clubRow, toSub, reason string) (bool, *clubOpErr) {
	if c.Deleted {
		return false, &clubOpErr{http.StatusConflict, "deleted", "club is deleted"}
	}
	if toSub == "" {
		return false, &clubOpErr{http.StatusBadRequest, "", "to_sub required"}
	}
	tx, err := h.begin(r)
	if err != nil {
		return false, errClubDB
	}
	defer tx.rollback()
	role, member := h.memberRole(tx.tx, c.ID, toSub)
	if !member {
		return false, &clubOpErr{http.StatusNotFound, "", "that person is not a member of this club"}
	}
	promoted := false
	if role != "manager" { // an operator reassigning a club may promote the new owner to keep the owner-is-manager invariant
		if _, err := tx.tx.Exec(`UPDATE club_memberships SET role='manager' WHERE club_id=? AND sub=?`, c.ID, toSub); err != nil {
			return false, errClubDB
		}
		promoted = true
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET owner_sub=?, updated_at=? WHERE id=?`, toSub, clubNow(), c.ID); err != nil {
		return false, errClubDB
	}
	tx.audit(c.ID, actor, "admin.transfer", toSub, map[string]any{"reason": reason, "from": c.OwnerSub, "to": toSub, "promoted": promoted}, true)
	if err := tx.commit(); err != nil {
		return false, errClubDB
	}
	return promoted, nil
}

func (h *ClubsHandler) adminTransfer(w http.ResponseWriter, r *http.Request, v clubViewer, id, toSub, reason string) {
	c, ok := h.adminClub(w, id)
	if !ok {
		return
	}
	promoted, e := h.adminTransferCore(r, v.sub, c, toSub, reason)
	if e != nil {
		writeOpErr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": c.ID, "owner_sub": toSub, "promoted": promoted})
}

// adminDeleteCore soft-deletes a club (pending invites and requests are killed, memberships kept).
func (h *ClubsHandler) adminDeleteCore(r *http.Request, actor string, c clubRow, reason string) *clubOpErr {
	if c.Deleted {
		return &clubOpErr{http.StatusConflict, "deleted", "club is already deleted"}
	}
	tx, err := h.begin(r)
	if err != nil {
		return errClubDB
	}
	defer tx.rollback()
	softDeleteClub(tx, c.ID)
	tx.audit(c.ID, actor, "admin.delete", "", map[string]any{"reason": reason, "slug": c.Slug}, true)
	if err := tx.commit(); err != nil {
		return errClubDB
	}
	return nil
}

func (h *ClubsHandler) adminDelete(w http.ResponseWriter, r *http.Request, v clubViewer, id, reason string) {
	c, ok := h.adminClub(w, id)
	if !ok {
		return
	}
	if e := h.adminDeleteCore(r, v.sub, c, reason); e != nil {
		writeOpErr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
