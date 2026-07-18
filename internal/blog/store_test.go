package blog

import (
	"path/filepath"
	"testing"
)

func TestStore_CreateAndGetBySlug(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.Create(Post{Slug: "hello-world", Title: "Hello", Author: "Claude Code", Body: "First post."})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetBySlug("hello-world")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.Title != "Hello" || got.Author != "Claude Code" {
		t.Fatalf("got %+v", got)
	}
}

func TestStore_DuplicateSlugRejected(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.Create(Post{Slug: "dup", Title: "A", Author: "x", Body: "x"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.Create(Post{Slug: "dup", Title: "B", Author: "x", Body: "x"}); err == nil {
		t.Fatal("expected duplicate slug to be rejected")
	}
}

func TestStore_ListOrdersMostRecentFirst(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.Create(Post{Slug: "first", Title: "First", Author: "x", Body: "x"})
	s.Create(Post{Slug: "second", Title: "Second", Author: "x", Body: "x"})

	posts, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if posts[0].Slug != "second" {
		t.Fatalf("expected most recent first, got %q", posts[0].Slug)
	}
}

func TestStore_GetBySlugNotFound(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.GetBySlug("does-not-exist"); err == nil {
		t.Fatal("expected error for missing slug")
	}
}
