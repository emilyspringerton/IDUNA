package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/util"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements IAMStore against an embedded SQLite database.
// It is the zero-ops backend: no external process, no DSN, no human required.
//
// The schema is applied via RunSQLiteMigrations, which translates the canonical
// MySQL migrations in migrations/truestore/ to SQLite-compatible SQL at startup.
//
// Upgrade path: the IAMStore interface is identical to MySQLStore. When you're
// ready for real MySQL, set MYSQL_DSN and the application switches automatically.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) a SQLite database at the given path.
// Pass ":memory:" for tests or ephemeral use.
func OpenSQLite(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	// SQLite is not safe for concurrent writers; serialize writes through one connection.
	db.SetMaxOpenConns(1)
	return db, nil
}

// NewSQLiteStore wraps an open SQLite *sql.DB.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// RunSQLiteMigrations applies all pending migrations from migrationsDir to the
// SQLite database, translating MySQL DDL to SQLite-compatible SQL on the fly.
// It is idempotent: already-applied migrations (tracked by filename in
// schema_migrations) are skipped.
func RunSQLiteMigrations(db *sql.DB, migrationsDir string) error {
	// Bootstrap the migrations tracking table itself.
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT NOT NULL PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", migrationsDir, err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}

		var applied bool
		err := db.QueryRow(`SELECT 1 FROM schema_migrations WHERE filename=?`, e.Name()).Scan(&applied)
		if err == nil {
			continue // already applied
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", e.Name(), err)
		}

		raw, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}

		translated := mysqlToSQLite(string(raw))

		stmts := splitStatements(translated)
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("apply migration %s: %w\nSQL: %s", e.Name(), err, stmt)
			}
		}

		_, err = db.Exec(`INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)`,
			e.Name(), time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("record migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// --- IAMStore implementation ---

func (s *SQLiteStore) GetOrCreateUserByGoogleSubject(ctx context.Context, googleSub, email string) (*auth.User, bool, error) {
	u, err := s.sqliteGetUserByGoogleSubject(ctx, googleSub)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	id, err := util.NewUUID()
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO users
		 (id, email, google_subject, status, roles_json, honor_accepted_current, honor_code_sha, honor_code_version, created_at, updated_at)
		 VALUES (?, ?, ?, 'PENDING', json_array(), 0, '', 0, ?, ?)`,
		id, email, googleSub, now, now,
	)
	if err != nil {
		u2, err2 := s.sqliteGetUserByGoogleSubject(ctx, googleSub)
		if err2 == nil {
			return u2, false, nil
		}
		return nil, false, err
	}

	payload, _ := json.Marshal(map[string]string{"email": email, "google_subject": googleSub})
	_ = s.AppendIAMEvent(ctx, "UserCreated", "USER", id, "", payload)

	u, err = s.sqliteGetUserByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return u, true, nil
}

func (s *SQLiteStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	return s.sqliteGetUserByID(ctx, id)
}

func (s *SQLiteStore) GetEffectivePermissions(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT p.name
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p       ON p.id = rp.permission_id
		WHERE ur.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		perms = append(perms, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Append cap.query.full when the user has an active Emily+ subscription.
	sub, _ := s.GetUserSubscription(ctx, userID)
	if sub.IsActive() {
		perms = append(perms, "cap.query.full")
	}

	sort.Strings(perms)
	return perms, nil
}

func (s *SQLiteStore) AppendIAMEvent(ctx context.Context, eventType, aggregateType, aggregateID, operatorID string, payload []byte) error {
	if payload == nil {
		payload = []byte("null")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO iam_event_stream
		 (event_type, aggregate_type, aggregate_id, operator_id, payload, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		eventType, aggregateType, aggregateID, operatorID, payload, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) UpdateUserStatus(ctx context.Context, userID, status, operatorID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().UTC().Format(time.RFC3339Nano), userID,
	)
	if err != nil {
		return err
	}
	eventType := "UserStatusChanged"
	switch status {
	case "SUSPENDED":
		eventType = "UserSuspended"
	case "ACTIVE":
		eventType = "UserActivated"
	}
	payload, _ := json.Marshal(map[string]string{"status": status})
	_ = s.AppendIAMEvent(ctx, eventType, "USER", userID, operatorID, payload)
	return nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context, limit int) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(gamertag,''), status,
		       COALESCE(roles_json, json_array()),
		       honor_accepted_current, COALESCE(honor_code_sha,''), honor_code_version, COALESCE(honor_code_text,'')
		FROM users ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return sqliteScanUsers(rows)
}

func (s *SQLiteStore) AssignRole(ctx context.Context, userID, roleID, operatorID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO user_roles (user_id, role_id, assigned_at) VALUES (?, ?, ?)`,
		userID, roleID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"role_id": roleID})
	_ = s.AppendIAMEvent(ctx, "RoleAssigned", "USER", userID, operatorID, payload)
	return nil
}

