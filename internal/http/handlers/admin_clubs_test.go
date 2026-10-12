package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"
)

// adminClubsEnv wires the real Back Office handler (AdminHandler.Init mux) behind the same middleware stack
// main.go uses for /admin/ (RequireCookieAuth + RequirePermission("iduna.admin")), over the clubs test env's DB.
type adminClubsEnv struct {
	*clubEnv
	gate     http.Handler
	eventLog userlog.EventLog
}

func newAdminClubsEnv(t *testing.T) *adminClubsEnv {
	t.Helper()
	e := newClubEnv(t)
	el, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = el.Close() })
	ah := &handlers.AdminHandler{Store: &stubApplesStore{}, DB: e.db, EventLog: el}
	ah.Init()
	gate := middleware.RequireCookieAuth(e.keys, nil, "/admin/login", 0)(middleware.RequirePermission("iduna.admin")(ah))
	return &adminClubsEnv{clubEnv: e, gate: gate, eventLog: el}
}

// as issues a request through the gate with an admin cookie for sub (perms default iduna.admin).
func (a *adminClubsEnv) as(sub string, perms []string, method, path string, form url.Values) *httptest.ResponseRecorder {
	a.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if sub != "" {
		tok := a.token(sub, perms...)
		req.AddCookie(&http.Cookie{Name: "iduna_session", Value: tok})
	}
	w := httptest.NewRecorder()
	a.gate.ServeHTTP(w, req)
	return w
}

func (a *adminClubsEnv) admin(method, path string, form url.Values) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.as("op-1", []string{"iduna.admin"}, method, path, form)
}

func (a *adminClubsEnv) auditCount(club, action string, platformOnly bool) int {
	a.t.Helper()
	q := `SELECT COUNT(*) FROM club_audit_log WHERE club_id=? AND action=?`
	if platformOnly {
		q += ` AND platform_admin=1 AND actor_sub='op-1'`
	}
	var n int
	if err := a.db.QueryRow(q, club, action).Scan(&n); err != nil {
		a.t.Fatal(err)
	}
	return n
}

