package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"iduna/internal/backlog"
	"iduna/internal/http/middleware"
)

// KanbanHandler is the prioritization layer on top of EMILY/BACKLOG.md's own
// sprint sections. Founder real-time, built up across several messages: "ok
// lets build a kanban layer on top of our sprints that lets us assign
// priority for the next open dev to picj up - for now it can be simple 2
// tiers of special next queues priority and cruise" -> "it allows us to
// drag from backlog into 1 of the 2 priority queues backlog numbers stay
// the same it gets a kanban tracking something" -> "gui kanban interface 3
// columns in iduna" -> "i can ask the ai agent to work from the priority or
// cruise backlog".
//
// backlog_item_id (e.g. "S202-27") is the real BACKLOG.md item id -- this
// table only tracks which of the 3 columns (backlog/priority/cruise) a
// card sits in and its position within that column, never a copy of the
// item's own text. BACKLOG.md itself stays the one authoritative source of
// the item's actual content/status.
//
//	GET    /api/v1/kanban/cards[?queue=priority]
//	  -> [{"id":1,"backlog_item_id":"S202-27","title":"...","queue":"priority","position":0,...}]
//	POST   /api/v1/kanban/cards   {"backlog_item_id":"S202-27","title":"..."}
//	  -> 201, {"id":1}  (queue defaults to "backlog")
//	PATCH  /api/v1/kanban/cards/{id}   {"queue":"priority","position":2}
//	  -> 200  (the "drag" action -- moves a card between columns and/or reorders within one)
//	DELETE /api/v1/kanban/cards/{id}
//	  -> 204
// BacklogPath, when set, turns on eventual-consistency sync with
// EMILY/BACKLOG.md itself (founder real-time, 2026-09-02: "if it gets
// added to backlog via the kanban interface it needs to wind up in the
// golden backlog file in git and as we work it needs to all stay in sync
// -- for example when we finish something it needs to move off the kanban
// board"). Two real, independent directions, both best-effort/fire-and-
// forget (never blocks or fails the real DB write a caller is waiting on):
//
//  1. create(): a new card whose backlog_item_id isn't already a real
//     line in BACKLOG.md gets one appended for real, committed, and
//     pushed (see syncNewItemToBacklogGit) -- the same real
//     git-add/commit/push-with-retry idiom apples.go's own
//     syncAppleToGit already established, not a new pattern.
//  2. list(): any card whose backlog_item_id is confirmed CHECKED in the
//     live file is deleted from kanban_cards before being returned --
//     "finishing something moves it off the board" for real, not just
//     visually.
//
// Empty BacklogPath disables both (kanban still works as pure metadata,
// the original design) -- a real, deliberate off switch, not an oversight.
type KanbanHandler struct {
	DB          *sql.DB
	BacklogPath string
}

// backlogFileMu serializes writes to BACKLOG.md + its git add/commit/push
// across concurrent create() calls -- a distinct mutex from apples.go's own
// gitSyncMu since it guards a different working tree (EMILY, not APPLES).
var backlogFileMu sync.Mutex

// kanbanIntakeSectionHeading is the one standing, real section every
// kanban-originated new item lands under -- deliberately NOT guessing
// which existing topical SECTION a card typed into the kanban UI belongs
// to (kanban.go's own doc comment already establishes IDs and containing
// section numbers as independent, real, unrelated numbers elsewhere in
// this file). A human/agent can re-file an entry into a more fitting
// section later; this just guarantees it's real, in git, and never lost.
const kanbanIntakeSectionHeading = "## SECTION 9000: ADDED VIA IDUNA KANBAN INTERFACE (eventual-consistency intake)"

