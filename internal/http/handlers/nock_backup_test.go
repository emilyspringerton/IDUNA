package handlers

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNockBackup_ZipHasSnapshotAndTables(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/x.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE shankpit_levels (id INTEGER PRIMARY KEY, name TEXT, walls_json TEXT)`,
		`INSERT INTO shankpit_levels VALUES (1, 'L1', '[]')`,
		`CREATE TABLE shankpit_rl_checkpoints (id INTEGER PRIMARY KEY, blob BLOB)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	(&NockBackupHandler{DB: db, SQLite: true}).ServeHTTP(rec, httptest.NewRequest("GET", "/admin/nock/api/backup", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for _, want := range []string{"iduna.db", "nock/shankpit_levels.json", "README.txt"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if got["nock/shankpit_rl_checkpoints.json"] || got["nock/users.json"] {
		t.Errorf("unexpected table dumped: %v", got)
	}

	// list + selection: only the picked table, no snapshot
	rec = httptest.NewRecorder()
	h := &NockBackupHandler{DB: db, SQLite: true}
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/nock/api/backup?list=1", nil))
	if !bytes.Contains(rec.Body.Bytes(), []byte("shankpit_levels")) || !bytes.Contains(rec.Body.Bytes(), []byte(`"database"`)) {
		t.Fatalf("list: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/nock/api/backup?sources=shankpit_levels", nil))
	zr, _ = zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	got = map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if got["iduna.db"] || !got["nock/shankpit_levels.json"] {
		t.Fatalf("selection ignored: %v", got)
	}
}
