package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// registerDBTools wires all DB inspection and migration tools onto the dispatcher.
func registerDBTools(d *ToolDispatcher, db *sql.DB, idunaRoot string) {
	registerMigrationTools(d, db, idunaRoot)
	registerSchemaTools(d, db)
}

func registerSchemaTools(d *ToolDispatcher, db *sql.DB) {
	d.Register(ToolDef{
		Name:        "db_status",
		Description: "Ping the database and return connection health, schema_migrations table presence, and current database name. Always call this first in a health sweep.",
		Parameters:  ToolParameters{Type: "object", Properties: map[string]ToolPropSchema{}},
	}, func(args map[string]any) (string, error) {
		if err := db.Ping(); err != nil {
			return "", fmt.Errorf("db ping failed: %w", err)
		}
		var dbName string
		_ = db.QueryRow("SELECT DATABASE()").Scan(&dbName)

		var migTableExists int
		_ = db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = 'schema_migrations'`).Scan(&migTableExists)

		var migCount int
		if migTableExists > 0 {
			_ = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migCount)
		}

		status := fmt.Sprintf("db_name=%s  schema_migrations_table=%v  migrations_applied=%d",
			dbName, migTableExists > 0, migCount)
		return status, nil
	})

	d.Register(ToolDef{
		Name:        "schema_tables",
		Description: "List all tables in the current database with their engine, row count estimate, and creation time.",
		Parameters:  ToolParameters{Type: "object", Properties: map[string]ToolPropSchema{}},
	}, func(args map[string]any) (string, error) {
		rows, err := db.Query(`SELECT
				table_name,
				engine,
				COALESCE(table_rows, 0),
				COALESCE(data_length + index_length, 0),
				COALESCE(create_time, '?')
			FROM information_schema.tables
			WHERE table_schema = DATABASE()
			ORDER BY table_name`)
		if err != nil {
			return "", err
		}
		defer rows.Close()

		var sb strings.Builder
		count := 0
		for rows.Next() {
			var name, engine, created string
			var rowEst, sizeBytes int64
			if err := rows.Scan(&name, &engine, &rowEst, &sizeBytes, &created); err != nil {
				continue
			}
			fmt.Fprintf(&sb, "%-40s  engine=%-8s  ~rows=%-8d  size=%-10d  created=%s\n",
				name, engine, rowEst, sizeBytes, created)
			count++
		}
		if count == 0 {
			return "no tables found in current database", nil
		}
		return fmt.Sprintf("Tables (%d):\n%s", count, sb.String()), nil
	})

	d.Register(ToolDef{
		Name:        "schema_describe",
		Description: "Describe a specific table: columns with types/nullability/defaults and all indexes. Use this to verify a migration was applied correctly.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolPropSchema{
				"table": {Type: "string", Description: "Table name to describe"},
			},
			Required: []string{"table"},
		},
	}, func(args map[string]any) (string, error) {
		table, _ := args["table"].(string)
		if table == "" {
			return "", fmt.Errorf("table name is required")
		}
		// Validate table name to prevent injection
		for _, ch := range table {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
				return "", fmt.Errorf("invalid table name: %q", table)
			}
		}

		// Columns
		colRows, err := db.Query(`SELECT
				column_name, column_type, is_nullable, column_default, extra
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ?
			ORDER BY ordinal_position`, table)
		if err != nil {
			return "", err
		}
		defer colRows.Close()

		var sb strings.Builder
		fmt.Fprintf(&sb, "Table: %s\n\nCOLUMNS:\n", table)
		colCount := 0
		for colRows.Next() {
			var name, colType, nullable string
			var def, extra sql.NullString
			if err := colRows.Scan(&name, &colType, &nullable, &def, &extra); err != nil {
				continue
			}
			defStr := "NULL"
			if def.Valid {
				defStr = def.String
			}
			extraStr := ""
			if extra.Valid && extra.String != "" {
				extraStr = "  [" + extra.String + "]"
			}
			fmt.Fprintf(&sb, "  %-30s  %-40s  nullable=%-3s  default=%s%s\n",
				name, colType, nullable, defStr, extraStr)
			colCount++
		}
		if colCount == 0 {
			return fmt.Sprintf("table %q not found or has no columns", table), nil
		}

		// Indexes
		idxRows, err := db.Query(`SELECT
				index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index), non_unique
			FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = ?
			GROUP BY index_name, non_unique
			ORDER BY index_name`, table)
		if err == nil {
			defer idxRows.Close()
			fmt.Fprintf(&sb, "\nINDEXES:\n")
			for idxRows.Next() {
				var name, cols string
				var nonUnique int
				if err := idxRows.Scan(&name, &cols, &nonUnique); err != nil {
					continue
				}
				uniqueStr := ""
				if nonUnique == 0 {
					uniqueStr = " UNIQUE"
				}
				fmt.Fprintf(&sb, "  %-30s  columns=(%s)%s\n", name, cols, uniqueStr)
			}
		}
		return sb.String(), nil
	})

	d.Register(ToolDef{
		Name:        "db_row_counts",
		Description: "Return approximate row counts for the key IDUNA IAM tables. A quick way to verify data is flowing correctly after migrations.",
		Parameters:  ToolParameters{Type: "object", Properties: map[string]ToolPropSchema{}},
	}, func(args map[string]any) (string, error) {
		tables := []string{
			"users", "roles", "permissions",
			"user_roles", "role_permissions",
			"agents", "agent_permissions",
			"iam_event_stream", "schema_migrations",
			"device_auth_requests", "exchange_codes", "event_store",
		}
		var sb strings.Builder
		for _, t := range tables {
			var count int
			err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", t)).Scan(&count)
			if err != nil {
				fmt.Fprintf(&sb, "  %-30s  ERROR: %v\n", t, err)
			} else {
				fmt.Fprintf(&sb, "  %-30s  %d rows\n", t, count)
			}
		}
		return sb.String(), nil
	})

	d.Register(ToolDef{
		Name:        "db_query",
		Description: "Run a read-only SELECT query against the IDUNA database. Only SELECT statements are allowed — use this to inspect data, verify migrations, or diagnose issues.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolPropSchema{
				"sql": {Type: "string", Description: "A SELECT query to execute (read-only)"},
			},
			Required: []string{"sql"},
		},
	}, func(args map[string]any) (string, error) {
		query, _ := args["sql"].(string)
		query = strings.TrimSpace(query)
		if query == "" {
			return "", fmt.Errorf("sql is required")
		}
		// Safety: only allow SELECT statements
		upper := strings.ToUpper(query)
		for _, banned := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "TRUNCATE", "GRANT", "REVOKE", "REPLACE"} {
			if strings.HasPrefix(upper, banned) || strings.Contains(upper, "\n"+banned) || strings.Contains(upper, " "+banned+" ") || strings.Contains(upper, ";"+banned) {
				return "", fmt.Errorf("only SELECT statements are allowed; got keyword: %s", banned)
			}
		}
		if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "SHOW") && !strings.HasPrefix(upper, "DESCRIBE") && !strings.HasPrefix(upper, "EXPLAIN") {
			return "", fmt.Errorf("only SELECT/SHOW/DESCRIBE/EXPLAIN statements are allowed")
		}

		rows, err := db.Query(query)
		if err != nil {
			return "", fmt.Errorf("query error: %w", err)
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			return "", err
		}

		var sb strings.Builder
		fmt.Fprintln(&sb, strings.Join(cols, "\t"))
		fmt.Fprintln(&sb, strings.Repeat("-", len(strings.Join(cols, "\t"))))

		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}

		rowCount := 0
		for rows.Next() {
			if rowCount >= 200 {
				fmt.Fprintln(&sb, "[truncated at 200 rows]")
				break
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				if v == nil {
					parts[i] = "NULL"
				} else {
					parts[i] = fmt.Sprintf("%v", v)
				}
			}
			fmt.Fprintln(&sb, strings.Join(parts, "\t"))
			rowCount++
		}
		if rowCount == 0 {
			return "(no rows returned)", nil
		}
		return sb.String(), nil
	})
}