type kanbanCard struct {
	ID            int64  `json:"id"`
	BacklogItemID string `json:"backlog_item_id"`
	Title         string `json:"title"`
	Queue         string `json:"queue"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

var validKanbanQueues = map[string]bool{"backlog": true, "priority": true, "cruise": true}

func (h *KanbanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "kanban not available", http.StatusServiceUnavailable)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	case http.MethodPatch:
		h.update(w, r)
	case http.MethodDelete:
		h.delete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *KanbanHandler) list(w http.ResponseWriter, r *http.Request) {
	queue := r.URL.Query().Get("queue")
	var rows *sql.Rows
	var err error
	if queue != "" {
		if !validKanbanQueues[queue] {
			http.Error(w, "queue must be one of: backlog, priority, cruise", http.StatusBadRequest)
			return
		}
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, backlog_item_id, title, queue, position, created_at, updated_at
			 FROM kanban_cards WHERE queue = ? ORDER BY position ASC, id ASC`, queue)
	} else {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, backlog_item_id, title, queue, position, created_at, updated_at
			 FROM kanban_cards ORDER BY queue ASC, position ASC, id ASC`)
	}
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []kanbanCard{}
	for rows.Next() {
		var c kanbanCard
		if err := rows.Scan(&c.ID, &c.BacklogItemID, &c.Title, &c.Queue, &c.Position, &c.CreatedAt, &c.UpdatedAt); err != nil {
			continue
		}
		out = append(out, c)
	}
	rows.Close()

	out = h.removeCompletedCards(r, out)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// removeCompletedCards: "when we finish something it needs to move off the
// kanban board" (founder real-time, 2026-09-02) -- any card whose
// backlog_item_id is confirmed CHECKED in the live BACKLOG.md is deleted
// from kanban_cards for real (not just hidden), then excluded from this
// response, so the board reflects "done" the moment the file does. A card
// whose id isn't found at all in the file (renamed section, moved,
// deleted) is left alone -- only a POSITIVE checked confirmation removes
// anything, never an absence. Best-effort: a backlog read failure just
// returns cards unfiltered, logged, same as every other best-effort path
// in this file.
func (h *KanbanHandler) removeCompletedCards(r *http.Request, cards []kanbanCard) []kanbanCard {
	if h.BacklogPath == "" || len(cards) == 0 {
		return cards
	}
	items, err := backlog.ParseFile(h.BacklogPath)
	if err != nil {
		log.Printf("[kanban] read backlog for completed-card check: %v", err)
		return cards
	}
	byID := backlog.ByID(items)

	kept := make([]kanbanCard, 0, len(cards))
	for _, c := range cards {
		if it, ok := byID[c.BacklogItemID]; ok && it.Checked {
			if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM kanban_cards WHERE id = ?`, c.ID); err != nil {
				log.Printf("[kanban] failed to remove completed card id=%d (%s): %v", c.ID, c.BacklogItemID, err)
				kept = append(kept, c) // couldn't remove it -- still show it rather than silently drop
				continue
			}
			log.Printf("[kanban] %s marked done in BACKLOG.md -- removed from the board", c.BacklogItemID)
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func (h *KanbanHandler) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BacklogItemID string `json:"backlog_item_id"`
		Title         string `json:"title"`
		Queue         string `json:"queue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	body.BacklogItemID = strings.TrimSpace(body.BacklogItemID)
	body.Title = strings.TrimSpace(body.Title)
	if body.BacklogItemID == "" || len(body.BacklogItemID) > 32 {
		http.Error(w, "backlog_item_id required, max 32 chars", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	if len(body.Title) > 200 {
		body.Title = body.Title[:200]
	}
	if body.Queue == "" {
		body.Queue = "backlog"
	}
	if !validKanbanQueues[body.Queue] {
		http.Error(w, "queue must be one of: backlog, priority, cruise", http.StatusBadRequest)
		return
	}

	// New cards land at the end of their column -- real next-position lookup,
	// not just 0, so a fresh card doesn't jump ahead of everything already
	// ranked there.
	var maxPos sql.NullInt64
	_ = h.DB.QueryRowContext(r.Context(), `SELECT MAX(position) FROM kanban_cards WHERE queue = ?`, body.Queue).Scan(&maxPos)
	nextPos := 0
	if maxPos.Valid {
		nextPos = int(maxPos.Int64) + 1
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO kanban_cards (backlog_item_id, title, queue, position) VALUES (?, ?, ?, ?)`,
		body.BacklogItemID, body.Title, body.Queue, nextPos)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int64{"id": id})

	// Fire-and-forget: never let a slow/failed git sync hold up the response
	// a caller (the kanban page's own fetch, or a real bearer-auth agent
	// caller) is waiting on for the DB write, which has already succeeded.
	if h.BacklogPath != "" {
		go h.syncNewItemToBacklogGitIfMissing(body.BacklogItemID, body.Title)
	}
}