func (a *adminClubsEnv) totalAudit() int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM club_audit_log`).Scan(&n)
	return n
}

func TestAdminClubs_AuthGate(t *testing.T) {
	a := newAdminClubsEnv(t)
	// No session: redirected to login for a browser, 401 JSON otherwise.
	req := httptest.NewRequest(http.MethodGet, "/admin/clubs", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	a.gate.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/admin/login") {
		t.Fatalf("anon: %d loc=%q", w.Code, w.Header().Get("Location"))
	}
	if w := a.as("op-9", nil, http.MethodGet, "/admin/clubs", nil); w.Code != http.StatusForbidden {
		t.Fatalf("signed-in non-admin index: %d want 403", w.Code)
	}
	for _, p := range []string{"/admin/clubs/settings", "/admin/clubs/catalog", "/admin/clubs/x"} {
		if w := a.as("op-9", []string{"wotan.clubs.admin"}, http.MethodGet, p, nil); w.Code != http.StatusForbidden {
			t.Fatalf("non-iduna.admin %s: %d want 403", p, w.Code)
		}
	}
	if w := a.as("op-9", nil, http.MethodPost, "/admin/clubs/settings", url.Values{"reason": {"nope nope"}}); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin POST: %d want 403", w.Code)
	}
	if w := a.admin(http.MethodGet, "/admin/clubs", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "WOTAN Clubs") {
		t.Fatalf("admin index: %d", w.Code)
	}
}

func TestAdminClubs_IndexShowsSecretAndFilters(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob")
	secret := a.mk("alice", "dark-den", 1)
	social := a.mk("bob", "open-house", 3)
	body := a.admin(http.MethodGet, "/admin/clubs", nil).Body.String()
	for _, want := range []string{"dark-den", "open-house", "Secret", "Social", "/admin/clubs/" + secret, "/admin/clubs/" + social} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	// tier filter and search
	body = a.admin(http.MethodGet, "/admin/clubs?tier=3", nil).Body.String()
	if strings.Contains(body, "dark-den") || !strings.Contains(body, "open-house") {
		t.Errorf("tier=3 filter wrong")
	}
	body = a.admin(http.MethodGet, "/admin/clubs?q=dark", nil).Body.String()
	if !strings.Contains(body, "dark-den") || strings.Contains(body, "open-house") {
		t.Errorf("search wrong")
	}
	// suspended + deleted filters
	a.mustAct(secret, "suspend", url.Values{"reason": {"spam investigation"}})
	if b := a.admin(http.MethodGet, "/admin/clubs?suspended=1", nil).Body.String(); !strings.Contains(b, "dark-den") || strings.Contains(b, "open-house") {
		t.Errorf("suspended filter wrong")
	}
	a.mustAct(social, "delete", url.Values{"reason": {"owner request"}})
	if b := a.admin(http.MethodGet, "/admin/clubs", nil).Body.String(); strings.Contains(b, "open-house") {
		t.Errorf("deleted club shown by default")
	}
	if b := a.admin(http.MethodGet, "/admin/clubs?deleted=only", nil).Body.String(); !strings.Contains(b, "open-house") || strings.Contains(b, "dark-den") {
		t.Errorf("deleted=only wrong")
	}
	// hostile sort value falls back, never reaches SQL
	if w := a.admin(http.MethodGet, "/admin/clubs?sort=1;DROP+TABLE+clubs", nil); w.Code != http.StatusOK {
		t.Errorf("hostile sort: %d", w.Code)
	}
}

// mustAct posts an action and requires the 303 redirect back to the detail page.
func (a *adminClubsEnv) mustAct(club, action string, form url.Values) {
	a.t.Helper()
	w := a.admin(http.MethodPost, "/admin/clubs/"+club+"/"+action, form)
	if w.Code != http.StatusSeeOther {
		a.t.Fatalf("%s: %d body=%s", action, w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/admin/clubs/"+club) {
		a.t.Fatalf("%s: redirect %q", action, loc)
	}
}

func TestAdminClubs_DetailEscapesAndLogsSecretView(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob")
	secret := a.mk("alice", "dark-den", 1)
	a.addMember("alice", secret, "bob", "coach")
	if _, err := a.db.Exec(`UPDATE clubs SET name=?, description=? WHERE id=?`, `<script>alert(1)</script>`, `<img src=x onerror=alert(2)>`, secret); err != nil {
		t.Fatal(err)
	}
	if n := a.auditCount(secret, "admin.view", true); n != 0 {
		t.Fatalf("view rows before: %d", n)
	}
	w := a.admin(http.MethodGet, "/admin/clubs/"+secret, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script>alert(1)") || strings.Contains(body, "<img src=x") {
		t.Fatalf("club fields not escaped: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected escaped name")
	}
	if n := a.auditCount(secret, "admin.view", true); n != 1 {
		t.Fatalf("Secret club view must be audit-logged with platform_admin=1 and operator sub: got %d", n)
	}
	// Effective-permission matrix: coach has feed.moderate but not settings.edit; owner holds club.delete.
	for _, want := range []string{"club.feed.moderate", "club.owner.transfer", "alice_hs", "bob_hs", "coach", "manager", "owner"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if w := a.admin(http.MethodGet, "/admin/clubs/does-not-exist", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown club: %d", w.Code)
	}
}

func TestAdminClubs_EveryActionRequiresReason(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob")
	id := a.mk("alice", "reasoned", 2)
	a.addMember("alice", id, "bob", "player")
	_ = a.invite("alice", id, "player")
	forms := map[string]url.Values{
		"suspend": {}, "unsuspend": {}, "delete": {}, "restore": {},
		"tier": {"tier": {"3"}}, "exclusive": {"exclusive": {"1"}},
		"transfer": {"to_sub": {"bob"}}, "remove-member": {"sub": {"bob"}},
		"revoke-invites": {}, "revoke-invite": {"invite_id": {"x"}},
	}
	for action, f := range forms {
		for _, reason := range []string{"", "  ", "ab"} {
			f.Set("reason", reason)
			before := a.totalAudit()
			w := a.admin(http.MethodPost, "/admin/clubs/"+id+"/"+action, f)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s reason=%q: %d want 422", action, reason, w.Code)
			}
			if a.totalAudit() != before {
				t.Fatalf("%s reason=%q wrote an audit row", action, reason)
			}
		}
	}
	// nothing changed
	var tier, susp, del, excl int
	var owner string
	_ = a.db.QueryRow(`SELECT privacy_tier, suspended, deleted, is_exclusive, owner_sub FROM clubs WHERE id=?`, id).Scan(&tier, &susp, &del, &excl, &owner)
	if tier != 2 || susp != 0 || del != 0 || excl != 0 || owner != "alice" {
		t.Fatalf("state changed without reason: %d %d %d %d %s", tier, susp, del, excl, owner)
	}
	var members, pending int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=?`, id).Scan(&members)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM club_invites WHERE club_id=? AND revoked_at IS NULL AND used_at IS NULL`, id).Scan(&pending)
	if members != 2 || pending != 1 {
		t.Fatalf("members=%d pending=%d", members, pending)
	}
	// settings need one too
	if w := a.admin(http.MethodPost, "/admin/clubs/settings", url.Values{"clubs_per_user": {"2"}}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("settings w/o reason: %d", w.Code)
	}
}

func TestAdminClubs_ActionsAuditedWithPlatformFlagAndEvent(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob", "carol")
	id := a.mk("alice", "actions", 2)
	a.addMember("alice", id, "bob", "player")
	a.addMember("alice", id, "carol", "coach")
	_ = a.invite("alice", id, "player")
	_ = a.invite("alice", id, "player")
	r := func(s string) url.Values { return url.Values{"reason": {s}} }
	with := func(k, v string) url.Values { f := r("operator decision"); f.Set(k, v); return f }

	a.mustAct(id, "suspend", r("under review"))
	a.mustAct(id, "unsuspend", r("review done"))
	a.mustAct(id, "tier", with("tier", "1"))
	a.mustAct(id, "tier", with("tier", "3"))
	a.mustAct(id, "exclusive", with("exclusive", "1"))
	a.mustAct(id, "exclusive", with("exclusive", "0"))
	a.mustAct(id, "transfer", with("to_sub", "bob"))
	a.mustAct(id, "remove-member", with("sub", "carol"))
	a.mustAct(id, "revoke-invites", r("clean slate"))
	a.mustAct(id, "delete", r("tos violation"))
	a.mustAct(id, "restore", r("appeal granted"))

	for _, act := range []string{"suspend", "unsuspend", "tier", "exclusive", "transfer", "member_remove", "invites_revoke_all", "delete", "restore"} {
		if a.auditCount(id, "admin."+act, true) == 0 {
			t.Errorf("no platform_admin=1 audit row for admin.%s with actor op-1", act)
		}
	}
	// reason recorded in meta
	var meta string
	_ = a.db.QueryRow(`SELECT meta_json FROM club_audit_log WHERE club_id=? AND action='admin.suspend'`, id).Scan(&meta)
	if !strings.Contains(meta, "under review") {
		t.Errorf("meta=%s", meta)
	}
	// effects
	var owner string
	var role string
	var tier, del int
	_ = a.db.QueryRow(`SELECT owner_sub, privacy_tier, deleted FROM clubs WHERE id=?`, id).Scan(&owner, &tier, &del)
	_ = a.db.QueryRow(`SELECT role FROM club_memberships WHERE club_id=? AND sub='bob'`, id).Scan(&role)
	if owner != "bob" || role != "manager" || tier != 3 || del != 0 {
		t.Errorf("owner=%s bobrole=%s tier=%d del=%d", owner, role, tier, del)
	}
	var carol int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=? AND sub='carol'`, id).Scan(&carol)
	if carol != 0 {
		t.Errorf("carol still a member")
	}
	// unified event mirror
	recs, err := a.eventLog.ReadFrom(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, rec := range recs {
		seen[rec.Event.Type] = true
	}
	for _, want := range []string{"iduna:wotan.club.admin.suspend", "iduna:wotan.club.admin.transfer", "iduna:wotan.club.admin.delete", "iduna:wotan.club.admin.restore"} {
		if !seen[want] {
			t.Errorf("missing unified event %s (have %v)", want, seen)
		}
	}
}

