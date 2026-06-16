package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// ApplesHandler handles /api/v1/apples routes.
// POST /api/v1/apples                  requires apples.write
// GET  /api/v1/apples                  requires apples.read
// GET  /api/v1/apples/{id}             requires apples.read
// GET  /api/v1/apples/stats/daily-tokens?days=7  requires apples.read
type ApplesHandler struct {
	Store        store.IAMStore
	ApplesGitDir string // path to APPLES git repo; if set, every new Apple is auto-synced
}

func (h *ApplesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip /api/v1/apples prefix and check for sub-paths.
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/apples")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodPost:
			h.create(w, r)
		case http.MethodGet:
			h.list(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}

	if path == "stats/daily-tokens" {
		if r.Method == http.MethodGet {
			h.dailyTokenStats(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}

	// path is the id segment
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "invalid apple id",
		})
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.get(w, r, id)
}

// POST /api/v1/apples
func (h *ApplesHandler) create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.write") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.write permission required",
		})
		return
	}

	agentID := middleware.SubjectFromContext(r.Context())
	if agentID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":    "UNAUTHORIZED",
			"message": "missing subject",
		})
		return
	}

	var body struct {
		SourceRepo string          `json:"source_repo"`
		RunID      string          `json:"run_id"`
		AppleType  string          `json:"apple_type"`
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		Metadata   json.RawMessage `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "invalid JSON body",
		})
		return
	}
	if body.SourceRepo == "" || body.RunID == "" || body.AppleType == "" || body.Title == "" || body.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "BAD_REQUEST",
			"message": "source_repo, run_id, apple_type, title, and body are required",
		})
		return
	}

	apple := auth.AppleRecord{
		AgentID:    agentID,
		SourceRepo: body.SourceRepo,
		RunID:      body.RunID,
		AppleType:  body.AppleType,
		Title:      body.Title,
		Body:       body.Body,
		Metadata:   []byte(body.Metadata),
	}
	id, err := h.Store.AppendApple(r.Context(), apple)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to store apple",
		})
		return
	}
	if h.ApplesGitDir != "" {
		apple.ID = id
		go h.syncAppleToGit(apple)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// GET /api/v1/apples
func (h *ApplesHandler) list(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sourceRepo := q.Get("source_repo")
	appleType := q.Get("apple_type")
	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	apples, err := h.Store.ListApples(r.Context(), agentID, sourceRepo, appleType, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to list apples",
		})
		return
	}

	type appleListItem struct {
		ID         int64  `json:"id"`
		AgentID    string `json:"agent_id"`
		SourceRepo string `json:"source_repo"`
		RunID      string `json:"run_id"`
		AppleType  string `json:"apple_type"`
		Title      string `json:"title"`
		RecordedAt string `json:"recorded_at"`
	}
	items := make([]appleListItem, 0, len(apples))
	for _, a := range apples {
		items = append(items, appleListItem{
			ID:         a.ID,
			AgentID:    a.AgentID,
			SourceRepo: a.SourceRepo,
			RunID:      a.RunID,
			AppleType:  a.AppleType,
			Title:      a.Title,
			RecordedAt: a.RecordedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apples": items})
}

// GET /api/v1/apples/{id}
func (h *ApplesHandler) get(w http.ResponseWriter, r *http.Request, id int64) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	apple, err := h.Store.GetApple(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"code":    "NOT_FOUND",
			"message": "apple not found",
		})
		return
	}

	var meta any
	if len(apple.Metadata) > 0 && string(apple.Metadata) != "null" {
		_ = json.Unmarshal(apple.Metadata, &meta)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          apple.ID,
		"agent_id":    apple.AgentID,
		"source_repo": apple.SourceRepo,
		"run_id":      apple.RunID,
		"apple_type":  apple.AppleType,
		"title":       apple.Title,
		"body":        apple.Body,
		"metadata":    meta,
		"recorded_at": apple.RecordedAt.UTC().Format(time.RFC3339Nano),
	})
}

// GET /api/v1/apples/stats/daily-tokens?days=7
// Returns daily token usage aggregated from Apple metadata for sparkline display.
// Response: {"days": 7, "stats": [{"date":"2026-06-14","tokens":12345}, ...]}
func (h *ApplesHandler) dailyTokenStats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if !hasClaimPermission(claims, "apples.read") && !hasClaimPermission(claims, "apples.admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "apples.read permission required",
		})
		return
	}

	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}

	stats, err := h.Store.DailyTokenStats(r.Context(), days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "INTERNAL",
			"message": "failed to aggregate token stats",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"days":  days,
		"stats": stats,
	})
}

// syncAppleToGit writes the Apple as a JSON file to ApplesGitDir, updates MANIFEST.json,
// commits both, and pushes. Runs as a goroutine; all failures are logged and non-fatal.
func (h *ApplesHandler) syncAppleToGit(apple auth.AppleRecord) {
	gitDir := h.ApplesGitDir
	today := time.Now().UTC().Format("20060102")

	// Write YYYYMMDD/NNN_type.json
	dir := filepath.Join(gitDir, today)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[apples-git] mkdir %s: %v", dir, err)
		return
	}
	fname := fmt.Sprintf("%d_%s.json", apple.ID, strings.ReplaceAll(apple.AppleType, "_", "-"))
	fpath := filepath.Join(dir, fname)
	record := map[string]any{
		"id":          apple.ID,
		"agent_id":    apple.AgentID,
		"apple_type":  apple.AppleType,
		"source_repo": apple.SourceRepo,
		"run_id":      apple.RunID,
		"title":       apple.Title,
		"body":        apple.Body,
		"archived_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		log.Printf("[apples-git] marshal apple: %v", err)
		return
	}
	if err := os.WriteFile(fpath, append(data, '\n'), 0o644); err != nil {
		log.Printf("[apples-git] write %s: %v", fpath, err)
		return
	}

	// Update MANIFEST.json
	appleGitUpdateManifest(gitDir, apple, today)

	// git add -A + commit + push
	title := apple.Title
	if len(title) > 60 {
		title = title[:60]
	}
	commitMsg := fmt.Sprintf("apple: #%d %s — %s", apple.ID, apple.AppleType, title)
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=iduna", "GIT_AUTHOR_EMAIL=iduna@einhorn.internal",
		"GIT_COMMITTER_NAME=iduna", "GIT_COMMITTER_EMAIL=iduna@einhorn.internal",
	)
	addCmd := exec.Command("git", "-C", gitDir, "add", "-A")
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("[apples-git] git add: %v\n%s", err, out)
		return
	}
	commitCmd := exec.Command("git", "-C", gitDir, "commit", "-m", commitMsg)
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("[apples-git] git commit: %v\n%s", err, out)
		return
	}
	pushCmd := exec.Command("git", "-C", gitDir, "push")
	pushCmd.Env = gitEnv
	if out, err := pushCmd.CombinedOutput(); err != nil {
		log.Printf("[apples-git] git push: %v\n%s", err, out)
		return
	}
	log.Printf("[apples-git] synced Apple #%d → %s/%s", apple.ID, today, fname)
}

// appleGitUpdateManifest reads MANIFEST.json, appends the new entry, and writes it back.
// Best-effort: failures are logged, sync continues.
func appleGitUpdateManifest(gitDir string, apple auth.AppleRecord, date string) {
	type manifestEntry struct {
		ID         int64  `json:"id"`
		Type       string `json:"type"`
		Title      string `json:"title"`
		SourceRepo string `json:"source_repo"`
		Date       string `json:"date"`
		ArchivedAt string `json:"archived_at"`
	}
	type manifest struct {
		GeneratedAt string          `json:"generated_at"`
		Repo        string          `json:"repo"`
		Count       int             `json:"count"`
		Apples      []manifestEntry `json:"apples"`
	}

	manifestPath := filepath.Join(gitDir, "MANIFEST.json")
	var m manifest
	if raw, err := os.ReadFile(manifestPath); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	if m.Repo == "" {
		m.Repo = "APPLES"
	}
	for _, e := range m.Apples {
		if e.ID == apple.ID {
			return // idempotent
		}
	}
	title := apple.Title
	if len(title) > 140 {
		title = title[:140]
	}
	m.Apples = append(m.Apples, manifestEntry{
		ID:         apple.ID,
		Type:       apple.AppleType,
		Title:      title,
		SourceRepo: apple.SourceRepo,
		Date:       date,
		ArchivedAt: time.Now().UTC().Format(time.RFC3339),
	})
	m.Count = len(m.Apples)
	m.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		log.Printf("[apples-git] manifest marshal: %v", err)
		return
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		log.Printf("[apples-git] manifest write: %v", err)
	}
}

// hasClaimPermission checks the "permissions" claim in the JWT for a specific permission.
// This duplicates the logic in middleware but allows the handler to check multiple
// permissions without middleware wrapping each route individually.
func hasClaimPermission(claims map[string]any, perm string) bool {
	if claims == nil {
		return false
	}
	perms, ok := claims["permissions"]
	if !ok {
		return false
	}
	switch v := perms.(type) {
	case []any:
		for _, p := range v {
			if s, ok := p.(string); ok && s == perm {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if s == perm {
				return true
			}
		}
	}
	return false
}
