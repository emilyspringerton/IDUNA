package device

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"iduna/internal/auth"
)

// SQLiteStore implements Store for an embedded SQLite database.
// It is identical in behaviour to MySQLStore but adapts the two MySQL-specific
// patterns that appear in the device flow:
//   - UTC_TIMESTAMP(6) → pass time.Now().UTC() as a parameter
//   - ON DUPLICATE KEY UPDATE → INSERT OR REPLACE
//
// Real, found-live bug (2026-09-24, while building BIG_O's own new IDUNA device-auth phone app --
// every real /auth/device/poll call against a freshly-started, definitely-valid, definitely-not-
// expired device_code came back DEVICE_CODE_INVALID_OR_EXPIRED, reproduced with plain curl too, so
// not a BIG_O-side bug). Root cause, confirmed via an isolated modernc.org/sqlite repro: every
// OTHER store in this codebase (internal/store/sqlite.go) explicitly formats a time.Time as
// time.RFC3339Nano text before writing it, and scans time columns back into a string + manual
// time.Parse(time.RFC3339Nano, ...) -- never a bare *time.Time Scan destination. This file was the
// one exception: it passed raw time.Time values straight through to ExecContext (modernc.org/
// sqlite silently stores them via time.Time's own String() method -- "2026-09-24 05:59:13.7869...
// +0000 UTC", NOT RFC3339Nano) and scanned straight into *time.Time/sql.NullTime destinations,
// which modernc.org/sqlite's Scan does not support for a TEXT column at all ("unsupported Scan,
// storing driver.Value type string into type *time.Time") -- confirmed reproducing 100% of the
// time, not an edge case, meaning the real, live device-auth poll flow had never worked. Every
// write below now formats explicitly; every read now scans into a string/sql.NullString and
// parses manually, matching internal/store/sqlite.go's own already-proven-correct convention
// exactly. The Request/Exchange struct shapes (time.Time/sql.NullTime fields) are UNCHANGED --
// this fix is confined to this file's own read/write mechanics, no ripple into service.go.
type SQLiteStore struct{ db *sql.DB }

// NewSQLiteDeviceStore wraps an open SQLite *sql.DB for the device flow.
func NewSQLiteDeviceStore(db *sql.DB) *SQLiteStore { return &SQLiteStore{db: db} }

func (s *SQLiteStore) InsertDeviceRequest(ctx context.Context, req *Request) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `INSERT INTO device_auth_requests
(stream_id,device_code_hash,user_code_norm,user_code_display,status,created_at,expires_at,poll_interval_ms)
VALUES (?,?,?,?,'pending',?,?,?)`,
		req.StreamID, req.DeviceCodeHash[:], req.UserCodeNorm, req.UserCodeDisplay,
		now, req.ExpiresAt.UTC().Format(time.RFC3339Nano), req.PollIntervalMS)
	if err != nil {
		return err
	}
	req.ID, _ = res.LastInsertId()
	return nil
}

func (s *SQLiteStore) GetDeviceRequestByDeviceHash(ctx context.Context, deviceHash [32]byte) (*Request, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,stream_id,user_code_norm,user_code_display,status,expires_at,
		        poll_interval_ms,last_poll_at,authorized_user_id,exchange_code_plain,exchange_code_expires_at
		 FROM device_auth_requests WHERE device_code_hash=?`, deviceHash[:])
	var req Request
	var expiresAtStr string
	var lastPollAtStr, exchangePlainStr, exchangeExpiresStr sql.NullString
	if err := row.Scan(&req.ID, &req.StreamID, &req.UserCodeNorm, &req.UserCodeDisplay,
		&req.Status, &expiresAtStr, &req.PollIntervalMS, &lastPollAtStr,
		&req.AuthorizedUserID, &exchangePlainStr, &exchangeExpiresStr); err != nil {
		return nil, err
	}
	req.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAtStr)
	if lastPollAtStr.Valid && lastPollAtStr.String != "" {
		if t, perr := time.Parse(time.RFC3339Nano, lastPollAtStr.String); perr == nil {
			req.LastPollAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	req.ExchangePlain = exchangePlainStr
	if exchangeExpiresStr.Valid && exchangeExpiresStr.String != "" {
		if t, perr := time.Parse(time.RFC3339Nano, exchangeExpiresStr.String); perr == nil {
			req.ExchangeExpires = sql.NullTime{Time: t, Valid: true}
		}
	}
	return &req, nil
}

func (s *SQLiteStore) GetDeviceRequestByUserCode(ctx context.Context, userCodeNorm string) (*Request, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,stream_id,status,expires_at FROM device_auth_requests WHERE user_code_norm=?`, userCodeNorm)
	var req Request
	var expiresAtStr string
	if err := row.Scan(&req.ID, &req.StreamID, &req.Status, &expiresAtStr); err != nil {
		return nil, err
	}
	req.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAtStr)
	return &req, nil
}

