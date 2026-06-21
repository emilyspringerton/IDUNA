package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlayersHandler handles SHANKPIT player registration and profile retrieval.
//
//	POST /api/v1/players/register — upsert player record; returns JWT-compatible player_id
//	GET  /api/v1/players/{id}    — public profile (kills, deaths, kd_ratio, sessions)
type PlayersHandler struct {
	DB *sql.DB
}

type registerRequest struct {
	Provider    string `json:"provider"`     // "google", "iduna_local"
	ProviderSub string `json:"provider_sub"` // OAuth sub or local UID
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type playerProfile struct {
	PlayerID    string    `json:"player_id"`
	DisplayName string    `json:"display_name"`
	Provider    string    `json:"provider"`
	Email       string    `json:"email,omitempty"`
	Kills       int       `json:"kills"`
	Deaths      int       `json:"deaths"`
	KDRatio     float64   `json:"kd_ratio"`
	Sessions    int       `json:"sessions"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen    time.Time `json:"last_seen"`
}

func (h *PlayersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if r.Method == http.MethodPost && (path == "/api/v1/players/register" || strings.HasSuffix(path, "/register")) {
		h.handleRegister(w, r)
		return
	}

	// POST /api/v1/players/{id}/session — increment kills/deaths/sessions from game server.
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/session") {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/players/"), "/"), "/")
		if len(parts) == 2 && parts[1] == "session" {
			h.handleSessionEnd(w, r, parts[0])
			return
		}
	}

	// GET /api/v1/players/{id}
	if r.Method == http.MethodGet {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/players/"), "/")
		if len(parts) == 1 && parts[0] != "" {
			h.handleGetPlayer(w, r, parts[0])
			return
		}
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (h *PlayersHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Provider == "" || req.ProviderSub == "" {
		http.Error(w, "provider and provider_sub required", http.StatusBadRequest)
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = "player-" + req.ProviderSub[:min(8, len(req.ProviderSub))]
	}

	db := h.DB
	if db == nil {
		http.Error(w, "players not available", http.StatusServiceUnavailable)
		return
	}

	// Upsert: if (provider, provider_sub) exists, update display_name + last_seen; else insert.
	var playerID string
	err := db.QueryRowContext(r.Context(),
		`SELECT player_id FROM players WHERE provider=? AND provider_sub=?`,
		req.Provider, req.ProviderSub,
	).Scan(&playerID)

	switch {
	case err == nil:
		// Existing player — update display name and last_seen.
		_, _ = db.ExecContext(r.Context(),
			`UPDATE players SET display_name=?, email=?, last_seen=CURRENT_TIMESTAMP WHERE player_id=?`,
			req.DisplayName, req.Email, playerID,
		)
	case err == sql.ErrNoRows:
		// New player — insert.
		playerID = uuid.New().String()
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO players (player_id, display_name, provider, provider_sub, email)
			 VALUES (?,?,?,?,?)`,
			playerID, req.DisplayName, req.Provider, req.ProviderSub, req.Email,
		)
		if err != nil {
			http.Error(w, "registration failed", http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"player_id":    playerID,
		"display_name": req.DisplayName,
	})
}

func (h *PlayersHandler) handleGetPlayer(w http.ResponseWriter, r *http.Request, id string) {
	db := h.DB
	if db == nil {
		http.Error(w, "players not available", http.StatusServiceUnavailable)
		return
	}

	var p playerProfile
	var email sql.NullString
	err := db.QueryRowContext(r.Context(),
		`SELECT player_id, display_name, provider, email, kills, deaths, sessions, registered_at, last_seen
		 FROM players WHERE player_id=?`, id,
	).Scan(&p.PlayerID, &p.DisplayName, &p.Provider, &email,
		&p.Kills, &p.Deaths, &p.Sessions, &p.RegisteredAt, &p.LastSeen)

	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if email.Valid {
		p.Email = email.String
	}
	if p.Deaths > 0 {
		p.KDRatio = float64(p.Kills) / float64(p.Deaths)
	} else {
		p.KDRatio = float64(p.Kills)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (h *PlayersHandler) handleSessionEnd(w http.ResponseWriter, r *http.Request, playerID string) {
	var body struct {
		Kills  int `json:"kills"`
		Deaths int `json:"deaths"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	db := h.DB
	if db == nil {
		http.Error(w, "players not available", http.StatusServiceUnavailable)
		return
	}

	res, err := db.ExecContext(r.Context(),
		`UPDATE players SET sessions=sessions+1, kills=kills+?, deaths=deaths+?, last_seen=CURRENT_TIMESTAMP WHERE player_id=?`,
		body.Kills, body.Deaths, playerID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, "player not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"updated": true, "kills_added": body.Kills, "deaths_added": body.Deaths})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
