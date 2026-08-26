package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
type KanbanHandler struct {
	DB *sql.DB
}

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
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
