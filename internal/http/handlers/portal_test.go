package handlers_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/blog"
	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

// Portal's own auth gating (RequireCookieAuth + RequirePermission) is
// wired in main.go and already covered by
// internal/http/middleware/auth_test.go's own suite -- these tests just
// cover the handler's own rendering, the same split every other handler
// in this package uses.

func TestPortalHandler_Login_RendersSignInWidget(t *testing.T) {
	h := &handlers.PortalHandler{GoogleClientID: "test-client-id.apps.googleusercontent.com"}
	req := httptest.NewRequest(http.MethodGet, "/portal/login?next=%2Fportal", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "test-client-id.apps.googleusercontent.com") {
		t.Errorf("expected the configured GoogleClientID in the rendered page, got: %s", body)
	}
	if !strings.Contains(body, "/api/v1/auth/google") {
		t.Errorf("expected the login page to POST credentials to /api/v1/auth/google, got: %s", body)
	}
}

func TestPortalHandler_Login_NoClientIDShowsFallback(t *testing.T) {
	h := &handlers.PortalHandler{} // GoogleClientID unset
	req := httptest.NewRequest(http.MethodGet, "/portal/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not yet configured") {
		t.Errorf("expected an honest fallback message when GOOGLE_CLIENT_ID is unset, got: %s", rr.Body.String())
	}
}

func TestPortalHandler_Login_WrongMethod(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodPost, "/portal/login", nil)
	rr := httptest.NewRecorder()
	h.Login(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-GET, got %d", rr.Code)
	}
}

// TestPortalHandler_LocalLogin_EmitsEvents -- S226-03: a real success and a real failure both
// land in the unified log with the right event Type, distinct from LocalAuthHandler's own
// iduna:auth.local.* events (this is the cookie-based /portal/login surface, a real, separate
// login path).
func TestPortalHandler_LocalLogin_EmitsEvents(t *testing.T) {
	keys, err := jwt.GenerateKeys()
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	proj := &stubUserProjector{byEmail: map[string]*userlog.LocalUser{
		"alice@example.com": {LocalUID: 1, Email: "alice@example.com", Status: "active",
			PasswordHash: mustHash(t, "correct-horse-battery-staple")},
	}}
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.PortalHandler{Keys: keys, Proj: proj, EventLog: eventLog}

	post := func(email, password string) int {
		form := url.Values{"email": {email}, "password": {password}}
		req := httptest.NewRequest(http.MethodPost, "/portal/login", bytes.NewBufferString(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.LocalLogin(rr, req)
		return rr.Code
	}

	if code := post("alice@example.com", "correct-horse-battery-staple"); code != http.StatusSeeOther {
		t.Fatalf("success login: status = %d, want 303", code)
	}
	if code := post("alice@example.com", "wrong-password"); code != http.StatusOK {
		// LocalLogin re-renders the login page (200) with an error, rather than a redirect.
		t.Fatalf("failed login: status = %d, want 200 (re-rendered login page)", code)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:auth.portal.success" {
		t.Errorf("event 0 Type = %q, want iduna:auth.portal.success", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:auth.portal.failure" {
		t.Errorf("event 1 Type = %q, want iduna:auth.portal.failure", recs[1].Event.Type)
	}
}

// TestPortalHandler_Logs_NoQueryShowsForm -- with no query params, the page renders the search
// form and no results table (and doesn't touch the event log at all).
func TestPortalHandler_Logs_NoQueryShowsForm(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodGet, "/portal/logs", nil)
	rr := httptest.NewRecorder()
	h.Logs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `name="search"`) || !strings.Contains(body, `name="regex"`) {
		t.Errorf("expected a search form with search+regex fields, got: %s", body)
	}
	if strings.Contains(body, "matching event") {
		t.Errorf("expected no results section with no query, got: %s", body)
	}
}

// TestPortalHandler_Logs_RealQueryReturnsResults -- a real query against a real event log
// returns the matching event, rendered in the page.
func TestPortalHandler_Logs_RealQueryReturnsResults(t *testing.T) {
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	_, err = eventLog.Append(context.Background(), userlog.Event{
		ID: "e1", Type: "iduna:auth.local.failure", Source: "iduna-auth",
		Data: []byte(`{"email":"alice@example.com"}`),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	h := &handlers.PortalHandler{EventLog: eventLog}
	req := httptest.NewRequest(http.MethodGet, "/portal/logs?search=type=iduna:auth.local.failure", nil)
	rr := httptest.NewRecorder()
	h.Logs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 matching event") {
		t.Errorf("expected exactly 1 matching event reported, got: %s", body)
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("expected the matched event's own data rendered, got: %s", body)
	}
}

// TestPortalHandler_Logs_BadRegexShowsError -- a real, honest error message, not a panic or a
// silently-empty result set.
func TestPortalHandler_Logs_BadRegexShowsError(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodGet, "/portal/logs?regex=%28%5B", nil) // "(["
	rr := httptest.NewRecorder()
	h.Logs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (the page itself renders fine, with an inline error), got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid regex") {
		t.Errorf("expected an inline invalid-regex error, got: %s", rr.Body.String())
	}
}

