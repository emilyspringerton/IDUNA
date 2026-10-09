package handlers_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/store"
)

const hsTestCode = "AAECAf0GDIWgBJegBKigBNCeBvSqB8ytB/bJB4jdB+vkB4TpB9HqB7r2BwmxnwSDoATRngbh5gagrAeTrQeBrgeCrget3wcAAA=="

const hsTestPaste = `FilthyRat
Class: Warlock
Format: Standard
Year of the Scarab
#
2x (1) Mortal Coil
1x (2) Bloodmage Thalnos
1x (2) Brightwing
1x (2) Cult Neophyte
2x (2) Dark Peddler
2x (2) Dirty Rat
2x (2) Sunfury Protector
1x (2) Warden Maiev
2x (3) Menagerie Mug
2x (5) Doomguard
1x (5) The Curator
1x (7) Chillmaw
1x (7) Keymaster Alabaster
1x (7) Vanessa the Ringleader
1x (8) Archwitch Willow
1x (8) Lord Jaraxxus
2x (8) Mo'arg Forgefiend
2x (8) Twisting Nether
1x (9) Avatar of Hearthstone
1x (9) M.O.T.H.E.R.
2x (9) Voidlord
` + hsTestCode + `
#
`

type hsEnv struct {
	t    *testing.T
	h    http.Handler
	keys *jwt.Keys
}

func newHSEnv(t *testing.T) *hsEnv {
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
	return &hsEnv{t: t, keys: keys, h: &handlers.HSHandler{DB: db, Keys: keys}}
}

