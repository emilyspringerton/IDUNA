package handlers_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/store"
)

const inviteURLBase = "https://wotan.okemily.com/clubs/join.html#"

type clubEnv struct {
	t    *testing.T
	h    http.Handler
	hs   http.Handler
	keys *jwt.Keys
	db   *sql.DB
	n    int
}

func newClubEnv(t *testing.T) *clubEnv {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../../migrations/truestore"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	keys, _ := jwt.GenerateKeys()
	return &clubEnv{t: t, keys: keys, db: db,
		h:  &handlers.ClubsHandler{DB: db, Keys: keys},
		hs: &handlers.HSHandler{DB: db, Keys: keys}}
}

func (e *clubEnv) token(sub string, perms ...string) string {
	claims := map[string]any{"sub": sub, "iss": "https://test.internal", "aud": "farthq-ecosystem",
		"exp": time.Now().Add(time.Hour).Unix()}
	if len(perms) > 0 {
		claims["permissions"] = perms
	}
	tok, err := jwt.Sign(e.keys, claims)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// do sends as a signed-in subject (sub == "" => anonymous). path is relative to /api/v1/clubs/.
func (e *clubEnv) do(sub, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	return e.doTok(e.tokenFor(sub), method, "/api/v1/clubs/"+path, body)
}

func (e *clubEnv) tokenFor(sub string) string {
	if sub == "" {
		return ""
	}
	return e.token(sub)
}

func (e *clubEnv) doTok(tok, method, full, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, full, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *clubEnv) must(want int, sub, method, path, body string) map[string]any {
	e.t.Helper()
	code, out := e.do(sub, method, path, body)
	if code != want {
		e.t.Fatalf("%s %s as %q: status %d want %d, body %v", method, path, sub, code, want, out)
	}
	return out
}

func (e *clubEnv) code(want int, wantCode, sub, method, path, body string) {
	e.t.Helper()
	out := e.must(want, sub, method, path, body)
	if wantCode != "" && out["code"] != wantCode {
		e.t.Fatalf("%s %s as %q: code %v want %q (%v)", method, path, sub, out["code"], wantCode, out)
	}
}

func (e *clubEnv) handle(sub, handle string) {
	e.t.Helper()
	if _, err := e.db.Exec(`INSERT INTO hs_profiles (sub, handle) VALUES (?,?)`, sub, handle); err != nil {
		e.t.Fatal(err)
	}
}

func (e *clubEnv) people(names ...string) {
	for _, n := range names {
		e.handle(n, n+"_hs")
	}
}

func (e *clubEnv) mk(owner, slug string, tier int) string {
	e.t.Helper()
	out := e.must(201, owner, "POST", "", fmt.Sprintf(`{"name":"Club %s","slug":%q,"privacy_tier":%d}`, slug, slug, tier))
	return out["id"].(string)
}

func (e *clubEnv) invite(actor, club, role string) (token string) {
	e.t.Helper()
	out := e.must(201, actor, "POST", club+"/invites", fmt.Sprintf(`{"role":%q}`, role))
	u := out["url"].(string)
	if !strings.HasPrefix(u, inviteURLBase) {
		e.t.Fatalf("bad invite url %q", u)
	}
	return strings.TrimPrefix(u, inviteURLBase)
}

// addMember invites with the actor and joins as sub.
func (e *clubEnv) addMember(actor, club, sub, role string) {
	e.t.Helper()
	tok := e.invite(actor, club, role)
	e.must(200, sub, "POST", "join", jstr(map[string]string{"token": tok}))
}

func (e *clubEnv) roleOf(club, sub string) string {
	var r string
	_ = e.db.QueryRow(`SELECT role FROM club_memberships WHERE club_id=? AND sub=?`, club, sub).Scan(&r)
	return r
}

func (e *clubEnv) deck(owner, title string, private int) int64 {
	e.n++
	res, err := e.db.Exec(`INSERT INTO hs_decks (owner_sub, title, class, format, deckstring, card_count, private) VALUES (?,?,?,?,?,?,?)`,
		owner, title, "Mage", "Standard", fmt.Sprintf("code-%d", e.n), 30, private)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func items(out map[string]any) []any {
	v, _ := out["items"].([]any)
	return v
}

func TestClub_RoleLifecycle(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "dave")
	e.code(409, "handle_required", "nohandle", "POST", "", `{"name":"No Handle","slug":"no-handle"}`)
	e.must(401, "", "POST", "", `{"name":"Anon Club","slug":"anon-club"}`)

	c := e.mk("alice", "alpha-club", 2)
	me := e.must(200, "alice", "GET", c+"/me", "")
	if me["role"] != "manager" || me["is_owner"] != true {
		t.Fatalf("creator me: %v", me)
	}
	perms := fmt.Sprint(me["perms"])
	for _, want := range []string{"club.owner.transfer", "club.delete", "club.audit.view", "club.members.invite.elevated"} {
		if !strings.Contains(perms, want) {
			t.Fatalf("owner missing %s: %s", want, perms)
		}
	}

	e.addMember("alice", c, "bob", "coach")
	e.addMember("bob", c, "carol", "player")
	e.addMember("bob", c, "dave", "player")
	if e.roleOf(c, "bob") != "coach" || e.roleOf(c, "carol") != "player" {
		t.Fatal("roles not applied")
	}

	// coach invite caps
	e.code(403, "forbidden", "bob", "POST", c+"/invites", `{"role":"coach"}`)
	e.code(403, "role_too_high", "bob", "POST", c+"/invites", `{"role":"manager"}`)
	e.code(422, "bad_role", "alice", "POST", c+"/invites", `{"role":"owner"}`)

	// player denials
	for _, d := range []struct{ method, path, body string }{
		{"POST", c + "/invites", `{}`}, {"GET", c + "/invites", ""}, {"PATCH", c, `{"name":"Hijacked"}`},
		{"PUT", c + "/visibility", `{"privacy_tier":3}`}, {"GET", c + "/audit", ""}, {"GET", c + "/requests", ""},
		{"PUT", c + "/featured-decks/1", ""}, {"PUT", c + "/feed/1/pin", ""}, {"DELETE", c, ""},
		{"POST", c + "/transfer", `{"to_sub":"bob"}`}, {"PATCH", c + "/members/dave", `{"role":"coach"}`},
		{"DELETE", c + "/members/dave", ""}, {"DELETE", c + "/members/bob", ""},
	} {
		e.code(403, "", "carol", d.method, d.path, d.body)
	}
	// coach denials (manager-only surface + owner-only)
	for _, d := range []struct{ method, path, body string }{
		{"PATCH", c, `{"name":"Hijacked"}`}, {"PUT", c + "/visibility", `{"privacy_tier":3}`}, {"GET", c + "/audit", ""},
		{"POST", c + "/transfer", `{"to_sub":"bob"}`}, {"DELETE", c, ""}, {"PATCH", c + "/members/carol", `{"role":"coach"}`},
		{"DELETE", c + "/members/alice", ""}, {"PATCH", c + "/members/alice", `{"role":"player"}`},
	} {
		e.code(403, "", "bob", d.method, d.path, d.body)
	}
	if got := e.must(200, "carol", "GET", c+"/me", "")["perms"]; strings.Contains(fmt.Sprint(got), "club.members.invite") {
		t.Fatalf("player perms leak invite: %v", got)
	}

	// coach may remove a player, not a coach
	e.must(200, "bob", "DELETE", c+"/members/dave", "")
	if e.roleOf(c, "dave") != "" {
		t.Fatal("dave not removed")
	}
	e.addMember("alice", c, "dave", "coach")
	e.code(403, "forbidden", "bob", "DELETE", c+"/members/dave", "")
	e.code(403, "forbidden", "dave", "DELETE", c+"/members/bob", "")

	// promote / demote, no self change
	e.must(200, "alice", "PATCH", c+"/members/carol", `{"role":"coach"}`)
	e.code(409, "no_change", "alice", "PATCH", c+"/members/carol", `{"role":"coach"}`)
	e.code(403, "self_change", "alice", "PATCH", c+"/members/alice", `{"role":"coach"}`)
	e.code(403, "self_change", "carol", "PATCH", c+"/members/carol", `{"role":"manager"}`)
	e.code(403, "owner_immune", "alice", "DELETE", c+"/members/alice", "")
	e.code(403, "owner_immune", "alice", "POST", c+"/leave", "")
	e.must(200, "alice", "PATCH", c+"/members/bob", `{"role":"manager"}`)
	e.code(403, "owner_immune", "bob", "PATCH", c+"/members/alice", `{"role":"player"}`)
	e.code(403, "owner_immune", "bob", "DELETE", c+"/members/alice", "")
	e.must(200, "bob", "PATCH", c+"/members/dave", `{"role":"player"}`) // manager demotes coach

	// transfer: owner only, target must be a manager
	e.code(422, "target_not_manager", "alice", "POST", c+"/transfer", `{"to_sub":"dave"}`)
	e.code(403, "forbidden", "bob", "POST", c+"/transfer", `{"to_sub":"bob"}`)
	e.must(200, "alice", "POST", c+"/transfer", `{"to_sub":"bob"}`)
	e.code(403, "forbidden", "alice", "POST", c+"/transfer", `{"to_sub":"alice"}`) // no longer owner
	e.code(403, "forbidden", "alice", "DELETE", c, "")
	e.must(200, "alice", "POST", c+"/leave", "") // former owner, another manager remains
	e.code(403, "owner_immune", "bob", "POST", c+"/leave", "")

	// owner deletes (soft); it disappears everywhere
	e.must(200, "bob", "DELETE", c, "")
	e.must(404, "", "GET", "by-slug/alpha-club", "")
	e.must(404, "bob", "GET", c+"/me", "")
	if n := len(items(e.must(200, "bob", "GET", "me/clubs", ""))); n != 0 {
		t.Fatalf("deleted club still in me/clubs: %d", n)
	}
	// audit rows exist for the lifecycle (append-only table written by every mutation)
	var rows int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM club_audit_log WHERE club_id=?`, c).Scan(&rows)
	if rows < 12 {
		t.Fatalf("expected a rich audit trail, got %d rows", rows)
	}
}

func TestClub_LastManagerProtection(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob")
	c := e.mk("alice", "solo-mgr", 2)
	e.addMember("alice", c, "bob", "coach")
	// the owner is always a manager so the guard normally fires as owner_immune; force an ownerless legacy
	// row to prove the last-manager invariant independently.
	if _, err := e.db.Exec(`UPDATE clubs SET owner_sub='' WHERE id=?`, c); err != nil {
		t.Fatal(err)
	}
	e.code(409, "last_manager", "alice", "POST", c+"/leave", "")
	e.code(409, "last_manager", "alice", "DELETE", c+"/members/alice", "")
	e.must(200, "alice", "PATCH", c+"/members/bob", `{"role":"manager"}`)
	e.must(200, "bob", "PATCH", c+"/members/alice", `{"role":"coach"}`) // two managers: demotion allowed
	e.code(409, "last_manager", "bob", "POST", c+"/leave", "")
}

func TestClub_SecretNotFoundVsPrivateForbidden(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "outsider")
	sec := e.mk("alice", "shadow-cell", 1)
	prv := e.mk("alice", "open-door", 2)

	internal := []struct{ method, tmpl, body string }{
		{"GET", "%s/me", ""}, {"POST", "%s/token", ""}, {"GET", "%s/members", ""}, {"GET", "%s/invites", ""},
		{"POST", "%s/invites", `{}`}, {"GET", "%s/feed", ""}, {"POST", "%s/feed", `{"body":"hi"}`}, {"GET", "%s/audit", ""},
		{"GET", "%s/requests", ""}, {"PATCH", "%s", `{"name":"Nope Nope"}`}, {"PUT", "%s/visibility", `{"privacy_tier":3}`},
		{"POST", "%s/leave", ""}, {"DELETE", "%s", ""}, {"POST", "%s/transfer", `{"to_sub":"outsider"}`},
		{"PUT", "%s/featured-decks/1", ""}, {"PATCH", "%s/members/me", `{"show_badge":false}`},
	}
	for _, d := range internal {
		e.must(404, "outsider", d.method, fmt.Sprintf(d.tmpl, sec), d.body)
		e.must(403, "outsider", d.method, fmt.Sprintf(d.tmpl, prv), d.body)
		// bodies never differ in a way that confirms the secret club exists
		_, a := e.do("outsider", d.method, fmt.Sprintf(d.tmpl, sec), d.body)
		_, b := e.do("outsider", d.method, fmt.Sprintf(d.tmpl, "00000000-0000-4000-8000-000000000000"), d.body)
		if jstr(a) != jstr(b) {
			t.Fatalf("%s %s: secret 404 body differs from nonexistent club: %v vs %v", d.method, d.tmpl, a, b)
		}
	}
	// public routes
	e.must(404, "outsider", "GET", "by-slug/shadow-cell", "")
	e.must(404, "", "GET", "by-slug/shadow-cell", "")
	e.must(404, "outsider", "GET", "by-slug/shadow-cell/roster", "")
	e.must(404, "outsider", "GET", "by-slug/shadow-cell/decks", "")
	e.must(404, "outsider", "POST", "by-slug/shadow-cell/requests", `{}`)
	e.must(200, "", "GET", "by-slug/open-door", "")
	e.must(200, "", "GET", "by-slug/open-door/roster", "")
	e.code(403, "tier_forbidden", "outsider", "GET", "by-slug/open-door/decks", "")
	// directory never lists Secret
	dir := e.must(200, "", "GET", "", "")
	for _, it := range items(dir) {
		if it.(map[string]any)["slug"] == "shadow-cell" {
			t.Fatal("secret club in directory")
		}
	}
	if len(items(dir)) != 1 {
		t.Fatalf("directory: %v", dir)
	}
	// ...and not on a member's public badges, but visible as a mutual workspace
	e.people("bob")
	e.addMember("alice", sec, "bob", "player")
	if n := len(items(e.must(200, "", "GET", "users/alice_hs/clubs", ""))); n != 1 { // only open-door
		t.Fatalf("anon badges: %d", n)
	}
	mut := items(e.must(200, "bob", "GET", "users/alice_hs/clubs", ""))
	if len(mut) != 2 {
		t.Fatalf("mutual badges: %v", mut)
	}
	if n := len(items(e.must(200, "outsider", "GET", "users/alice_hs/clubs", ""))); n != 1 {
		t.Fatalf("outsider badges: %d", n)
	}
}

func TestClub_CrossTenantIsolation(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "zed", "bob", "carol")
	a := e.mk("alice", "club-a", 1)
	b := e.mk("zed", "club-b", 2)
	e.addMember("alice", a, "bob", "coach")
	e.addMember("zed", b, "carol", "player")

	// coach of A acting as coach on B
	for _, d := range []struct{ method, path, body string }{
		{"GET", b + "/members", ""}, {"POST", b + "/invites", `{}`}, {"GET", b + "/feed", ""},
		{"POST", b + "/feed", `{"body":"x"}`}, {"PUT", b + "/featured-decks/1", ""}, {"GET", b + "/requests", ""},
		{"DELETE", b + "/members/carol", ""}, {"PATCH", b + "/members/carol", `{"role":"coach"}`},
	} {
		e.must(403, "bob", d.method, d.path, d.body)
	}
	// tampered club_id in the body is ignored: the post lands in A (the path), never in B
	out := e.must(201, "bob", "POST", a+"/feed", jstr(map[string]any{"body": "from A", "club_id": b}))
	pidA := int(out["id"].(float64))
	var inA, inB int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM club_posts WHERE club_id=?`, a).Scan(&inA)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM club_posts WHERE club_id=?`, b).Scan(&inB)
	if inA != 1 || inB != 0 {
		t.Fatalf("club_id from body leaked: A=%d B=%d", inA, inB)
	}
	// a body naming A against B's path stays on B's authorization (bob has no standing there)
	e.must(403, "bob", "PUT", b+"/visibility", jstr(map[string]any{"club_id": a, "privacy_tier": 3}))
	// zed (manager of B) cannot reach A's rows through B's path
	e.must(404, "zed", "GET", fmt.Sprintf("%s/feed/%d/comments", b, pidA), "")
	e.must(404, "zed", "DELETE", fmt.Sprintf("%s/feed/%d", b, pidA), "")
	e.must(404, "zed", "PUT", fmt.Sprintf("%s/feed/%d/pin", b, pidA), "")
	e.must(404, "zed", "POST", fmt.Sprintf("%s/feed/%d/comments", b, pidA), `{"body":"nope"}`)
	// ... nor to A's invites or members
	inv := e.must(201, "alice", "POST", a+"/invites", `{}`)["id"].(string)
	e.must(404, "zed", "DELETE", b+"/invites/"+inv, "")
	e.must(404, "zed", "PATCH", b+"/members/bob", `{"role":"manager"}`)
	e.must(404, "zed", "DELETE", b+"/members/bob", "")
	// and zed is just an outsider on A (secret => 404)
	e.must(404, "zed", "GET", a+"/members", "")
	// A's invite still works afterward (revocation under B's path was a no-op)
	tok := e.invite("alice", a, "player")
	e.must(200, "carol", "POST", "join", jstr(map[string]string{"token": tok}))
}

func TestClub_StaleMembershipAfterRemoval(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol")
	sec := e.mk("alice", "stale-secret", 1)
	prv := e.mk("alice", "stale-private", 2)
	for _, c := range []string{sec, prv} {
		e.addMember("alice", c, "bob", "coach")
	}
	bobTok := e.token("bob") // minted while still a member and still valid after removal
	for _, c := range []string{sec, prv} {
		if code, _ := e.doTok(bobTok, "GET", "/api/v1/clubs/"+c+"/members", ""); code != 200 {
			t.Fatalf("member read: %d", code)
		}
	}
	e.must(200, "alice", "DELETE", sec+"/members/bob", "")
	e.must(200, "alice", "DELETE", prv+"/members/bob", "")
	if code, _ := e.doTok(bobTok, "GET", "/api/v1/clubs/"+sec+"/members", ""); code != 404 {
		t.Fatalf("removed from secret: %d want 404", code)
	}
	if code, _ := e.doTok(bobTok, "GET", "/api/v1/clubs/"+prv+"/members", ""); code != 403 {
		t.Fatalf("removed from private: %d want 403", code)
	}
	if code, _ := e.doTok(bobTok, "POST", "/api/v1/clubs/"+prv+"/feed", `{"body":"still here?"}`); code != 403 {
		t.Fatalf("removed member posted: %d", code)
	}
	// an invite bob minted before removal dies with his standing
	e.addMember("alice", prv, "bob", "coach")
	tok := e.invite("bob", prv, "player")
	e.must(200, "alice", "DELETE", prv+"/members/bob", "")
	e.code(404, "invite_invalid", "carol", "POST", "join", jstr(map[string]string{"token": tok}))
}

func TestClub_ExclusiveConflicts(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "dave", "erin", "frank")
	x := e.must(201, "alice", "POST", "", `{"name":"Club X","slug":"club-x","privacy_tier":2,"is_exclusive":true}`)["id"].(string)
	y := e.mk("bob", "club-y", 2)
	e.addMember("bob", y, "carol", "player")

	// joining an exclusive club while in another => 409; peek reports it as blocked
	tok := e.invite("alice", x, "player")
	pk := e.must(200, "carol", "POST", "invites/peek", jstr(map[string]string{"token": tok}))
	if pk["blocked"] != "exclusive_conflict" {
		t.Fatalf("peek: %v", pk)
	}
	e.code(409, "exclusive_conflict", "carol", "POST", "join", jstr(map[string]string{"token": tok}))
	// and the invite is still unspent for someone who can take it
	e.must(200, "dave", "POST", "join", jstr(map[string]string{"token": tok}))
	// a member of an exclusive club cannot join (or create) anything else
	e.code(409, "exclusive_conflict", "dave", "POST", "join", jstr(map[string]string{"token": e.invite("bob", y, "player")}))
	e.code(409, "exclusive_conflict", "dave", "POST", "", `{"name":"Dave Club","slug":"dave-club"}`)
	// creating an exclusive club while already in another => 409
	e.code(409, "exclusive_conflict", "carol", "POST", "", `{"name":"Carol X","slug":"carol-x","is_exclusive":true}`)

	// turning exclusive ON with members that also belong elsewhere lists them (own members only)
	z := e.mk("erin", "club-z", 1) // Secret clubs count (OQ-4)
	e.addMember("erin", z, "carol", "player")
	conf := e.must(409, "bob", "PUT", y+"/visibility", `{"is_exclusive":true}`)
	if conf["code"] != "exclusive_conflict" {
		t.Fatalf("%v", conf)
	}
	ms := conf["members"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["handle"] != "carol_hs" {
		t.Fatalf("conflict list: %v", ms)
	}
	e.must(200, "carol", "POST", z+"/leave", "")
	e.must(200, "bob", "PUT", y+"/visibility", `{"is_exclusive":true}`)
	// now frank (member of nothing) joins Y; then Secret club Z can't take him
	e.addMember("bob", y, "frank", "player")
	e.code(409, "exclusive_conflict", "frank", "POST", "join", jstr(map[string]string{"token": e.invite("erin", z, "player")}))
	// turning it off is always allowed, and frees frank
	e.must(200, "bob", "PUT", y+"/visibility", `{"is_exclusive":false}`)
	e.addMember("erin", z, "frank", "player")
}

func TestClub_InviteReplayExpiryRevokeBound(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "dave")
	c := e.mk("alice", "invite-club", 2)

	out := e.must(201, "alice", "POST", c+"/invites", `{"ttl_hours":100000,"bound_handle":""}`)
	tok := strings.TrimPrefix(out["url"].(string), inviteURLBase)
	if len(tok) < 40 {
		t.Fatalf("token too short: %d", len(tok))
	}
	exp, _ := time.Parse(time.RFC3339, out["expires_at"].(string))
	if d := time.Until(exp); d > 169*time.Hour || d < 167*time.Hour {
		t.Fatalf("ttl not capped at 168h: %v", d)
	}
	// plaintext is never stored; only its sha256 is
	var hash string
	_ = e.db.QueryRow(`SELECT token_hash FROM club_invites WHERE id=?`, out["id"]).Scan(&hash)
	sum := sha256.Sum256([]byte(tok))
	if hash != hex.EncodeToString(sum[:]) || strings.Contains(hash, tok) {
		t.Fatalf("stored hash mismatch: %s", hash)
	}
	if list := e.must(200, "alice", "GET", c+"/invites", ""); strings.Contains(jstr(list), tok) {
		t.Fatal("invite list leaks the plaintext token")
	}

	// peek works for an anonymous holder and shows the club
	pk := e.must(200, "", "POST", "invites/peek", jstr(map[string]string{"token": tok}))
	if pk["club"].(map[string]any)["name"] != "Club invite-club" {
		t.Fatalf("peek: %v", pk)
	}
	e.must(401, "", "POST", "join", jstr(map[string]string{"token": tok}))

	// single use: replay is 410 invite_used, for the same user and for a different one
	e.must(200, "bob", "POST", "join", jstr(map[string]string{"token": tok}))
	e.code(410, "invite_used", "carol", "POST", "join", jstr(map[string]string{"token": tok}))
	e.code(410, "invite_used", "bob", "POST", "join", jstr(map[string]string{"token": tok}))
	e.code(410, "invite_used", "", "POST", "invites/peek", jstr(map[string]string{"token": tok}))

	// expiry
	t2 := e.invite("alice", c, "player")
	if _, err := e.db.Exec(`UPDATE club_invites SET expires_at='2020-01-01 00:00:00' WHERE token_hash=?`, hexSum(t2)); err != nil {
		t.Fatal(err)
	}
	e.code(410, "invite_expired", "carol", "POST", "join", jstr(map[string]string{"token": t2}))
	e.code(410, "invite_expired", "", "POST", "invites/peek", jstr(map[string]string{"token": t2}))

	// revoke
	o3 := e.must(201, "alice", "POST", c+"/invites", `{}`)
	t3 := strings.TrimPrefix(o3["url"].(string), inviteURLBase)
	e.must(200, "alice", "DELETE", c+"/invites/"+o3["id"].(string), "")
	e.code(404, "invite_invalid", "carol", "POST", "join", jstr(map[string]string{"token": t3}))
	e.must(404, "alice", "DELETE", c+"/invites/"+o3["id"].(string), "") // already revoked

	// bound to a handle
	o4 := e.must(201, "alice", "POST", c+"/invites", `{"bound_handle":"DAVE_hs"}`)
	t4 := strings.TrimPrefix(o4["url"].(string), inviteURLBase)
	e.code(403, "invite_bound", "carol", "POST", "join", jstr(map[string]string{"token": t4}))
	e.must(200, "dave", "POST", "join", jstr(map[string]string{"token": t4}))
	e.code(422, "unknown_handle", "alice", "POST", c+"/invites", `{"bound_handle":"nobody_here"}`)

	// garbage + handle required
	e.code(404, "invite_invalid", "carol", "POST", "join", `{"token":"not-a-real-token"}`)
	t5 := e.invite("alice", c, "player")
	e.code(409, "handle_required", "ghost", "POST", "join", jstr(map[string]string{"token": t5}))

	// pending invite cap
	if _, err := e.db.Exec(`INSERT INTO club_settings (key, value) VALUES ('pending_invites_per_club','2')`); err != nil {
		t.Fatal(err)
	}
	e.must(201, "alice", "POST", c+"/invites", `{}`) // t5 + this one = 2 pending (t5 above is unspent)
	e.code(409, "invite_cap", "alice", "POST", c+"/invites", `{}`)
}

func hexSum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestClub_VisibilityRevealAndPublicProjection(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "outsider")
	c := e.mk("alice", "social-club", 3)
	e.addMember("alice", c, "bob", "player")

	e.must(200, "alice", "PUT", c+"/visibility", `{"join_policy":"request","show_contributors":true}`)
	pub := e.must(200, "", "GET", "by-slug/social-club", "")
	for _, leak := range []string{"owner_sub", "prev_tier", "suspended", "suspended_reason", "deleted", "updated_at", "brand_json", "token"} {
		if _, ok := pub[leak]; ok {
			t.Fatalf("public projection leaks %q: %v", leak, pub)
		}
	}
	if pub["tier"] != "social" || pub["join_policy"] != "request" || pub["member_count"].(float64) != 2 {
		t.Fatalf("public view: %v", pub)
	}
	if v := pub["viewer"].(map[string]any); v["member"] != false || len(v["perms"].([]any)) != 0 {
		t.Fatalf("anon viewer: %v", v)
	}
	if bt := e.must(200, "alice", "GET", "by-slug/social-club", "")["viewer"].(map[string]any); bt["role"] != "manager" {
		t.Fatalf("member viewer: %v", bt)
	}

	// private cannot take requests
	e.code(422, "tier_forbidden", "alice", "PUT", c+"/visibility", `{"privacy_tier":2,"join_policy":"request"}`)
	// tier selector with memory: Social -> Secret remembers 3; leaving Social cancels request policy
	out := e.must(200, "alice", "PUT", c+"/visibility", `{"privacy_tier":1}`)
	if out["privacy_tier"].(float64) != 1 || out["prev_tier"].(float64) != 3 || out["join_policy"] != "invite" {
		t.Fatalf("to secret: %v", out)
	}
	e.must(404, "outsider", "GET", "by-slug/social-club", "")
	e.must(404, "outsider", "GET", "by-slug/social-club/roster", "")
	e.must(200, "bob", "GET", "by-slug/social-club", "") // members still see it
	if n := len(items(e.must(200, "", "GET", "", ""))); n != 0 {
		t.Fatalf("directory shows secret: %d", n)
	}
	rev := e.must(200, "alice", "PUT", c+"/visibility", `{"reveal":true}`)
	if rev["privacy_tier"].(float64) != 3 {
		t.Fatalf("reveal: %v", rev)
	}
	e.must(200, "outsider", "GET", "by-slug/social-club", "")
	if n := len(items(e.must(200, "", "GET", "", ""))); n != 1 {
		t.Fatalf("directory after reveal: %d", n)
	}
	e.code(422, "tier_forbidden", "alice", "PUT", c+"/visibility", `{"privacy_tier":9}`)

	// settings edit + brand validation + audit trail records the tier changes
	e.must(200, "alice", "PATCH", c, `{"tagline":"We <b>win</b>","brand":{"accent":"#ff00aa","banner_text":"GG"}}`)
	e.must(422, "alice", "PATCH", c, `{"brand":{"accent":"red"}}`)
	au := e.must(200, "alice", "GET", c+"/audit", "")
	if !strings.Contains(jstr(au), "visibility.set") || !strings.Contains(jstr(au), "settings.edit") {
		t.Fatalf("audit: %v", au)
	}
}

func TestClub_RosterPrefsAndBadges(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol")
	c := e.mk("alice", "roster-club", 2)
	e.addMember("alice", c, "bob", "coach")
	e.addMember("alice", c, "carol", "player")
	if _, err := e.db.Exec(`INSERT INTO hs_player_rank (sub, battletag, legend_rank, region) VALUES ('bob','Bob#1234',77,'EU')`); err != nil {
		t.Fatal(err)
	}
	ro := e.must(200, "", "GET", "by-slug/roster-club/roster", "")
	if len(items(ro)) != 3 || jstr(ro) == "" || strings.Contains(jstr(ro), "Bob#1234") {
		t.Fatalf("public roster (battletag must stay members-only): %v", ro)
	}
	first := items(ro)[0].(map[string]any)
	if first["handle"] != "alice_hs" || first["is_owner"] != true {
		t.Fatalf("ordering: %v", first)
	}
	mem := e.must(200, "carol", "GET", c+"/members", "")
	if !strings.Contains(jstr(mem), "Bob#1234") || !strings.Contains(jstr(mem), `"legend_rank":77`) {
		t.Fatalf("member roster should show battletag + rank: %v", mem)
	}
	// opt out of the public roster and of profile badges
	e.must(200, "carol", "PATCH", c+"/members/me", `{"public_visible":false,"show_badge":false}`)
	if n := len(items(e.must(200, "", "GET", "by-slug/roster-club/roster", ""))); n != 2 {
		t.Fatalf("roster after opt-out: %d", n)
	}
	if n := len(items(e.must(200, "", "GET", "users/carol_hs/clubs", ""))); n != 0 {
		t.Fatalf("badge after hide: %d", n)
	}
	if n := len(items(e.must(200, "", "GET", "users/bob_hs/clubs", ""))); n != 1 {
		t.Fatalf("bob badge: %d", n)
	}
	if got := e.must(200, "carol", "GET", c+"/members", "")["total"].(float64); got != 3 {
		t.Fatalf("members still lists opted-out: %v", got)
	}
	// me/clubs alias at /api/v1/me/clubs
	code, out := e.doTok(e.token("carol"), "GET", "/api/v1/me/clubs", "")
	if code != 200 || len(items(out)) != 1 {
		t.Fatalf("me/clubs alias: %d %v", code, out)
	}
	if code, _ := e.doTok("", "GET", "/api/v1/me/clubs", ""); code != 401 {
		t.Fatalf("anon me/clubs: %d", code)
	}
	// action affordances in the member list
	var bobRow map[string]any
	for _, it := range items(e.must(200, "alice", "GET", c+"/members", "")) {
		if m := it.(map[string]any); m["handle"] == "bob_hs" {
			bobRow = m
		}
	}
	if bobRow["can_remove"] != true || len(bobRow["can_set_roles"].([]any)) != 2 {
		t.Fatalf("manager affordances for coach row: %v", bobRow)
	}
	for _, it := range items(e.must(200, "bob", "GET", c+"/members", "")) {
		m := it.(map[string]any)
		if m["handle"] == "carol_hs" && m["can_remove"] != true {
			t.Fatalf("coach should be able to remove player: %v", m)
		}
		if m["handle"] == "alice_hs" && (m["can_remove"] != false || len(m["can_set_roles"].([]any)) != 0) {
			t.Fatalf("coach affordances on owner row: %v", m)
		}
	}
}

func TestClub_JoinRequests(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "dave")
	soc := e.mk("alice", "request-club", 3)
	prv := e.mk("alice", "no-request", 2)
	sec := e.mk("alice", "no-request-sec", 1)
	soc2 := e.mk("alice", "invite-only-soc", 3)
	e.must(200, "alice", "PUT", soc+"/visibility", `{"join_policy":"request"}`)

	e.code(403, "tier_forbidden", "carol", "POST", "by-slug/no-request/requests", `{}`)
	e.must(404, "carol", "POST", "by-slug/no-request-sec/requests", `{}`)
	e.code(403, "tier_forbidden", "carol", "POST", "by-slug/invite-only-soc/requests", `{}`)
	e.must(401, "", "POST", "by-slug/request-club/requests", `{}`)
	e.code(409, "handle_required", "ghost", "POST", "by-slug/request-club/requests", `{}`)
	_, _, _ = prv, sec, soc2

	e.must(201, "carol", "POST", "by-slug/request-club/requests", `{"message":"let me in"}`)
	e.code(409, "request_pending", "carol", "POST", "by-slug/request-club/requests", `{}`)
	if v := e.must(200, "carol", "GET", "by-slug/request-club", "")["viewer"].(map[string]any); v["pending_request"] != true {
		t.Fatalf("viewer: %v", v)
	}
	e.addMember("alice", soc, "bob", "player")
	e.code(403, "", "bob", "GET", soc+"/requests", "") // player cannot see the queue
	q := e.must(200, "alice", "GET", soc+"/requests", "")
	reqs := items(q)
	if len(reqs) != 1 || reqs[0].(map[string]any)["handle"] != "carol_hs" {
		t.Fatalf("queue: %v", q)
	}
	rid := reqs[0].(map[string]any)["id"].(string)
	e.code(403, "", "bob", "POST", soc+"/requests/"+rid+"/decision", `{"accept":true}`)
	e.must(200, "alice", "POST", soc+"/requests/"+rid+"/decision", `{"accept":true}`)
	if e.roleOf(soc, "carol") != "player" {
		t.Fatal("accepted request did not create membership")
	}
	e.must(404, "alice", "POST", soc+"/requests/"+rid+"/decision", `{"accept":true}`) // decided already
	e.code(409, "already_member", "carol", "POST", "by-slug/request-club/requests", `{}`)

	// rejection leaves no membership and frees the slot for another request
	e.must(201, "dave", "POST", "by-slug/request-club/requests", `{}`)
	rid2 := items(e.must(200, "alice", "GET", soc+"/requests", ""))[0].(map[string]any)["id"].(string)
	e.must(200, "alice", "POST", soc+"/requests/"+rid2+"/decision", `{"accept":false}`)
	if e.roleOf(soc, "dave") != "" {
		t.Fatal("rejected request joined")
	}
	e.must(201, "dave", "POST", "by-slug/request-club/requests", `{}`)
	// moving the club off Social cancels the pending request
	e.must(200, "alice", "PUT", soc+"/visibility", `{"privacy_tier":2}`)
	var pending int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM club_join_requests WHERE club_id=? AND status='pending'`, soc).Scan(&pending)
	if pending != 0 {
		t.Fatalf("pending requests survived tier change: %d", pending)
	}
}

