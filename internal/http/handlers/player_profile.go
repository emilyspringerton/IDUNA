// player_profile.go — GET /api/v1/players/{slug}/profile
//
// Returns enriched player data for GFD page-profile.php:
//   {display_name, job, fame:{Frequency,Bloc,Procurement}, last_scene, apples_count}
//
// The "slug" is the player's display_name (case-insensitive).
// Job and last_scene are read from the characters table via the players DB.
// apples_count is the count of Apples filed by this player (sourced from IAMStore).
// Fame fields are stored in a player_fame table if present; otherwise zero.
package handlers

import (
	"database/sql"
	"net/http"
	"strings"

	"iduna/internal/store"
)

// PlayerProfileHandler serves GET /api/v1/players/{slug}/profile.
type PlayerProfileHandler struct {
	DB    *sql.DB       // players / characters database
	Store store.IAMStore // for Apple count
}

type playerFame struct {
	Frequency   int `json:"Frequency"`
	Bloc        int `json:"Bloc"`
	Procurement int `json:"Procurement"`
}

type playerProfileResponse struct {
	DisplayName string     `json:"display_name"`
	Job         string     `json:"job"`       // e.g. "WAR/MNK"
	Fame        playerFame `json:"fame"`
	LastScene   int        `json:"last_scene"`
	ApplesCount int        `json:"apples_count"`
}

func (h *PlayerProfileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path: /api/v1/players/{slug}/profile
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/players/")
	path = strings.TrimSuffix(path, "/profile")
	slug := strings.TrimSpace(path)
	if slug == "" {
		http.Error(w, "slug required", http.StatusBadRequest)
		return
	}

	db := h.DB
	if db == nil {
		http.Error(w, "player data not available", http.StatusServiceUnavailable)
		return
	}

	// Resolve player by display_name (case-insensitive slug match).
	var playerID, displayName string
	err := db.QueryRowContext(r.Context(),
		`SELECT player_id, display_name FROM players WHERE LOWER(display_name)=LOWER(?) LIMIT 1`, slug,
	).Scan(&playerID, &displayName)
	if err == sql.ErrNoRows {
		http.Error(w, "player not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Read primary character's job and current scene.
	var jobMain, jobSub string
	var sceneID int
	_ = db.QueryRowContext(r.Context(),
		`SELECT job_main, job_sub, scene_id FROM characters WHERE player_id=? ORDER BY created_at ASC LIMIT 1`,
		playerID,
	).Scan(&jobMain, &jobSub, &sceneID)
	job := jobMain
	if jobSub != "" {
		job += "/" + jobSub
	}

	// Read fame — zero if table or row not present.
	var fame playerFame
	_ = db.QueryRowContext(r.Context(),
		`SELECT frequency, bloc, procurement FROM player_fame WHERE player_id=? LIMIT 1`, playerID,
	).Scan(&fame.Frequency, &fame.Bloc, &fame.Procurement)

	// Count Apples from IAMStore — agent_id matches player_id for GFD clients.
	applesCount := 0
	if h.Store != nil {
		apples, err := h.Store.ListApples(r.Context(), playerID, "", "", 9999)
		if err == nil {
			applesCount = len(apples)
		}
	}

	writeJSON(w, http.StatusOK, playerProfileResponse{
		DisplayName: displayName,
		Job:         job,
		Fame:        fame,
		LastScene:   sceneID,
		ApplesCount: applesCount,
	})
}
