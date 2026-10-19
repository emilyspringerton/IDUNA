package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/coder"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func coderServer(t *testing.T) (http.Handler, *jwt.Keys) {
	t.Helper()
	keys, _ := jwt.GenerateKeys()
	st, err := coder.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return middleware.RequireAuth(keys)(&handlers.CoderHandler{Store: st, Prefix: "/api/v1/coder"}), keys
}

func coderDo(h http.Handler, tok, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCoderAPI_FineGrainedIAM(t *testing.T) {
	h, keys := coderServer(t)
	none := makeAgentToken(t, keys, "nobody", []string{})
	alice := makeAgentToken(t, keys, "alice", []string{coder.PermSessionCreate, coder.PermSessionRead, coder.PermChatSend, coder.ToolPerm("grep")})
	bob := makeAgentToken(t, keys, "bob", []string{coder.PermSessionCreate, coder.PermSessionRead, coder.PermChatSend})
	worker := makeAgentToken(t, keys, "coder-worker", []string{coder.PermWorkerClaim, coder.PermWorkerReport})

	// every route denies a token with no permissions
	for _, c := range [][2]string{{"POST", "/sessions"}, {"GET", "/sessions"}, {"GET", "/tools"}, {"GET", "/lessons"}, {"POST", "/lessons"}, {"POST", "/work/claim"}} {
		if w := coderDo(h, none, c[0], "/api/v1/coder"+c[1], `{}`); w.Code != 403 {
			t.Errorf("%s %s: want 403 got %d", c[0], c[1], w.Code)
		}
	}
	// alice creates a session and sends a turn; bob can't read or post into it
	w := coderDo(h, alice, "POST", "/api/v1/coder/sessions", `{"title":"t"}`)
	var s struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 201 || s.ID == "" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := coderDo(h, alice, "POST", "/api/v1/coder/sessions/"+s.ID+"/messages", `{"content":"grep TODO"}`); w.Code != 202 {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if w := coderDo(h, bob, "GET", "/api/v1/coder/sessions/"+s.ID+"/messages", ""); w.Code != 403 {
		t.Errorf("bob reading alice's session: want 403 got %d", w.Code)
	}
	if w := coderDo(h, bob, "POST", "/api/v1/coder/sessions/"+s.ID+"/messages", `{"content":"x"}`); w.Code != 403 {
		t.Errorf("bob posting into alice's session: want 403 got %d", w.Code)
	}
	// users cannot claim work; the worker can, and tool events obey the sender's tool grants
	if w := coderDo(h, alice, "POST", "/api/v1/coder/work/claim", `{}`); w.Code != 403 {
		t.Errorf("alice claim: want 403 got %d", w.Code)
	}
	w = coderDo(h, worker, "POST", "/api/v1/coder/work/claim", `{}`)
	var wk struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &wk)
	if w.Code != 200 || wk.ID == 0 {
		t.Fatalf("claim: %d %s", w.Code, w.Body)
	}
	id := strconv.FormatInt(wk.ID, 10)
	if w := coderDo(h, worker, "POST", "/api/v1/coder/work/"+id+"/events", `{"role":"tool","tool":"grep","content":"a:1:TODO"}`); w.Code != 201 {
		t.Errorf("granted tool: want 201 got %d %s", w.Code, w.Body)
	}
	if w := coderDo(h, worker, "POST", "/api/v1/coder/work/"+id+"/events", `{"role":"tool","tool":"sed","content":"edit"}`); w.Code != 403 {
		t.Errorf("ungranted tool: want 403 got %d", w.Code)
	}
}

func TestCoderAPI_WorkerProvisionNeedsItsOwnPermission(t *testing.T) {
	h, keys := coderServer(t)
	// chat permissions do not let you mint worker agents
	user := makeAgentToken(t, keys, "alice", []string{coder.PermChatSend, coder.PermSessionCreate})
	if w := coderDo(h, user, "POST", "/api/v1/coder/workers", `{}`); w.Code != 403 {
		t.Errorf("want 403 got %d", w.Code)
	}
	// a worker identity cannot mint more workers either
	wk := makeAgentToken(t, keys, "w", []string{coder.PermWorkerClaim, coder.PermWorkerReport})
	if w := coderDo(h, wk, "POST", "/api/v1/coder/workers", `{}`); w.Code != 403 {
		t.Errorf("worker minting workers: want 403 got %d", w.Code)
	}
	// the permission holder reaches the handler (501 here only because this test server has no IAM store)
	prov := makeAgentToken(t, keys, "ops", []string{coder.PermWorkerProvision})
	if w := coderDo(h, prov, "POST", "/api/v1/coder/workers", `{}`); w.Code != 501 {
		t.Errorf("want 501 got %d", w.Code)
	}
	// and a worker cannot chat
	if w := coderDo(h, wk, "POST", "/api/v1/coder/sessions", `{}`); w.Code != 403 {
		t.Errorf("worker creating sessions: want 403 got %d", w.Code)
	}
}