func (s *SQLiteStore) RevokeRole(ctx context.Context, userID, roleID, operatorID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id=? AND role_id=?`, userID, roleID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"role_id": roleID})
	_ = s.AppendIAMEvent(ctx, "RoleRevoked", "USER", userID, operatorID, payload)
	return nil
}

func (s *SQLiteStore) ListRoles(ctx context.Context) ([]auth.Role, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(description,'') FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []auth.Role
	for rows.Next() {
		var r auth.Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

func (s *SQLiteStore) ListAgents(ctx context.Context) ([]auth.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner_user_id, name, type, status, created_at, updated_at
		FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []auth.Agent
	for rows.Next() {
		var a auth.Agent
		var createdStr, updatedStr string
		if err := rows.Scan(&a.ID, &a.OwnerUserID, &a.Name, &a.Type, &a.Status, &createdStr, &updatedStr); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *SQLiteStore) CreateAgent(ctx context.Context, ownerUserID, name, agentType, operatorID string) (*auth.Agent, error) {
	id, err := util.NewUUID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agents (id, owner_user_id, name, type, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'ACTIVE', ?, ?)`,
		id, ownerUserID, name, agentType, nowStr, nowStr,
	)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{"name": name, "type": agentType, "owner": ownerUserID})
	_ = s.AppendIAMEvent(ctx, "AgentCreated", "AGENT", id, operatorID, payload)
	return &auth.Agent{
		ID:          id,
		OwnerUserID: ownerUserID,
		Name:        name,
		Type:        agentType,
		Status:      "ACTIVE",
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *SQLiteStore) UpdateAgentStatus(ctx context.Context, agentID, status, operatorID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().UTC().Format(time.RFC3339Nano), agentID)
	if err != nil {
		return err
	}
	eventType := "AgentStatusChanged"
	if status == "SUSPENDED" {
		eventType = "AgentSuspended"
	}
	payload, _ := json.Marshal(map[string]string{"status": status})
	_ = s.AppendIAMEvent(ctx, eventType, "AGENT", agentID, operatorID, payload)
	return nil
}

