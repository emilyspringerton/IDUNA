package handlers_test

// S583: QR-code Back Office login. Real coverage: the full happy path (mint -> QR image -> poll
// pending -> approve -> poll delivers the session cookie exactly once -> a further poll reports
// consumed, never re-delivers the cookie), deny, expiry, and that /approve genuinely requires an
// authenticated iduna.admin session (the actual security boundary -- see admin_qr_login.go's own
// header comment for why the token itself must never be a bearer credential on its own).

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/util"
)

func newTestAdminQRLoginDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE admin_qr_login_codes (
		token         VARCHAR(64)  PRIMARY KEY,
		status        VARCHAR(20)  NOT NULL DEFAULT 'pending',
		agent_name    VARCHAR(200) NOT NULL DEFAULT '',
		session_token TEXT         NOT NULL DEFAULT '',
		created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at    DATETIME     NOT NULL,
		approved_at   DATETIME
	)`)
	if err != nil {
		t.Fatalf("create admin_qr_login_codes table: %v", err)
	}
	return db
}

func makeFullAgentToken(t *testing.T, keys *jwt.Keys, sub, agentName, agentType string, perms []string) string {
	t.Helper()
	token, err := jwt.Sign(keys, map[string]any{
		"sub":         sub,
		"agent_name":  agentName,
		"agent_type":  agentType,
		"permissions": perms,
		"iss":         "https://test.internal",
		"aud":         "farthq-ecosystem",
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

type qrLoginCreateOut struct {
	Token      string `json:"token"`
	ApproveURL string `json:"approve_url"`
	QRURL      string `json:"qr_url"`
	StatusURL  string `json:"status_url"`
}

func mintQRLoginCode(t *testing.T, h http.Handler) qrLoginCreateOut {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/api/codes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out qrLoginCreateOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return out
}

func pollQRLoginStatus(t *testing.T, h http.Handler, statusURL string) (int, map[string]any, *http.Response) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, statusURL, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, rec.Result()
}

func TestAdminQRLogin_FullApprovalRoundTrip(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestAdminQRLoginDB(t)
	publicH := &handlers.AdminQRLoginHandler{DB: db, BaseURL: "https://okemily.com"}
	approveH := middleware.RequireAuth(keys)(&handlers.AdminQRLoginApproveHandler{DB: db, Keys: keys, Issuer: "https://iam.test"})

	created := mintQRLoginCode(t, publicH)
	if created.Token == "" || created.StatusURL == "" || created.QRURL == "" {
		t.Fatalf("incomplete create response: %+v", created)
	}

	// The QR image itself must actually render a real PNG.
	imgReq := httptest.NewRequest(http.MethodGet, created.QRURL, nil)
	imgRec := httptest.NewRecorder()
	publicH.ServeHTTP(imgRec, imgReq)
	if imgRec.Code != http.StatusOK {
		t.Fatalf("qr image status = %d", imgRec.Code)
	}
	if ct := imgRec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("qr image content-type = %q", ct)
	}

	// Not yet approved.
	code, body, _ := pollQRLoginStatus(t, publicH, created.StatusURL)
	if code != http.StatusOK || body["status"] != "pending" {
		t.Fatalf("expected pending, got %d %+v", code, body)
	}

	// Approve, as a real authenticated iduna.admin agent session.
	approverToken := makeFullAgentToken(t, keys, "agent-emily", "EMILY", "human_proxy", []string{"iduna.admin"})
	approveReq := httptest.NewRequest(http.MethodPost, "/admin/login/qr/approve/"+created.Token, nil)
	approveReq.Header.Set("Authorization", "Bearer "+approverToken)
	approveRec := httptest.NewRecorder()
	approveH.ServeHTTP(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s", approveRec.Code, approveRec.Body.String())
	}

	// First poll after approval must deliver the real session cookie for the SAME agent.
	code, body, resp := pollQRLoginStatus(t, publicH, created.StatusURL)
	if code != http.StatusOK || body["status"] != "approved" {
		t.Fatalf("expected approved, got %d %+v", code, body)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "iduna_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected iduna_session cookie on the approved poll response")
	}
	claims, err := jwt.Verify(keys, sessionCookie.Value)
	if err != nil {
		t.Fatalf("minted session cookie does not verify: %v", err)
	}
	if claims["sub"] != "agent-emily" || claims["agent_name"] != "EMILY" {
		t.Fatalf("minted session claims don't match the approving agent: %+v", claims)
	}
	perms, _ := claims["permissions"].([]any)
	if len(perms) != 1 || perms[0] != "iduna.admin" {
		t.Fatalf("minted session did not carry the approving agent's permissions: %+v", claims["permissions"])
	}

	// A second poll must never re-deliver the cookie -- one-time use.
	code, body, resp2 := pollQRLoginStatus(t, publicH, created.StatusURL)
	if code != http.StatusOK || body["status"] != "consumed" {
		t.Fatalf("expected consumed on replay, got %d %+v", code, body)
	}
	for _, c := range resp2.Cookies() {
		if c.Name == "iduna_session" {
			t.Fatal("session cookie must not be re-delivered on a replayed poll")
		}
	}
}

func TestAdminQRLogin_ApproveRequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestAdminQRLoginDB(t)
	publicH := &handlers.AdminQRLoginHandler{DB: db, BaseURL: "https://okemily.com"}
	approveH := middleware.RequireAuth(keys)(&handlers.AdminQRLoginApproveHandler{DB: db, Keys: keys})

	created := mintQRLoginCode(t, publicH)

	req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/approve/"+created.Token, nil)
	rec := httptest.NewRecorder()
	approveH.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("approve with no auth: status = %d, want 401", rec.Code)
	}

	// The code must still be pending afterward -- an unauthenticated request must never move it.
	code, body, _ := pollQRLoginStatus(t, publicH, created.StatusURL)
	if code != http.StatusOK || body["status"] != "pending" {
		t.Fatalf("expected still pending after a rejected approve attempt, got %d %+v", code, body)
	}
}

func TestAdminQRLogin_Deny(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestAdminQRLoginDB(t)
	publicH := &handlers.AdminQRLoginHandler{DB: db, BaseURL: "https://okemily.com"}
	approveH := middleware.RequireAuth(keys)(&handlers.AdminQRLoginApproveHandler{DB: db, Keys: keys})
	created := mintQRLoginCode(t, publicH)

	approverToken := makeFullAgentToken(t, keys, "agent-emily", "EMILY", "human_proxy", []string{"iduna.admin"})
	form := url.Values{"deny": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/approve/"+created.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+approverToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	approveH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deny status = %d, body = %s", rec.Code, rec.Body.String())
	}

	code, body, resp := pollQRLoginStatus(t, publicH, created.StatusURL)
	if code != http.StatusOK || body["status"] != "denied" {
		t.Fatalf("expected denied, got %d %+v", code, body)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "iduna_session" {
			t.Fatal("a denied code must never deliver a session cookie")
		}
	}
}

func TestAdminQRLogin_ExpiredCodeCannotBeApproved(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestAdminQRLoginDB(t)
	approveH := middleware.RequireAuth(keys)(&handlers.AdminQRLoginApproveHandler{DB: db, Keys: keys})

	token := "expired-test-token"
	if _, err := db.Exec(`INSERT INTO admin_qr_login_codes (token, status, expires_at) VALUES (?, 'pending', ?)`,
		token, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("seed expired row: %v", err)
	}

	approverToken := makeFullAgentToken(t, keys, "agent-emily", "EMILY", "human_proxy", []string{"iduna.admin"})
	req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/approve/"+token, nil)
	req.Header.Set("Authorization", "Bearer "+approverToken)
	rec := httptest.NewRecorder()
	approveH.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("approving an expired code: status = %d, want 410", rec.Code)
	}
}

func TestAdminQRLogin_CreateIsRateLimited(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	_ = keys
	db := newTestAdminQRLoginDB(t)
	h := &handlers.AdminQRLoginHandler{DB: db, BaseURL: "https://okemily.com", StartLimiter: util.NewWindowRateLimiter(2, time.Minute)}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/api/codes", nil)
		req.RemoteAddr = "203.0.113.5:12345"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("request %d: status = %d, want 201", i, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login/qr/api/codes", nil)
	req.RemoteAddr = "203.0.113.5:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request from the same IP: status = %d, want 429", rec.Code)
	}
}