// syncNewItemToBacklogGitIfMissing checks the live file first (best-effort
// -- a read failure just skips the sync, logged, same as every other
// best-effort path here) and only appends+commits+pushes if id genuinely
// isn't already a real line in BACKLOG.md. A card created for an id that
// DOES already exist (the normal case -- most cards track a real item
// already written by a human/agent session) is a real no-op here, exactly
// as it should be: BACKLOG.md already has it, nothing to sync.
func (h *KanbanHandler) syncNewItemToBacklogGitIfMissing(id, title string) {
	items, err := backlog.ParseFile(h.BacklogPath)
	if err != nil {
		log.Printf("[kanban-git] read backlog: %v", err)
		return
	}
	if _, exists := backlog.ByID(items)[id]; exists {
		return
	}

	backlogFileMu.Lock()
	defer backlogFileMu.Unlock()

	// Re-check under the lock -- a concurrent create() for the same id
	// could have already appended it while this goroutine was waiting.
	items, err = backlog.ParseFile(h.BacklogPath)
	if err != nil {
		log.Printf("[kanban-git] re-read backlog: %v", err)
		return
	}
	if _, exists := backlog.ByID(items)[id]; exists {
		return
	}

	data, err := os.ReadFile(h.BacklogPath)
	if err != nil {
		log.Printf("[kanban-git] read %s: %v", h.BacklogPath, err)
		return
	}
	text := string(data)

	sessTag := currentSessionTag(emilyRootDefault())
	sessSuffix := ""
	if sessTag != "" {
		sessSuffix = "\n  (" + sessTag + ")"
	}
	entry := fmt.Sprintf("- [ ] **%s: %s** Added via the IDUNA kanban interface, not yet triaged into a real section.%s\n", id, title, sessSuffix)

	if !strings.Contains(text, kanbanIntakeSectionHeading) {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + kanbanIntakeSectionHeading + "\n\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += entry

	if err := os.WriteFile(h.BacklogPath, []byte(text), 0o644); err != nil {
		log.Printf("[kanban-git] write %s: %v", h.BacklogPath, err)
		return
	}

	emilyRoot := filepath.Dir(h.BacklogPath)
	commitMsg := fmt.Sprintf("backlog: + %s (added via IDUNA kanban interface)", id)
	if sessTag != "" {
		commitMsg += "\n\nsession: " + sessTag
	}
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=iduna", "GIT_AUTHOR_EMAIL=iduna@einhorn.internal",
		"GIT_COMMITTER_NAME=iduna", "GIT_COMMITTER_EMAIL=iduna@einhorn.internal",
	)
	addCmd := exec.Command("git", "-C", emilyRoot, "add", "BACKLOG.md")
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("[kanban-git] git add: %v\n%s", err, out)
		return
	}
	commitCmd := exec.Command("git", "-C", emilyRoot, "commit", "-m", commitMsg)
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("[kanban-git] git commit: %v\n%s", err, out)
		return
	}
	if err := gitPushWithRetry("kanban-git", emilyRoot, gitEnv); err != nil {
		log.Printf("[kanban-git] git push failed after retry: %v", err)
		return
	}
	log.Printf("[kanban-git] synced new backlog item %s → BACKLOG.md", id)
}

func (h *KanbanHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := kanbanIDFromPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid card id", http.StatusBadRequest)
		return
	}
	var body struct {
		Queue    *string `json:"queue"`
		Position *int    `json:"position"`
		Title    *string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Queue == nil && body.Position == nil && body.Title == nil {
		http.Error(w, "nothing to update -- provide queue, position, and/or title", http.StatusBadRequest)
		return
	}
	if body.Queue != nil && !validKanbanQueues[*body.Queue] {
		http.Error(w, "queue must be one of: backlog, priority, cruise", http.StatusBadRequest)
		return
	}

	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if body.Queue != nil {
		sets = append(sets, "queue = ?")
		args = append(args, *body.Queue)
	}
	if body.Position != nil {
		sets = append(sets, "position = ?")
		args = append(args, *body.Position)
	}
	if body.Title != nil {
		t := strings.TrimSpace(*body.Title)
		if t == "" {
			http.Error(w, "title cannot be blank", http.StatusBadRequest)
			return
		}
		if len(t) > 200 {
			t = t[:200]
		}
		sets = append(sets, "title = ?")
		args = append(args, t)
	}
	args = append(args, id)

	res, err := h.DB.ExecContext(r.Context(),
		"UPDATE kanban_cards SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "card not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *KanbanHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := kanbanIDFromPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid card id", http.StatusBadRequest)
		return
	}
	res, err := h.DB.ExecContext(r.Context(), `DELETE FROM kanban_cards WHERE id = ?`, id)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "card not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// kanbanIDFromPath extracts the numeric id from /api/v1/kanban/cards/{id}.
func kanbanIDFromPath(path string) (int64, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return 0, false
	}
	last := parts[len(parts)-1]
	id, err := strconv.ParseInt(last, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
