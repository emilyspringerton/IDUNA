package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth"
	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/honorcode"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

// honorEnv runs the /api/v1 honor-code routes exactly as main.go mounts them (behind RequireAuth), over the REAL
// migrations (newGameDB) for players + player_honor_code and the ceremony's stub store for users rows.
type honorEnv struct {
	keys *authjwt.Keys
	db   *sql.DB
	iam  *stubCeremonyStore
	mux  *http.ServeMux
}

func newHonorEnv(t *testing.T) *honorEnv {
	t.Helper()
	keys, err := authjwt.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	e := &honorEnv{keys: keys, db: newGameDB(t), iam: newStubCeremonyStore(), mux: http.NewServeMux()}
	h := &handlers.HonorCodeAPIHandler{IAM: e.iam, DB: e.db}
	e.mux.Handle("/api/v1/honor-code/accept", middleware.RequireAuth(keys)(http.HandlerFunc(h.HandleAccept)))
	e.mux.Handle("/api/v1/me/honor-code", middleware.RequireAuth(keys)(http.HandlerFunc(h.HandleMe)))
	return e
}

func (e *honorEnv) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	tok, err := authjwt.Sign(e.keys, claims)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// ssoToken has the shape player_email_auth.go's issueJWT mints for a WOTAN sign-in: sub = the players row.
func (e *honorEnv) ssoToken(t *testing.T, playerID string) string {
	return e.token(t, map[string]any{"sub": playerID, "display_name": "Sigrun", "email": "sigrun@example.test",
		"permissions": []string{}, "aud": "shankpit"})
}

