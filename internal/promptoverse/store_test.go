package promptoverse

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "promptoverse.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore_CreateAndGetBySlug(t *testing.T) {
	s := newTestStore(t)
	n := Node{
		Slug:           "test-node",
		Label:          "Test Node",
		Kind:           "historical",
		TopLevelPrompt: "a test prompt",
		ImageFile:      "test-node.png",
		Tags:           Tags{"era": "1990s", "medium": "photo"},
	}
	id, err := s.Create(n)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected nonzero id")
	}

	got, err := s.GetBySlug("test-node")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.Label != n.Label || got.Kind != n.Kind || got.TopLevelPrompt != n.TopLevelPrompt || got.ImageFile != n.ImageFile {
		t.Errorf("unexpected node: %+v", got)
	}
	if got.Tags["era"] != "1990s" || got.Tags["medium"] != "photo" {
		t.Errorf("tags did not round-trip: %+v", got.Tags)
	}
	if got.PublishedAt.IsZero() {
		t.Error("expected PublishedAt to be auto-stamped")
	}
}

func TestStore_CreateDuplicateSlugFails(t *testing.T) {
	s := newTestStore(t)
	n := Node{Slug: "dup", Label: "One", Kind: "historical", TopLevelPrompt: "p", ImageFile: "dup.png"}
	if _, err := s.Create(n); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := s.Create(n); err == nil {
		t.Fatal("expected duplicate slug to fail")
	}
}

func TestStore_GetBySlugNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetBySlug("nope"); err == nil {
		t.Fatal("expected error for missing slug")
	}
}

func TestStore_ListOrdersByPublishedAtDesc(t *testing.T) {
	s := newTestStore(t)
	older := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()

	if _, err := s.Create(Node{Slug: "older", Label: "Older", Kind: "historical", TopLevelPrompt: "p", ImageFile: "older.png", PublishedAt: older}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Node{Slug: "newer", Label: "Newer", Kind: "surreal", TopLevelPrompt: "p", ImageFile: "newer.png", PublishedAt: newer}); err != nil {
		t.Fatal(err)
	}

	nodes, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].Slug != "newer" || nodes[1].Slug != "older" {
		t.Errorf("expected newest-first order, got %s then %s", nodes[0].Slug, nodes[1].Slug)
	}
}

func TestStore_EmptyTagsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "no-tags", Label: "No Tags", Kind: "historical", TopLevelPrompt: "p", ImageFile: "no-tags.png"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBySlug("no-tags")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("expected empty tags, got %+v", got.Tags)
	}
}