// UpsertClusterHeartbeat upserts a cluster heartbeat using SQLite's INSERT OR REPLACE.
func (s *SQLiteStore) UpsertClusterHeartbeat(ctx context.Context, h auth.ClusterHeartbeat) error {
	caps := strings.Join(h.Capabilities, ",")
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cluster_heartbeats (agent_id, cluster_id, capabilities, load_score, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			cluster_id   = excluded.cluster_id,
			capabilities = excluded.capabilities,
			load_score   = excluded.load_score,
			last_seen    = excluded.last_seen
	`, h.AgentID, h.ClusterID, caps, h.LoadScore, h.LastSeen.UTC().Format(time.RFC3339Nano))
	return err
}

// ListActiveClusterHeartbeats returns clusters whose last_seen is within maxAge.
func (s *SQLiteStore) ListActiveClusterHeartbeats(ctx context.Context, maxAge time.Duration) ([]auth.ClusterHeartbeat, error) {
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, cluster_id, capabilities, load_score, last_seen
		FROM cluster_heartbeats
		WHERE last_seen >= ?
		ORDER BY load_score ASC, last_seen DESC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.ClusterHeartbeat
	for rows.Next() {
		var h auth.ClusterHeartbeat
		var caps, lastSeenStr string
		if err := rows.Scan(&h.AgentID, &h.ClusterID, &caps, &h.LoadScore, &lastSeenStr); err != nil {
			return nil, err
		}
		if caps != "" {
			h.Capabilities = strings.Split(caps, ",")
		}
		h.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeenStr)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListIAMEvents(ctx context.Context, limit int) ([]auth.IAMEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, event_type, aggregate_type, aggregate_id,
		       COALESCE(operator_id,''), COALESCE(payload,'null'), recorded_at
		FROM iam_event_stream ORDER BY event_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []auth.IAMEvent
	for rows.Next() {
		var e auth.IAMEvent
		var recordedStr string
		if err := rows.Scan(&e.EventID, &e.EventType, &e.AggregateType, &e.AggregateID,
			&e.OperatorID, &e.Payload, &recordedStr); err != nil {
			return nil, err
		}
		e.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedStr)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) SetAgentCredential(ctx context.Context, agentID, plaintextSecret, operatorID string) error {
	hash := sqliteHashAgentSecret(agentID, plaintextSecret)
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET api_key_hash=?, updated_at=? WHERE id=?`,
		hash, time.Now().UTC().Format(time.RFC3339Nano), agentID,
	)
	if err != nil {
		return fmt.Errorf("set agent credential: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"agent_id": agentID})
	_ = s.AppendIAMEvent(ctx, "AgentCredentialSet", "AGENT", agentID, operatorID, payload)
	return nil
}

func (s *SQLiteStore) AuthenticateAgent(ctx context.Context, agentName, plaintextSecret string) (*auth.Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, owner_user_id, name, type, status, COALESCE(api_key_hash,'')
		 FROM agents WHERE name=?`, agentName)
	var a auth.Agent
	var storedHash string
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.Name, &a.Type, &a.Status, &storedHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("agent not found")
		}
		return nil, err
	}
	if a.Status != "ACTIVE" {
		return nil, fmt.Errorf("agent is not active (status=%s)", a.Status)
	}
	if storedHash == "" {
		return nil, fmt.Errorf("no credential provisioned for agent %q", agentName)
	}
	if sqliteHashAgentSecret(a.ID, plaintextSecret) != storedHash {
		return nil, fmt.Errorf("invalid agent secret")
	}
	perms, err := s.GetAgentPermissions(ctx, a.ID)
	if err != nil {
		return nil, fmt.Errorf("get agent permissions: %w", err)
	}
	a.Permissions = perms
	return &a, nil
}

func (s *SQLiteStore) AppendApple(ctx context.Context, apple auth.AppleRecord) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	metadata := apple.Metadata
	if metadata == nil {
		metadata = []byte("null")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO apples (agent_id, source_repo, run_id, apple_type, title, body, metadata, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		apple.AgentID, apple.SourceRepo, apple.RunID, apple.AppleType,
		apple.Title, apple.Body, metadata, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert apple: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"apple_id":    id,
		"source_repo": apple.SourceRepo,
		"run_id":      apple.RunID,
		"apple_type":  apple.AppleType,
		"title":       apple.Title,
	})
	_, err = tx.ExecContext(ctx,
		`INSERT INTO iam_event_stream
		 (event_type, aggregate_type, aggregate_id, operator_id, payload, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"ApplePublished", "AGENT", apple.AgentID, apple.AgentID, payload, now,
	)
	if err != nil {
		return 0, fmt.Errorf("append iam event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) ListApples(ctx context.Context, agentID, sourceRepo, appleType string, limit int) ([]auth.AppleRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT id, agent_id, source_repo, run_id, apple_type, title, recorded_at
	      FROM apples WHERE 1=1`
	args := []any{}
	if agentID != "" {
		q += " AND agent_id = ?"
		args = append(args, agentID)
	}
	if sourceRepo != "" {
		q += " AND source_repo = ?"
		args = append(args, sourceRepo)
	}
	if appleType != "" {
		q += " AND apple_type = ?"
		args = append(args, appleType)
	}
	q += " ORDER BY recorded_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var apples []auth.AppleRecord
	for rows.Next() {
		var a auth.AppleRecord
		var recordedStr string
		if err := rows.Scan(&a.ID, &a.AgentID, &a.SourceRepo, &a.RunID, &a.AppleType, &a.Title, &recordedStr); err != nil {
			return nil, err
		}
		a.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedStr)
		apples = append(apples, a)
	}
	return apples, rows.Err()
}

func (s *SQLiteStore) GetApple(ctx context.Context, id int64) (*auth.AppleRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, source_repo, run_id, apple_type, title, body, COALESCE(metadata,'null'), recorded_at
		 FROM apples WHERE id = ?`, id)
	var a auth.AppleRecord
	var recordedStr string
	if err := row.Scan(&a.ID, &a.AgentID, &a.SourceRepo, &a.RunID, &a.AppleType, &a.Title, &a.Body, &a.Metadata, &recordedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("apple %d not found", id)
		}
		return nil, err
	}
	a.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedStr)
	return &a, nil
}

func (s *SQLiteStore) GetAgentPermissions(ctx context.Context, agentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.name FROM permissions p
		 JOIN agent_permissions ap ON ap.permission_id = p.id
		 WHERE ap.agent_id = ?
		 ORDER BY p.name`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		perms = append(perms, name)
	}
	return perms, rows.Err()
}

// --- Push tokens ---

func (s *SQLiteStore) UpsertPushToken(ctx context.Context, token auth.PushToken) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO push_tokens (agent_name, platform, fcm_token, fingerprint, registered_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent_name, fingerprint) DO UPDATE SET
		   fcm_token = excluded.fcm_token,
		   platform = excluded.platform,
		   last_used_at = excluded.last_used_at`,
		token.AgentName, token.Platform, token.FCMToken, token.Fingerprint, now, now,
	)
	return err
}

