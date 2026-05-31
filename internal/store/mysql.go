package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"iduna/internal/auth"
	"iduna/internal/util"
)

// MySQLStore implements IAMStore against a MySQL 8 database.
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore creates a new MySQLStore backed by the provided *sql.DB.
func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

// GetOrCreateUserByGoogleSubject looks up a user by google_subject; if missing,
// inserts a new PENDING user and emits a UserCreated IAM event.
func (s *MySQLStore) GetOrCreateUserByGoogleSubject(ctx context.Context, googleSub, email string) (*auth.User, bool, error) {
	u, err := s.getUserByGoogleSubject(ctx, googleSub)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	// User not found — create one.
	id, err := util.NewUUID()
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, google_subject, status, roles_json, created_at, updated_at)
		 VALUES (?, ?, ?, 'PENDING', JSON_ARRAY(), ?, ?)`,
		id, email, googleSub, now, now,
	)
	if err != nil {
		// Possible race — another request may have inserted concurrently.
		u2, err2 := s.getUserByGoogleSubject(ctx, googleSub)
		if err2 == nil {
			return u2, false, nil
		}
		return nil, false, err
	}

	payload, _ := json.Marshal(map[string]string{"email": email, "google_subject": googleSub})
	_ = s.AppendIAMEvent(ctx, "UserCreated", "USER", id, "", payload)

	u, err = s.getUserByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return u, true, nil
}

// GetUserByID returns a user by their string UUID.
func (s *MySQLStore) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	return s.getUserByID(ctx, id)
}

// GetEffectivePermissions returns all distinct permission names granted to the
// user through their assigned roles, sorted lexicographically.
func (s *MySQLStore) GetEffectivePermissions(ctx context.Context, userID string) ([]string, error) {
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
	sort.Strings(perms)
	return perms, nil
}

// AppendIAMEvent inserts a row into iam_event_stream.
func (s *MySQLStore) AppendIAMEvent(ctx context.Context, eventType, aggregateType, aggregateID, operatorID string, payload []byte) error {
	if payload == nil {
		payload = []byte("null")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO iam_event_stream (event_type, aggregate_type, aggregate_id, operator_id, payload, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		eventType, aggregateType, aggregateID, operatorID, payload, time.Now().UTC(),
	)
	return err
}

// UpdateUserStatus changes the user's status and emits a UserSuspended or
// UserActivated IAM event.
func (s *MySQLStore) UpdateUserStatus(ctx context.Context, userID, status, operatorID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET status=?, updated_at=NOW(6) WHERE id=?`,
		status, userID,
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

// --- internal helpers ---

func (s *MySQLStore) getUserByGoogleSubject(ctx context.Context, googleSub string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(gamertag,''), status,
		       COALESCE(roles_json, JSON_ARRAY()),
		       honor_accepted_current, COALESCE(honor_code_sha,''), honor_code_version, COALESCE(honor_code_text,'')
		FROM users WHERE google_subject=?`, googleSub)
	return scanUser(row)
}

func (s *MySQLStore) getUserByID(ctx context.Context, id string) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(gamertag,''), status,
		       COALESCE(roles_json, JSON_ARRAY()),
		       honor_accepted_current, COALESCE(honor_code_sha,''), honor_code_version, COALESCE(honor_code_text,'')
		FROM users WHERE id=?`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*auth.User, error) {
	var u auth.User
	var idStr string
	var rolesJSON []byte
	if err := row.Scan(
		&idStr,
		&u.Email,
		&u.Handle,
		&u.Status,
		&rolesJSON,
		&u.HonorAccepted,
		&u.HonorCurrentSHA,
		&u.HonorCurrentVer,
		&u.HonorCurrentText,
	); err != nil {
		return nil, err
	}
	u.IDString = idStr
	// Also populate the legacy [16]byte ID for compatibility with device flow.
	copy(u.ID[:], []byte(idStr))
	_ = json.Unmarshal(rolesJSON, &u.Roles)
	return &u, nil
}
