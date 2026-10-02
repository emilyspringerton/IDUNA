package handlers

// kanban_comments.go -- comments on kanban cards. Founder real-time, 2026-10-02: "add comments to kanban
// cards so you can leave your questions as comments and a kanban user can leave replies it should be
// tracked by login". Claude (via the bearer API / `emily kanban comment`) asks a question on the card; a
// board user answers in the browser; every comment records who posted it from the caller's own token.
//
//	GET  .../cards/{id}/comments           -> [{"id":1,"card_id":7,"author":"frostpenelope1@gmail.com","author_sub":"...","body":"...","created_at":"..."}]
//	POST .../cards/{id}/comments {"body":"..."}  -> 201, the stored comment
//
// Served by KanbanHandler on both the bearer (/api/v1/kanban/cards/...) and cookie (/admin/kanban/api/cards/...) routes.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/http/middleware"
)

const kanbanCommentMax = 4000

type kanbanCommentOut struct {
	ID        int64  `json:"id"`
	CardID    int64  `json:"card_id"`
	Author    string `json:"author"`
	AuthorSub string `json:"author_sub"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// kanbanCommentsCardID returns the card id when path is .../cards/{id}/comments.
func kanbanCommentsCardID(path string) (int64, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-1] != "comments" {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[len(parts)-2], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// kanbanAuthor resolves who is calling from the token claims: subject for identity, and the most
// readable of agent_name / email / name for display.
func kanbanAuthor(claims map[string]any) (sub, name string) {
	sub, _ = claims["sub"].(string)
	for _, k := range []string{"agent_name", "email", "name"} {
		if v, ok := claims[k].(string); ok && v != "" {
			return sub, v
		}
	}
	return sub, sub
}

func (h *KanbanHandler) comments(w http.ResponseWriter, r *http.Request, cardID int64) {
	var exists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM kanban_cards WHERE id = ?`, cardID).Scan(&exists); err != nil || exists == 0 {
		http.Error(w, "card not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := h.DB.QueryContext(r.Context(),
			`SELECT id, card_id, author_sub, author_name, body, created_at FROM kanban_comments WHERE card_id = ? ORDER BY id ASC`, cardID)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := []kanbanCommentOut{}
		for rows.Next() {
			var c kanbanCommentOut
			if err := rows.Scan(&c.ID, &c.CardID, &c.AuthorSub, &c.Author, &c.Body, &c.CreatedAt); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				return
			}
			out = append(out, c)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		text := strings.TrimSpace(body.Body)
		if text == "" || len(text) > kanbanCommentMax {
			http.Error(w, "body must be 1-4000 characters", http.StatusBadRequest)
			return
		}
		sub, name := kanbanAuthor(middleware.ClaimsFromContext(r.Context()))
		if sub == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		res, err := h.DB.ExecContext(r.Context(),
			`INSERT INTO kanban_comments (card_id, author_sub, author_name, body) VALUES (?, ?, ?, ?)`, cardID, sub, name, text)
		if err != nil {
			http.Error(w, "insert failed", http.StatusInternalServerError)
			return
		}
		id, _ := res.LastInsertId()
		var c kanbanCommentOut
		if err := h.DB.QueryRowContext(r.Context(),
			`SELECT id, card_id, author_sub, author_name, body, created_at FROM kanban_comments WHERE id = ?`, id).
			Scan(&c.ID, &c.CardID, &c.AuthorSub, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			http.Error(w, "read back failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