func (s *SQLiteStore) GetPushToken(ctx context.Context, agentName string) (*auth.PushToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, platform, fcm_token, COALESCE(fingerprint,''), registered_at, last_used_at
		 FROM push_tokens WHERE agent_name = ?
		 ORDER BY last_used_at DESC LIMIT 1`, agentName)
	var t auth.PushToken
	var regStr, lastStr string
	if err := row.Scan(&t.ID, &t.AgentName, &t.Platform, &t.FCMToken, &t.Fingerprint, &regStr, &lastStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.RegisteredAt, _ = time.Parse(time.RFC3339Nano, regStr)
	t.LastUsedAt, _ = time.Parse(time.RFC3339Nano, lastStr)
	return &t, nil
}

func (s *SQLiteStore) CreateCameraObservation(ctx context.Context, obs auth.CameraObservation) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO camera_observations (agent_name, image_data, media_type, prompt, status, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?)`,
		obs.AgentName, obs.ImageData, obs.MediaType, obs.Prompt, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) UpdateCameraObservation(ctx context.Context, id int64, analysis string, appleID int64, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE camera_observations SET analysis=?, apple_id=?, status=?, processed_at=? WHERE id=?`,
		analysis, appleID, status, now, id,
	)
	return err
}

func (s *SQLiteStore) GetCameraObservation(ctx context.Context, id int64) (*auth.CameraObservation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, image_data, media_type, COALESCE(prompt,''), COALESCE(analysis,''),
		        COALESCE(apple_id,0), status, created_at, processed_at
		 FROM camera_observations WHERE id=?`, id)
	return scanCameraObservation(row)
}