// TestPortalHandler_Search_NoQueryShowsForm -- same real "empty state" shape as the Logs page.
func TestPortalHandler_Search_NoQueryShowsForm(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodGet, "/portal/search", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `name="q"`) {
		t.Errorf("expected a search form with a q field, got: %s", body)
	}
	if strings.Contains(body, "matching apple") || strings.Contains(body, "matching event") {
		t.Errorf("expected no results section with no query, got: %s", body)
	}
}

// TestPortalHandler_Search_RealQueryReturnsBothCorpora -- a real query returns real, distinct
// results from BOTH the Apples store and the unified event log in one page, kanban card 1111's
// own literal point.
func TestPortalHandler_Search_RealQueryReturnsBothCorpora(t *testing.T) {
	store := &stubApplesStore{}
	if _, err := store.AppendApple(context.Background(), auth.AppleRecord{
		AgentID: "a", SourceRepo: "R", AppleType: "completion",
		Title: "a real widget apple", Body: "body text",
	}); err != nil {
		t.Fatalf("AppendApple: %v", err)
	}

	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	if _, err := eventLog.Append(context.Background(), userlog.Event{
		ID: "e1", Type: "iduna:auth.local.failure", Source: "iduna-auth",
		Data: []byte(`{"note":"a real widget event"}`),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	h := &handlers.PortalHandler{Store: store, EventLog: eventLog}
	req := httptest.NewRequest(http.MethodGet, "/portal/search?q=widget", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "1 matching apple") {
		t.Errorf("expected 1 matching apple reported, got: %s", body)
	}
	if !strings.Contains(body, "1 matching event") {
		t.Errorf("expected 1 matching event reported, got: %s", body)
	}
	if !strings.Contains(body, "a real widget apple") || !strings.Contains(body, "a real widget event") {
		t.Errorf("expected both real results actually rendered, got: %s", body)
	}
}

// TestPortalHandler_Search_UnconfiguredStoreDoesNotBlockLogs -- real, deliberate independence:
// a nil Store must not prevent the log-events half from returning real results.
func TestPortalHandler_Search_UnconfiguredStoreDoesNotBlockLogs(t *testing.T) {
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	if _, err := eventLog.Append(context.Background(), userlog.Event{
		ID: "e1", Type: "iduna:auth.local.failure", Source: "iduna-auth",
		Data: []byte(`{"note":"findme"}`),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	h := &handlers.PortalHandler{EventLog: eventLog} // Store left nil
	req := httptest.NewRequest(http.MethodGet, "/portal/search?q=findme", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "apples store is not configured") {
		t.Errorf("expected an honest apples-not-configured message, got: %s", body)
	}
	if !strings.Contains(body, "1 matching event") {
		t.Errorf("expected the log-events half to still work despite the nil Store, got: %s", body)
	}
}

// TestPortalHandler_Search_IncludesBlogPosts -- kanban card 9944 ("OG IDUNA unified search"):
// found live that card 1111's own original search only covered Apples + Log Events, with no
// blog corpus at all despite IDUNA's own real, prominent blog content. Real third corpus, same
// independent-availability shape as the other two.
func TestPortalHandler_Search_IncludesBlogPosts(t *testing.T) {
	blogStore, err := blog.Open(t.TempDir() + "/blog.db")
	if err != nil {
		t.Fatalf("blog.Open: %v", err)
	}
	t.Cleanup(func() { _ = blogStore.Close() })
	if _, err := blogStore.Create(blog.Post{
		Slug: "widget-post", Title: "A Real Widget Post", Author: "Tyler", Body: "body text",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	h := &handlers.PortalHandler{BlogStore: blogStore}
	req := httptest.NewRequest(http.MethodGet, "/portal/search?q=widget", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "1 matching post") {
		t.Errorf("expected 1 matching blog post reported, got: %s", body)
	}
	if !strings.Contains(body, "A Real Widget Post") {
		t.Errorf("expected the real post title rendered, got: %s", body)
	}
}

// TestPortalHandler_Search_UnconfiguredBlogStoreDoesNotBlockOthers -- same independence
// guarantee as the existing Store/EventLog test above, extended to the new third corpus.
func TestPortalHandler_Search_UnconfiguredBlogStoreDoesNotBlockOthers(t *testing.T) {
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	if _, err := eventLog.Append(context.Background(), userlog.Event{
		ID: "e1", Type: "iduna:auth.local.failure", Source: "iduna-auth",
		Data: []byte(`{"note":"findme"}`),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	h := &handlers.PortalHandler{EventLog: eventLog} // BlogStore left nil
	req := httptest.NewRequest(http.MethodGet, "/portal/search?q=findme", nil)
	rr := httptest.NewRecorder()
	h.Search(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "blog store is not configured") {
		t.Errorf("expected an honest blog-not-configured message, got: %s", body)
	}
	if !strings.Contains(body, "1 matching event") {
		t.Errorf("expected the log-events half to still work despite the nil BlogStore, got: %s", body)
	}
}

func TestPortalHandler_Home_ListsTools(t *testing.T) {
	h := &handlers.PortalHandler{}
	req := httptest.NewRequest(http.MethodGet, "/portal", nil)
	rr := httptest.NewRecorder()
	h.Home(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Jupyter") {
		t.Errorf("expected Jupyter listed on the portal, got: %s", body)
	}
	if !strings.Contains(body, "SARENA_NOTEBOOK") {
		t.Errorf("expected SARENA_NOTEBOOK listed on the portal, got: %s", body)
	}
}
