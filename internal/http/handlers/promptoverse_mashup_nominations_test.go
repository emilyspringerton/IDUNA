package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/promptoverse"
)

// stubMashupIAMStore wraps stubAgentStore (defined in agent_auth_test.go,
// same handlers_test package) but with a configurable GetUserByID, since
// the embedded no-op version always returns a nil user -- which would
// panic on user.HonorAccepted in the handler under test.
type stubMashupIAMStore struct {
	stubAgentStore
	user *auth.User
}

func (s *stubMashupIAMStore) GetUserByID(context.Context, string) (*auth.User, error) {
	if s.user == nil {
		return nil, errNoSuchUser
	}
	return s.user, nil
}

var errNoSuchUser = &stubNoSuchUserErr{}

type stubNoSuchUserErr struct{}

func (*stubNoSuchUserErr) Error() string { return "no such user" }

func newTestPromptOVerseStore(t *testing.T) *promptoverse.Store {
	t.Helper()
	s, err := promptoverse.Open(filepath.Join(t.TempDir(), "promptoverse.db"))
	if err != nil {
		t.Fatalf("promptoverse.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedSubjectForNominationTest(t *testing.T, s *promptoverse.Store, subject string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		slug := subject + "-leaf"
		if i == 1 {
			slug += "-2"
		}
		if _, err := s.Create(promptoverse.Node{
			Slug: slugifyLower(slug), Label: "style", Subject: subject, Kind: "surreal",
			EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "a.png",
		}); err != nil {
			t.Fatalf("seed %q: %v", subject, err)
		}
	}
}

func slugifyLower(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == ' ' || r == '-' || r == '_':
			out = append(out, '-')
		}
	}
	return string(out)
}

func testJWTKeys(t *testing.T) *jwt.Keys {
	t.Helper()
	keys, err := jwt.GenerateKeys()
	if err != nil {
		t.Fatalf("jwt.GenerateKeys: %v", err)
	}
	return keys
}

func tokenFor(t *testing.T, keys *jwt.Keys, sub string) string {
	t.Helper()
	tok, err := jwt.Sign(keys, map[string]any{"sub": sub, "permissions": []string{}})
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return tok
}

func TestMashupNominationsHandler_Create_RequiresHonorCodeAccepted(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	seedSubjectForNominationTest(t, pStore, "Raccoon")

	iamStore := &stubMashupIAMStore{user: &auth.User{IDString: "user-1", HonorAccepted: false}}
	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: iamStore}

	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	protected := middleware.RequireAuth(keys)(http.HandlerFunc(h.Create))
	h.RegisterRoutes(mux, protected, http.NotFoundHandler())

	body, _ := json.Marshal(map[string]string{"subject_a": "Fractal", "subject_b": "Raccoon"})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/mashup-nominations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, keys, "user-1"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (honor code required), got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "HONOR_CODE_REQUIRED" {
		t.Errorf("expected HONOR_CODE_REQUIRED, got %v", resp["code"])
	}
}

func TestMashupNominationsHandler_Create_Succeeds(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	seedSubjectForNominationTest(t, pStore, "Raccoon")

	iamStore := &stubMashupIAMStore{user: &auth.User{IDString: "user-1", HonorAccepted: true}}
	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: iamStore}

	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	protected := middleware.RequireAuth(keys)(http.HandlerFunc(h.Create))
	h.RegisterRoutes(mux, protected, http.NotFoundHandler())

	body, _ := json.Marshal(map[string]string{"subject_a": "Fractal", "subject_b": "Raccoon"})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/mashup-nominations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, keys, "user-1"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	nominations, err := pStore.ListMashupNominations("")
	if err != nil {
		t.Fatal(err)
	}
	if len(nominations) != 1 || nominations[0].Status != "pending" {
		t.Errorf("unexpected nominations after create: %+v", nominations)
	}
}

