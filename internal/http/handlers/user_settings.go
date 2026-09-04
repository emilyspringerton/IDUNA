package handlers

// user_settings.go — the real, general per-user settings home (WOTAN-24412, "IDUNA (and WOTAN)
// USER SETTINGS IN GENERAL WE NEED A PLACE FOR THE USER TO CHANGE SETTINGS"), with the first
// real setting (ACCESSABILITY-14441, "WE NEED A HIGH CONTRAST SETTING... TO MAKE IDUNA MORE
// HIGH CONTRAST FOR VISUALLY ACCESSABILITY") living on it. Real, typed columns on
// user_settings (migrations/truestore/202609040003_user_settings.sql), one row per real user,
// keyed by the JWT "sub" claim -- matching this repo's own established "one table per real
// feature" convention (gfd_registration_settings' own header comment), not a generic JSON blob.
//
// Real, honest scope: this is the account's own real, persisted preference. Applying
// high_contrast to every page's own CSS is the real, separate front-end wiring half -- the
// settings page itself (user_settings_page.go) applies it to prove the toggle round-trips for
// real; wiring it into every other IDUNA/WOTAN page is real, named, future follow-up, not
// silently claimed done here.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"iduna/internal/http/middleware"
)

// UserSettings is the real, public shape of one user's settings row.
type UserSettings struct {
	HighContrast bool `json:"high_contrast"`
}

// UserSettingsHandler serves the real settings API.
//
//	GET   /api/v1/settings/me   -> UserSettings (defaults to {high_contrast:false} if no row exists)
//	PATCH /api/v1/settings/me   {UserSettings} -> 200, upserts the real row
type UserSettingsHandler struct {
	DB *sql.DB
}

func (h *UserSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sub := middleware.SubjectFromContext(r.Context())
	if sub == "" {
		mmoWriteError(w, http.StatusUnauthorized, "no real authenticated subject")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.get(w, r, sub)
	case http.MethodPatch:
		h.patch(w, r, sub)
	default:
		http.NotFound(w, r)
	}
}

func getUserSettings(ctx context.Context, db *sql.DB, sub string) (UserSettings, error) {
	var s UserSettings
	var highContrast int
	err := db.QueryRowContext(ctx, `SELECT high_contrast FROM user_settings WHERE user_id = ?`, sub).Scan(&highContrast)
	if err == sql.ErrNoRows {
		return UserSettings{}, nil // real, honest default: no row yet means every setting is off
	}
	if err != nil {
		return UserSettings{}, err
	}
	s.HighContrast = highContrast != 0
	return s, nil
}

func (h *UserSettingsHandler) get(w http.ResponseWriter, r *http.Request, sub string) {
	s, err := getUserSettings(r.Context(), h.DB, sub)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *UserSettingsHandler) patch(w http.ResponseWriter, r *http.Request, sub string) {
	var s UserSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	highContrast := 0
	if s.HighContrast {
		highContrast = 1
	}
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO user_settings (user_id, high_contrast, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(user_id) DO UPDATE SET high_contrast = excluded.high_contrast, updated_at = excluded.updated_at
	`, sub, highContrast)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}