func (s *SQLiteStore) ListCameraObservations(ctx context.Context, agentName, status string, limit int) ([]auth.CameraObservation, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, agent_name, image_data, media_type, COALESCE(prompt,''), COALESCE(analysis,''),
			        COALESCE(apple_id,0), status, created_at, processed_at
			 FROM camera_observations WHERE agent_name=? AND status=?
			 ORDER BY created_at DESC LIMIT ?`, agentName, status, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, agent_name, image_data, media_type, COALESCE(prompt,''), COALESCE(analysis,''),
			        COALESCE(apple_id,0), status, created_at, processed_at
			 FROM camera_observations WHERE agent_name=?
			 ORDER BY created_at DESC LIMIT ?`, agentName, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.CameraObservation
	for rows.Next() {
		obs, err := scanCameraObservationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *obs)
	}
	return out, rows.Err()
}

func scanCameraObservation(row *sql.Row) (*auth.CameraObservation, error) {
	var obs auth.CameraObservation
	var createdStr, processedStr sql.NullString
	if err := row.Scan(&obs.ID, &obs.AgentName, &obs.ImageData, &obs.MediaType,
		&obs.Prompt, &obs.Analysis, &obs.AppleID, &obs.Status, &createdStr, &processedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	obs.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr.String)
	if processedStr.Valid && processedStr.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, processedStr.String)
		obs.ProcessedAt = &t
	}
	return &obs, nil
}

func scanCameraObservationRow(rows *sql.Rows) (*auth.CameraObservation, error) {
	var obs auth.CameraObservation
	var createdStr, processedStr sql.NullString
	if err := rows.Scan(&obs.ID, &obs.AgentName, &obs.ImageData, &obs.MediaType,
		&obs.Prompt, &obs.Analysis, &obs.AppleID, &obs.Status, &createdStr, &processedStr); err != nil {
		return nil, err
	}
	obs.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr.String)
	if processedStr.Valid && processedStr.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, processedStr.String)
		obs.ProcessedAt = &t
	}
	return &obs, nil
}

func (s *SQLiteStore) CreateSprintItem(ctx context.Context, item auth.SprintItem) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO heimdal_sprints (agent_name, requirement, criteria_json, status, created_at, updated_at)
		 VALUES (?, ?, '[]', 'pending', ?, ?)`,
		item.AgentName, item.Requirement, now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) UpdateSprintItem(ctx context.Context, id int64, criteriaJSON, roadmapID, status string, appleID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE heimdal_sprints SET criteria_json=?, roadmap_id=?, status=?, apple_id=?, updated_at=? WHERE id=?`,
		criteriaJSON, roadmapID, status, appleID, now, id,
	)
	return err
}

