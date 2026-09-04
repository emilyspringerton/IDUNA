package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"iduna/internal/http/middleware"
)

// NotesHandler is IDUNA Notebook's own real Phase 1 (kanban IN-000/IN-001, "we need an icloud
// like affordances for creating notes... notebooks are sarena based but actually just advertised
// as regular notes"; full real scoping in docs/IDUNA_NOTEBOOK_NORTHSTAR.md). Real, deliberate
// design: plain, self-serve notes CRUD, owner-scoped by the caller's own real JWT subject, gated
// behind ordinary RequireAuth -- NOT the shared admin permission kanban.go's own board reuses,
// since this is a real, personal, per-user feature (the literal "iCloud Notes" shape), not
// internal sprint-planning tooling. No code-cell chrome, no JEWEL/Jupyter concept anywhere in
// this file -- answers this scoping pass's own real open question 1 toward "a genuinely separate
// CRUD feature," not a JEWEL-backed one.
type NotesHandler struct {
	DB *sql.DB
}

type note struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (h *NotesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "notes not available", http.StatusServiceUnavailable)
		return
	}
	sub := middleware.SubjectFromContext(r.Context())
	if sub == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.list(w, r, sub)
	case http.MethodPost:
		h.create(w, r, sub)
	case http.MethodPatch:
		h.update(w, r, sub)
	case http.MethodDelete:
		h.delete(w, r, sub)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *NotesHandler) list(w http.ResponseWriter, r *http.Request, sub string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, title, body, created_at, updated_at FROM notes
		 WHERE owner_subject = ? ORDER BY updated_at DESC, id DESC`, sub)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.CreatedAt, &n.UpdatedAt); err != nil {
			continue
		}
		out = append(out, n)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *NotesHandler) create(w http.ResponseWriter, r *http.Request, sub string) {
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		body.Title = "Untitled Note" // a real, honest default -- an iCloud-Notes-shaped note is
		// allowed to start with zero real typed content, matching that real product's own
		// "tap + to create a blank note" convention, not a hard validation error here.
	}
	if len(body.Title) > 200 {
		body.Title = body.Title[:200]
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO notes (owner_subject, title, body) VALUES (?, ?, ?)`,
		sub, body.Title, body.Body)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
}

func (h *NotesHandler) update(w http.ResponseWriter, r *http.Request, sub string) {
	id, ok := kanbanIDFromPath(r.URL.Path) // real, shared "last path segment as int64" helper --
	// kanban.go's own, generic enough to reuse rather than duplicate.
	if !ok {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	var body struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Title == nil && body.Body == nil {
		http.Error(w, "nothing to update -- provide title and/or body", http.StatusBadRequest)
		return
	}

	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
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
	if body.Body != nil {
		sets = append(sets, "body = ?")
		args = append(args, *body.Body)
	}
	// Real, decisive ownership check: owner_subject = ? in the WHERE clause, not a separate
	// SELECT-then-check -- a caller can never even learn whether a note with this id exists
	// under someone ELSE's ownership (a real, honest 404, not a 403 that would leak existence).
	args = append(args, id, sub)

	res, err := h.DB.ExecContext(r.Context(),
		"UPDATE notes SET "+strings.Join(sets, ", ")+" WHERE id = ? AND owner_subject = ?", args...)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *NotesHandler) delete(w http.ResponseWriter, r *http.Request, sub string) {
	id, ok := kanbanIDFromPath(r.URL.Path)
	if !ok {
		http.Error(w, "invalid note id", http.StatusBadRequest)
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`DELETE FROM notes WHERE id = ? AND owner_subject = ?`, id, sub)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}
