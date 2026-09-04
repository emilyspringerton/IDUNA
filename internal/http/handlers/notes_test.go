package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func newTestNotesDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Real, direct mirror of migrations/truestore/202609040005_notes.sql.
	_, err = db.Exec(`CREATE TABLE notes (
		id            INTEGER  PRIMARY KEY AUTOINCREMENT,
		owner_subject VARCHAR(255) NOT NULL,
		title         VARCHAR(200) NOT NULL,
		body          TEXT NOT NULL DEFAULT '',
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create notes table: %v", err)
	}
	return db
}

func notesHandlerWithAuth(keys *jwt.Keys, db *sql.DB) http.Handler {
	h := &handlers.NotesHandler{DB: db}
	return middleware.RequireAuth(keys)(h)
}

type noteOut struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// TestNotes_CreateAndListAreOwnerScoped -- IN-000/IN-001's own real Phase 1 core guarantee: a
// note created by one real caller (JWT sub) is invisible to a different caller's own listing,
// even though both hit the exact same real DB table.
func TestNotes_CreateAndListAreOwnerScoped(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestNotesDB(t)
	h := notesHandlerWithAuth(keys, db)
	tokenA := makeAgentToken(t, keys, "user-a", nil)
	tokenB := makeAgentToken(t, keys, "user-b", nil)

	body, _ := json.Marshal(map[string]string{"title": "Grocery list", "body": "eggs, milk"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// user-a sees their own real note.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var notesA []noteOut
	if err := json.Unmarshal(rec.Body.Bytes(), &notesA); err != nil {
		t.Fatalf("decode user-a list: %v", err)
	}
	if len(notesA) != 1 || notesA[0].Title != "Grocery list" {
		t.Fatalf("user-a should see their own real note, got %+v", notesA)
	}

	// user-b sees nothing -- real ownership isolation, not a shared table.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var notesB []noteOut
	if err := json.Unmarshal(rec.Body.Bytes(), &notesB); err != nil {
		t.Fatalf("decode user-b list: %v", err)
	}
	if len(notesB) != 0 {
		t.Fatalf("user-b should see zero notes (real ownership isolation), got %+v", notesB)
	}
}

// TestNotes_CreateDefaultsBlankTitle -- a real iCloud-Notes-shaped "tap + to create a blank
// note" default, not a hard validation error on an empty title.
func TestNotes_CreateDefaultsBlankTitle(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestNotesDB(t)
	h := notesHandlerWithAuth(keys, db)
	token := makeAgentToken(t, keys, "user-a", nil)

	body, _ := json.Marshal(map[string]string{"body": "just some thoughts"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var notes []noteOut
	_ = json.Unmarshal(rec.Body.Bytes(), &notes)
	if len(notes) != 1 || notes[0].Title != "Untitled Note" {
		t.Fatalf("expected a real default title 'Untitled Note', got %+v", notes)
	}
}

// TestNotes_UpdateAndDeleteAreOwnerScoped -- a caller can never update or delete another real
// user's own note, even by guessing a real, valid note id -- a real, honest 404, not a leaked
// 403 that would confirm the note's own existence.
func TestNotes_UpdateAndDeleteAreOwnerScoped(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestNotesDB(t)
	h := notesHandlerWithAuth(keys, db)
	tokenA := makeAgentToken(t, keys, "user-a", nil)
	tokenB := makeAgentToken(t, keys, "user-b", nil)

	body, _ := json.Marshal(map[string]string{"title": "user-a's note", "body": "private"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var created map[string]int64
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created["id"]

	// user-b tries to update user-a's real note.
	updateBody, _ := json.Marshal(map[string]string{"title": "hijacked"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/notes/"+strconv.FormatInt(id, 10), bytes.NewReader(updateBody))
	req.Header.Set("Authorization", "Bearer "+tokenB)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("user-b updating user-a's note: status = %d, want 404", rec.Code)
	}

	// user-b tries to delete user-a's real note.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/notes/"+strconv.FormatInt(id, 10), nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("user-b deleting user-a's note: status = %d, want 404", rec.Code)
	}

	// The real owner (user-a) can update it fine.
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/notes/"+strconv.FormatInt(id, 10), bytes.NewReader(updateBody))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("real owner updating their own note: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