func TestAdminClubs_Refusals(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob", "dave")
	id := a.mk("alice", "refuse", 2)
	a.addMember("alice", id, "bob", "player")
	f := func(k, v string) url.Values { return url.Values{"reason": {"because reasons"}, k: {v}} }
	expect := func(want int, action string, form url.Values) {
		t.Helper()
		if w := a.admin(http.MethodPost, "/admin/clubs/"+id+"/"+action, form); w.Code != want {
			t.Fatalf("%s: %d want %d (%s)", action, w.Code, want, w.Body.String())
		}
	}
	expect(http.StatusNotFound, "transfer", f("to_sub", "dave"))    // not a member
	expect(http.StatusConflict, "remove-member", f("sub", "alice")) // owner / last manager
	expect(http.StatusNotFound, "remove-member", f("sub", "dave"))
	expect(http.StatusUnprocessableEntity, "tier", f("tier", "9"))
	expect(http.StatusUnprocessableEntity, "tier", f("tier", "2")) // already there
	expect(http.StatusNotFound, "no-such-action", f("x", "y"))

	// exclusive refused while a member is in another club
	other := a.mk("dave", "elsewhere", 2)
	a.addMember("dave", other, "bob", "player")
	expect(http.StatusConflict, "exclusive", f("exclusive", "1"))
	// everything but restore refused on a deleted club
	a.mustAct(id, "delete", f("x", "y"))
	expect(http.StatusConflict, "suspend", f("x", "y"))
	expect(http.StatusConflict, "tier", f("tier", "3"))
	expect(http.StatusConflict, "delete", f("x", "y"))
	expect(http.StatusConflict, "unsuspend", f("x", "y"))
	a.mustAct(id, "restore", f("x", "y"))
	expect(http.StatusConflict, "restore", f("x", "y"))
	// GET on an action URL is not an action
	if w := a.admin(http.MethodGet, "/admin/clubs/"+id+"/suspend", nil); w.Code != http.StatusNotFound {
		t.Errorf("GET action: %d", w.Code)
	}
}