func TestClub_FeedAndFeaturedDecks(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "outsider")
	c := e.mk("alice", "feed-club", 3)
	e.addMember("alice", c, "bob", "coach")
	e.addMember("alice", c, "carol", "player")

	pubDeck := e.deck("alice", "Public Mage", 0)
	privDeck := e.deck("alice", "Secret Tech", 1)

	e.must(422, "carol", "POST", c+"/feed", `{"body":""}`)
	e.must(422, "carol", "POST", c+"/feed", jstr(map[string]any{"body": "look", "pinned_deck_id": privDeck}))
	p1 := int(e.must(201, "carol", "POST", c+"/feed", jstr(map[string]any{"body": "my <i>list</i>", "pinned_deck_id": pubDeck}))["id"].(float64))
	p2 := int(e.must(201, "bob", "POST", c+"/feed", `{"body":"scrim at 8"}`)["id"].(float64))
	e.must(403, "carol", "PUT", fmt.Sprintf("%s/feed/%d/pin", c, p2), "")
	e.must(200, "bob", "PUT", fmt.Sprintf("%s/feed/%d/pin", c, p2), "")
	feed := items(e.must(200, "carol", "GET", c+"/feed", ""))
	if len(feed) != 2 || feed[0].(map[string]any)["id"].(float64) != float64(p2) || feed[0].(map[string]any)["pinned"] != true {
		t.Fatalf("pinned must sort first: %v", feed)
	}
	if feed[1].(map[string]any)["pinned_deck"].(map[string]any)["title"] != "Public Mage" {
		t.Fatalf("pinned deck: %v", feed[1])
	}
	// comments
	e.must(201, "carol", "POST", fmt.Sprintf("%s/feed/%d/comments", c, p2), `{"body":"on it"}`)
	cm := items(e.must(200, "bob", "GET", fmt.Sprintf("%s/feed/%d/comments", c, p2), ""))
	if len(cm) != 1 {
		t.Fatalf("comments: %v", cm)
	}
	e.must(404, "carol", "GET", fmt.Sprintf("%s/feed/99999/comments", c), "")
	// outsider sees none of it
	e.must(403, "outsider", "GET", c+"/feed", "")
	// delete: authors own posts; moderators anyone's; players not others'
	e.must(403, "carol", "DELETE", fmt.Sprintf("%s/feed/%d", c, p2), "")
	e.must(200, "bob", "DELETE", fmt.Sprintf("%s/feed/%d", c, p1), "") // coach moderates
	e.must(200, "bob", "DELETE", fmt.Sprintf("%s/feed/%d", c, p2), "") // own post
	if n := len(items(e.must(200, "alice", "GET", c+"/feed", ""))); n != 0 {
		t.Fatalf("feed after deletes: %d", n)
	}

	// featured decks: coach+, public decks only, shows on the Social public page, first-publisher claim
	e.must(403, "carol", "PUT", fmt.Sprintf("%s/featured-decks/%d", c, pubDeck), "")
	e.must(422, "bob", "PUT", fmt.Sprintf("%s/featured-decks/%d", c, privDeck), "")
	e.must(422, "bob", "PUT", c+"/featured-decks/424242", "")
	e.must(200, "bob", "PUT", fmt.Sprintf("%s/featured-decks/%d", c, pubDeck), "")
	e.must(200, "bob", "PUT", fmt.Sprintf("%s/featured-decks/%d", c, pubDeck), "") // idempotent
	pd := items(e.must(200, "", "GET", "by-slug/feed-club/decks", ""))
	if len(pd) != 1 || pd[0].(map[string]any)["claimed_by"] != "alice_hs" {
		t.Fatalf("public decks: %v", pd)
	}
	if fd := e.must(200, "", "GET", "by-slug/feed-club", "")["featured_decks"].([]any); len(fd) != 1 {
		t.Fatalf("view featured: %v", fd)
	}
	// a deck made private later disappears from the public page
	if _, err := e.db.Exec(`UPDATE hs_decks SET private=1 WHERE id=?`, pubDeck); err != nil {
		t.Fatal(err)
	}
	if n := len(items(e.must(200, "", "GET", "by-slug/feed-club/decks", ""))); n != 0 {
		t.Fatalf("private deck still public: %d", n)
	}
	e.must(200, "bob", "DELETE", fmt.Sprintf("%s/featured-decks/%d", c, pubDeck), "")
	// prestige = featured decks + their likes (directory)
	d2 := e.deck("alice", "Liked", 0)
	_, _ = e.db.Exec(`UPDATE hs_decks SET likes=4 WHERE id=?`, d2)
	e.must(200, "bob", "PUT", fmt.Sprintf("%s/featured-decks/%d", c, d2), "")
	dir := items(e.must(200, "", "GET", "?sort=prestige", ""))
	if len(dir) != 1 || dir[0].(map[string]any)["prestige"].(float64) != 5 {
		t.Fatalf("prestige: %v", dir)
	}
}

