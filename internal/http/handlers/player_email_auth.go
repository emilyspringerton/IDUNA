package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	authjwt "iduna/internal/auth/jwt"
)

// DragonsNShit char_id: real testing accounts (2026-08-04, founder: "i need a way to create
// dragonsnshit accounts for testing - i need iduna login i think it should live in iduna create
// account for dragonsnshit"). Before this, every disposable test character this session used --
// including this exact same day's own KO-recovery and Home Point Crystal verification passes --
// was a raw SQLite INSERT straight into IDUNA's own characters table, bypassing the real account
// system entirely (no real login, no real player row, nothing a human tester could actually sign
// into). Real fix: an optional `character_name` field on the existing email/register endpoint --
// living in the same place a real DragonsNShit-playing account is already created, not a second,
// parallel "test account" endpoint -- that also inserts a real `characters` row (same shape
// mmo.go's own handleCreateCharacter uses) for the freshly-registered player, atomically, in the
// same request. The returned JSON now carries character_id + character_name alongside the real
// login credentials, so a caller has everything needed to both log in (email/password, real JWT)
// and immediately drive apps2/mud's own HTTP API with a real character_id -- no SQL access needed.

// PlayerEmailAuthHandler handles email+password auth for SHANKPIT players.
//
//	POST /api/v1/auth/email/register — create player + credential, return JWT
//	POST /api/v1/auth/email/login    — verify credential, return JWT
type PlayerEmailAuthHandler struct {
	DB     *sql.DB
	Keys   *authjwt.Keys
	Issuer string
}

func (h *PlayerEmailAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/register"):
		h.handleRegister(w, r)
	case strings.HasSuffix(r.URL.Path, "/login"):
		h.handleLogin(w, r)
	default:
		http.NotFound(w, r)
	}
}

type emailAuthRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	// CharacterName, if set, also creates a real DragonsNShit character for this new player in
	// the same request (register.go's own createCharacterRequest shape, JobMain defaults to WAR
	// same as mmo.go's own handleCreateCharacter). Registration-only -- handleLogin has no
	// equivalent field, since a returning player's character already exists.
	CharacterName string `json:"character_name"`
	CharacterJob  string `json:"character_job"`
}

func (h *PlayerEmailAuthHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req emailAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if req.DisplayName == "" {
		at := strings.Index(req.Email, "@")
		if at > 0 {
			req.DisplayName = req.Email[:at]
		} else {
			req.DisplayName = req.Email
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Upsert player; fail cleanly if email already exists.
	var existingPlayerID string
	lookupErr := h.DB.QueryRowContext(r.Context(),
		`SELECT player_id FROM player_credentials WHERE email=?`, req.Email,
	).Scan(&existingPlayerID)
	if lookupErr == nil {
		http.Error(w, "email already registered", http.StatusConflict)
		return
	}

	// Waitlist mode (kanban GFD-UA-001, second half: "need a toggle in iduna back office to
	// turn it into a waiting list once we have some initial testers"). Checked after the
	// duplicate-email check above so a returning player still gets the real "already
	// registered" error rather than a confusing "you're on the waitlist" for an account they
	// already have. Stores the hashed password now so an admin approval later doesn't need the
	// player to re-register.
	mode, err := gfdRegistrationMode(r.Context(), h.DB)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if mode == gfdRegistrationModeWaitlist {
		_, err := h.DB.ExecContext(r.Context(),
			`INSERT INTO gfd_waitlist (email, display_name, password_hash, character_name, character_job)
			 VALUES (?,?,?,?,?)`,
			req.Email, req.DisplayName, string(hash), strings.TrimSpace(req.CharacterName), strings.TrimSpace(strings.ToUpper(req.CharacterJob)),
		)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				http.Error(w, "you're already on the waitlist", http.StatusConflict)
				return
			}
			http.Error(w, "waitlist signup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"waitlisted": true,
			"message":    "GFD registration is currently invite-only. You've been added to the waitlist.",
		})
		return
	}

	playerID := uuid.New().String()
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO players (player_id, display_name, provider, provider_sub, email) VALUES (?,?,?,?,?)`,
		playerID, req.DisplayName, "email", req.Email, req.Email,
	)
	if err != nil {
		tx.Rollback()
		http.Error(w, "registration failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO player_credentials (player_id, email, password_hash) VALUES (?,?,?)`,
		playerID, req.Email, string(hash),
	)
	if err != nil {
		tx.Rollback()
		http.Error(w, "registration failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var characterID string
	req.CharacterName = strings.TrimSpace(req.CharacterName)
	if req.CharacterName != "" {
		jobMain := strings.TrimSpace(strings.ToUpper(req.CharacterJob))
		if jobMain == "" {
			jobMain = "WAR"
		}
		characterID = uuid.New().String()
		nowStr := time.Now().UTC().Format(time.RFC3339)
		_, err = tx.ExecContext(r.Context(),
			`INSERT INTO characters (character_id, player_id, name, job_main, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			characterID, playerID, req.CharacterName, jobMain, nowStr, nowStr,
		)
		if err != nil {
			tx.Rollback()
			if strings.Contains(err.Error(), "UNIQUE") {
				http.Error(w, "character name already taken", http.StatusConflict)
				return
			}
			http.Error(w, "character creation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "commit failed", http.StatusInternalServerError)
		return
	}

	token, err := h.issueJWT(playerID, req.DisplayName, req.Email)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"player_id":    playerID,
		"display_name": req.DisplayName,
		"token":        token,
	}
	if characterID != "" {
		resp["character_id"] = characterID
		resp["character_name"] = req.CharacterName
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *PlayerEmailAuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req emailAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	var playerID, displayName, hash string
	var disabledAt sql.NullString
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT pc.player_id, p.display_name, pc.password_hash, p.disabled_at
		 FROM player_credentials pc JOIN players p ON pc.player_id=p.player_id
		 WHERE pc.email=?`, req.Email,
	).Scan(&playerID, &displayName, &hash, &disabledAt)
	if err == sql.ErrNoRows {
		http.Error(w, "invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		http.Error(w, "invalid email or password", http.StatusUnauthorized)
		return
	}
	// First Game Master tool (2026-08-05, founder: "a way to disable accounts"). Checked after
	// the password compare, not before -- rejecting a disabled account with the same generic
	// "invalid email or password" the wrong-password case uses would let a player probe whether
	// their OWN password still works while genuinely disabled, but a distinct message here is
	// fine: they already proved they own the credential, so "account disabled" tells a real
	// account owner (not an attacker guessing passwords) something they're entitled to know.
	if disabledAt.Valid {
		http.Error(w, "this account has been disabled", http.StatusForbidden)
		return
	}

	// Update last_seen.
	_, _ = h.DB.ExecContext(r.Context(),
		`UPDATE players SET last_seen=CURRENT_TIMESTAMP WHERE player_id=?`, playerID,
	)

	token, err := h.issueJWT(playerID, displayName, req.Email)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"player_id":    playerID,
		"display_name": displayName,
		"token":        token,
	})
}

func (h *PlayerEmailAuthHandler) issueJWT(playerID, displayName, email string) (string, error) {
	return authjwt.Sign(h.Keys, map[string]any{
		"sub":          playerID,
		"display_name": displayName,
		"email":        email,
		"iss":          h.Issuer,
		"aud":          "shankpit",
		"iat":          time.Now().Unix(),
		"exp":          time.Now().Add(72 * time.Hour).Unix(),
	})
}
