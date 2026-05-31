package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// bootstrapMigrationsTable creates the schema_migrations tracking table if it
// doesn't exist. Bob always runs this on startup before any other operation.
func bootstrapMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id           INT          NOT NULL AUTO_INCREMENT PRIMARY KEY,
		filename     VARCHAR(255) NOT NULL UNIQUE,
		sha256       CHAR(64)     NOT NULL,
		applied_at   TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
		applied_by   VARCHAR(64)  NOT NULL DEFAULT 'bob-agent',
		duration_ms  INT          NOT NULL DEFAULT 0
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	return err
}

// migrationFile describes a discovered .sql file in migrations/truestore/.
type migrationFile struct {
	Filename string
	Path     string
	SHA256   string
	Applied  bool
	AppliedAt *time.Time
}

// listMigrations scans the migrations directory and cross-references against
// schema_migrations to produce an ordered list with applied/pending status.
func listMigrations(db *sql.DB, idunaRoot string) ([]migrationFile, error) {
	dir := filepath.Join(idunaRoot, "migrations", "truestore")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("migrations directory not found: %s", dir)
		}
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	// Load applied migrations from DB.
	rows, err := db.Query(`SELECT filename, sha256, applied_at FROM schema_migrations ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]time.Time{}
	for rows.Next() {
		var fname, sha string
		var at time.Time
		if err := rows.Scan(&fname, &sha, &at); err != nil {
			return nil, err
		}
		applied[fname] = at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		h := fmt.Sprintf("%x", sha256.Sum256(content))
		mf := migrationFile{
			Filename: e.Name(),
			Path:     path,
			SHA256:   h,
		}
		if at, ok := applied[e.Name()]; ok {
			mf.Applied = true
			t := at
			mf.AppliedAt = &t
		}
		out = append(out, mf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, nil
}

// applyMigration executes a single migration file inside a transaction and
// records it in schema_migrations.
func applyMigration(db *sql.DB, mf migrationFile) error {
	content, err := os.ReadFile(mf.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", mf.Filename, err)
	}

	start := time.Now()

	// MySQL doesn't support multi-statement transactions for DDL in the same
	// way as Postgres, but we can still wrap DML. Split on semicolons and run
	// each statement individually; skip empty statements.
	statements := splitSQL(string(content))
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec statement in %s: %w\nSQL: %.200s", mf.Filename, err, stmt)
		}
	}

	durMS := int(time.Since(start).Milliseconds())
	_, err = db.Exec(`INSERT INTO schema_migrations (filename, sha256, applied_by, duration_ms)
		VALUES (?, ?, 'bob-agent', ?)
		ON DUPLICATE KEY UPDATE sha256=VALUES(sha256), applied_at=CURRENT_TIMESTAMP(6), duration_ms=VALUES(duration_ms)`,
		mf.Filename, mf.SHA256, durMS)
	return err
}

// splitSQL splits a SQL file on semicolons, respecting that some statements may
// contain semicolons inside string literals or comments. This is a best-effort
// split sufficient for well-formed DDL migration files.
func splitSQL(src string) []string {
	var stmts []string
	var cur strings.Builder
	inLineComment := false
	inBlockComment := false
	inString := false
	var stringChar rune

	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		switch {
		case inLineComment:
			if ch == '\n' {
				inLineComment = false
			}
			cur.WriteRune(ch)
		case inBlockComment:
			if ch == '*' && next == '/' {
				inBlockComment = false
				cur.WriteRune(ch)
				cur.WriteRune(next)
				i++
			} else {
				cur.WriteRune(ch)
			}
		case inString:
			cur.WriteRune(ch)
			if ch == stringChar && (i == 0 || runes[i-1] != '\\') {
				inString = false
			}
		case ch == '-' && next == '-':
			inLineComment = true
			cur.WriteRune(ch)
		case ch == '/' && next == '*':
			inBlockComment = true
			cur.WriteRune(ch)
		case ch == '\'' || ch == '"' || ch == '`':
			inString = true
			stringChar = ch
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

// ── Migration tools ───────────────────────────────────────────────────────────

func registerMigrationTools(d *ToolDispatcher, db *sql.DB, idunaRoot string) {
	d.Register(ToolDef{
		Name:        "migrate_status",
		Description: "List all migration files in migrations/truestore/ with their applied/pending status and SHA256. Call this first to understand the current migration state before running anything.",
		Parameters:  ToolParameters{Type: "object", Properties: map[string]ToolPropSchema{}},
	}, func(args map[string]any) (string, error) {
		migs, err := listMigrations(db, idunaRoot)
		if err != nil {
			return "", err
		}
		if len(migs) == 0 {
			return "no migration files found in migrations/truestore/", nil
		}
		var sb strings.Builder
		applied, pending := 0, 0
		for _, m := range migs {
			if m.Applied {
				applied++
				at := ""
				if m.AppliedAt != nil {
					at = m.AppliedAt.UTC().Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(&sb, "✓ APPLIED  %s  (applied %s)\n", m.Filename, at)
			} else {
				pending++
				fmt.Fprintf(&sb, "⋯ PENDING  %s\n", m.Filename)
			}
		}
		fmt.Fprintf(&sb, "\nTotal: %d applied, %d pending", applied, pending)
		return sb.String(), nil
	})

	d.Register(ToolDef{
		Name:        "migrate_run",
		Description: "Apply all pending migrations in filename order. Each migration is applied atomically; if one fails the run stops and the error is reported. Safe to call repeatedly — already-applied migrations are skipped.",
		Parameters:  ToolParameters{Type: "object", Properties: map[string]ToolPropSchema{}},
	}, func(args map[string]any) (string, error) {
		migs, err := listMigrations(db, idunaRoot)
		if err != nil {
			return "", err
		}
		var applied []string
		for _, m := range migs {
			if m.Applied {
				continue
			}
			if err := applyMigration(db, m); err != nil {
				return fmt.Sprintf("ERROR applying %s: %v\nApplied before failure: %v", m.Filename, err, applied), err
			}
			applied = append(applied, m.Filename)
		}
		if len(applied) == 0 {
			return "all migrations already applied — nothing to do", nil
		}
		return fmt.Sprintf("applied %d migration(s):\n%s", len(applied), strings.Join(applied, "\n")), nil
	})

	d.Register(ToolDef{
		Name:        "migrate_run_one",
		Description: "Apply a specific migration file by filename (e.g. '202602220001_device_auth.sql'). Will fail if already applied.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolPropSchema{
				"filename": {Type: "string", Description: "Exact filename from migrations/truestore/"},
			},
			Required: []string{"filename"},
		},
	}, func(args map[string]any) (string, error) {
		target, _ := args["filename"].(string)
		if target == "" {
			return "", fmt.Errorf("filename is required")
		}
		migs, err := listMigrations(db, idunaRoot)
		if err != nil {
			return "", err
		}
		for _, m := range migs {
			if m.Filename != target {
				continue
			}
			if m.Applied {
				return fmt.Sprintf("%s is already applied", target), nil
			}
			if err := applyMigration(db, m); err != nil {
				return "", fmt.Errorf("apply %s: %w", target, err)
			}
			return fmt.Sprintf("applied %s", target), nil
		}
		return "", fmt.Errorf("migration file not found: %s", target)
	})
}
