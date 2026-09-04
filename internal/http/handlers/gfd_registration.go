package handlers

// gfd_registration.go — the real waitlist toggle for GFD player registration (kanban GFD-UA-001,
// second half: "need a toggle in iduna back office to turn it into a waiting list once we have
// some initial testers"). The first half of this card (a real sign-up button in the client that
// shells out to IDUNA's own register form) shipped earlier this session -- see BACKLOG.md's own
// GFD-UA-001 entry for that commit.
//
// Single-row settings table (gfd_registration_settings), not a generic KV store -- matches this
// repo's own established one-table-per-real-feature migration convention (checked directly, no
// existing generic settings table anywhere in migrations/). PlayerEmailAuthHandler.handleRegister
// checks this mode before creating a real account; in waitlist mode it stores the request
// (including the already-bcrypt-hashed password) in gfd_waitlist instead, so approving a
// waitlisted player later doesn't require them to re-register.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type gfdRegistrationModeValue string

const (
	gfdRegistrationModeOpen     gfdRegistrationModeValue = "open"
	gfdRegistrationModeWaitlist gfdRegistrationModeValue = "waitlist"
)

// gfdRegistrationMode reads the current mode. Defaults to "open" (fail open, not closed --
// a DB hiccup here should never silently lock real players out of registering) if the settings
// row is somehow missing, which the migration's own INSERT OR IGNORE should prevent in practice.
func gfdRegistrationMode(ctx context.Context, db *sql.DB) (gfdRegistrationModeValue, error) {
	var mode string
	err := db.QueryRowContext(ctx, `SELECT mode FROM gfd_registration_settings WHERE id=1`).Scan(&mode)
	if err == sql.ErrNoRows {
		return gfdRegistrationModeOpen, nil
	}
	if err != nil {
		return gfdRegistrationModeOpen, err
	}
	if mode == string(gfdRegistrationModeWaitlist) {
		return gfdRegistrationModeWaitlist, nil
	}
	return gfdRegistrationModeOpen, nil
}

// GfdRegistrationHandler serves the real Back Office admin surface for the waitlist toggle and
// the waitlist itself.
//
//	GET   /admin/gfd-registration/api/mode              -> {"mode":"open"|"waitlist"}
//	PATCH /admin/gfd-registration/api/mode               {"mode":"open"|"waitlist"} -> 200
//	GET   /admin/gfd-registration/api/waitlist           -> [{id,email,display_name,character_name,requested_at,approved_at}, ...]
//	POST  /admin/gfd-registration/api/waitlist/{id}/approve -> 200, creates the real account
type GfdRegistrationHandler struct {
	DB *sql.DB
}

// gfdWaitlistEntry is the real, public shape of a waitlist row -- password_hash never leaves
// this handler.
type gfdWaitlistEntry struct {
	ID            int64  `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	CharacterName string `json:"character_name,omitempty"`
	CharacterJob  string `json:"character_job,omitempty"`
	RequestedAt   string `json:"requested_at"`
	ApprovedAt    string `json:"approved_at,omitempty"`
}

func (h *GfdRegistrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/admin/gfd-registration/api/mode" && r.Method == http.MethodGet:
		h.getMode(w, r)
	case path == "/admin/gfd-registration/api/mode" && r.Method == http.MethodPatch:
		h.setMode(w, r)
	case path == "/admin/gfd-registration/api/waitlist" && r.Method == http.MethodGet:
		h.listWaitlist(w, r)
	case strings.HasPrefix(path, "/admin/gfd-registration/api/waitlist/") && strings.HasSuffix(path, "/approve") && r.Method == http.MethodPost:
		idStr := strings.TrimSuffix(strings.TrimPrefix(path, "/admin/gfd-registration/api/waitlist/"), "/approve")
		h.approve(w, r, idStr)
	default:
		http.NotFound(w, r)
	}
}

func (h *GfdRegistrationHandler) getMode(w http.ResponseWriter, r *http.Request) {
	mode, err := gfdRegistrationMode(r.Context(), h.DB)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": string(mode)})
}

func (h *GfdRegistrationHandler) setMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Mode != string(gfdRegistrationModeOpen) && body.Mode != string(gfdRegistrationModeWaitlist) {
		mmoWriteError(w, http.StatusBadRequest, `mode must be "open" or "waitlist"`)
		return
	}
	_, err := h.DB.ExecContext(r.Context(),
		`UPDATE gfd_registration_settings SET mode=?, updated_at=datetime('now') WHERE id=1`, body.Mode,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": body.Mode})
}

func (h *GfdRegistrationHandler) listWaitlist(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, email, display_name, character_name, character_job, requested_at, COALESCE(approved_at, '')
		 FROM gfd_waitlist ORDER BY requested_at ASC`,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []gfdWaitlistEntry{}
	for rows.Next() {
		var e gfdWaitlistEntry
		if err := rows.Scan(&e.ID, &e.Email, &e.DisplayName, &e.CharacterName, &e.CharacterJob, &e.RequestedAt, &e.ApprovedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// approve promotes one waitlist row into a real player account -- the exact same
// players/player_credentials rows PlayerEmailAuthHandler.handleRegister itself creates, reusing
// the already-hashed password captured at waitlist-signup time so the player never has to
// re-register. Idempotent-safe: a row with approved_at already set is refused, not re-run.
func (h *GfdRegistrationHandler) approve(w http.ResponseWriter, r *http.Request, idStr string) {
	var email, displayName, hash, charName, charJob string
	var approvedAt sql.NullString
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT email, display_name, password_hash, character_name, character_job, approved_at FROM gfd_waitlist WHERE id=?`, idStr,
	).Scan(&email, &displayName, &hash, &charName, &charJob, &approvedAt)
	if err == sql.ErrNoRows {
		mmoWriteError(w, http.StatusNotFound, "waitlist entry not found")
		return
	}
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if approvedAt.Valid {
		mmoWriteError(w, http.StatusConflict, "already approved")
		return
	}

	var existingPlayerID string
	lookupErr := h.DB.QueryRowContext(r.Context(),
		`SELECT player_id FROM player_credentials WHERE email=?`, email,
	).Scan(&existingPlayerID)
	if lookupErr == nil {
		mmoWriteError(w, http.StatusConflict, "email already has a real account")
		return
	}

	playerID := uuid.New().String()
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES (?,?,?,?,?)`,
		playerID, displayName, "email", email, email,
	); err != nil {
		tx.Rollback()
		mmoWriteError(w, http.StatusInternalServerError, "account creation failed: "+err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO player_credentials (player_id, email, password_hash) VALUES (?,?,?)`,
		playerID, email, hash,
	); err != nil {
		tx.Rollback()
		mmoWriteError(w, http.StatusInternalServerError, "account creation failed: "+err.Error())
		return
	}
	if charName != "" {
		nowStr := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO characters (character_id, player_id, name, job_main, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			uuid.New().String(), playerID, charName, orDefault(charJob, "WAR"), nowStr, nowStr,
		); err != nil {
			tx.Rollback()
			mmoWriteError(w, http.StatusInternalServerError, "character creation failed: "+err.Error())
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE gfd_waitlist SET approved_at=datetime('now') WHERE id=?`, idStr,
	); err != nil {
		tx.Rollback()
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "commit failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"player_id": playerID, "email": email})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
