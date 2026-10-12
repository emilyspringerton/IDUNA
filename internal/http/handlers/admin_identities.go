package handlers

// admin_identities.go -- the IDUNA Back Office console for WOTAN identities (IDENTITY_NORTHSTAR section 8),
// following admin_clubs.go: mounted under /admin/identities by AdminHandler.Init so it inherits the /admin/
// gate in main.go (RequireCookieAuth + RequirePermission("iduna.admin")); the operator is the session subject.
//
//	GET  /admin/identities                      index: search by player id / handle / BattleTag / Twitch / key prefix,
//	                                            plus the recent identity_taken dispute queue
//	GET  /admin/identities/{player_id}          detail: identities (incl. revoked), upload keys, grace, clubs, audit
//	POST /admin/identities/{player_id}/unlink   force-unlink one identity          (identity_id, reason)
//	POST /admin/identities/{player_id}/revoke-key   revoke one upload key          (key_id, reason)
//	POST /admin/identities/{player_id}/merge    absorb another player into this one (from_sub, reason)
//
// Every POST requires a reason (3-300 chars), writes identity_audit_log with platform_admin=1 under the
// operator's sub and mirrors to the unified event log as iduna:wotan.identity.admin.*. CSRF protection is the
// session cookie's SameSite=Lax, exactly like every other /admin/ form.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"iduna/internal/clubs"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"
)

const identitiesAdminPageSize = 50

var identityNotices = map[string]string{
	"unlink": "Identity unlinked.", "revoke-key": "Upload key revoked.", "merge": "Players merged.",
}

func (h *AdminHandler) identitiesRouter(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "identities console unavailable: no database handle", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/identities"), "/")
	p := []string{}
	if rest != "" {
		p = strings.Split(rest, "/")
	}
	switch {
	case len(p) == 0 && r.Method == http.MethodGet:
		h.identitiesIndex(w, r)
	case len(p) == 1 && r.Method == http.MethodGet:
		h.identitiesDetail(w, r, p[0], "", http.StatusOK)
	case len(p) == 2 && r.Method == http.MethodPost:
		h.identitiesAction(w, r, p[0], p[1])
	default:
		http.NotFound(w, r)
	}
}

// ---- index --------------------------------------------------------------------------------------------------

type identityIndexRow struct {
	PlayerID, Handle, Identities, Grace string
	ActiveKeys                          int
}

type identityDisputeRow struct {
	At, PlayerID, Provider, Owner string
}

func (h *AdminHandler) identitiesIndex(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	offset := atoiDefault(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	var ids []string
	var q string
	var args []any
	if search != "" {
		like := hsLike(search)
		q = `SELECT player_id FROM (
			SELECT player_id FROM player_identities WHERE display LIKE ? ESCAPE '\' OR provider_sub=? OR player_id=?
			UNION SELECT sub FROM hs_profiles WHERE handle LIKE ? ESCAPE '\'
			UNION SELECT player_id FROM hs_upload_keys WHERE key_prefix LIKE ? ESCAPE '\'
		) ORDER BY player_id LIMIT ? OFFSET ?`
		args = []any{like, search, search, like, like, identitiesAdminPageSize, offset}
	} else {
		q = `SELECT player_id FROM player_identities GROUP BY player_id ORDER BY MAX(id) DESC LIMIT ? OFFSET ?`
		args = []any{identitiesAdminPageSize, offset}
	}
	if rows, err := h.DB.Query(q, args...); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
	}
	var items []identityIndexRow
	for _, id := range ids {
		row := identityIndexRow{PlayerID: id}
		_ = h.DB.QueryRow(`SELECT handle FROM hs_profiles WHERE sub=?`, id).Scan(&row.Handle)
		var parts []string
		for _, it := range identitiesOf(h.DB, id, false) {
			parts = append(parts, it.Provider+": "+it.Display)
		}
		row.Identities = strings.Join(parts, " | ")
		_ = h.DB.QueryRow(`SELECT COUNT(*) FROM hs_upload_keys WHERE player_id=? AND revoked_at IS NULL`, id).Scan(&row.ActiveKeys)
		var g string
		if h.DB.QueryRow(`SELECT grace_until FROM player_identity_grace WHERE player_id=?`, id).Scan(&g) == nil {
			row.Grace = hsTime(g)
		}
		items = append(items, row)
	}
	var disputes []identityDisputeRow
	if rows, err := h.DB.Query(`SELECT at, player_id, meta_json FROM identity_audit_log WHERE action='link.identity_taken' ORDER BY id DESC LIMIT 20`); err == nil {
		for rows.Next() {
			var d identityDisputeRow
			var meta string
			if rows.Scan(&d.At, &d.PlayerID, &meta) != nil {
				continue
			}
			d.At = hsTime(d.At)
			d.Provider = jsonStr(meta, "provider")
			d.Owner = jsonStr(meta, "owner_player_id")
			disputes = append(disputes, d)
		}
		rows.Close()
	}
	data := map[string]any{"Title": "WOTAN Identities", "Items": items, "Q": search, "Disputes": disputes, "From": offset + 1, "To": offset + len(items)}
	if offset > 0 {
		prev := offset - identitiesAdminPageSize
		if prev < 0 {
			prev = 0
		}
		data["PrevLink"] = "/admin/identities?q=" + url.QueryEscape(search) + "&offset=" + strconv.Itoa(prev)
	}
	if len(items) == identitiesAdminPageSize {
		data["NextLink"] = "/admin/identities?q=" + url.QueryEscape(search) + "&offset=" + strconv.Itoa(offset+identitiesAdminPageSize)
	}
	renderHTML(w, adminIdentitiesIndexTmpl, data)
}