func TestClub_SuspendedIsReadOnly(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "outsider")
	c := e.mk("alice", "frozen-club", 2)
	e.addMember("alice", c, "bob", "player")
	e.must(201, "bob", "POST", c+"/feed", `{"body":"before"}`)
	if _, err := e.db.Exec(`UPDATE clubs SET suspended=1 WHERE id=?`, c); err != nil {
		t.Fatal(err)
	}
	e.must(404, "outsider", "GET", "by-slug/frozen-club", "")
	e.must(404, "outsider", "GET", c+"/members", "")
	e.must(200, "bob", "GET", c+"/feed", "")
	e.code(403, "club_suspended", "bob", "POST", c+"/feed", `{"body":"after"}`)
	e.code(403, "club_suspended", "alice", "POST", c+"/invites", `{}`)
	e.code(403, "club_suspended", "alice", "PATCH", c, `{"tagline":"x"}`)
	e.must(200, "bob", "POST", c+"/leave", "") // leaving stays allowed
	if n := len(items(e.must(200, "", "GET", "", ""))); n != 0 {
		t.Fatalf("suspended club listed: %d", n)
	}
}

func TestClub_ContextTokenAndNotIdentity(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "outsider")
	c := e.mk("alice", "token-club", 2)
	e.addMember("alice", c, "bob", "coach")
	e.must(403, "outsider", "POST", c+"/token", "")
	out := e.must(200, "bob", "POST", c+"/token", "")
	claims, err := jwt.Verify(e.keys, out["token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if claims["aud"] != "wotan-club" || claims["sub"] != "bob" || claims["club_id"] != c || claims["club_role"] != "coach" {
		t.Fatalf("claims: %v", claims)
	}
	if ttl := claims["exp"].(float64) - claims["iat"].(float64); ttl != 900 {
		t.Fatalf("ttl %v want 900", ttl)
	}
	ps := fmt.Sprint(claims["permissions"])
	if !strings.Contains(ps, "club.feed.moderate") || strings.Contains(ps, "club.settings.edit") {
		t.Fatalf("permissions for coach: %s", ps)
	}
	// the token is never accepted as WOTAN identity by clubs or HS
	if code, _ := e.doTok(out["token"].(string), "GET", "/api/v1/clubs/"+c+"/members", ""); code != 401 {
		t.Fatalf("club token accepted as identity by clubs api: %d", code)
	}
	if code, _ := e.doTok(out["token"].(string), "POST", "/api/v1/clubs/"+c+"/feed", `{"body":"x"}`); code != 401 {
		t.Fatalf("club token wrote to clubs api: %d", code)
	}
	req := httptest.NewRequest("POST", "/api/v1/hs/wall", strings.NewReader(`{"body":"x"}`))
	req.Header.Set("Authorization", "Bearer "+out["token"].(string))
	w := httptest.NewRecorder()
	e.hs.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("club token accepted by HS api: %d", w.Code)
	}
}