func (s *SQLiteStore) GetSprintItem(ctx context.Context, id int64) (*auth.SprintItem, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_name, requirement, criteria_json, COALESCE(roadmap_id,''), status,
		        COALESCE(apple_id,0), created_at, updated_at
		 FROM heimdal_sprints WHERE id=?`, id)
	return scanSprintItem(row)
}

func (s *SQLiteStore) ListSprintItems(ctx context.Context, agentName, status string, limit int) ([]auth.SprintItem, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, agent_name, requirement, criteria_json, COALESCE(roadmap_id,''), status,
	                 COALESCE(apple_id,0), created_at, updated_at
	          FROM heimdal_sprints`
	var args []any
	var where []string
	if agentName != "" {
		where = append(where, "agent_name=?")
		args = append(args, agentName)
	}
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.SprintItem
	for rows.Next() {
		item, err := scanSprintItemRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

func scanSprintItem(row *sql.Row) (*auth.SprintItem, error) {
	var item auth.SprintItem
	var createdStr, updatedStr string
	if err := row.Scan(&item.ID, &item.AgentName, &item.Requirement, &item.CriteriaJSON,
		&item.RoadmapID, &item.Status, &item.AppleID, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
	return &item, nil
}

func scanSprintItemRow(rows *sql.Rows) (*auth.SprintItem, error) {
	var item auth.SprintItem
	var createdStr, updatedStr string
	if err := rows.Scan(&item.ID, &item.AgentName, &item.Requirement, &item.CriteriaJSON,
		&item.RoadmapID, &item.Status, &item.AppleID, &createdStr, &updatedStr); err != nil {
		return nil, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
	return &item, nil
}

// --- internal helpers ---

func (s *SQLiteStore) sqliteGetUserByGoogleSubject(ctx context.Context, googleSub string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(gamertag,''), status,
		       COALESCE(roles_json, json_array()),
		       honor_accepted_current, COALESCE(honor_code_sha,''), honor_code_version, COALESCE(honor_code_text,'')
		FROM users WHERE google_subject=?`, googleSub)
	return sqliteScanUser(row)
}

func (s *SQLiteStore) sqliteGetUserByID(ctx context.Context, id string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(gamertag,''), status,
		       COALESCE(roles_json, json_array()),
		       honor_accepted_current, COALESCE(honor_code_sha,''), honor_code_version, COALESCE(honor_code_text,'')
		FROM users WHERE id=?`, id)
	return sqliteScanUser(row)
}

func sqliteScanUser(row *sql.Row) (*auth.User, error) {
	var u auth.User
	var idStr string
	var rolesJSON []byte
	if err := row.Scan(
		&idStr, &u.Email, &u.Handle, &u.Status, &rolesJSON,
		&u.HonorAccepted, &u.HonorCurrentSHA, &u.HonorCurrentVer, &u.HonorCurrentText,
	); err != nil {
		return nil, err
	}
	u.IDString = idStr
	copy(u.ID[:], []byte(idStr))
	_ = json.Unmarshal(rolesJSON, &u.Roles)
	return &u, nil
}

func sqliteScanUsers(rows *sql.Rows) ([]auth.User, error) {
	var users []auth.User
	for rows.Next() {
		var u auth.User
		var idStr string
		var rolesJSON []byte
		if err := rows.Scan(
			&idStr, &u.Email, &u.Handle, &u.Status, &rolesJSON,
			&u.HonorAccepted, &u.HonorCurrentSHA, &u.HonorCurrentVer, &u.HonorCurrentText,
		); err != nil {
			return nil, err
		}
		u.IDString = idStr
		copy(u.ID[:], []byte(idStr))
		_ = json.Unmarshal(rolesJSON, &u.Roles)
		users = append(users, u)
	}
	return users, rows.Err()
}

// GetUserSubscription returns the most recent subscription record for userID.
// Returns nil, nil when no record exists.
func (s *SQLiteStore) GetUserSubscription(ctx context.Context, userID string) (*auth.Subscription, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, plan, status, expires_at, created_at, updated_at
		FROM user_subscriptions
		WHERE user_id = ?
		ORDER BY id DESC LIMIT 1`, userID)

	var sub auth.Subscription
	var expiresAt, createdAt, updatedAt string
	err := row.Scan(&sub.ID, &sub.UserID, &sub.Plan, &sub.Status,
		&expiresAt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if expiresAt != "" {
		sub.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	}
	sub.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sub.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &sub, nil
}

// UpsertUserSubscription inserts or updates the subscription for sub.UserID.
func (s *SQLiteStore) UpsertUserSubscription(ctx context.Context, sub auth.Subscription) error {
	expiresAt := ""
	if !sub.ExpiresAt.IsZero() {
		expiresAt = sub.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_subscriptions (user_id, plan, status, expires_at, updated_at)
		VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		ON CONFLICT(user_id) DO UPDATE SET
			plan       = excluded.plan,
			status     = excluded.status,
			expires_at = excluded.expires_at,
			updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')`,
		sub.UserID, sub.Plan, sub.Status, expiresAt)
	return err
}

// DailyTokenStats aggregates tokens_used from Apple metadata, grouped by UTC date.
// It generates a complete series for the last `days` days so clients get a full
// sparkline even on days with no activity.
func (s *SQLiteStore) DailyTokenStats(ctx context.Context, days int) ([]auth.DailyTokenStat, error) {
	if days <= 0 {
		days = 7
	}
	// Build the series from the DB: sum json_extract(metadata, '$.tokens_used') per day.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			strftime('%Y-%m-%d', recorded_at) AS day,
			COALESCE(SUM(CAST(json_extract(metadata, '$.tokens_used') AS INTEGER)), 0) AS tokens
		FROM apples
		WHERE recorded_at >= datetime('now', '-' || ? || ' days')
		GROUP BY day
		ORDER BY day ASC`,
		days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect DB results into a map.
	byDate := make(map[string]int64, days)
	for rows.Next() {
		var day string
		var tokens int64
		if err := rows.Scan(&day, &tokens); err != nil {
			return nil, err
		}
		byDate[day] = tokens
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill the full series (zero-pad missing days).
	stats := make([]auth.DailyTokenStat, days)
	for i := range stats {
		d := time.Now().UTC().AddDate(0, 0, -(days-1-i)).Format("2006-01-02")
		stats[i] = auth.DailyTokenStat{Date: d, Tokens: byDate[d]}
	}
	return stats, nil
}

// ── GFD subscription tiers (S124-02) ─────────────────────────────────────────

func (s *SQLiteStore) ListSubscriptionTiers(ctx context.Context) ([]GFDTier, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tier_id, name, monthly_usd, annual_usd, features
		FROM subscription_tiers
		WHERE active = 1
		ORDER BY monthly_usd ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GFDTier
	for rows.Next() {
		var t GFDTier
		var featJSON string
		if err := rows.Scan(&t.TierID, &t.Name, &t.MonthlyUSD, &t.AnnualUSD, &featJSON); err != nil {
			return nil, err
		}
		// Decode features JSON array.
		_ = json.Unmarshal([]byte(featJSON), &t.Features)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetGFDUserTier(ctx context.Context, userID string) (*string, error) {
	var tierID *string
	err := s.db.QueryRowContext(ctx,
		`SELECT tier_id FROM user_subscriptions WHERE user_id = ?`, userID,
	).Scan(&tierID)
	if err != nil && err.Error() == "sql: no rows in result set" {
		return nil, nil
	}
	return tierID, err
}

func (s *SQLiteStore) SetGFDUserTier(ctx context.Context, userID, tierID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_subscriptions SET tier_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE user_id = ?`, tierID, userID)
	return err
}

func (s *SQLiteStore) RecordStripeEvent(ctx context.Context, eventID, eventType, userID, payload string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO stripe_events (id, type, user_id, payload)
		VALUES (?, ?, ?, ?)`, eventID, eventType, userID, payload)
	return err
}

// ── Monitors ─────────────────────────────────────────────────────────────────

func (s *SQLiteStore) CreateMonitor(ctx context.Context, m auth.Monitor) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO monitors (name, slug, timeout_seconds, grace_seconds, owner,
		                      alert_slack_channel, alert_email, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown')`,
		m.Name, m.Slug, m.TimeoutSeconds, m.GraceSeconds, m.Owner,
		m.AlertSlackChannel, m.AlertEmail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) GetMonitorBySlug(ctx context.Context, slug string) (*auth.Monitor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, timeout_seconds, grace_seconds, owner,
		        last_checkin_at, alerted_at, alert_slack_channel, alert_email,
		        status, created_at, updated_at
		 FROM monitors WHERE slug = ?`, slug)
	return sqliteScanMonitor(row)
}

func (s *SQLiteStore) GetMonitorByID(ctx context.Context, id int64) (*auth.Monitor, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, timeout_seconds, grace_seconds, owner,
		        last_checkin_at, alerted_at, alert_slack_channel, alert_email,
		        status, created_at, updated_at
		 FROM monitors WHERE id = ?`, id)
	return sqliteScanMonitor(row)
}