func jsonStr(raw, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// ---- detail -------------------------------------------------------------------------------------------------

type identityKeyView struct {
	ID                                        int64
	Prefix, Label, Created, LastUsed, Revoked string
	UploadsOK, Rejected                       int
	Active                                    bool
}

type identityAuditView struct {
	ID                      int64
	At, Actor, Action, Meta string
	Platform                bool
}

func (h *AdminHandler) identitiesDetail(w http.ResponseWriter, r *http.Request, playerID, errMsg string, status int) {
	ensureLocalIdentities(h.DB, playerID)
	its := identitiesOf(h.DB, playerID, true)
	var known int
	if len(its) == 0 {
		if h.DB.QueryRow(`SELECT 1 FROM hs_profiles WHERE sub=? UNION SELECT 1 FROM players WHERE player_id=? LIMIT 1`, playerID, playerID).Scan(&known) != nil {
			http.NotFound(w, r)
			return
		}
	}
	var handle string
	_ = h.DB.QueryRow(`SELECT handle FROM hs_profiles WHERE sub=?`, playerID).Scan(&handle)
	g := identityGate(h.DB, playerID, nil)
	d := map[string]any{"Title": "Identities: " + playerID, "PlayerID": playerID, "Handle": handle, "Identities": its,
		"Gate": g, "GraceUntil": hsTime(g.GraceUntil), "Error": errMsg, "Notice": identityNotices[r.URL.Query().Get("notice")]}

	var keys []identityKeyView
	if rows, err := h.DB.Query(`SELECT id, key_prefix, label, created_at, COALESCE(last_used_at,''), COALESCE(revoked_at,''), uploads_ok, rejected_tag_mismatch
		FROM hs_upload_keys WHERE player_id=? ORDER BY id DESC LIMIT 50`, playerID); err == nil {
		for rows.Next() {
			var k identityKeyView
			if rows.Scan(&k.ID, &k.Prefix, &k.Label, &k.Created, &k.LastUsed, &k.Revoked, &k.UploadsOK, &k.Rejected) == nil {
				k.Active = k.Revoked == ""
				k.Created, k.LastUsed, k.Revoked = hsTime(k.Created), hsTime(k.LastUsed), hsTime(k.Revoked)
				keys = append(keys, k)
			}
		}
		rows.Close()
	}
	d["Keys"] = keys

	var clubRows []string
	if rows, err := h.DB.Query(`SELECT c.slug, m.role, c.is_exclusive FROM club_memberships m JOIN clubs c ON c.id=m.club_id
		WHERE m.sub=? AND c.deleted=0 ORDER BY c.slug`, playerID); err == nil {
		for rows.Next() {
			var slug, role string
			var ex int
			if rows.Scan(&slug, &role, &ex) == nil {
				s := slug + " (" + role + ")"
				if ex == 1 {
					s += " exclusive"
				}
				clubRows = append(clubRows, s)
			}
		}
		rows.Close()
	}
	d["Clubs"] = clubRows

	var audit []identityAuditView
	if rows, err := h.DB.Query(`SELECT id, at, actor_sub, action, meta_json, platform_admin FROM identity_audit_log
		WHERE player_id=? ORDER BY id DESC LIMIT 50`, playerID); err == nil {
		for rows.Next() {
			var a identityAuditView
			var pa int
			if rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.Meta, &pa) == nil {
				a.Platform, a.At = pa == 1, hsTime(a.At)
				audit = append(audit, a)
			}
		}
		rows.Close()
	}
	d["Audit"] = audit
	w.WriteHeader(status)
	renderHTML(w, adminIdentityDetailTmpl, d)
}

