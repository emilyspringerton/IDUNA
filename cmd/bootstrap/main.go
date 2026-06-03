// cmd/bootstrap — IDUNA one-shot startup bootstrap.
//
// Narrow purpose: run DB migrations, seed system agents from config/agents.json,
// and generate agent API key secrets. Exits 0 on success, 1 on failure.
// No LLM, no HTTP server, no tool loop. This is a setup CLI, not an agent.
//
// Run once before starting IDUNA:
//
//	MYSQL_DSN="user:pass@tcp(host:3306)/iduna" go run ./cmd/bootstrap
//
// On success it writes var/agent-secrets.env with each agent's secret.
// Source that file before starting agents:
//
//	source var/agent-secrets.env
//
// Idempotent: safe to re-run. Already-applied migrations are skipped.
// Already-provisioned agent credentials are NOT rotated — only missing ones
// are generated. Pass -rotate to force regeneration of all secrets.
//
// Env vars:
//
//	MYSQL_DSN     — required; MySQL DSN e.g. "user:pass@tcp(host:3306)/dbname?parseTime=true"
//	IDUNA_ROOT    — path to IDUNA repo root (default: "." = working directory)
//	AGENTS_CONFIG — path to agents.json (default: IDUNA_ROOT/config/agents.json)

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	rotate := flag.Bool("rotate", false, "rotate all agent secrets (generates new secrets even if already provisioned)")
	dryRun := flag.Bool("dry-run", false, "print what would be done without modifying the database")
	flag.Parse()

	logger := log.New(os.Stdout, "bootstrap: ", log.LstdFlags|log.LUTC)

	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		logger.Fatal("MYSQL_DSN is required. Set it to your MySQL connection string, e.g.:\n  export MYSQL_DSN=\"user:pass@tcp(host:3306)/iduna?parseTime=true\"")
	}

	idunaRoot := envOr("IDUNA_ROOT", ".")
	agentsConfig := envOr("AGENTS_CONFIG", filepath.Join(idunaRoot, "config", "agents.json"))

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		logger.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(time.Minute)

	ctx := context.Background()

	if err := pingWithRetry(ctx, db, logger, 10, 2*time.Second); err != nil {
		logger.Fatalf("db unreachable: %v", err)
	}
	logger.Println("✓ database reachable")

	if *dryRun {
		logger.Println("dry-run mode: no changes will be made")
	}

	// Step 1: migrations.
	logger.Println("── step 1: migrations ──")
	if err := runMigrations(ctx, db, idunaRoot, *dryRun, logger); err != nil {
		logger.Fatalf("migrations failed: %v", err)
	}

	// Step 2: agent permissions.
	logger.Println("── step 2: agent permissions ──")
	cfg, err := loadAgentsConfig(agentsConfig)
	if err != nil {
		logger.Fatalf("load agents config %s: %v", agentsConfig, err)
	}

	if err := seedAgentPermissions(ctx, db, cfg, *dryRun, logger); err != nil {
		logger.Fatalf("seed agent permissions: %v", err)
	}

	// Step 3: agent secrets.
	logger.Println("── step 3: agent secrets ──")
	secrets, err := provisionSecrets(ctx, db, cfg, *rotate, *dryRun, logger)
	if err != nil {
		logger.Fatalf("provision secrets: %v", err)
	}

	if !*dryRun && len(secrets) > 0 {
		outDir := filepath.Join(idunaRoot, "var")
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			logger.Fatalf("create var dir: %v", err)
		}
		envPath := filepath.Join(outDir, "agent-secrets.env")
		if err := writeSecretsEnv(envPath, secrets); err != nil {
			logger.Fatalf("write secrets env: %v", err)
		}
		logger.Printf("✓ secrets written to %s", envPath)
		logger.Println("  source var/agent-secrets.env before starting agents")
	}

	logger.Println("── bootstrap complete ──")
}

// ── DB helpers ────────────────────────────────────────────────────────────────

func pingWithRetry(ctx context.Context, db *sql.DB, logger *log.Logger, attempts int, delay time.Duration) error {
	for i := 0; i < attempts; i++ {
		if err := db.PingContext(ctx); err == nil {
			return nil
		} else if i < attempts-1 {
			logger.Printf("  waiting for db (attempt %d/%d): %v", i+1, attempts, err)
			time.Sleep(delay)
		} else {
			return err
		}
	}
	return nil
}

// ── Migrations ────────────────────────────────────────────────────────────────

