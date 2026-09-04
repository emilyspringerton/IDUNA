package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
)

func doRegistration(h *handlers.GfdRegistrationHandler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestRegistration_DefaultModeIsOpen confirms the migration's own INSERT OR IGNORE seed row
// (mode='open') is what a fresh install actually gets -- registration must never silently start
// in waitlist mode.
func TestRegistration_DefaultModeIsOpen(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	h := &handlers.GfdRegistrationHandler{DB: db}

	w := doRegistration(h, http.MethodGet, "/admin/gfd-registration/api/mode", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mode"] != "open" {
		t.Fatalf("expected default mode 'open', got %q", resp["mode"])
	}
}

// TestRegistration_WaitlistMode_RegisterDoesNotCreateRealAccount is the real, load-bearing
// behavior this whole feature exists for: flipping the toggle must actually stop
// PlayerEmailAuthHandler.handleRegister from creating a real players/player_credentials row.
func TestRegistration_WaitlistMode_RegisterDoesNotCreateRealAccount(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	regH := &handlers.GfdRegistrationHandler{DB: db}
	authH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}

	w := doRegistration(regH, http.MethodPatch, "/admin/gfd-registration/api/mode", `{"mode":"waitlist"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set mode: status = %d, body = %s", w.Code, w.Body.String())
	}

	regBody := `{"email":"waitlisted@example.com","password":"correcthorsebattery","character_name":"WaitChar"}`
	rw := doEmailAuth(authH, "/api/v1/auth/email/register", regBody)
	if rw.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for waitlisted signup, got %d: %s", rw.Code, rw.Body.String())
	}
	var respBody map[string]any
	json.Unmarshal(rw.Body.Bytes(), &respBody)
	if respBody["waitlisted"] != true {
		t.Fatalf("expected waitlisted:true in response, got %+v", respBody)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_credentials WHERE email=?`, "waitlisted@example.com").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected NO real account to be created in waitlist mode, found %d", count)
	}

	var waitlistCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gfd_waitlist WHERE email=?`, "waitlisted@example.com").Scan(&waitlistCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if waitlistCount != 1 {
		t.Fatalf("expected exactly 1 waitlist row, found %d", waitlistCount)
	}
}

// TestRegistration_WaitlistMode_DuplicateEmailRejected mirrors handleRegister's own real
// "email already registered" contract for the waitlist path.
func TestRegistration_WaitlistMode_DuplicateEmailRejected(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	regH := &handlers.GfdRegistrationHandler{DB: db}
	authH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}
	doRegistration(regH, http.MethodPatch, "/admin/gfd-registration/api/mode", `{"mode":"waitlist"}`)

	regBody := `{"email":"dup@example.com","password":"correcthorsebattery"}`
	doEmailAuth(authH, "/api/v1/auth/email/register", regBody)
	w := doEmailAuth(authH, "/api/v1/auth/email/register", regBody)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate waitlist signup, got %d: %s", w.Code, w.Body.String())
	}
}

// TestRegistration_ReturningPlayerWithRealAccount_NotWaitlisted confirms a player who already
// has a real account (registered before waitlist mode was turned on) still gets the ordinary
// "email already registered" error, not a confusing waitlist message.
func TestRegistration_ReturningPlayerWithRealAccount_NotWaitlisted(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	regH := &handlers.GfdRegistrationHandler{DB: db}
	authH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}

	regBody := `{"email":"already@example.com","password":"correcthorsebattery"}`
	if w := doEmailAuth(authH, "/api/v1/auth/email/register", regBody); w.Code != http.StatusOK {
		t.Fatalf("initial register: status=%d body=%s", w.Code, w.Body.String())
	}

	doRegistration(regH, http.MethodPatch, "/admin/gfd-registration/api/mode", `{"mode":"waitlist"}`)
	w := doEmailAuth(authH, "/api/v1/auth/email/register", regBody)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 'already registered', got %d: %s", w.Code, w.Body.String())
	}
}

// TestRegistration_Approve_CreatesRealAccountUsingStoredHash confirms an admin approval creates
// the exact real account a normal open-mode registration would have, and that the player can log
// in with the password they originally chose (never asked to re-register).
func TestRegistration_Approve_CreatesRealAccountUsingStoredHash(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	regH := &handlers.GfdRegistrationHandler{DB: db}
	authH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}
	doRegistration(regH, http.MethodPatch, "/admin/gfd-registration/api/mode", `{"mode":"waitlist"}`)

	regBody := `{"email":"approve-me@example.com","password":"correcthorsebattery","character_name":"ApprovedChar","character_job":"WHM"}`
	doEmailAuth(authH, "/api/v1/auth/email/register", regBody)

	lw := doRegistration(regH, http.MethodGet, "/admin/gfd-registration/api/waitlist", "")
	var entries []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &entries)
	if len(entries) != 1 {
		t.Fatalf("expected 1 waitlist entry, got %d", len(entries))
	}
	id := int(entries[0]["id"].(float64))

	aw := doRegistration(regH, http.MethodPost, "/admin/gfd-registration/api/waitlist/"+regItoa(id)+"/approve", "")
	if aw.Code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", aw.Code, aw.Body.String())
	}

	// Switch back to open mode isn't required for login -- login never checks the mode.
	loginBody := `{"email":"approve-me@example.com","password":"correcthorsebattery"}`
	lgw := doEmailAuth(authH, "/api/v1/auth/email/login", loginBody)
	if lgw.Code != http.StatusOK {
		t.Fatalf("login after approval should succeed with the original password: status=%d body=%s", lgw.Code, lgw.Body.String())
	}

	var charCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM characters WHERE name=?`, "ApprovedChar").Scan(&charCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if charCount != 1 {
		t.Fatalf("expected the character requested at signup to be created on approval, found %d", charCount)
	}
}

func TestRegistration_Approve_AlreadyApproved_Refused(t *testing.T) {
	db := newTestEmailAuthDB(t)
	defer db.Close()
	regH := &handlers.GfdRegistrationHandler{DB: db}
	authH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: newTestKeys(t), Issuer: "test"}
	doRegistration(regH, http.MethodPatch, "/admin/gfd-registration/api/mode", `{"mode":"waitlist"}`)
	doEmailAuth(authH, "/api/v1/auth/email/register", `{"email":"twice@example.com","password":"correcthorsebattery"}`)

	lw := doRegistration(regH, http.MethodGet, "/admin/gfd-registration/api/waitlist", "")
	var entries []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &entries)
	id := int(entries[0]["id"].(float64))

	doRegistration(regH, http.MethodPost, "/admin/gfd-registration/api/waitlist/"+regItoa(id)+"/approve", "")
	w := doRegistration(regH, http.MethodPost, "/admin/gfd-registration/api/waitlist/"+regItoa(id)+"/approve", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on double-approve, got %d: %s", w.Code, w.Body.String())
	}
}

func regItoa(i int) string {
	return strconv.Itoa(i)
}