// ---- actions ------------------------------------------------------------------------------------------------

func (h *AdminHandler) identitiesAction(w http.ResponseWriter, r *http.Request, playerID, action string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if _, ok := identityNotices[action]; !ok {
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
		h.identitiesDetail(w, r, playerID, "A reason of 3-300 characters is required for every platform action; nothing was changed.", http.StatusUnprocessableEntity)
		return
	}
	var opErr *clubOpErr
	switch action {
	case "unlink":
		opErr = adminForceUnlink(r.Context(), h.DB, h.EventLog, actor, playerID, atoiDefault(r.FormValue("identity_id"), 0),
			r.FormValue("allow_last_door") == "1", reason)
	case "revoke-key":
		opErr = adminRevokeKey(r.Context(), h.DB, h.EventLog, actor, playerID, int64(atoiDefault(r.FormValue("key_id"), 0)), reason)
	case "merge":
		opErr = MergePlayers(r.Context(), h.DB, h.EventLog, actor, strings.TrimSpace(r.FormValue("from_sub")), playerID, reason)
	}
	if opErr != nil {
		h.identitiesDetail(w, r, playerID, opErr.msg, opErr.status)
		return
	}
	http.Redirect(w, r, "/admin/identities/"+url.PathEscape(playerID)+"?notice="+url.QueryEscape(action), http.StatusSeeOther)
}

func adminForceUnlink(ctx context.Context, db *sql.DB, el userlog.EventLog, actor, playerID string, identityID int, allowLast bool, reason string) *clubOpErr {
	it, err := scanIdentity(db.QueryRow(`SELECT `+identityCols+` FROM player_identities WHERE id=? AND player_id=? AND status='active'`, identityID, playerID))
	if err != nil {
		return &clubOpErr{http.StatusNotFound, "not_found", "no such active identity on this player"}
	}
	if loginDoorProviders[it.Provider] && loginDoorCount(db, playerID) <= 1 && !allowLast {
		return &clubOpErr{http.StatusConflict, "last_login_door", "that is the player's last login door; tick the confirmation to unlink it anyway"}
	}
	tx, err := db.Begin()
	if err != nil {
		return errClubDB
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE player_identities SET status='revoked' WHERE id=?`, it.ID); err != nil {
		return errClubDB
	}
	revoked := int64(0)
	if it.Provider == idProvBattlenet {
		var left int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM player_identities WHERE player_id=? AND provider='battlenet' AND status='active'`, playerID).Scan(&left)
		if left == 0 { // keys require a Battle.net identity: they go with the last one
			res, err := tx.Exec(`UPDATE hs_upload_keys SET revoked_at=? WHERE player_id=? AND revoked_at IS NULL`, sqlNowUTC(), playerID)
			if err != nil {
				return errClubDB
			}
			revoked, _ = res.RowsAffected()
		}
	}
	if err := tx.Commit(); err != nil {
		return errClubDB
	}
	identityAudit(ctx, db, el, playerID, actor, "admin.unlink",
		map[string]any{"reason": reason, "identity_id": it.ID, "provider": it.Provider, "keys_revoked": revoked}, true)
	return nil
}

func adminRevokeKey(ctx context.Context, db *sql.DB, el userlog.EventLog, actor, playerID string, keyID int64, reason string) *clubOpErr {
	res, err := db.Exec(`UPDATE hs_upload_keys SET revoked_at=? WHERE id=? AND player_id=? AND revoked_at IS NULL`, sqlNowUTC(), keyID, playerID)
	if err != nil {
		return errClubDB
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &clubOpErr{http.StatusNotFound, "not_found", "no such active upload key on this player"}
	}
	identityAudit(ctx, db, el, playerID, actor, "admin.revoke_key", map[string]any{"reason": reason, "key_id": keyID}, true)
	return nil
}