func (s *SQLiteStore) UpdatePollState(ctx context.Context, requestID int64, lastPollAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_auth_requests SET last_poll_at=?, poll_count=poll_count+1 WHERE id=?`,
		lastPollAt.UTC().Format(time.RFC3339Nano), requestID)
	return err
}

func (s *SQLiteStore) AuthorizeRequest(ctx context.Context, requestID int64, userID []byte, ipHash [32]byte, uaHash [32]byte, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_auth_requests
		 SET status='authorized', authorized_user_id=?, authorized_at=?, authorized_ip_hash=?, authorized_ua_hash=?
		 WHERE id=?`, userID, now.UTC().Format(time.RFC3339Nano), ipHash[:], uaHash[:], requestID)
	return err
}

func (s *SQLiteStore) UpsertExchangeForRequest(ctx context.Context, req *Request, exchange *Exchange) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	expiresAtStr := exchange.ExpiresAt.UTC().Format(time.RFC3339Nano)
	// SQLite does not have ON DUPLICATE KEY UPDATE; use INSERT OR REPLACE.
	// The device_request_id is the natural unique key here.
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO exchange_codes
		 (exchange_code_hash, exchange_code_plain, user_id, device_request_id, created_at, expires_at, consumed_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		exchange.ExchangeHash[:], exchange.ExchangePlain, exchange.UserID,
		exchange.DeviceRequest, now, expiresAtStr)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE device_auth_requests
		 SET exchange_code_plain=?, exchange_code_hash=?, exchange_code_expires_at=?
		 WHERE id=?`,
		exchange.ExchangePlain, exchange.ExchangeHash[:], expiresAtStr, exchange.DeviceRequest)
	return err
}

func (s *SQLiteStore) GetExchangeByPlainOrHash(ctx context.Context, code string, hash [32]byte) (*Exchange, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, exchange_code_hash, exchange_code_plain, user_id, device_request_id, expires_at, consumed_at
		 FROM exchange_codes
		 WHERE exchange_code_hash=? OR exchange_code_plain=?
		 ORDER BY id DESC LIMIT 1`, hash[:], code)
	var ex Exchange
	var hashBytes []byte
	var expiresAtStr string
	var consumedAtStr sql.NullString
	if err := row.Scan(&ex.ID, &hashBytes, &ex.ExchangePlain, &ex.UserID,
		&ex.DeviceRequest, &expiresAtStr, &consumedAtStr); err != nil {
		return nil, err
	}
	copy(ex.ExchangeHash[:], hashBytes)
	ex.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAtStr)
	if consumedAtStr.Valid && consumedAtStr.String != "" {
		if t, perr := time.Parse(time.RFC3339Nano, consumedAtStr.String); perr == nil {
			ex.ConsumedAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	return &ex, nil
}

func (s *SQLiteStore) ConsumeExchange(ctx context.Context, exchangeID int64, deviceRequestID int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.ExecContext(ctx,
		`UPDATE exchange_codes SET consumed_at=? WHERE id=? AND consumed_at IS NULL`, now.UTC().Format(time.RFC3339Nano), exchangeID)
	if err != nil {
		return err
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return errors.New("already consumed")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE device_auth_requests SET status='consumed', exchange_code_plain=NULL WHERE id=?`,
		deviceRequestID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) LoadUserForToken(ctx context.Context, userID []byte) (*auth.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(gamertag,''), status, roles_json,
		        honor_accepted_current, honor_code_sha, honor_code_version, COALESCE(honor_code_text,'')
		 FROM users WHERE id=?`, string(userID))
	var out auth.User
	var rolesJSON []byte
	var idStr string
	if err := row.Scan(&idStr, &out.Handle, &out.Status, &rolesJSON,
		&out.HonorAccepted, &out.HonorCurrentSHA, &out.HonorCurrentVer, &out.HonorCurrentText); err != nil {
		return nil, err
	}
	copy(out.ID[:], []byte(idStr))
	_ = json.Unmarshal(rolesJSON, &out.Roles)
	return &out, nil
}

func (s *SQLiteStore) AppendEvent(ctx context.Context, streamType, streamID, eventType string, payload []byte, occurredAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO event_store
		 (stream_type, stream_id, event_type, payload_json, occurred_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		streamType, streamID, eventType, payload, occurredAt.UTC().Format(time.RFC3339Nano), now)
	return err
}
