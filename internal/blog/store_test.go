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

func TestStore_Search_MatchesTitleOrBody(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.Create(Post{Slug: "duck-post", Title: "The Duck Still Hasn't Moved", Author: "Tyler", Body: "arena stats update"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(Post{Slug: "other-post", Title: "Something Else", Author: "Tyler", Body: "mentions a duck in passing"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(Post{Slug: "unrelated", Title: "Unrelated", Author: "Tyler", Body: "no match here"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, err := s.Search("duck", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 matches (title + body), got %d: %+v", len(results), results)
	}
}

func TestStore_Search_LimitRespected(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "blog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for i := 0; i < 5; i++ {
		if _, err := s.Create(Post{Slug: "post-" + string(rune('a'+i)), Title: "Matching Post", Author: "Tyler", Body: "body"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	results, err := s.Search("Matching", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected limit of 2, got %d", len(results))
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
