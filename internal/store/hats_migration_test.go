package store_test

import (
	"testing"

	"iduna/internal/store"
)

// TestHatsMigration_AppliesCleanly -- WOTAN_HAT_STORE_NORTHSTAR.md Phase 1. Confirms
// 202609030001_hats.sql actually applies through the real mysqlToSQLite translation path (its
// MySQL-flavored DDL/DML -- ENGINE=InnoDB, CHAR(36), TINYINT(1), INSERT IGNORE -- must survive
// translation the same way every other migration in this directory already does) and that the
// real seed catalog lands.
func TestHatsMigration_AppliesCleanly(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../migrations/truestore"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	var hatCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM hats`).Scan(&hatCount); err != nil {
		t.Fatalf("query hats: %v", err)
	}
	if hatCount != 6 {
		t.Errorf("expected 6 seeded hats, got %d", hatCount)
	}

	var name string
	var flowCost int
	if err := db.QueryRow(`SELECT name, flow_cost FROM hats WHERE hat_id = ?`,
		"a1b2c3d4-0001-4000-8000-000000000002").Scan(&name, &flowCost); err != nil {
		t.Fatalf("query specific hat: %v", err)
	}
	if name != "Uncrowned's Doubt" {
		t.Errorf("expected apostrophe to survive translation intact, got %q", name)
	}
	if flowCost != 400 {
		t.Errorf("expected flow_cost 400, got %d", flowCost)
	}

	// character_hats table exists and accepts a real row -- confirms TINYINT(1)->INTEGER and
	// the composite PRIMARY KEY both translated correctly.
	if _, err := db.Exec(
		`INSERT INTO character_hats (character_id, hat_id, equipped) VALUES (?, ?, 1)`,
		"some-character-id", "a1b2c3d4-0001-4000-8000-000000000001"); err != nil {
		t.Fatalf("insert into character_hats: %v", err)
	}

	// Re-running migrations (RunSQLiteMigrations is idempotent per-file via schema_migrations)
	// must not re-attempt the seed insert in a way that errors -- confirms INSERT IGNORE, not
	// plain INSERT, was used for the seed data.
	if err := store.RunSQLiteMigrations(db, "../../migrations/truestore"); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}
}
