package handlers_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

func newGMTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE players (player_id TEXT PRIMARY KEY, display_name TEXT, disabled_at TEXT, registered_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`CREATE TABLE player_credentials (player_id TEXT PRIMARY KEY, email TEXT)`,
		`CREATE TABLE characters (character_id TEXT PRIMARY KEY, player_id TEXT, name TEXT, job_main TEXT)`,
		`CREATE TABLE player_permissions (player_id TEXT NOT NULL, permission TEXT NOT NULL, granted_by TEXT NOT NULL DEFAULT '', granted_at TEXT NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (player_id, permission))`,
		`INSERT INTO players (player_id, display_name) VALUES ('p1', 'Runner-1')`,
		`INSERT INTO player_credentials (player_id, email) VALUES ('p1', 'someone@example.com')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return db
}

// TestAdminGM_GrantRevokePermission_EmitsEventsAndPersists -- the real Back Office interface for
// granting roles to email/WOTAN accounts (founder real-time, 2026-10-09: "we are going to need an
// interface in iduna for adding roles to the email users"). A grant must land a real row that
// player_email_auth.go's own playerPermissions reads, and a revoke must remove it; both must be
// audit-logged with the acted-on player_id and permission.
func TestAdminGM_GrantRevokePermission_EmitsEventsAndPersists(t *testing.T) {
	db := newGMTestDB(t)
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	h := &handlers.AdminHandler{DB: db, EventLog: eventLog}
	h.Init()

	post := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post("/admin/gm/p1/grant_permission", "permission=edge.game.operator"); code != http.StatusSeeOther {
		t.Fatalf("grant: status = %d, want 303", code)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_permissions WHERE player_id='p1' AND permission='edge.game.operator'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the grant to persist a row, got count=%d", count)
	}

	if code := post("/admin/gm/p1/revoke_permission", "permission=edge.game.operator"); code != http.StatusSeeOther {
		t.Fatalf("revoke: status = %d, want 303", code)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_permissions WHERE player_id='p1' AND permission='edge.game.operator'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the revoke to remove the row, got count=%d", count)
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:admin.player_permission.grant" {
		t.Errorf("event 0 Type = %q, want iduna:admin.player_permission.grant", recs[0].Event.Type)
	}
	if recs[1].Event.Type != "iduna:admin.player_permission.revoke" {
		t.Errorf("event 1 Type = %q, want iduna:admin.player_permission.revoke", recs[1].Event.Type)
	}
	for _, rec := range recs {
		if !strings.Contains(string(rec.Event.Data), "p1") || !strings.Contains(string(rec.Event.Data), "edge.game.operator") {
			t.Errorf("event should record player_id and permission, got: %s", rec.Event.Data)
		}
	}
}

// TestAdminGM_GrantPermission_RejectsBadNames -- the only real gate on this free-text field since
// no permissions catalog exists for player accounts; must not silently accept garbage or write it
// to the DB.
func TestAdminGM_GrantPermission_RejectsBadNames(t *testing.T) {
	db := newGMTestDB(t)
	h := &handlers.AdminHandler{DB: db}
	h.Init()

	// "UPPER.CASE" is deliberately NOT tested here as a rejection case: the handler lowercases
	// before validating (same normalization every permission string elsewhere in this codebase
	// already gets), so it's accepted as "upper.case" -- consistent behavior, not a gap.
	bad := []string{"", "no-dot", "has spaces.here", "semi;colon.x"}
	for _, perm := range bad {
		req := httptest.NewRequest(http.MethodPost, "/admin/gm/p1/grant_permission", strings.NewReader("permission="+perm))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("permission %q: status = %d, want 400", perm, rr.Code)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM player_permissions`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows written for rejected permission names, got %d", count)
	}
}

// TestAdminGM_Search_ShowsGrantedPermissions -- gmLookup must surface existing grants in the
// search results (what the Back Office actually renders), not just accept new ones.
func TestAdminGM_Search_ShowsGrantedPermissions(t *testing.T) {
	db := newGMTestDB(t)
	if _, err := db.Exec(`INSERT INTO player_permissions (player_id, permission) VALUES ('p1', 'edge.game.operator')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &handlers.AdminHandler{DB: db}
	h.Init()

	req := httptest.NewRequest(http.MethodGet, "/admin/gm?q=someone", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "edge.game.operator") {
		t.Errorf("expected the rendered page to show the granted permission, got: %s", rr.Body.String())
	}
}