func TestAdminClubs_SuspendedClubPublicAndWorkspace(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob", "eve")
	id := a.mk("alice", "frozen", 3)
	a.addMember("alice", id, "bob", "player")
	a.must(200, "eve", "GET", "by-slug/frozen", "")
	a.must(201, "bob", "POST", id+"/feed", `{"body":"before the freeze"}`)

	a.mustAct(id, "suspend", url.Values{"reason": {"investigating reports"}})

	a.must(404, "eve", "GET", "by-slug/frozen", "")
	a.must(404, "", "GET", "by-slug/frozen", "")
	a.code(403, "club_suspended", "bob", "POST", id+"/feed", `{"body":"during the freeze"}`)
	a.code(403, "club_suspended", "alice", "POST", id+"/invites", `{"role":"player"}`)
	// members can still read
	a.must(200, "bob", "GET", id+"/feed", "")

	a.mustAct(id, "unsuspend", url.Values{"reason": {"all clear"}})
	a.must(200, "eve", "GET", "by-slug/frozen", "")
	a.must(201, "bob", "POST", id+"/feed", `{"body":"after the freeze"}`)
}

func TestAdminClubs_SettingsValidationAndEffect(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice")
	r := "tuning caps"
	post := func(f url.Values) int {
		f.Set("reason", r)
		return a.admin(http.MethodPost, "/admin/clubs/settings", f).Code
	}
	for _, bad := range []url.Values{
		{"clubs_per_user": {"0"}}, {"clubs_per_user": {"-3"}}, {"clubs_per_user": {"abc"}}, {"clubs_per_user": {"101"}},
		{"invite_ttl_max_hours": {"169"}}, {"members_per_club": {"5001"}}, {"pending_invites_per_club": {"1.5"}},
		{"featured_decks_cap": {"0"}},
		{"clubs_per_user": {"2"}, "members_per_club": {"0"}}, // one bad value rejects the whole save
	} {
		if c := post(bad); c != http.StatusUnprocessableEntity {
			t.Fatalf("%v: %d want 422", bad, c)
		}
	}
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM club_settings`).Scan(&n)
	if n != 0 {
		t.Fatalf("invalid saves wrote %d rows", n)
	}
	// defaults shown
	if b := a.admin(http.MethodGet, "/admin/clubs/settings", nil).Body.String(); !strings.Contains(b, "clubs_per_user") || !strings.Contains(b, "168") {
		t.Fatalf("settings page missing defaults")
	}
	if c := post(url.Values{"clubs_per_user": {"1"}, "featured_decks_cap": {"3"}}); c != http.StatusSeeOther {
		t.Fatalf("valid save: %d", c)
	}
	var v string
	_ = a.db.QueryRow(`SELECT value FROM club_settings WHERE key='clubs_per_user'`).Scan(&v)
	if v != "1" {
		t.Fatalf("stored %q", v)
	}
	if a.auditCount("", "admin.setting", true) != 2 {
		t.Fatalf("want 2 admin.setting audit rows (one per changed key)")
	}
	// The public handler really reads the cap: second club for the same owner is refused.
	a.mk("alice", "first-club", 2)
	a.code(403, "club_limit", "alice", "POST", "", `{"name":"Second","slug":"second-club","privacy_tier":2}`)
	// ... and raising it lifts the cap.
	if c := post(url.Values{"clubs_per_user": {"2"}}); c != http.StatusSeeOther {
		t.Fatalf("raise: %d", c)
	}
	a.mk("alice", "second-club", 2)
	// no-op save is not an error and not a write
	before := a.totalAudit()
	if c := post(url.Values{"clubs_per_user": {"2"}}); c != http.StatusOK {
		t.Fatalf("noop: %d", c)
	}
	if a.totalAudit() != before {
		t.Fatalf("no-op wrote audit")
	}
}

func TestAdminClubs_CatalogPage(t *testing.T) {
	a := newAdminClubsEnv(t)
	w := a.admin(http.MethodGet, "/admin/clubs/catalog", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog: %d", w.Code)
	}
	b := w.Body.String()
	for _, want := range []string{"club.view", "club.members.promote", "club.owner.transfer", "club.delete",
		"wotan.club.create", "wotan.clubs.admin", "wotan.clubs.moderate", "no role (implicit / unassigned)"} {
		if !strings.Contains(b, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
	// the migration grants admin+moderate to the seeded admin role, so at least one role name is listed.
	if strings.Count(b, "no role (implicit / unassigned)") != 1 {
		t.Errorf("only wotan.club.create should be unassigned")
	}
}

func TestAdminClubs_ReportsLinkedAndAccepted(t *testing.T) {
	a := newAdminClubsEnv(t)
	a.people("alice", "bob")
	id := a.mk("alice", "reported", 3)
	a.addMember("alice", id, "bob", "player")
	post := a.must(201, "bob", "POST", id+"/feed", `{"body":"hello"}`)
	pid := post["id"]
	// the public report endpoint accepts the two new kinds
	tok := a.token("bob")
	send := func(kind, target, reason string) int {
		req := httptest.NewRequest("POST", "/api/v1/hs/reports", strings.NewReader(`{"kind":"`+kind+`","id":"`+target+`","reason":"`+reason+`"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		a.hs.ServeHTTP(w, req)
		return w.Code
	}
	if c := send("club", id, "scam club <b>x</b>"); c != http.StatusAccepted {
		t.Fatalf("club report: %d", c)
	}
	postID := jstr(pid) // JSON number or string: trim quotes
	postID = strings.Trim(postID, `"`)
	if c := send("club_post", postID, "abusive post"); c != http.StatusAccepted {
		t.Fatalf("club_post report: %d", c)
	}
	if c := send("bogus", id, "x"); c != http.StatusUnprocessableEntity {
		t.Fatalf("unknown kind: %d", c)
	}
	b := a.admin(http.MethodGet, "/admin/clubs/"+id, nil).Body.String()
	if !strings.Contains(b, "abusive post") || !strings.Contains(b, "scam club") {
		t.Fatalf("linked reports not shown")
	}
	if strings.Contains(b, "<b>x</b>") {
		t.Fatalf("report reason not escaped")
	}
}
