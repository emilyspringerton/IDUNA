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
	if err := tx.Commit(); err != nil {
		http.Error(w, "commit failed", http.StatusInternalServerError)
		return
	}

	token, err := h.issueJWT(playerID, req.DisplayName, req.Email)
	if err != nil {
		http.Error(w, "JWT signing failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"player_id":    playerID,
		"display_name": req.DisplayName,
		"token":        token,
	})
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
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT pc.player_id, p.display_name, pc.password_hash
		 FROM player_credentials pc JOIN players p ON pc.player_id=p.player_id
		 WHERE pc.email=?`, req.Email,
	).Scan(&playerID, &displayName, &hash)
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
