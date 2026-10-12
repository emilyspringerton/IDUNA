package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
)

func decodeMap(b []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// ---- helpers --------------------------------------------------------------------------------------------

func (e *clubEnv) hsTok(tok, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1/hs/"+path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.hs.ServeHTTP(w, req)
	return w.Code, decodeMap(w.Body.Bytes())
}

func (e *clubEnv) hsDo(sub, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	return e.hsTok(e.tokenFor(sub), method, path, body)
}

func (e *clubEnv) hsMust(want int, sub, method, path, body string) map[string]any {
	e.t.Helper()
	code, out := e.hsDo(sub, method, path, body)
	if code != want {
		e.t.Fatalf("hs %s %s as %q: status %d want %d, body %v", method, path, sub, code, want, out)
	}
	return out
}

func (e *clubEnv) uploadToken(sub string) string {
	tok, err := jwt.Sign(e.keys, map[string]any{"sub": sub, "aud": "hs-live-upload", "iss": "iduna",
		"exp": time.Now().Add(time.Hour).Unix(), "permissions": []string{"hs.live.upload"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *clubEnv) seedHeroes() {
	for _, c := range [][3]string{{"1", "HERO_08", "MAGE"}, {"2", "HERO_06", "DRUID"}} {
		if _, err := e.db.Exec(`INSERT OR IGNORE INTO hs_cards (dbf_id,name,class,card_id) VALUES (?,?,?,?)`, c[0], c[1], c[2], c[1]); err != nil {
			e.t.Fatal(err)
		}
	}
}

// gameBody builds a tracker record; turns is the uniqueness knob (fingerprint), name is the uploader's tag.
func gameBody(turns, r1, r2 int, myName string) string {
	return jstr(map[string]any{
		"game_type": "GT_RANKED", "format": "FT_STANDARD", "player1": myName, "player2": "Foe#2",
		"result1": r1, "result2": r2, "turns": turns, "complete": 1, "me": 1,
		"entities": []map[string]any{
			{"id": 64, "player": 1, "card": "HERO_08", "card0": "HERO_08", "zone0": "PLAY"},
			{"id": 66, "player": 2, "card": "HERO_06", "card0": "HERO_06", "zone0": "PLAY"},
		},
		"timeline": [][]any{{"t", 1}},
	})
}

func (e *clubEnv) vis(id float64) (clubID, visibility string) {
	_ = e.db.QueryRow(`SELECT club_id, visibility FROM hs_games WHERE id=?`, int64(id)).Scan(&clubID, &visibility)
	return
}

// ---- sync target ----------------------------------------------------------------------------------------

func TestClubP2B_SyncTargetValidation(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice", "bob", "carol")
	c := e.mk("alice", "sync-club", 2)
	e.addMember("alice", c, "bob", "player")

	// default
	if out := e.hsMust(200, "bob", "GET", "me/sync-target", ""); out["kind"] != "personal" || out["valid"] != true {
		t.Fatalf("default: %v", out)
	}
	// a non-member cannot target the club
	e.hsMust(403, "carol", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	e.hsMust(403, "carol", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": "nope"}))
	e.hsMust(422, "bob", "PUT", "me/sync-target", `{"kind":"everyone"}`)
	e.hsMust(401, "", "PUT", "me/sync-target", `{"kind":"none"}`)
	// a member can
	e.hsMust(200, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	out := e.hsMust(200, "bob", "GET", "me/sync-target", "")
	if out["kind"] != "club" || out["club_id"] != c || out["club_name"] != "Club sync-club" || out["valid"] != true {
		t.Fatalf("club target: %v", out)
	}
	// upload-scoped token: display only, no club id, and nothing else
	up := e.uploadToken("bob")
	code, lt := e.hsTok(up, "GET", "live/sync-target", "")
	if code != 200 || lt["kind"] != "club" || lt["club_name"] != "Club sync-club" || lt["valid"] != true {
		t.Fatalf("live sync-target: %d %v", code, lt)
	}
	if _, has := lt["club_id"]; has {
		t.Fatalf("uplink view must not carry a club id: %v", lt)
	}
	if code, _ := e.hsTok(up, "GET", "me/sync-target", ""); code != 401 {
		t.Fatalf("upload token reached me/sync-target: %d", code)
	}
	if code, _ := e.hsTok(up, "PUT", "me/sync-target", `{"kind":"none"}`); code != 401 {
		t.Fatalf("upload token wrote a target: %d", code)
	}
	if code, _ := e.hsTok("", "GET", "live/sync-target", ""); code != 401 {
		t.Fatalf("anonymous live sync-target: %d", code)
	}
	// removed member: the stored target is now invalid and PUT is refused
	e.must(200, "alice", "DELETE", c+"/members/bob", "")
	out = e.hsMust(200, "bob", "GET", "me/sync-target", "")
	if out["valid"] != false || out["sync_target_invalid"] != true {
		t.Fatalf("removed member target still valid: %v", out)
	}
	e.hsMust(403, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	// re-join, then suspend the club
	e.addMember("alice", c, "bob", "player")
	e.hsMust(200, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	if _, err := e.db.Exec(`UPDATE clubs SET suspended=1 WHERE id=?`, c); err != nil {
		t.Fatal(err)
	}
	if out = e.hsMust(200, "bob", "GET", "me/sync-target", ""); out["valid"] != false {
		t.Fatalf("suspended club target valid: %v", out)
	}
	e.hsMust(409, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	// deleted club
	_, _ = e.db.Exec(`UPDATE clubs SET suspended=0, deleted=1 WHERE id=?`, c)
	if out = e.hsMust(200, "bob", "GET", "me/sync-target", ""); out["valid"] != false {
		t.Fatalf("deleted club target valid: %v", out)
	}
}

// ---- ingest routing + public leak ------------------------------------------------------------------------

func TestClubP2B_IngestRoutingAndPublicAggregateLeak(t *testing.T) {
	e := newClubEnv(t)
	e.seedHeroes()
	e.people("alice", "bob", "zed")
	c := e.mk("alice", "route-club", 1)
	e.addMember("alice", c, "bob", "player")

	// personal (default): current behaviour
	g1 := e.hsMust(201, "bob", "POST", "games", gameBody(10, 1, 2, "Bob#1111"))
	if cid, vis := e.vis(g1["id"].(float64)); cid != "" || vis != "personal" {
		t.Fatalf("personal routing: %q %q", cid, vis)
	}
	if _, bad := g1["sync_target_invalid"]; bad {
		t.Fatalf("unexpected invalid flag: %v", g1)
	}
	// club
	e.hsMust(200, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	g2 := e.hsMust(201, "bob", "POST", "games", gameBody(11, 1, 2, "Bob#1111"))
	if cid, vis := e.vis(g2["id"].(float64)); cid != c || vis != "club" {
		t.Fatalf("club routing: %q %q", cid, vis)
	}
	// a body that names a club is ignored (R9): no club_id field is ever read from the client
	body := strings.Replace(gameBody(12, 1, 2, "Bob#1111"), `{"complete"`, fmt.Sprintf(`{"club_id":%q,"complete"`, "other"), 1)
	g3 := e.hsMust(201, "bob", "POST", "games?club_id=other", body)
	if cid, _ := e.vis(g3["id"].(float64)); cid != c {
		t.Fatalf("client-named club honoured: %q", cid)
	}
	// none
	e.hsMust(200, "bob", "PUT", "me/sync-target", `{"kind":"none"}`)
	g4 := e.hsMust(201, "bob", "POST", "games", gameBody(13, 1, 2, "Bob#1111"))
	if cid, vis := e.vis(g4["id"].(float64)); cid != "" || vis != "private" {
		t.Fatalf("none routing: %q %q", cid, vis)
	}
	// removed from the club while the target still names it: private + flagged, never rerouted
	e.hsMust(200, "bob", "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": c}))
	e.must(200, "alice", "DELETE", c+"/members/bob", "")
	g5 := e.hsMust(201, "bob", "POST", "games", gameBody(14, 1, 2, "Bob#1111"))
	if cid, vis := e.vis(g5["id"].(float64)); cid != "" || vis != "private" || g5["sync_target_invalid"] != true {
		t.Fatalf("invalid-target routing: %q %q %v", cid, vis, g5)
	}
	// ...and the uplink-visible live state says so
	if st := e.hsMust(200, "bob", "GET", "live/state", ""); st["sync_target_invalid"] != true {
		t.Fatalf("live state missing sync_target_invalid: %v", st)
	}
	// suspended club
	e.addMember("alice", c, "bob", "player")
	_, _ = e.db.Exec(`UPDATE clubs SET suspended=1 WHERE id=?`, c)
	g6 := e.hsMust(201, "bob", "POST", "games", gameBody(15, 1, 2, "Bob#1111"))
	if cid, vis := e.vis(g6["id"].(float64)); cid != "" || vis != "private" {
		t.Fatalf("suspended routing: %q %q", cid, vis)
	}
	_, _ = e.db.Exec(`UPDATE clubs SET suspended=0 WHERE id=?`, c)

	// public aggregate: only the one personal game (g1) counts; club/private never leak
	m := e.hsMust(200, "", "GET", "stats/matchups", "")["matchups"].([]any)
	if len(m) != 1 || m[0].(map[string]any)["games"] != float64(1) {
		t.Fatalf("public matchups leaked non-personal games: %v", m)
	}
	// ?scope=mine is the owner's own and still sees every game
	mine := e.hsMust(200, "bob", "GET", "stats/matchups?scope=mine", "")["matchups"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["games"] != float64(6) {
		t.Fatalf("owner's own matchups: %v", mine)
	}
}

func TestClubP2B_DeckPoolsExcludeClubAndPrivate(t *testing.T) {
	e := newClubEnv(t)
	e.people("alice")
	deck := e.deck("alice", "Pool Deck", 0) // public deck, deckstring code-1
	code := "code-1"
	if _, err := e.db.Exec(`INSERT INTO hs_stat_shares (owner_sub, deck_code) VALUES ('alice', ?)`, code); err != nil {
		t.Fatal(err)
	}
	ins := func(i int, vis string) {
		if _, err := e.db.Exec(`INSERT INTO hs_games (owner_sub,fingerprint,me,result,complete,deck_code,visibility,club_id,record_json)
			VALUES ('alice',?,1,1,1,?,?,?,'{}')`, fmt.Sprintf("fp%d", i), code, vis, map[bool]string{true: "cl", false: ""}[vis == "club"]); err != nil {
			t.Fatal(err)
		}
	}
	ins(1, "personal")
	ins(2, "club")
	ins(3, "private")
	d := e.hsMust(200, "", "GET", fmt.Sprintf("decks/%d", deck), "")
	if d["games"] != float64(1) || d["wins"] != float64(1) {
		t.Fatalf("public deck pool counted club/private games: %v", d)
	}
	// the list endpoint carries the same columns
	l := e.hsMust(200, "", "GET", "decks", "")
	for _, x := range l["items"].([]any) {
		if g := x.(map[string]any)["games"]; g != float64(1) {
			t.Fatalf("deck list pool leaked: %v", x)
		}
	}
}

func TestClubP2B_RetargetOwnerOnly(t *testing.T) {
	e := newClubEnv(t)
	e.seedHeroes()
	e.people("alice", "bob", "carol")
	c := e.mk("alice", "retarget", 2)
	e.addMember("alice", c, "bob", "player")
	g := e.hsMust(201, "bob", "POST", "games", gameBody(20, 1, 2, "Bob#1"))
	id := fmt.Sprintf("%d", int(g["id"].(float64)))
	// manager of the club cannot pull/move a member's game
	e.hsMust(404, "alice", "PATCH", "games/"+id+"/target", `{"kind":"none"}`)
	e.hsMust(404, "carol", "PATCH", "games/"+id+"/target", `{"kind":"none"}`)
	// owner can move it into the club, then out
	e.hsMust(200, "bob", "PATCH", "games/"+id+"/target", jstr(map[string]any{"kind": "club", "club_id": c}))
	if cid, vis := e.vis(g["id"].(float64)); cid != c || vis != "club" {
		t.Fatalf("retarget club: %q %q", cid, vis)
	}
	e.hsMust(200, "bob", "PATCH", "games/"+id+"/target", `{"kind":"none"}`)
	if cid, vis := e.vis(g["id"].(float64)); cid != "" || vis != "private" {
		t.Fatalf("retarget none: %q %q", cid, vis)
	}
	// only onto targets they currently qualify for
	e.hsMust(403, "bob", "PATCH", "games/"+id+"/target", jstr(map[string]any{"kind": "club", "club_id": "other"}))
	e.must(200, "alice", "DELETE", c+"/members/bob", "")
	e.hsMust(403, "bob", "PATCH", "games/"+id+"/target", jstr(map[string]any{"kind": "club", "club_id": c}))
	e.hsMust(422, "bob", "PATCH", "games/"+id+"/target", `{"kind":"x"}`)
}

// ---- rank -----------------------------------------------------------------------------------------------

func TestClubP2B_RankVerification(t *testing.T) {
	e := newClubEnv(t)
	e.seedHeroes()
	e.people("alice", "bob")
	c := e.mk("alice", "rank-club", 3)
	e.addMember("alice", c, "bob", "player")

	e.hsMust(422, "bob", "PUT", "me/rank", `{"battletag":"not a tag"}`)
	e.hsMust(422, "bob", "PUT", "me/rank", `{"region":"MARS"}`)
	e.hsMust(422, "bob", "PUT", "me/rank", `{"legend_rank":-3}`)
	e.hsMust(401, "", "GET", "me/rank", "")
	// the client cannot set the server flags
	out := e.hsMust(200, "bob", "PUT", "me/rank", `{"battletag":"Bob#1234","region":"EU","legend_rank":777,"rank_verified":true,"battletag_verified":true}`)
	if out["rank_verified"] != false || out["battletag_verified"] != false || out["legend_rank"] != float64(777) {
		t.Fatalf("server flags client-controlled: %v", out)
	}
	// someone else's tag on the same account never verifies; the real one does once a game proves it
	e.hsMust(201, "bob", "POST", "games", gameBody(30, 1, 2, "Other#9999"))
	if out = e.hsMust(200, "bob", "GET", "me/rank", ""); out["battletag_verified"] != false {
		t.Fatalf("verified without a matching game: %v", out)
	}
	e.hsMust(201, "bob", "POST", "games", gameBody(31, 1, 2, "Bob#1234"))
	if out = e.hsMust(200, "bob", "GET", "me/rank", ""); out["battletag_verified"] != true || out["rank_verified"] != false {
		t.Fatalf("read refresh did not verify: %v", out)
	}
	// alice cannot ride bob's games: her own identical declaration stays unverified
	if out = e.hsMust(200, "alice", "PUT", "me/rank", `{"battletag":"Bob#1234"}`); out["battletag_verified"] != false {
		t.Fatalf("verified from someone else's games: %v", out)
	}
	// changing the tag re-computes on write
	if out = e.hsMust(200, "bob", "PUT", "me/rank", `{"battletag":"Bob#5555"}`); out["battletag_verified"] != false || out["legend_rank"] != float64(777) {
		t.Fatalf("tag change: %v", out)
	}
	e.hsMust(200, "bob", "PUT", "me/rank", `{"battletag":"Bob#1234"}`)

	// roster exposure: internal list has the tag, public roster never does
	mem := e.must(200, "alice", "GET", c+"/members", "")
	var found bool
	for _, x := range items(mem) {
		it := x.(map[string]any)
		if it["handle"] == "bob_hs" {
			found = true
			rk := it["rank"].(map[string]any)
			if it["battletag"] != "Bob#1234" || it["battletag_verified"] != true || rk["legend_rank"] != float64(777) ||
				rk["rank_verified"] != false || rk["battletag"] != "Bob#1234" || rk["battletag_verified"] != true {
				t.Fatalf("internal roster rank: %v", it)
			}
		}
	}
	if !found {
		t.Fatalf("bob missing from members: %v", mem)
	}
	pub := e.must(200, "", "GET", "by-slug/rank-club/roster", "")
	for _, x := range items(pub) {
		it := x.(map[string]any)
		if _, leak := it["battletag"]; leak {
			t.Fatalf("public roster leaked a BattleTag: %v", it)
		}
		if it["handle"] == "bob_hs" {
			rk := it["rank"].(map[string]any)
			if rk["legend_rank"] != float64(777) || rk["rank_verified"] != false {
				t.Fatalf("public rank: %v", rk)
			}
			if _, leak := rk["battletag"]; leak {
				t.Fatalf("public rank leaked a BattleTag: %v", rk)
			}
		}
	}
	// my/clubs carries the presentation flags the profile page needs
	mc := e.must(200, "bob", "GET", "me/clubs", "")
	it := items(mc)[0].(map[string]any)
	if _, ok := it["public_visible"]; !ok {
		t.Fatalf("me/clubs lacks public_visible: %v", it)
	}
	if _, ok := it["show_badge"]; !ok {
		t.Fatalf("me/clubs lacks show_badge: %v", it)
	}
}

// ---- analytics ------------------------------------------------------------------------------------------

func TestClubP2B_AnalyticsAndContributors(t *testing.T) {
	e := newClubEnv(t)
	e.seedHeroes()
	e.people("alice", "bob", "carol", "dave", "mallory")
	a := e.mk("alice", "an-a", 3) // Social
	b := e.mk("dave", "an-b", 1)  // Secret, separate tenant
	e.addMember("alice", a, "bob", "player")
	e.addMember("alice", a, "carol", "player")

	route := func(sub, club string) {
		e.hsMust(200, sub, "PUT", "me/sync-target", jstr(map[string]any{"kind": "club", "club_id": club}))
	}
	route("alice", a)
	route("bob", a)
	route("carol", a)
	route("dave", b)
	n := 100
	play := func(sub string, r1, r2 int) {
		n++
		e.hsMust(201, sub, "POST", "games", gameBody(n, r1, r2, strings.Title(sub)+"#1"))
	}
	play("bob", 1, 2)
	play("bob", 1, 2)
	play("bob", 2, 1)
	play("carol", 1, 2)
	play("alice", 2, 1)
	play("dave", 1, 2) // tenant B: must never show up in A
	play("dave", 1, 2)

	// A's matrix: 5 games, 3 wins 2 losses, MAGE vs DRUID
	mx := e.must(200, "bob", "GET", a+"/analytics/matchups?window=week", "")
	cell := mx["matrix"].(map[string]any)["MAGE"].(map[string]any)["DRUID"].(map[string]any)
	if cell["games"] != float64(5) || cell["wins"] != float64(3) || cell["losses"] != float64(2) || mx["min_sample"] != float64(5) {
		t.Fatalf("matrix: %v", mx)
	}
	// B's own matrix sees only B's two
	cellB := e.must(200, "dave", "GET", b+"/analytics/matchups", "")["matrix"].(map[string]any)["MAGE"].(map[string]any)["DRUID"].(map[string]any)
	if cellB["games"] != float64(2) {
		t.Fatalf("tenant B matrix: %v", cellB)
	}
	// cross-tenant: outsider on a Secret club is 404, an outsider on a Social club is 403; anon 401
	e.must(404, "alice", "GET", b+"/analytics/matchups", "")
	e.must(404, "alice", "GET", b+"/analytics/contributors", "")
	e.must(403, "mallory", "GET", a+"/analytics/matchups", "")
	e.must(403, "mallory", "GET", a+"/analytics/contributors", "")
	e.must(401, "", "GET", a+"/analytics/matchups", "")
	e.must(422, "bob", "GET", a+"/analytics/matchups?window=forever", "")
	// a club_id smuggled in the query is ignored
	cellQ := e.must(200, "dave", "GET", b+"/analytics/matchups?club_id="+a, "")["matrix"].(map[string]any)["MAGE"].(map[string]any)["DRUID"].(map[string]any)
	if cellQ["games"] != float64(2) {
		t.Fatalf("query club_id honoured: %v", cellQ)
	}
	// old games fall outside the week window but stay in all
	if _, err := e.db.Exec(`UPDATE hs_games SET created_at=datetime('now','-20 days') WHERE owner_sub='carol'`); err != nil {
		t.Fatal(err)
	}
	wk := e.must(200, "bob", "GET", a+"/analytics/matchups?window=week", "")["matrix"].(map[string]any)["MAGE"].(map[string]any)["DRUID"].(map[string]any)
	mo := e.must(200, "bob", "GET", a+"/analytics/matchups?window=month", "")["matrix"].(map[string]any)["MAGE"].(map[string]any)["DRUID"].(map[string]any)
	if wk["games"] != float64(4) || mo["games"] != float64(5) {
		t.Fatalf("windows: week=%v month=%v", wk, mo)
	}

	// internal contributors board
	cb := items(e.must(200, "bob", "GET", a+"/analytics/contributors?window=all", ""))
	if len(cb) != 3 {
		t.Fatalf("contributors: %v", cb)
	}
	top := cb[0].(map[string]any)
	if top["handle"] != "bob_hs" || top["wins"] != float64(2) || top["losses"] != float64(1) || top["games"] != float64(3) {
		t.Fatalf("top contributor: %v", top)
	}
	// carol leaves: her rows are retained but de-identified
	e.must(200, "carol", "POST", a+"/leave", "")
	cb = items(e.must(200, "bob", "GET", a+"/analytics/contributors?window=all", ""))
	var former, sawCarol bool
	for _, x := range cb {
		it := x.(map[string]any)
		if it["handle"] == "carol_hs" {
			sawCarol = true
		}
		if it["former_member"] == true && it["handle"] == "" && it["games"] == float64(1) {
			former = true
		}
	}
	if sawCarol || !former {
		t.Fatalf("former member not de-identified: %v", cb)
	}

	// public board: Social + toggle off => forbidden; on => only public_visible current members
	e.must(403, "", "GET", "by-slug/an-a/contributors", "")
	e.must(200, "alice", "PUT", a+"/visibility", `{"show_contributors":true}`)
	pub := items(e.must(200, "", "GET", "by-slug/an-a/contributors?window=all", ""))
	if len(pub) != 2 {
		t.Fatalf("public board (alice+bob, no former member): %v", pub)
	}
	for _, x := range pub {
		it := x.(map[string]any)
		if it["handle"] == "" || it["former_member"] == true {
			t.Fatalf("public board leaked a de-identified row: %v", it)
		}
	}
	e.must(200, "bob", "PATCH", a+"/members/me", `{"public_visible":false}`)
	pub = items(e.must(200, "", "GET", "by-slug/an-a/contributors?window=all", ""))
	if len(pub) != 1 || pub[0].(map[string]any)["handle"] != "alice_hs" {
		t.Fatalf("opted-out member still public: %v", pub)
	}
	// Secret clubs are 404 to outsiders, Private clubs are never public
	e.must(404, "", "GET", "by-slug/an-b/contributors", "")
	p := e.mk("mallory", "an-p", 2)
	e.must(200, "mallory", "PUT", p+"/visibility", `{"show_contributors":true}`)
	e.must(403, "", "GET", "by-slug/an-p/contributors", "")
}

var _ = http.StatusOK

func TestClubP2B_UplinkScriptShowsTargetButNeverSendsClub(t *testing.T) {
	e := newClubEnv(t)
	req := httptest.NewRequest("GET", "/api/v1/hs/live/uplink.ps1", nil)
	w := httptest.NewRecorder()
	e.hs.ServeHTTP(w, req)
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, "/api/v1/hs/live/sync-target") || !strings.Contains(b, "Syncing to:") {
		t.Fatalf("uplink does not display the sync target: %d", w.Code)
	}
	if strings.Contains(strings.ToLower(b), "club_id") || strings.Contains(strings.ToLower(b), "clubid") {
		t.Fatalf("uplink script must never send a club identifier (R9)")
	}
}
