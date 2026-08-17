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
		Label:          "Renaissance oil painting",
		Subject:        "Master Chief (Halo)",
		Kind:           "surreal",
		EZPrompt:       "renaissance oil painting master chief halo",
		ExpandedPrompt: "a test expanded prompt",
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
	if got.Label != n.Label || got.Subject != n.Subject || got.Kind != n.Kind ||
		got.EZPrompt != n.EZPrompt || got.ExpandedPrompt != n.ExpandedPrompt || got.ImageFile != n.ImageFile {
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
	n := Node{Slug: "dup", Label: "One", Kind: "historical", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "dup.png"}
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

	if _, err := s.Create(Node{Slug: "older", Label: "Older", Kind: "historical", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "older.png", PublishedAt: older}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Node{Slug: "newer", Label: "Newer", Kind: "surreal", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "newer.png", PublishedAt: newer}); err != nil {
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
	if _, err := s.Create(Node{Slug: "no-tags", Label: "No Tags", Kind: "historical", EZPrompt: "p", ExpandedPrompt: "p", ImageFile: "no-tags.png"}); err != nil {
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

func TestStore_TwoSubjectsUnderSameLabel(t *testing.T) {
	// Regression for the "style is the subcategory, subject varies within
	// it" model: two nodes can share a Label (e.g. "Renaissance oil
	// painting") while differing only in Subject (baseball card vs Master
	// Chief), and both must round-trip independently.
	s := newTestStore(t)
	if _, err := s.Create(Node{Slug: "renaissance-baseball", Label: "Renaissance oil painting", Subject: "baseball card", Kind: "surreal", EZPrompt: "renaissance oil painting baseball card", ExpandedPrompt: "p1", ImageFile: "a.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Node{Slug: "renaissance-masterchief", Label: "Renaissance oil painting", Subject: "Master Chief (Halo)", Kind: "surreal", EZPrompt: "renaissance oil painting master chief halo", ExpandedPrompt: "p2", ImageFile: "b.png"}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	labels := map[string]int{}
	for _, n := range nodes {
		labels[n.Label]++
	}
	if labels["Renaissance oil painting"] != 2 {
		t.Errorf("expected both nodes to share the Label, got counts: %+v", labels)
	}
}