func runMigrations(ctx context.Context, db *sql.DB, idunaRoot string, dryRun bool, logger *log.Logger) error {
	if !dryRun {
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			id           INT          NOT NULL AUTO_INCREMENT PRIMARY KEY,
			filename     VARCHAR(255) NOT NULL UNIQUE,
			sha256       CHAR(64)     NOT NULL,
			applied_at   TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
			applied_by   VARCHAR(64)  NOT NULL DEFAULT 'bootstrap',
			duration_ms  INT          NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
	}

	dir := filepath.Join(idunaRoot, "migrations", "truestore")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	// Load applied set.
	applied := map[string]bool{}
	if !dryRun {
		rows, err := db.QueryContext(ctx, `SELECT filename FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("query schema_migrations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				return err
			}
			applied[f] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	pending := 0
	for _, fname := range files {
		if applied[fname] {
			logger.Printf("  ✓ %s (already applied)", fname)
			continue
		}
		pending++
		path := filepath.Join(dir, fname)
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", fname, err)
		}
		h := fmt.Sprintf("%x", sha256.Sum256(content))
		if dryRun {
			logger.Printf("  ⋯ %s (would apply)", fname)
			continue
		}
		start := time.Now()
		for _, stmt := range splitSQL(string(content)) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("exec %s: %w\nSQL: %.200s", fname, err, stmt)
			}
		}
		durMS := int(time.Since(start).Milliseconds())
		_, err = db.ExecContext(ctx,
			`INSERT INTO schema_migrations (filename, sha256, applied_by, duration_ms) VALUES (?, ?, 'bootstrap', ?)
			 ON DUPLICATE KEY UPDATE sha256=VALUES(sha256), applied_at=CURRENT_TIMESTAMP(6), duration_ms=VALUES(duration_ms)`,
			fname, h, durMS)
		if err != nil {
			return fmt.Errorf("record %s: %w", fname, err)
		}
		logger.Printf("  ✓ %s applied (%dms)", fname, durMS)
	}
	if pending == 0 {
		logger.Println("  all migrations already applied")
	}
	return nil
}

// splitSQL splits a SQL script on statement-terminating semicolons, handling
// string literals and -- line comments. Good enough for well-formed DDL/DML.
func splitSQL(src string) []string {
	var stmts []string
	var cur strings.Builder
	inLine, inBlock, inStr := false, false, false
	var strChar rune
	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch {
		case inLine:
			if ch == '\n' {
				inLine = false
			}
			cur.WriteRune(ch)
		case inBlock:
			if ch == '*' && next == '/' {
				inBlock = false
				cur.WriteRune(ch)
				cur.WriteRune(next)
				i++
			} else {
				cur.WriteRune(ch)
			}
		case inStr:
			cur.WriteRune(ch)
			if ch == strChar && (i == 0 || runes[i-1] != '\\') {
				inStr = false
			}
		case ch == '-' && next == '-':
			inLine = true
			cur.WriteRune(ch)
		case ch == '/' && next == '*':
			inBlock = true
			cur.WriteRune(ch)
		case ch == '\'' || ch == '"' || ch == '`':
			inStr = true
			strChar = ch
			cur.WriteRune(ch)
		case ch == ';':
			stmts = append(stmts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(ch)
		}
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		stmts = append(stmts, t)
	}
	return stmts
}

// ── Agent config ──────────────────────────────────────────────────────────────

type agentDef struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type agentsConfig struct {
	SystemUserID string     `json:"system_user_id"`
	Agents       []agentDef `json:"agents"`
}

func loadAgentsConfig(path string) (*agentsConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg agentsConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.SystemUserID == "" {
		return nil, fmt.Errorf("system_user_id is required in agents config")
	}
	return &cfg, nil
}

// ── Permission seeding ────────────────────────────────────────────────────────

func seedAgentPermissions(ctx context.Context, db *sql.DB, cfg *agentsConfig, dryRun bool, logger *log.Logger) error {
	// Build permission name → ID map from DB.
	permMap := map[string]string{}
	if !dryRun {
		rows, err := db.QueryContext(ctx, `SELECT id, name FROM permissions`)
		if err != nil {
			return fmt.Errorf("load permissions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			permMap[name] = id
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	for _, a := range cfg.Agents {
		if len(a.Permissions) == 0 {
			continue
		}
		for _, permName := range a.Permissions {
			permID, ok := permMap[permName]
			if !ok {
				if dryRun {
					logger.Printf("  ⋯ %s ← %s (permission not found — would fail)", a.Name, permName)
					continue
				}
				return fmt.Errorf("permission %q not found in DB (agent %s) — run migrations first", permName, a.Name)
			}
			if dryRun {
				logger.Printf("  ⋯ %s ← %s", a.Name, permName)
				continue
			}
			_, err := db.ExecContext(ctx,
				`INSERT IGNORE INTO agent_permissions (agent_id, permission_id) VALUES (?, ?)`,
				a.ID, permID)
			if err != nil {
				return fmt.Errorf("grant %s to %s: %w", permName, a.Name, err)
			}
		}
		if !dryRun {
			logger.Printf("  ✓ %s — %d permission(s) ensured", a.Name, len(a.Permissions))
		}
	}
	return nil
}

// ── Secret provisioning ───────────────────────────────────────────────────────

type agentSecret struct {
	Name      string
	ID        string
	Plaintext string
	Generated bool
}

func provisionSecrets(ctx context.Context, db *sql.DB, cfg *agentsConfig, rotate, dryRun bool, logger *log.Logger) ([]agentSecret, error) {
	var secrets []agentSecret

	for _, a := range cfg.Agents {
		// Check whether agent already has a credential.
		hasCredential := false
		if !dryRun {
			var hash sql.NullString
			err := db.QueryRowContext(ctx,
				`SELECT api_key_hash FROM agents WHERE id = ?`, a.ID).Scan(&hash)
			if err == sql.ErrNoRows {
				logger.Printf("  ✗ %s (ID %s) not found in agents table — did migrations run?", a.Name, a.ID)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("check credential for %s: %w", a.Name, err)
			}
			hasCredential = hash.Valid && hash.String != ""
		}

		if hasCredential && !rotate {
			logger.Printf("  ✓ %s — credential already provisioned (pass -rotate to regenerate)", a.Name)
			continue
		}

		plaintext, err := generateSecret(32)
		if err != nil {
			return nil, fmt.Errorf("generate secret for %s: %w", a.Name, err)
		}

		if dryRun {
			action := "would provision"
			if hasCredential {
				action = "would rotate"
			}
			logger.Printf("  ⋯ %s — %s credential", a.Name, action)
			continue
		}

		hash := hashSecret(a.ID, plaintext)
		_, err = db.ExecContext(ctx,
			`UPDATE agents SET api_key_hash = ?, updated_at = ? WHERE id = ?`,
			hash, time.Now().UTC(), a.ID)
		if err != nil {
			return nil, fmt.Errorf("store credential for %s: %w", a.Name, err)
		}

		action := "provisioned"
		if hasCredential {
			action = "rotated"
		}
		logger.Printf("  ✓ %s — credential %s", a.Name, action)
		secrets = append(secrets, agentSecret{Name: a.Name, ID: a.ID, Plaintext: plaintext, Generated: true})
	}
	return secrets, nil
}

// generateSecret returns a cryptographically random hex string of byteLen bytes.
func generateSecret(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashSecret mirrors the logic in internal/store/mysql.go: SHA-256 of agentID+plaintext.
// The agentID acts as a per-agent salt preventing precomputation across agents.
func hashSecret(agentID, plaintext string) string {
	h := sha256.New()
	h.Write([]byte(agentID))
	h.Write([]byte(":"))
	h.Write([]byte(plaintext))
	return hex.EncodeToString(h.Sum(nil))
}

// writeSecretsEnv writes agent secrets as shell-sourceable env vars.
// The file is created with 0600 permissions (owner-only).
// Format: IDUNA_SECRET_<UPPERNAME>=<plaintext>
func writeSecretsEnv(path string, secrets []agentSecret) error {
	var sb strings.Builder
	sb.WriteString("# IDUNA agent secrets — generated by cmd/bootstrap\n")
	sb.WriteString("# Source this file before starting agents: source var/agent-secrets.env\n")
	sb.WriteString("# DO NOT COMMIT THIS FILE. It is git-ignored by default.\n")
	sb.WriteString(fmt.Sprintf("# Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	for _, s := range secrets {
		envKey := "IDUNA_SECRET_" + strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_"))
		// Also write the agent name as the env var name agents expect.
		sb.WriteString(fmt.Sprintf("export %s=%s\n", envKey, s.Plaintext))
	}

	// Write convenience mappings that match agent env var conventions.
	sb.WriteString("\n# Convenience aliases (match agent IDUNA_AGENT_SECRET env var convention):\n")
	for _, s := range secrets {
		envKey := "IDUNA_SECRET_" + strings.ToUpper(strings.ReplaceAll(s.Name, "-", "_"))
		sb.WriteString(fmt.Sprintf("# Agent %s: set IDUNA_AGENT_SECRET=${%s} when starting that agent\n", s.Name, envKey))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