func (e *honorEnv) addPlayer(t *testing.T, playerID string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO players (player_id, display_name, provider, provider_sub) VALUES (?, 'Sigrun', 'iduna_local', ?)`,
		playerID, "sub-"+playerID); err != nil {
		t.Fatal(err)
	}
}

func (e *honorEnv) do(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	raw := []byte{}
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func (e *honorEnv) seals(t *testing.T, playerID string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM player_honor_code WHERE player_id = ?`, playerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var sealBody = map[string]string{"sha256": honorcode.CurrentSHA256}

// The bug this file exists for: a WOTAN visitor's SSO JWT names a players row, and the ceremony's users-row UPDATE
// answered {ok:true} while recording nothing. Here the seal must land, be readable, and survive.
func TestHonorCodeAPI_SSOPlayerSealIsRecorded(t *testing.T) {
	e := newHonorEnv(t)
	pid := "7d1c2a9e-0000-4000-8000-000000000001"
	e.addPlayer(t, pid)
	tok := e.ssoToken(t, pid)

	code, m := e.do(t, "GET", "/api/v1/me/honor-code", tok, nil)
	if code != http.StatusOK || m["sealed"] != nil || m["honor_code"].(map[string]any)["required"] != true {
		t.Fatalf("before sealing: %d %v", code, m)
	}
	code, m = e.do(t, "POST", "/api/v1/honor-code/accept", tok, sealBody)
	if code != http.StatusOK || m["ok"] != true {
		t.Fatalf("seal: %d %v", code, m)
	}
	sealed, _ := m["sealed"].(map[string]any)
	if sealed["sha256"] != honorcode.CurrentSHA256 || sealed["version"] != float64(honorcode.CurrentVersion) || sealed["accepted_at"] == nil {
		t.Errorf("sealed = %v", sealed)
	}
	if n := e.seals(t, pid); n != 1 {
		t.Fatalf("player_honor_code rows = %d, want 1", n)
	}
	code, m = e.do(t, "GET", "/api/v1/me/honor-code", tok, nil)
	if code != http.StatusOK || m["sealed"] == nil || m["honor_code"].(map[string]any)["required"] != false {
		t.Fatalf("after sealing: %d %v", code, m)
	}
	if m["honor_code"].(map[string]any)["body_markdown"] != honorcode.CurrentText {
		t.Error("me/honor-code must carry the current text")
	}
}

// "This cannot be unmade": sealing again is idempotent and the first seal's time stands.
func TestHonorCodeAPI_FirstSealStands(t *testing.T) {
	e := newHonorEnv(t)
	pid := "7d1c2a9e-0000-4000-8000-000000000002"
	e.addPlayer(t, pid)
	if _, err := e.db.Exec(`INSERT INTO player_honor_code (player_id, version, sha256, accepted_at) VALUES (?, ?, ?, '2026-01-01T00:00:00Z')`,
		pid, honorcode.CurrentVersion, honorcode.CurrentSHA256); err != nil {
		t.Fatal(err)
	}
	code, m := e.do(t, "POST", "/api/v1/honor-code/accept", e.ssoToken(t, pid), sealBody)
	if code != http.StatusOK {
		t.Fatalf("re-seal: %d %v", code, m)
	}
	if got := m["sealed"].(map[string]any)["accepted_at"]; got != "2026-01-01T00:00:00Z" {
		t.Errorf("accepted_at = %v, want the first seal's 2026-01-01T00:00:00Z", got)
	}
	if n := e.seals(t, pid); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestHonorCodeAPI_StaleShaIsRefusedWithTheCurrentCode(t *testing.T) {
	e := newHonorEnv(t)
	pid := "7d1c2a9e-0000-4000-8000-000000000003"
	e.addPlayer(t, pid)
	code, m := e.do(t, "POST", "/api/v1/honor-code/accept", e.ssoToken(t, pid), map[string]string{"sha256": "deadbeef"})
	if code != http.StatusForbidden || m["code"] != "HONOR_CODE_STALE" {
		t.Fatalf("stale: %d %v", code, m)
	}
	honor := m["honor_code"].(map[string]any)
	if honor["sha256"] != honorcode.CurrentSHA256 || honor["required"] != true || honor["body_markdown"] != honorcode.CurrentText {
		t.Errorf("stale answer must carry the current code, got %v", honor)
	}
	if n := e.seals(t, pid); n != 0 {
		t.Errorf("a stale seal recorded %d rows", n)
	}
}

func TestHonorCodeAPI_RefusesNonPersonsAndNeverFakesOK(t *testing.T) {
	e := newHonorEnv(t)
	pid := "7d1c2a9e-0000-4000-8000-000000000004"
	e.addPlayer(t, pid)
	off := "7d1c2a9e-0000-4000-8000-000000000005"
	e.addPlayer(t, off)
	if _, err := e.db.Exec(`UPDATE players SET disabled_at = CURRENT_TIMESTAMP WHERE player_id = ?`, off); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"no token", "", http.StatusUnauthorized, ""},
		{"agent", e.token(t, map[string]any{"sub": "agent-x", "agent_name": "X", "aud": "farthq-ecosystem"}), http.StatusForbidden, "AGENT_TOKEN"},
		{"game token", e.token(t, map[string]any{"sub": "guest:" + pid, "player_id": pid, "game": "deadweight", "aud": "farthq-ecosystem"}), http.StatusForbidden, "GAME_TOKEN"},
		{"unknown subject", e.ssoToken(t, "7d1c2a9e-0000-4000-8000-00000000dead"), http.StatusNotFound, "NOT_FOUND"},
		{"disabled player", e.ssoToken(t, off), http.StatusForbidden, "IDENTITY_SUSPENDED"},
	}
	for _, tc := range cases {
		for _, path := range []string{"/api/v1/honor-code/accept", "/api/v1/me/honor-code"} {
			method := "GET"
			if path == "/api/v1/honor-code/accept" {
				method = "POST"
			}
			code, m := e.do(t, method, path, tc.token, sealBody)
			if code != tc.status || (tc.code != "" && m["code"] != tc.code) || m["ok"] == true {
				t.Errorf("%s %s: got %d %v, want %d %s", tc.name, path, code, m, tc.status, tc.code)
			}
		}
	}
	if n := e.seals(t, off) + e.seals(t, pid); n != 0 {
		t.Errorf("refused callers recorded %d rows", n)
	}
	tok := e.ssoToken(t, pid)
	if code, _ := e.do(t, "GET", "/api/v1/honor-code/accept", tok, nil); code != http.StatusNotFound {
		t.Errorf("GET accept: %d, want 404", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/me/honor-code", tok, sealBody); code != http.StatusNotFound {
		t.Errorf("POST me/honor-code: %d, want 404", code)
	}
}

// The web ceremony's own identities (users rows) still seal through the ceremony's store call.
func TestHonorCodeAPI_CeremonyUserSealsOnUsersRow(t *testing.T) {
	e := newHonorEnv(t)
	e.iam.users["u-1"] = &auth.User{IDString: "u-1", Email: "u1@example.test"}
	tok := e.token(t, map[string]any{"sub": "u-1", "aud": "farthq-ecosystem"})
	code, m := e.do(t, "POST", "/api/v1/honor-code/accept", tok, sealBody)
	if code != http.StatusOK || m["ok"] != true {
		t.Fatalf("seal: %d %v", code, m)
	}
	if u := e.iam.users["u-1"]; !u.HonorAccepted || u.HonorCurrentVer != honorcode.CurrentVersion || u.HonorCurrentSHA != honorcode.CurrentSHA256 {
		t.Errorf("users row not sealed: %+v", u)
	}
	code, m = e.do(t, "GET", "/api/v1/me/honor-code", tok, nil)
	if code != http.StatusOK || m["honor_code"].(map[string]any)["required"] != false || m["sealed"].(map[string]any)["sha256"] != honorcode.CurrentSHA256 {
		t.Fatalf("me: %d %v", code, m)
	}
}
