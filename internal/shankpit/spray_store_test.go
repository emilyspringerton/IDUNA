package shankpit_test

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

func newTestSprayStore(t *testing.T) *shankpit.SprayStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE shankpit_sprays (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			name       VARCHAR(200) NOT NULL,
			width      INTEGER NOT NULL,
			height     INTEGER NOT NULL,
			png_data   BLOB NOT NULL,
			is_default BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create shankpit_sprays: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_shankpit_sprays_name ON shankpit_sprays(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &shankpit.SprayStore{DB: db}
}

func TestCreateSpray_FirstOneBecomesDefault(t *testing.T) {
	s := newTestSprayStore(t)
	sp, err := s.CreateSpray(context.Background(), "Tag One", 64, 64, []byte("png"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !sp.IsDefault {
		t.Fatal("expected the first-ever spray to become the real default automatically")
	}
}

func TestCreateSpray_SecondOneIsNotDefault(t *testing.T) {
	s := newTestSprayStore(t)
	if _, err := s.CreateSpray(context.Background(), "First", 64, 64, []byte("png")); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := s.CreateSpray(context.Background(), "Second", 64, 64, []byte("png"))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.IsDefault {
		t.Fatal("expected the second spray to NOT be default")
	}
}

func TestCreateSpray_RejectsEmptyPNG(t *testing.T) {
	s := newTestSprayStore(t)
	if _, err := s.CreateSpray(context.Background(), "Empty", 64, 64, nil); err == nil {
		t.Fatal("expected an error for empty png data")
	}
}

func TestSetDefaultSpray_ClearsPreviousDefault(t *testing.T) {
	s := newTestSprayStore(t)
	first, err := s.CreateSpray(context.Background(), "First", 64, 64, []byte("png"))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := s.CreateSpray(context.Background(), "Second", 64, 64, []byte("png"))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := s.SetDefaultSpray(context.Background(), second.ID); err != nil {
		t.Fatalf("set default: %v", err)
	}
	gotFirst, err := s.GetSpray(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if gotFirst.IsDefault {
		t.Fatal("expected the first spray's default flag to be cleared when the second became default")
	}
	def, err := s.GetDefaultSpray(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def == nil || def.ID != second.ID {
		t.Fatalf("expected the second spray to be the real default, got %+v", def)
	}
}

func TestGetDefaultSpray_NilWhenNoneExist(t *testing.T) {
	s := newTestSprayStore(t)
	def, err := s.GetDefaultSpray(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if def != nil {
		t.Fatalf("expected nil default on a fresh store, got %+v", def)
	}
}

func TestDeleteSpray(t *testing.T) {
	s := newTestSprayStore(t)
	created, err := s.CreateSpray(context.Background(), "Doomed", 64, 64, []byte("png"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteSpray(context.Background(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetSpray(context.Background(), created.ID); err == nil {
		t.Fatal("expected spray to be gone after delete")
	}
}