// MergePlayers is the operator-assisted merge: player `from` is absorbed into `into` (the master id that
// survives). Identities, upload keys, the HS profile (when `into` has none), decks, games, wall posts, club
// memberships (higher role wins on overlap) and club ownership, sync target / rank / settings rows, per-account
// permissions, a password credential (when `into` has none) and the grace window are re-pointed in one
// transaction. It is refused (nothing changes) when `from` and `into` both hold a Twitch identity, or when the
// merged membership set would put an account in an exclusive club together with any other club.
func MergePlayers(ctx context.Context, db *sql.DB, el userlog.EventLog, actor, from, into, reason string) *clubOpErr {
	if from == "" || into == "" {
		return &clubOpErr{http.StatusBadRequest, "bad_request", "from_sub and a target player are required"}
	}
	if from == into {
		return &clubOpErr{http.StatusUnprocessableEntity, "same_player", "cannot merge a player into itself"}
	}
	tx, err := db.Begin()
	if err != nil {
		return errClubDB
	}
	defer tx.Rollback()
	known := func(sub string) bool {
		var one int
		return tx.QueryRow(`SELECT 1 FROM players WHERE player_id=? UNION SELECT 1 FROM player_identities WHERE player_id=?
			UNION SELECT 1 FROM hs_profiles WHERE sub=? LIMIT 1`, sub, sub, sub).Scan(&one) == nil
	}
	if !known(from) {
		return &clubOpErr{http.StatusNotFound, "not_found", "no such player to merge from"}
	}
	if !known(into) {
		return &clubOpErr{http.StatusNotFound, "not_found", "no such player to merge into"}
	}
	var tw1, tw2 int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM player_identities WHERE player_id=? AND provider='twitch' AND status='active'`, from).Scan(&tw1)
	_ = tx.QueryRow(`SELECT COUNT(*) FROM player_identities WHERE player_id=? AND provider='twitch' AND status='active'`, into).Scan(&tw2)
	if tw1 > 0 && tw2 > 0 {
		return &clubOpErr{http.StatusConflict, "twitch_conflict", "both players have a Twitch identity linked; unlink one first (a player may hold only one)"}
	}

	// Membership union + exclusivity rule.
	type mem struct {
		role      string
		exclusive bool
		slug      string
	}
	load := func(sub string) map[string]mem {
		out := map[string]mem{}
		rows, err := tx.Query(`SELECT m.club_id, m.role, c.is_exclusive, c.slug FROM club_memberships m JOIN clubs c ON c.id=m.club_id
			WHERE m.sub=? AND c.deleted=0`, sub)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var id, role, slug string
			var ex int
			if rows.Scan(&id, &role, &ex, &slug) == nil {
				out[id] = mem{role, ex == 1, slug}
			}
		}
		return out
	}
	fm, im := load(from), load(into)
	union := map[string]mem{}
	for k, v := range fm {
		union[k] = v
	}
	for k, v := range im {
		union[k] = v
	}
	for id, m := range union {
		if !m.exclusive {
			continue
		}
		_, inFrom := fm[id]
		_, inInto := im[id]
		switch {
		case inFrom && inInto:
			return &clubOpErr{http.StatusConflict, "exclusive_conflict", "both players are members of exclusive club " + m.slug + "; resolve membership first"}
		case len(union) > 1:
			return &clubOpErr{http.StatusConflict, "exclusive_conflict", "the merged account would be in exclusive club " + m.slug + " and another club; resolve membership first"}
		}
	}

	exec := func(q string, args ...any) bool {
		_, e := tx.Exec(q, args...)
		return e == nil
	}
	ok := exec(`UPDATE player_identities SET player_id=? WHERE player_id=?`, into, from) &&
		exec(`UPDATE hs_upload_keys SET player_id=? WHERE player_id=?`, into, from) &&
		exec(`UPDATE hs_profiles SET sub=? WHERE sub=? AND NOT EXISTS (SELECT 1 FROM hs_profiles WHERE sub=?)`, into, from, into) &&
		exec(`UPDATE OR IGNORE hs_decks SET owner_sub=? WHERE owner_sub=?`, into, from) &&
		exec(`UPDATE OR IGNORE hs_games SET owner_sub=? WHERE owner_sub=?`, into, from) &&
		exec(`UPDATE hs_wall_posts SET owner_sub=? WHERE owner_sub=?`, into, from)
	for id, m := range fm {
		if !ok {
			break
		}
		if cur, both := im[id]; both {
			if clubs.Rank(clubs.Role(m.role)) > clubs.Rank(clubs.Role(cur.role)) {
				ok = exec(`UPDATE club_memberships SET role=? WHERE club_id=? AND sub=?`, m.role, id, into)
			}
			ok = ok && exec(`DELETE FROM club_memberships WHERE club_id=? AND sub=?`, id, from)
		} else {
			ok = exec(`UPDATE club_memberships SET sub=? WHERE club_id=? AND sub=?`, into, id, from)
		}
	}
	for _, tbl := range []string{"hs_sync_targets", "hs_player_rank", "hs_settings"} {
		ok = ok && exec(`UPDATE `+tbl+` SET sub=? WHERE sub=? AND NOT EXISTS (SELECT 1 FROM `+tbl+` WHERE sub=?)`, into, from, into) &&
			exec(`DELETE FROM `+tbl+` WHERE sub=?`, from)
	}
	ok = ok &&
		exec(`UPDATE clubs SET owner_sub=? WHERE owner_sub=?`, into, from) &&
		exec(`INSERT OR IGNORE INTO player_permissions (player_id, permission, granted_by) SELECT ?, permission, ? FROM player_permissions WHERE player_id=?`, into, actor, from) &&
		exec(`DELETE FROM player_permissions WHERE player_id=?`, from) &&
		exec(`UPDATE player_credentials SET player_id=? WHERE player_id=? AND NOT EXISTS (SELECT 1 FROM player_credentials WHERE player_id=?)`, into, from, into) &&
		exec(`INSERT OR REPLACE INTO player_identity_grace (player_id, grace_until, reason)
			SELECT ?, MAX(grace_until), 'merged' FROM player_identity_grace WHERE player_id IN (?, ?) HAVING COUNT(*) > 0`, into, from, into) &&
		exec(`DELETE FROM player_identity_grace WHERE player_id=?`, from)
	if !ok {
		return errClubDB
	}
	if err := tx.Commit(); err != nil {
		return errClubDB
	}
	meta := map[string]any{"reason": reason, "from": from, "into": into}
	identityAudit(ctx, db, el, into, actor, "admin.merge", meta, true)
	identityAudit(ctx, db, el, from, actor, "admin.merged_away", meta, true)
	return nil
}

// ---- templates ------------------------------------------------------------------------------------------------

const adminIdentitiesSubnav = `<p class="meta"><a href="/admin/identities">Identities</a> &middot; <a href="/admin/clubs">Clubs</a></p>`

var adminIdentitiesIndexTmpl = mustParseTmpl("identities-index", `
{{define "body"}}
<h1>WOTAN Identities</h1>
`+adminIdentitiesSubnav+`
<p class="meta">Linked Battle.net / Google / Twitch / email identities, tracker upload keys and the identity_taken dispute queue. Battle.net and Twitch names shown here are never public.</p>
<form method="GET" action="/admin/identities" class="section-card">
  <input type="text" name="q" value="{{.Q}}" placeholder="player id, handle, BattleTag, Twitch login, email, key prefix" style="width:380px">
  <input type="submit" value="Search">
</form>
{{if .Disputes}}
<div class="section-card">
<h2>Recent identity_taken disputes</h2>
<table><tr><th>When</th><th>Player who tried</th><th>Provider</th><th>Current owner</th></tr>
{{range .Disputes}}<tr><td class="meta">{{.At}}</td><td><a href="/admin/identities/{{.PlayerID}}">{{.PlayerID}}</a></td><td>{{.Provider}}</td><td>{{if .Owner}}<a href="/admin/identities/{{.Owner}}">{{.Owner}}</a>{{end}}</td></tr>{{end}}
</table>
<p class="meta">To resolve: open the owner, then use "Merge another player into this one" with the trying player's id.</p>
</div>
{{end}}
{{if .Items}}
<p class="meta">{{.From}}-{{.To}}</p>
<table>
<tr><th>Player</th><th>Handle</th><th>Identities</th><th>Active keys</th><th>Gate grace until</th></tr>
{{range .Items}}
<tr><td><a href="/admin/identities/{{.PlayerID}}">{{.PlayerID}}</a></td><td>{{.Handle}}</td><td class="meta">{{.Identities}}</td><td>{{.ActiveKeys}}</td><td class="meta">{{.Grace}}</td></tr>
{{end}}
</table>
<p>{{if .PrevLink}}<a href="{{.PrevLink}}">&larr; previous</a>{{end}} {{if .NextLink}}<a href="{{.NextLink}}">next &rarr;</a>{{end}}</p>
{{else}}<p class="empty">No players match.</p>{{end}}
{{end}}`)

var adminIdentityDetailTmpl = mustParseTmpl("identity-detail", `
{{define "body"}}
<h1>Identities: {{if .Handle}}{{.Handle}} {{end}}<span class="meta">{{.PlayerID}}</span></h1>
`+adminIdentitiesSubnav+`
{{if .Error}}<div class="err section-card" style="border-color:#b85c5c;color:#7a1a1a">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="section-card" style="border-color:#1a5c2a;color:#1a5c2a">{{.Notice}}</div>{{end}}

<div class="section-card">
<h2>Battle.net gate</h2>
<table>
<tr><td>Battle.net linked</td><td>{{.Gate.BattlenetLinked}}</td></tr>
<tr><td>Bypass permission (wotan.identity.bypass)</td><td>{{.Gate.Bypass}}</td></tr>
<tr><td>Grace window</td><td>{{if .GraceUntil}}until {{.GraceUntil}}{{if .Gate.InGrace}} (active){{else}} (expired){{end}}{{else}}none{{end}}</td></tr>
<tr><td>Gated actions allowed</td><td>{{.Gate.Open}}</td></tr>
</table>
</div>

<div class="section-card">
<h2>Identities</h2>
{{if .Identities}}
<table>
<tr><th>#</th><th>Provider</th><th>Display</th><th>Subject</th><th>Status</th><th>Linked</th><th>Last login</th><th></th></tr>
{{range .Identities}}
<tr><td>{{.ID}}</td><td>{{.Provider}}</td><td>{{.Display}}</td><td class="meta">{{.Sub}}</td><td>{{.Status}}</td><td class="meta">{{.LinkedAt}}</td><td class="meta">{{.LastLoginAt}}</td>
<td>{{if eq .Status "active"}}<form method="POST" action="/admin/identities/{{$.PlayerID}}/unlink">
  <input type="hidden" name="identity_id" value="{{.ID}}">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:170px">
  <label class="meta"><input type="checkbox" name="allow_last_door" value="1"> allow last door</label>
  <input type="submit" class="danger" value="Force unlink"></form>{{end}}</td></tr>
{{end}}
</table>
{{else}}<p class="empty">No identities.</p>{{end}}
</div>

<div class="section-card">
<h2>Upload keys</h2>
{{if .Keys}}
<table><tr><th>#</th><th>Prefix</th><th>Label</th><th>Created</th><th>Last used</th><th>OK</th><th>Rejected (tag mismatch)</th><th></th></tr>
{{range .Keys}}
<tr><td>{{.ID}}</td><td class="meta">{{.Prefix}}</td><td>{{.Label}}</td><td class="meta">{{.Created}}</td><td class="meta">{{.LastUsed}}</td><td>{{.UploadsOK}}</td><td>{{.Rejected}}</td>
<td>{{if .Active}}<form method="POST" action="/admin/identities/{{$.PlayerID}}/revoke-key">
  <input type="hidden" name="key_id" value="{{.ID}}">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:170px">
  <input type="submit" class="danger" value="Revoke"></form>{{else}}<span class="meta">revoked {{.Revoked}}</span>{{end}}</td></tr>
{{end}}
</table>
{{else}}<p class="empty">No upload keys.</p>{{end}}
</div>

<div class="section-card">
<h2>Merge another player into this one</h2>
<p class="meta">This player's id survives. The other player's identities, keys, profile (if this player has none), decks, games, club memberships and permissions move here. Refused when both hold a Twitch identity, or when an exclusive club would end up alongside another club.</p>
<form method="POST" action="/admin/identities/{{.PlayerID}}/merge">
  <input type="text" name="from_sub" placeholder="player id to absorb" required style="width:300px">
  <input type="text" name="reason" placeholder="reason (required)" required minlength="3" maxlength="300" style="width:240px">
  <input type="submit" class="danger" value="Merge" onclick="return confirm('Merge that player into this one? This cannot be undone.')">
</form>
{{if .Clubs}}<p class="meta">This player's clubs: {{range .Clubs}}{{.}}; {{end}}</p>{{end}}
</div>

<div class="section-card">
<h2>Audit log</h2>
{{if .Audit}}
<table><tr><th>#</th><th>When</th><th>Actor</th><th>Action</th><th>Meta</th></tr>
{{range .Audit}}<tr><td>{{.ID}}</td><td class="meta">{{.At}}</td><td class="meta">{{.Actor}}{{if .Platform}} <span class="badge badge-pending">platform</span>{{end}}</td><td>{{.Action}}</td><td class="meta">{{.Meta}}</td></tr>{{end}}
</table>
{{else}}<p class="empty">No audit entries.</p>{{end}}
</div>
{{end}}`)