func TestClub_CreateValidationAndCaps(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "dave")
	e.must(422, "alice", "POST", "", `{"name":"x","slug":"valid-slug"}`)
	e.must(422, "alice", "POST", "", `{"name":"Valid Name","slug":"A_B"}`)
	e.must(422, "alice", "POST", "", `{"name":"Valid Name","slug":"ab"}`)
	e.code(422, "slug_reserved", "alice", "POST", "", `{"name":"Valid Name","slug":"admin"}`)
	e.code(422, "slug_reserved", "alice", "POST", "", `{"name":"Valid Name","slug":"by-slug"}`)
	e.code(422, "tier_forbidden", "alice", "POST", "", `{"name":"Valid Name","slug":"good-slug","privacy_tier":4}`)
	c := e.must(201, "alice", "POST", "", `{"name":"Valid Name","slug":"Good-Slug"}`)
	if c["slug"] != "good-slug" {
		t.Fatalf("slug not lowered: %v", c)
	}
	if me := e.must(200, "alice", "GET", c["id"].(string)+"/me", ""); me["club"].(map[string]any)["privacy_tier"].(float64) != 1 {
		t.Fatalf("default tier must be Secret: %v", me)
	}
	e.code(409, "slug_taken", "bob", "POST", "", `{"name":"Other Name","slug":"GOOD-SLUG"}`)

	// per-user creation cap comes from club_settings (default 5)
	if _, err := e.db.Exec(`INSERT INTO club_settings (key, value) VALUES ('clubs_per_user','2')`); err != nil {
		t.Fatal(err)
	}
	e.must(201, "alice", "POST", "", `{"name":"Second Club","slug":"second-club"}`)
	e.code(403, "club_limit", "alice", "POST", "", `{"name":"Third Club","slug":"third-club"}`)
	// deleting frees a slot
	e.must(200, "alice", "DELETE", c["id"].(string), "")
	e.must(201, "alice", "POST", "", `{"name":"Third Club","slug":"third-club"}`)

	// members cap
	if _, err := e.db.Exec(`INSERT INTO club_settings (key, value) VALUES ('members_per_club','2')`); err != nil {
		t.Fatal(err)
	}
	full := e.mk("bob", "tiny-club", 2)
	e.addMember("bob", full, "carol", "player")
	e.code(409, "club_full", "dave", "POST", "join", jstr(map[string]string{"token": e.invite("bob", full, "player")}))
}

