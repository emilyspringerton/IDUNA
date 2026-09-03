package store_test

import (
	"context"
	"testing"
	"time"

	"iduna/internal/auth"
	"iduna/internal/store"
)

func setupSearchStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../migrations/truestore"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return store.NewSQLiteStore(db)
}

// TestSearchApples_MatchesTitleOrBody -- real, new capability (kanban card 1111, "IDUNA UNIFIED
// SEARCH INTERFACE"): ListApples only ever filtered by exact agent_id/source_repo/apple_type;
// there was no free-text search over apple content at all before this.
func TestSearchApples_MatchesTitleOrBody(t *testing.T) {
	s := setupSearchStore(t)
	ctx := context.Background()

	mustAppend := func(title, body string) {
		if _, err := s.AppendApple(ctx, auth.AppleRecord{
			AgentID: "test-agent", SourceRepo: "TESTREPO", AppleType: "completion",
			Title: title, Body: body, RecordedAt: time.Now(),
		}); err != nil {
			t.Fatalf("AppendApple: %v", err)
		}
	}
	mustAppend("Fixed the kanban board", "A real, detailed body about the kanban board fix.")
	mustAppend("Unrelated apple", "Nothing to do with the search term at all.")
	mustAppend("Another title", "This one mentions the kanban board deep in its own body text.")

	// Real match via TITLE.
	results, err := s.SearchApples(ctx, "kanban board", 50)
	if err != nil {
		t.Fatalf("SearchApples: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 real matches (one by title, one by body), got %d: %+v", len(results), results)
	}

	// Real, case-insensitive match.
	results, err = s.SearchApples(ctx, "KANBAN", 50)
	if err != nil {
		t.Fatalf("SearchApples (uppercase): %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected case-insensitive matching, got %d results", len(results))
	}

	// A real miss.
	results, err = s.SearchApples(ctx, "no-such-term-anywhere", 50)
	if err != nil {
		t.Fatalf("SearchApples (miss): %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected zero matches for a genuinely absent term, got %d", len(results))
	}
}

// TestSearchApples_LimitRespected -- real limit clamping, matching ListApples's own established
// convention (0 or negative defaults to 50, anything above 500 clamps to 500).
func TestSearchApples_LimitRespected(t *testing.T) {
	s := setupSearchStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.AppendApple(ctx, auth.AppleRecord{
			AgentID: "a", SourceRepo: "R", AppleType: "completion",
			Title: "shared-term apple", Body: "body", RecordedAt: time.Now(),
		}); err != nil {
			t.Fatalf("AppendApple: %v", err)
		}
	}
	results, err := s.SearchApples(ctx, "shared-term", 2)
	if err != nil {
		t.Fatalf("SearchApples: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected exactly 2 results respecting the real limit, got %d", len(results))
	}
}
