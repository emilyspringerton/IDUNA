// cmd/create-admin-agent — provisions a new human-operator agent with the
// iduna.admin permission, for signing into the Back Office at /admin/login
// (that form authenticates agent_name + agent_secret, same as any other
// IDUNA agent -- see internal/http/handlers/admin_login.go).
//
// One-shot CLI, not a long-running service. Talks to the same embedded
// SQLite database the running iduna.service uses (SQLITE_PATH / IDUNA_ROOT
// env vars, same resolution main.go's own embedded-mode branch uses).
//
// Usage:
//
//	go run ./cmd/create-admin-agent -name EDDY
//
// Prints the plaintext secret ONCE -- it is never retrievable again, only
// its hash is persisted (same one-time-reveal contract as the Back Office's
// own "regenerate secret" button, admin.go's agentAction "secret" case).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"iduna/internal/store"
)

// systemUserID is config/agents.json's own well-known owner for agents not
// tied to a specific real human user record (its own top-level
// "system_user_id" field) -- reused here rather than inventing a second
// placeholder convention.
const systemUserID = "00000000-0000-4000-8000-000000000001"

func main() {
	name := flag.String("name", "", "agent name to create (required)")
	dbPath := flag.String("db", "", "sqlite db path (default: $SQLITE_PATH or <IDUNA_ROOT>/var/iduna.db)")
	secretLen := flag.Int("secret-bytes", 32, "random secret length in bytes (hex-encoded, so the printed secret is 2x this many characters)")
	flag.Parse()
	if *name == "" {
		fmt.Fprintln(os.Stderr, "usage: create-admin-agent -name <NAME>")
		os.Exit(1)
	}

	idunaRoot := envOr("IDUNA_ROOT", ".")
	path := *dbPath
	if path == "" {
		path = envOr("SQLITE_PATH", filepath.Join(idunaRoot, "var", "iduna.db"))
	}

	db, err := store.OpenSQLite(path)
	if err != nil {
		log.Fatalf("open sqlite %s: %v", path, err)
	}
	defer db.Close()
	s := store.NewSQLiteStore(db)

	ctx := context.Background()
	operator := "cmd/create-admin-agent"

	agent, err := s.CreateAgent(ctx, systemUserID, *name, "human_operator", operator)
	if err != nil {
		log.Fatalf("create agent %q: %v", *name, err)
	}

	secretBytes := make([]byte, *secretLen)
	if _, err := rand.Read(secretBytes); err != nil {
		log.Fatalf("generate secret: %v", err)
	}
	secret := hex.EncodeToString(secretBytes)

	if err := s.SetAgentCredential(ctx, agent.ID, secret, operator); err != nil {
		log.Fatalf("set credential: %v", err)
	}
	if err := s.GrantAgentPermission(ctx, agent.ID, "iduna.admin", operator); err != nil {
		log.Fatalf("grant iduna.admin: %v", err)
	}

	fmt.Printf("agent_id=%s\n", agent.ID)
	fmt.Printf("agent_name=%s\n", *name)
	fmt.Printf("agent_secret=%s\n", secret)
	fmt.Println("Store this secret now -- it cannot be retrieved again (only its hash is kept).")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