func (s *SQLiteStore) ListMonitors(ctx context.Context, owner string) ([]auth.Monitor, error) {
	var rows *sql.Rows
	var err error
	if owner == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, name, slug, timeout_seconds, grace_seconds, owner,
			        last_checkin_at, alerted_at, alert_slack_channel, alert_email,
			        status, created_at, updated_at
			 FROM monitors ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, name, slug, timeout_seconds, grace_seconds, owner,
			        last_checkin_at, alerted_at, alert_slack_channel, alert_email,
			        status, created_at, updated_at
			 FROM monitors WHERE owner = ? ORDER BY created_at DESC`, owner)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.Monitor
	for rows.Next() {
		m, err := sqliteScanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) RecordCheckin(ctx context.Context, slug string, now time.Time) error {
	ts := now.UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE monitors
		SET last_checkin_at = ?, alerted_at = NULL, status = 'healthy',
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE slug = ?`, ts, slug)
	return err
}

func (s *SQLiteStore) MarkMonitorAlerted(ctx context.Context, id int64, now time.Time) error {
	ts := now.UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE monitors
		SET alerted_at = ?, status = 'failing',
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE id = ?`, ts, id)
	return err
}

func (s *SQLiteStore) ListOverdueMonitors(ctx context.Context, now time.Time) ([]auth.Monitor, error) {
	// Returns monitors where the deadline (last_checkin_at + timeout + grace OR
	// created_at + timeout + grace if never checked in) has passed and no alert has
	// been sent yet (alerted_at IS NULL).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, slug, timeout_seconds, grace_seconds, owner,
		        last_checkin_at, alerted_at, alert_slack_channel, alert_email,
		        status, created_at, updated_at
		 FROM monitors
		 WHERE alerted_at IS NULL
		   AND (
		     (last_checkin_at IS NOT NULL
		      AND datetime(last_checkin_at, '+' || (timeout_seconds + grace_seconds) || ' seconds') < ?)
		     OR
		     (last_checkin_at IS NULL
		      AND datetime(created_at, '+' || (timeout_seconds + grace_seconds) || ' seconds') < ?)
		   )
		 ORDER BY id`,
		now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auth.Monitor
	for rows.Next() {
		m, err := sqliteScanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteMonitor(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	return err
}

type monitorScanner interface {
	Scan(dest ...any) error
}

func sqliteScanMonitor(row monitorScanner) (*auth.Monitor, error) {
	var m auth.Monitor
	var lastCheckin, alertedAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(
		&m.ID, &m.Name, &m.Slug, &m.TimeoutSeconds, &m.GraceSeconds, &m.Owner,
		&lastCheckin, &alertedAt, &m.AlertSlackChannel, &m.AlertEmail,
		&m.Status, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastCheckin.Valid && lastCheckin.String != "" {
		t, _ := time.Parse(time.RFC3339, lastCheckin.String)
		m.LastCheckinAt = &t
	}
	if alertedAt.Valid && alertedAt.String != "" {
		t, _ := time.Parse(time.RFC3339, alertedAt.String)
		m.AlertedAt = &t
	}
	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &m, nil
}

func sqliteHashAgentSecret(agentID, plaintext string) string {
	h := sha256.New()
	h.Write([]byte(agentID))
	h.Write([]byte(":"))
	h.Write([]byte(plaintext))
	return hex.EncodeToString(h.Sum(nil))
}