func TestMashupNominationsHandler_Create_RejectsUnknownSubject(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	// "Raccoon" deliberately not seeded -- doesn't exist as a real subject.

	iamStore := &stubMashupIAMStore{user: &auth.User{IDString: "user-1", HonorAccepted: true}}
	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: iamStore}

	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	protected := middleware.RequireAuth(keys)(http.HandlerFunc(h.Create))
	h.RegisterRoutes(mux, protected, http.NotFoundHandler())

	body, _ := json.Marshal(map[string]string{"subject_a": "Fractal", "subject_b": "Raccoon"})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/mashup-nominations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, keys, "user-1"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a nonexistent subject, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMashupNominationsHandler_Create_RejectsUnauthenticated(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	iamStore := &stubMashupIAMStore{}
	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: iamStore}

	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	protected := middleware.RequireAuth(keys)(http.HandlerFunc(h.Create))
	h.RegisterRoutes(mux, protected, http.NotFoundHandler())

	body, _ := json.Marshal(map[string]string{"subject_a": "Fractal", "subject_b": "Raccoon"})
	req := httptest.NewRequest("POST", "/api/v1/promptoverse/mashup-nominations", bytes.NewReader(body))
	// No Authorization header.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMashupNominationsHandler_List_IsPublic(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	seedSubjectForNominationTest(t, pStore, "Raccoon")
	if _, err := pStore.CreateMashupNomination("Fractal", "Raccoon", "user-1"); err != nil {
		t.Fatal(err)
	}

	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: &stubMashupIAMStore{}}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, http.NotFoundHandler(), http.NotFoundHandler())

	req := httptest.NewRequest("GET", "/api/v1/promptoverse/mashup-nominations", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for the public list endpoint, got %d", rec.Code)
	}
	var resp struct {
		Nominations []map[string]any `json:"nominations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Nominations) != 1 {
		t.Errorf("expected 1 nomination in the public listing, got %d", len(resp.Nominations))
	}
}

func TestMashupNominationsHandler_Review_RequiresPermission(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	seedSubjectForNominationTest(t, pStore, "Raccoon")
	id, err := pStore.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != nil {
		t.Fatal(err)
	}

	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: &stubMashupIAMStore{}}
	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	reviewProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.mashups.review")(http.HandlerFunc(h.Review)))
	h.RegisterRoutes(mux, http.NotFoundHandler(), reviewProtected)

	// A regular user's token has no permissions -- must be rejected.
	body, _ := json.Marshal(map[string]string{"status": "approved"})
	req := httptest.NewRequest("PATCH", "/api/v1/promptoverse/mashup-nominations/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenFor(t, keys, "user-1"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a user without promptoverse.mashups.review, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMashupNominationsHandler_Review_ApprovesWithPermission(t *testing.T) {
	pStore := newTestPromptOVerseStore(t)
	seedSubjectForNominationTest(t, pStore, "Fractal")
	seedSubjectForNominationTest(t, pStore, "Raccoon")
	id, err := pStore.CreateMashupNomination("Fractal", "Raccoon", "user-1")
	if err != nil {
		t.Fatal(err)
	}

	h := &handlers.MashupNominationsHandler{Store: pStore, IAMStore: &stubMashupIAMStore{}}
	keys := testJWTKeys(t)
	mux := http.NewServeMux()
	reviewProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.mashups.review")(http.HandlerFunc(h.Review)))
	h.RegisterRoutes(mux, http.NotFoundHandler(), reviewProtected)

	tok, err := jwt.Sign(keys, map[string]any{"sub": "admin-1", "permissions": []string{"promptoverse.mashups.review"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"status": "approved"})
	req := httptest.NewRequest("PATCH", "/api/v1/promptoverse/mashup-nominations/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	nominations, err := pStore.ListMashupNominations("approved")
	if err != nil {
		t.Fatal(err)
	}
	if len(nominations) != 1 || nominations[0].ReviewedBy != "admin-1" {
		t.Errorf("unexpected nominations after review: %+v", nominations)
	}
}
