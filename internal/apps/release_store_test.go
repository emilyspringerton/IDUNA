package apps

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newReleaseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE app_releases (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			app_slug          TEXT NOT NULL,
			platform          TEXT NOT NULL,
			version           TEXT NOT NULL,
			session_tag       TEXT NOT NULL,
			filename          TEXT NOT NULL,
			sha256            TEXT NOT NULL,
			size_bytes        INTEGER NOT NULL,
			blob_path         TEXT NOT NULL,
			gpg_signature     TEXT NOT NULL DEFAULT '',
			gpg_key_id        TEXT NOT NULL DEFAULT '',
			github_commit_sha TEXT NOT NULL DEFAULT '',
			github_run_url    TEXT NOT NULL DEFAULT '',
			is_latest         INTEGER NOT NULL DEFAULT 0,
			created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create app_releases: %v", err)
	}
	return db
}

const testSessionTag = "sess-20260922-0930-a1b2c3d4"

func TestCreate_RejectsBadSessionTag(t *testing.T) {
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: t.TempDir()}
	_, err := s.Create(context.Background(), "deadweight", "linux_x86_64", "0.58.0", "not-a-real-tag",
		"dw_gui", []byte("binary"), "sig", "keyid", "abc123", "https://github.com/x/y/actions/runs/1")
	if err == nil {
		t.Fatal("expected error for malformed session_tag")
	}
}

func TestCreate_RejectsBadAppSlug(t *testing.T) {
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: t.TempDir()}
	_, err := s.Create(context.Background(), "Not Valid!", "linux_x86_64", "0.58.0", testSessionTag,
		"dw_gui", []byte("binary"), "", "", "", "")
	if err == nil {
		t.Fatal("expected error for invalid app_slug")
	}
}

func TestCreate_RealRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: dir}
	data := []byte("fake dw_gui binary contents")
	rel, err := s.Create(context.Background(), "deadweight", "linux_x86_64", "0.58.0", testSessionTag,
		"dw_gui", data, "-----BEGIN PGP SIGNATURE-----\n...\n-----END PGP SIGNATURE-----", "A9BEEFEC4DF16C2E",
		"abc123def456", "https://github.com/emilyspringerton/DEADWEIGHT/actions/runs/12345")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !rel.IsLatest {
		t.Fatal("first release for a (app_slug, platform) pair should be is_latest")
	}
	if rel.SHA256 == "" || rel.SizeBytes != int64(len(data)) {
		t.Fatalf("sha256/size_bytes not computed correctly: %+v", rel)
	}
	if rel.GPGKeyID != "A9BEEFEC4DF16C2E" {
		t.Fatalf("gpg_key_id not persisted: %+v", rel)
	}

	got, blob, err := s.ReadBlob(context.Background(), rel.ID)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if string(blob) != string(data) {
		t.Fatal("blob contents don't match what was uploaded")
	}
	if got.ID != rel.ID {
		t.Fatal("ReadBlob returned wrong release metadata")
	}
}

// TestCreate_NewReleaseDemotesPriorLatest -- the real single-selection invariant: uploading a
// second release for the SAME (app_slug, platform) pair must both promote the new one AND demote
// the old one, in the same transaction, same real discipline
// shankpit.CheckpointStore.SetActiveOpponent already established for its own "exactly one active"
// rule.
func TestCreate_NewReleaseDemotesPriorLatest(t *testing.T) {
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: t.TempDir()}
	first, err := s.Create(context.Background(), "deadweight", "linux_x86_64", "0.58.0", testSessionTag,
		"dw_gui", []byte("v1"), "", "", "", "")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := s.Create(context.Background(), "deadweight", "linux_x86_64", "0.59.0", testSessionTag,
		"dw_gui", []byte("v2"), "", "", "", "")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if !second.IsLatest {
		t.Fatal("second (newest) release should be is_latest")
	}
	refetchedFirst, err := s.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if refetchedFirst.IsLatest {
		t.Fatal("first release should have been demoted when the second was created")
	}

	latest, err := s.GetLatest(context.Background(), "deadweight", "linux_x86_64")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil || latest.ID != second.ID {
		t.Fatalf("GetLatest should return the second release, got %+v", latest)
	}
}

// TestCreate_IndependentPerPlatform -- a Windows release and a Linux release for the same app
// must not interfere with each other's own is_latest flag.
func TestCreate_IndependentPerPlatform(t *testing.T) {
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: t.TempDir()}
	linux, err := s.Create(context.Background(), "deadweight", "linux_x86_64", "0.58.0", testSessionTag,
		"dw_gui", []byte("linux"), "", "", "", "")
	if err != nil {
		t.Fatalf("create linux: %v", err)
	}
	_, err = s.Create(context.Background(), "deadweight", "windows_x86_64", "0.58.0", testSessionTag,
		"dw_gui.exe", []byte("windows"), "", "", "", "")
	if err != nil {
		t.Fatalf("create windows: %v", err)
	}
	refetchedLinux, err := s.Get(context.Background(), linux.ID)
	if err != nil {
		t.Fatalf("get linux: %v", err)
	}
	if !refetchedLinux.IsLatest {
		t.Fatal("linux release should still be is_latest after an unrelated windows release was created")
	}
}

func TestGetLatest_NilNotErrorWhenNothingPublished(t *testing.T) {
	s := &ReleaseStore{DB: newReleaseTestDB(t), BlobDir: t.TempDir()}
	latest, err := s.GetLatest(context.Background(), "deadweight", "linux_x86_64")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if latest != nil {
		t.Fatalf("expected nil for a never-published (app_slug, platform), got %+v", latest)
	}
}
