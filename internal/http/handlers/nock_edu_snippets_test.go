package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
)

func newEduSnippetStore(t *testing.T) *nock.EduSnippetStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE nock_edu_snippets (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, widget_name TEXT NOT NULL, source TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
		CREATE UNIQUE INDEX i ON nock_edu_snippets(widget_name, name)`); err != nil {
		t.Fatal(err)
	}
	return &nock.EduSnippetStore{DB: db}
}

func eduDo(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func TestNockEduSnippets_CRUDAndPublicFilter(t *testing.T) {
	store := newEduSnippetStore(t)
	admin := &handlers.NockEduSnippetsHandler{Store: store}
	pub := &handlers.NockEduSnippetsPublicHandler{Store: store}

	rec := eduDo(admin, http.MethodPost, "/admin/nock/api/edu-snippets", map[string]string{"name": "open-all", "widget_name": "ORB", "source": "open_gate();\n"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var e nock.EduSnippet
	json.Unmarshal(rec.Body.Bytes(), &e)
	if e.ID == 0 || e.WidgetName != "ORB" {
		t.Fatalf("bad row %+v", e)
	}
	eduDo(admin, http.MethodPost, "/admin/nock/api/edu-snippets", map[string]string{"name": "other", "widget_name": "DOOR_A", "source": "print(1);"})

	// duplicate (widget, name) is rejected
	if rec := eduDo(admin, http.MethodPost, "/admin/nock/api/edu-snippets", map[string]string{"name": "open-all", "widget_name": "ORB", "source": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate: want 400 got %d", rec.Code)
	}
	// public list is filterable by widget and read-only
	rec = eduDo(pub, http.MethodGet, "/api/v1/nock-edu-snippets?widget=ORB", nil)
	var list []nock.EduSnippet
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0].Name != "open-all" {
		t.Fatalf("public filter: %d %s", rec.Code, rec.Body.String())
	}
	rec = eduDo(pub, http.MethodGet, "/api/v1/nock-edu-snippets", nil)
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("public all: want 2 got %d", len(list))
	}
	if rec := eduDo(pub, http.MethodPost, "/api/v1/nock-edu-snippets", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("public POST: want 405 got %d", rec.Code)
	}
	// update, then bad updates
	rec = eduDo(admin, http.MethodPut, "/admin/nock/api/edu-snippets/1", map[string]string{"source": "close_gate();"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "close_gate") {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if rec := eduDo(admin, http.MethodPut, "/admin/nock/api/edu-snippets/1", map[string]string{"source": strings.Repeat("a", nock.EduSnippetMaxSource+1)}); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize: want 400 got %d", rec.Code)
	}
	if rec := eduDo(admin, http.MethodPut, "/admin/nock/api/edu-snippets/1", map[string]string{"source": "   "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank: want 400 got %d", rec.Code)
	}
	if rec := eduDo(admin, http.MethodPost, "/admin/nock/api/edu-snippets", map[string]string{"name": "bad name!", "widget_name": "ORB", "source": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad name: want 400 got %d", rec.Code)
	}
	// delete
	if rec := eduDo(admin, http.MethodDelete, "/admin/nock/api/edu-snippets/1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := eduDo(admin, http.MethodGet, "/admin/nock/api/edu-snippets/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: want 404 got %d", rec.Code)
	}
}
