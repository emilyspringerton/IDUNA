package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func newJobLevelsDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newInventoryDB(t) // real characters table, same convention every mmo_*_test.go uses
	_, err := db.Exec(`
		CREATE TABLE character_job_levels (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			character_id  TEXT NOT NULL,
			job           TEXT NOT NULL,
			level         INTEGER NOT NULL DEFAULT 1,
			current_xp    INTEGER NOT NULL DEFAULT 0,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create character_job_levels: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_cjl_char_job ON character_job_levels(character_id, job)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return db
}

func patchJobLevel(t *testing.T, h http.Handler, token, characterID, job string, level, currentXP int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]int{"level": level, "current_xp": currentXP})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/"+characterID+"/job-levels/"+job, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func getJobLevels(t *testing.T, h http.Handler, token, characterID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/"+characterID+"/job-levels", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpdateJobLevel_AgentJWTSucceedsAndUpserts(t *testing.T) {
	db := newJobLevelsDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-job-1")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-10", "DRAGONSNSHIT-MUD")

	rec := patchJobLevel(t, h, token, "char-job-1", "RDM", 1, 0)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("create: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Second write to the SAME (character, job) pair must upsert, not duplicate.
	rec2 := patchJobLevel(t, h, token, "char-job-1", "RDM", 5, 120)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("update: expected 204, got %d: %s", rec2.Code, rec2.Body.String())
	}

	recList := getJobLevels(t, h, token, "char-job-1")
	if recList.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", recList.Code, recList.Body.String())
	}
	var levels []handlers.JobLevelEntry
	if err := json.Unmarshal(recList.Body.Bytes(), &levels); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(levels) != 1 || levels[0].Job != "RDM" || levels[0].Level != 5 || levels[0].CurrentXP != 120 {
		t.Fatalf("expected exactly one upserted RDM row at level 5, got %+v", levels)
	}
}

func TestUpdateJobLevel_MultipleJobsTrackedIndependently(t *testing.T) {
	db := newJobLevelsDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-job-2")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-11", "DRAGONSNSHIT-MUD")

	patchJobLevel(t, h, token, "char-job-2", "WAR", 10, 500)
	patchJobLevel(t, h, token, "char-job-2", "RDM", 1, 0)

	recList := getJobLevels(t, h, token, "char-job-2")
	var levels []handlers.JobLevelEntry
	json.Unmarshal(recList.Body.Bytes(), &levels)
	if len(levels) != 2 {
		t.Fatalf("expected 2 independently-tracked jobs, got %+v", levels)
	}
	byJob := map[string]handlers.JobLevelEntry{}
	for _, l := range levels {
		byJob[l.Job] = l
	}
	if byJob["WAR"].Level != 10 {
		t.Errorf("WAR level: got %d, want 10 (switching to RDM must not affect it)", byJob["WAR"].Level)
	}
	if byJob["RDM"].Level != 1 {
		t.Errorf("RDM level: got %d, want 1 (a job played for the first time starts at 1)", byJob["RDM"].Level)
	}
}

func TestUpdateJobLevel_PlayerJWTRejected(t *testing.T) {
	db := newJobLevelsDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-job-3")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "player-1")

	rec := patchJobLevel(t, h, token, "char-job-3", "WAR", 99, 0)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateJobLevel_CharacterNotFound(t *testing.T) {
	db := newJobLevelsDB(t)
	defer db.Close()

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-12", "DRAGONSNSHIT-MUD")

	rec := patchJobLevel(t, h, token, "does-not-exist", "WAR", 5, 0)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetJobLevels_EmptyForNeverPlayedCharacter(t *testing.T) {
	db := newJobLevelsDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-job-4")

	h := &handlers.MMOHandler{DB: db}
	rec := getJobLevels(t, h, "", "char-job-4")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var levels []handlers.JobLevelEntry
	json.Unmarshal(rec.Body.Bytes(), &levels)
	if len(levels) != 0 {
		t.Errorf("expected an empty list for a character with no job-level rows yet, got %+v", levels)
	}
}