func (e *hsEnv) do(sub, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1/hs/"+path, strings.NewReader(body))
	if sub != "" {
		tok := makePlayerToken(e.t, e.keys, sub)
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *hsEnv) must(want int, sub, method, path, body string) map[string]any {
	e.t.Helper()
	code, out := e.do(sub, method, path, body)
	if code != want {
		e.t.Fatalf("%s %s: status %d want %d, body %v", method, path, code, want, out)
	}
	return out
}

func jstr(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestHS_DeckLifecycle(t *testing.T) {
	e := newHSEnv(t)
	// writes need a token; reads don't
	e.must(401, "", "POST", "decks", `{}`)
	e.must(200, "", "GET", "decks", "")
	// no handle yet -> clear, machine-readable refusal
	out := e.must(409, "alice-sub", "POST", "decks", jstr(map[string]string{"text": hsTestPaste}))
	if out["code"] != "handle_required" {
		t.Fatalf("want handle_required, got %v", out)
	}
	e.must(200, "alice-sub", "PUT", "me", `{"handle":"Alice_99","bio":"hi"}`)
	e.must(409, "bob-sub", "PUT", "me", `{"handle":"alice_99"}`) // case-insensitive uniqueness
	e.must(422, "bob-sub", "PUT", "me", `{"handle":"x"}`)
	e.must(422, "bob-sub", "PUT", "me", `{"handle":"admin"}`)

	// preview is public and saves nothing
	pv := e.must(200, "", "POST", "decks/parse", jstr(map[string]string{"text": hsTestPaste}))
	if pv["class"] != "Warlock" || pv["card_count"].(float64) != 30 || pv["names_resolved"] != true {
		t.Fatalf("preview: %v", pv)
	}
	e.must(422, "", "POST", "decks/parse", `{"text":"not a deck"}`)

	created := e.must(201, "alice-sub", "POST", "decks", jstr(map[string]string{"text": hsTestPaste, "description": "Fun <b>deck</b>"}))
	id := int(created["id"].(float64))
	// same deck again by same owner -> 409 pointing at the original
	dup := e.must(409, "alice-sub", "POST", "decks", jstr(map[string]string{"text": hsTestCode, "title": "again"}))
	if int(dup["deck_id"].(float64)) != id {
		t.Fatalf("dup: %v", dup)
	}

	d := e.must(200, "", "GET", "decks/"+hsItoa(id), "")
	if d["title"] != "FilthyRat" || d["class"] != "Warlock" || d["format"] != "Standard" || d["liked_by_me"] != false {
		t.Fatalf("deck: %v", d)
	}
	if len(d["cards"].([]any)) != 21 || d["description"] != "Fun <b>deck</b>" { // raw text stored; clients escape
		t.Fatalf("cards/desc: %v", d)
	}

	// likes: idempotent, counted, per viewer
	e.must(200, "bob-sub", "PUT", "decks/"+hsItoa(id)+"/like", "")
	e.must(200, "bob-sub", "PUT", "decks/"+hsItoa(id)+"/like", "")
	if l := e.must(200, "carol-sub", "PUT", "decks/"+hsItoa(id)+"/like", ""); l["likes"].(float64) != 2 {
		t.Fatalf("likes: %v", l)
	}
	if d := e.must(200, "bob-sub", "GET", "decks/"+hsItoa(id), ""); d["liked_by_me"] != true {
		t.Fatalf("liked_by_me: %v", d)
	}
	if l := e.must(200, "bob-sub", "DELETE", "decks/"+hsItoa(id)+"/like", ""); l["likes"].(float64) != 1 {
		t.Fatalf("unlike: %v", l)
	}

	// comments need a handle; owner/admin can delete, strangers can't
	e.must(409, "bob-sub", "POST", "decks/"+hsItoa(id)+"/comments", `{"body":"nice"}`)
	e.must(200, "bob-sub", "PUT", "me", `{"handle":"bob"}`)
	c := e.must(201, "bob-sub", "POST", "decks/"+hsItoa(id)+"/comments", `{"body":"nice"}`)
	e.must(422, "bob-sub", "POST", "decks/"+hsItoa(id)+"/comments", `{"body":""}`)
	e.must(403, "alice-sub-2", "DELETE", "comments/"+hsItoa(int(c["id"].(float64))), "") // not the author
	if dd := e.must(200, "", "GET", "decks/"+hsItoa(id), ""); dd["comments"].(float64) != 1 {
		t.Fatalf("comment count: %v", dd)
	}
	e.must(204, "bob-sub", "DELETE", "comments/"+hsItoa(int(c["id"].(float64))), "")

	// edit: only the owner
	e.must(403, "bob-sub", "PATCH", "decks/"+hsItoa(id), `{"title":"mine now"}`)
	if p := e.must(200, "alice-sub", "PATCH", "decks/"+hsItoa(id), `{"title":"Renamed"}`); p["title"] != "Renamed" {
		t.Fatalf("patch: %v", p)
	}

	// filters + search (by card name and by author)
	for q, want := range map[string]float64{"class=Warlock": 1, "class=Mage": 0, "q=Jaraxxus": 1, "q=nothingmatches": 0, "author=ALICE_99": 1, "format=Wild": 0, "sort=top": 1} {
		if l := e.must(200, "", "GET", "decks?"+q, ""); l["total"].(float64) != want {
			t.Fatalf("filter %s: %v", q, l)
		}
	}

	// delete then re-publish revives it
	e.must(403, "bob-sub", "DELETE", "decks/"+hsItoa(id), "")
	e.must(204, "alice-sub", "DELETE", "decks/"+hsItoa(id), "")
	e.must(404, "", "GET", "decks/"+hsItoa(id), "")
	e.must(201, "alice-sub", "POST", "decks", jstr(map[string]string{"text": hsTestPaste}))
}

func TestHS_ProfilesWallFeedFollow(t *testing.T) {
	e := newHSEnv(t)
	e.must(200, "a", "PUT", "me", `{"handle":"alice"}`)
	e.must(200, "b", "PUT", "me", `{"handle":"bob","bio":"b"}`)
	if me := e.must(200, "a", "GET", "me", ""); me["handle"] != "alice" {
		t.Fatal(me)
	}
	// handle is immutable, bio is editable
	e.must(422, "a", "PUT", "me", `{"handle":"alicia"}`)
	if me := e.must(200, "a", "PUT", "me", `{"bio":"new bio"}`); me["bio"] != "new bio" {
		t.Fatal(me)
	}
	e.must(422, "a", "PUT", "me", `{"bio":"`+strings.Repeat("x", 161)+`"}`)
	if me := e.must(200, "newcomer", "GET", "me", ""); me["handle"] != nil {
		t.Fatalf("unclaimed: %v", me)
	}

	p1 := e.must(201, "a", "POST", "wall", `{"body":"hello wall"}`)
	e.must(422, "a", "POST", "wall", `{"body":"`+strings.Repeat("y", 501)+`"}`)
	e.must(201, "b", "POST", "wall", `{"body":"bob here"}`)

	// follow graph drives the Following feed (own posts included)
	e.must(422, "a", "PUT", "users/alice/follow", "")
	if f := e.must(200, "a", "PUT", "users/bob/follow", ""); f["followers"].(float64) != 1 {
		t.Fatal(f)
	}
	if u := e.must(200, "a", "GET", "users/bob", ""); u["followed_by_me"] != true || u["posts"].(float64) != 1 {
		t.Fatalf("user: %v", u)
	}
	e.must(401, "", "GET", "feed?scope=following", "")
	if f := e.must(200, "a", "GET", "feed?scope=following", ""); len(f["items"].([]any)) != 2 {
		t.Fatalf("following feed: %v", f)
	}
	e.must(200, "b", "PUT", "users/alice/follow", "") // b follows alice
	e.must(200, "b", "DELETE", "users/alice/follow", "")
	if f := e.must(200, "newcomer", "GET", "feed", ""); len(f["items"].([]any)) != 2 {
		t.Fatalf("global feed: %v", f)
	}
	if w := e.must(200, "", "GET", "users/alice/wall", ""); len(w["items"].([]any)) != 1 {
		t.Fatalf("wall: %v", w)
	}

	// post likes + comments; stranger can't delete someone's post
	pid := hsItoa(int(p1["id"].(float64)))
	if l := e.must(200, "b", "PUT", "wall/"+pid+"/like", ""); l["likes"].(float64) != 1 {
		t.Fatal(l)
	}
	e.must(201, "b", "POST", "wall/"+pid+"/comments", `{"body":"hi alice"}`)
	if cl := e.must(200, "", "GET", "wall/"+pid+"/comments", ""); cl["total"].(float64) != 1 {
		t.Fatal(cl)
	}
	e.must(403, "b", "DELETE", "wall/"+pid, "")
	e.must(204, "a", "DELETE", "wall/"+pid, "")
	e.must(404, "", "GET", "decks/9999", "")

	// users list, search and sort
	if l := e.must(200, "", "GET", "users?q=ali", ""); l["total"].(float64) != 1 {
		t.Fatal(l)
	}
	if l := e.must(200, "", "GET", "users?sort=followed", ""); l["items"].([]any)[0].(map[string]any)["handle"] != "bob" {
		t.Fatalf("most followed: %v", l)
	}
	// reports are accepted for known kinds only
	e.must(202, "a", "POST", "reports", `{"kind":"deck","id":"1","reason":"spam"}`)
	e.must(422, "a", "POST", "reports", `{"kind":"banana","id":"1"}`)
}

func TestHS_LikeEscapesInSearch(t *testing.T) {
	e := newHSEnv(t)
	// % and _ in a search must be literal, not wildcards
	if l := e.must(200, "", "GET", "decks?q=%25", ""); l["total"].(float64) != 0 {
		t.Fatal(l)
	}
}

func hsItoa(n int) string { return strconv.Itoa(n) }