func TestClub_PlatformAdminAPI(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol", "outsider")
	sec := e.mk("alice", "secret-admin", 1)
	e.addMember("alice", sec, "bob", "coach")
	e.addMember("alice", sec, "carol", "player")
	admin := e.token("op-1", "wotan.clubs.admin")
	mod := e.token("op-2", "wotan.clubs.moderate")
	plain := e.token("op-3", "iduna.admin")
	base := "/api/v1/clubs/admin/clubs"

	if code, _ := e.doTok("", "GET", base+"?reason=audit+check", ""); code != 401 {
		t.Fatalf("anon: %d", code)
	}
	if code, _ := e.doTok(plain, "GET", base+"?reason=audit+check", ""); code != 403 {
		t.Fatalf("no wotan perm: %d", code)
	}
	if code, _ := e.doTok(e.token("alice"), "GET", base+"?reason=audit+check", ""); code != 403 {
		t.Fatalf("club owner is not a platform admin: %d", code)
	}
	// every call requires a reason
	code, out := e.doTok(mod, "GET", base, "")
	if code != 422 || out["code"] != "reason_required" {
		t.Fatalf("reason: %d %v", code, out)
	}
	// moderators read (and see Secret clubs), never write
	code, out = e.doTok(mod, "GET", base+"?reason=routine+review", "")
	if code != 200 || len(items(out)) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, _ := e.doTok(mod, "POST", base+"/"+sec+"/suspend", `{"reason":"because"}`); code != 403 {
		t.Fatalf("moderate suspended: %d", code)
	}
	if code, _ := e.doTok(mod, "DELETE", base+"/"+sec+"?reason=because", ""); code != 403 {
		t.Fatalf("moderate deleted: %d", code)
	}
	code, out = e.doTok(mod, "GET", base+"/"+sec+"?reason=ticket+4411", "")
	if code != 200 || len(out["members"].([]any)) != 3 || out["tier"] != "secret" {
		t.Fatalf("get: %d %v", code, out)
	}
	if m := out["members"].([]any)[0].(map[string]any); len(m["effective_perms"].([]any)) == 0 {
		t.Fatalf("effective perms missing: %v", m)
	}
	// admin: reasonless write refused, then suspend
	if code, _ := e.doTok(admin, "POST", base+"/"+sec+"/suspend", `{}`); code != 422 {
		t.Fatalf("no reason: %d", code)
	}
	if code, _ := e.doTok(admin, "POST", base+"/"+sec+"/suspend", `{"reason":"abuse report 77"}`); code != 200 {
		t.Fatalf("suspend: %d", code)
	}
	e.code(403, "club_suspended", "bob", "POST", sec+"/feed", `{"body":"x"}`)
	e.must(200, "bob", "GET", sec+"/feed", "")
	if code, _ := e.doTok(admin, "POST", base+"/"+sec+"/unsuspend", `{"reason":"cleared"}`); code != 200 {
		t.Fatalf("unsuspend: %d", code)
	}
	e.must(201, "bob", "POST", sec+"/feed", `{"body":"back"}`)
	// transfer: promotes the target to manager so owner-is-manager holds
	if code, _ := e.doTok(admin, "POST", base+"/"+sec+"/transfer", `{"to_sub":"nobody-here","reason":"succession"}`); code != 404 {
		t.Fatalf("transfer to non-member: %d", code)
	}
	code, out = e.doTok(admin, "POST", base+"/"+sec+"/transfer", `{"to_sub":"carol","reason":"owner unreachable"}`)
	if code != 200 || out["promoted"] != true || e.roleOf(sec, "carol") != "manager" {
		t.Fatalf("transfer: %d %v", code, out)
	}
	e.must(200, "carol", "POST", sec+"/transfer", `{"to_sub":"alice"}`) // new owner can hand back
	// audit: platform actions carry platform_admin=1 and the reason; operators are masked in club views
	code, out = e.doTok(admin, "GET", base+"/"+sec+"/audit?reason=review", "")
	if code != 200 || !strings.Contains(jstr(out), "abuse report 77") || !strings.Contains(jstr(out), "admin.transfer") {
		t.Fatalf("admin audit: %d %v", code, out)
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM club_audit_log WHERE platform_admin=1 AND club_id=?`, sec).Scan(&n)
	if n != 5 {
		t.Fatalf("platform audit rows: %d", n)
	}
	cv := jstr(e.must(200, "alice", "GET", sec+"/audit", ""))
	if !strings.Contains(cv, "IDUNA operator") || strings.Contains(cv, "op-1") {
		t.Fatalf("club-facing audit should mask operators: %s", cv)
	}
	// delete (soft); a Secret club can be found and removed by operators
	if code, _ := e.doTok(admin, "DELETE", base+"/"+sec, `{"reason":"tos violation"}`); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	e.must(404, "alice", "GET", sec+"/me", "")
	if code, _ := e.doTok(admin, "DELETE", base+"/"+sec+"?reason=again+again", ""); code != 409 {
		t.Fatalf("double delete: %d", code)
	}
	code, out = e.doTok(admin, "GET", base+"?deleted=1&reason=find+it", "")
	if code != 200 || len(items(out)) != 1 {
		t.Fatalf("deleted listing: %d %v", code, out)
	}
}
