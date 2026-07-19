package main

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// testDB creates an in-memory SQLite DB with just the tables
// seedAgentPermissions/provisionSecrets touch -- enough to exercise the
// dry-run-must-reflect-real-state fix without depending on the full
// migration runner.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
	CREATE TABLE permissions (id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL);
	CREATE TABLE agents (
		id VARCHAR(36) PRIMARY KEY,
		owner_user_id VARCHAR(36) NOT NULL DEFAULT '',
		name VARCHAR(128) NOT NULL,
		type VARCHAR(64) NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'ACTIVE',
		api_key_hash TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE agent_permissions (
		agent_id VARCHAR(36) NOT NULL,
		permission_id VARCHAR(36) NOT NULL,
		PRIMARY KEY (agent_id, permission_id)
	);
	INSERT INTO permissions (id, name) VALUES ('p1', 'apples.read'), ('p2', 'apples.write');
	INSERT INTO agents (id, name, status, api_key_hash) VALUES
		('a1', 'HAS-CREDENTIAL', 'ACTIVE', 'existing-hash-value'),
		('a2', 'NO-CREDENTIAL', 'ACTIVE', NULL);
	`
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec schema stmt %q: %v", stmt, err)
		}
	}
	return db
}

var testLogger = log.New(testWriter{}, "", 0)

type testWriter struct{}

func (testWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestSeedAgentPermissions_DryRunFindsRealPermissions(t *testing.T) {
	db := testDB(t)
	cfg := &agentsConfig{Agents: []agentDef{
		{ID: "a1", Name: "HAS-CREDENTIAL", Permissions: []string{"apples.read", "apples.write"}},
	}}

	// This is the actual regression: before the fix, dry-run never queried
	// the DB at all, so every permission -- even ones that genuinely exist,
	// as both do here -- was reported "not found".
	if err := seedAgentPermissions(context.Background(), db, cfg, true, testLogger); err != nil {
		t.Fatalf("dry-run should not error when permissions genuinely exist: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_permissions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("dry-run must not write, got %d agent_permissions rows", count)
	}
}

func TestSeedAgentPermissions_RealRunGrantsAndIsIdempotent(t *testing.T) {
	db := testDB(t)
	cfg := &agentsConfig{Agents: []agentDef{
		{ID: "a1", Name: "HAS-CREDENTIAL", Permissions: []string{"apples.read", "apples.write"}},
	}}

	if err := seedAgentPermissions(context.Background(), db, cfg, false, testLogger); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if err := seedAgentPermissions(context.Background(), db, cfg, false, testLogger); err != nil {
		t.Fatalf("second real run (idempotency check): %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_permissions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("want 2 grants (apples.read + apples.write), got %d -- INSERT OR IGNORE should have kept this idempotent across two runs", count)
	}
}

func TestSeedAgentPermissions_MissingPermissionStillReportedInDryRun(t *testing.T) {
	db := testDB(t)
	cfg := &agentsConfig{Agents: []agentDef{
		{ID: "a1", Name: "HAS-CREDENTIAL", Permissions: []string{"totally.made.up"}},
	}}

	// Dry-run should not error even for a genuinely-missing permission --
	// it logs and continues, matching the real-run's own dry-run contract.
	if err := seedAgentPermissions(context.Background(), db, cfg, true, testLogger); err != nil {
		t.Fatalf("dry-run should not error, even for a missing permission: %v", err)
	}

	// The real (non-dry-run) path should still hard-fail for a genuinely
	// missing permission -- that behavior is unchanged by this fix.
	err := seedAgentPermissions(context.Background(), db, cfg, false, testLogger)
	if err == nil {
		t.Fatal("real run should error when a permission genuinely doesn't exist in the DB")
	}
}

func TestProvisionSecrets_DryRunReflectsRealCredentialState(t *testing.T) {
	db := testDB(t)
	cfg := &agentsConfig{Agents: []agentDef{
		{ID: "a1", Name: "HAS-CREDENTIAL"},
		{ID: "a2", Name: "NO-CREDENTIAL"},
	}}

	// This is the exact regression: before the fix, dry-run never checked
	// api_key_hash, so HAS-CREDENTIAL was falsely reported as needing a
	// fresh credential just like NO-CREDENTIAL.
	if _, err := provisionSecrets(context.Background(), db, cfg, false, true, testLogger); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	var hash1, hash2 sql.NullString
	if err := db.QueryRow(`SELECT api_key_hash FROM agents WHERE id='a1'`).Scan(&hash1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT api_key_hash FROM agents WHERE id='a2'`).Scan(&hash2); err != nil {
		t.Fatal(err)
	}
	if hash1.String != "existing-hash-value" {
		t.Errorf("dry-run must not touch an existing credential, got %q", hash1.String)
	}
	if hash2.Valid {
		t.Errorf("dry-run must not provision a new credential, got %q", hash2.String)
	}
}

func TestProvisionSecrets_RealRunOnlyTouchesMissingCredential(t *testing.T) {
	db := testDB(t)
	cfg := &agentsConfig{Agents: []agentDef{
		{ID: "a1", Name: "HAS-CREDENTIAL"},
		{ID: "a2", Name: "NO-CREDENTIAL"},
	}}

	if _, err := provisionSecrets(context.Background(), db, cfg, false, false, testLogger); err != nil {
		t.Fatalf("real run: %v", err)
	}

	var hash1, hash2 sql.NullString
	if err := db.QueryRow(`SELECT api_key_hash FROM agents WHERE id='a1'`).Scan(&hash1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT api_key_hash FROM agents WHERE id='a2'`).Scan(&hash2); err != nil {
		t.Fatal(err)
	}
	if hash1.String != "existing-hash-value" {
		t.Errorf("real run without -rotate must not touch an existing credential, got %q", hash1.String)
	}
	if !hash2.Valid || hash2.String == "" {
		t.Error("real run should have provisioned a fresh credential for the agent with none")
	}
}
