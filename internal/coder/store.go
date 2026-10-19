// Package coder is the IDUNA-side state for the coder agent: chat sessions, a work queue the
// agent worker (emily.cli: `emily coder work`) claims turns from, and reviewer lessons that the
// worker injects into the agent's system prompt (the persistent feedback loop).
//
// IAM: every API is gated by a fine-grained permission (see perms.go). Tool calls the worker
// reports are re-checked here against the permissions of the human who sent the turn, so the
// worker cannot exercise a tool its sender was not granted.
package coder

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS coder_sessions (
  id TEXT PRIMARY KEY, owner TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS coder_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  role TEXT NOT NULL,              -- user | assistant | tool
  content TEXT NOT NULL,
  tool_name TEXT NOT NULL DEFAULT '',
  work_id INTEGER NOT NULL DEFAULT 0, -- for assistant/tool rows: the user message (turn) they answer
  sender TEXT NOT NULL DEFAULT '',
  sender_perms TEXT NOT NULL DEFAULT '[]', -- user rows: the sender's permissions at send time
  status TEXT NOT NULL DEFAULT '',  -- user rows: queued | claimed | done | failed
  worker TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS coder_messages_session ON coder_messages(session_id, id);
CREATE INDEX IF NOT EXISTS coder_messages_queue ON coder_messages(status, id);
CREATE TABLE IF NOT EXISTS coder_lessons (
  id INTEGER PRIMARY KEY AUTOINCREMENT, text TEXT NOT NULL, author TEXT NOT NULL, created_at TIMESTAMP NOT NULL
);`

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open coder db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate coder db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Session struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	WorkID    int64     `json:"work_id,omitempty"`
	Sender    string    `json:"sender,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Work is a claimed turn: the user message, the permissions of its sender, and the history.
type Work struct {
	Message
	SenderPerms []string  `json:"sender_perms"`
	History     []Message `json:"history"`
	Lessons     []Lesson  `json:"lessons"`
}

type Lesson struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateSession(id, owner, title string) (Session, error) {
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO coder_sessions (id, owner, title, created_at) VALUES (?,?,?,?)`, id, owner, title, now)
	return Session{ID: id, Owner: owner, Title: title, CreatedAt: now}, err
}

func (s *Store) GetSession(id string) (Session, error) {
	var x Session
	err := s.db.QueryRow(`SELECT id, owner, title, created_at FROM coder_sessions WHERE id=?`, id).Scan(&x.ID, &x.Owner, &x.Title, &x.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// ListSessions returns the owner's sessions, or every session when owner is "".
func (s *Store) ListSessions(owner string) ([]Session, error) {
	q, args := `SELECT id, owner, title, created_at FROM coder_sessions ORDER BY created_at DESC LIMIT 200`, []any{}
	if owner != "" {
		q, args = `SELECT id, owner, title, created_at FROM coder_sessions WHERE owner=? ORDER BY created_at DESC LIMIT 200`, []any{owner}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.Owner, &x.Title, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Send queues a user turn, recording the sender's permissions for later tool-call enforcement.
func (s *Store) Send(sessionID, sender string, perms []string, content string) (Message, error) {
	pj, _ := json.Marshal(perms)
	now := time.Now().UTC()
	r, err := s.db.Exec(`INSERT INTO coder_messages (session_id, role, content, sender, sender_perms, status, created_at) VALUES (?,?,?,?,?,?,?)`,
		sessionID, "user", content, sender, string(pj), "queued", now)
	if err != nil {
		return Message{}, err
	}
	id, _ := r.LastInsertId()
	return Message{ID: id, SessionID: sessionID, Role: "user", Content: content, Sender: sender, Status: "queued", CreatedAt: now}, nil
}

func (s *Store) Messages(sessionID string, afterID int64) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, session_id, role, content, tool_name, work_id, sender, status, created_at
		FROM coder_messages WHERE session_id=? AND id>? ORDER BY id LIMIT 500`, sessionID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolName, &m.WorkID, &m.Sender, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClaimNext atomically claims the oldest queued turn for worker. Returns ErrNotFound if none.
func (s *Store) ClaimNext(worker string) (Work, error) {
	var w Work
	var perms string
	tx, err := s.db.Begin()
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT id, session_id, role, content, sender, sender_perms, created_at FROM coder_messages
		WHERE role='user' AND status='queued' ORDER BY id LIMIT 1`).Scan(&w.ID, &w.SessionID, &w.Role, &w.Content, &w.Sender, &perms, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if _, err := tx.Exec(`UPDATE coder_messages SET status='claimed', worker=? WHERE id=?`, worker, w.ID); err != nil {
		return w, err
	}
	if err := tx.Commit(); err != nil {
		return w, err
	}
	w.Status = "claimed"
	json.Unmarshal([]byte(perms), &w.SenderPerms)
	hist, err := s.Messages(w.SessionID, 0)
	if err != nil {
		return w, err
	}
	for _, m := range hist {
		if m.ID < w.ID && (m.Role == "user" || (m.Role == "assistant" && m.ToolName == "")) {
			w.History = append(w.History, m)
		}
	}
	w.Lessons, _ = s.Lessons()
	return w, nil
}

// Report records a worker event (assistant text or a tool call/result) against a claimed turn.
// A tool event is refused unless the turn's sender held coder.tool.<name> (or coder.admin /
// iduna.admin): server-side tool IAM, independent of what the worker claims to enforce.
func (s *Store) Report(workID int64, worker, role, toolName, content string) (Message, error) {
	var sessionID, perms, status, claimedBy string
	err := s.db.QueryRow(`SELECT session_id, sender_perms, status, worker FROM coder_messages WHERE id=? AND role='user'`, workID).
		Scan(&sessionID, &perms, &status, &claimedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	if status != "claimed" || claimedBy != worker {
		return Message{}, fmt.Errorf("%w: turn %d is not claimed by %q", ErrForbidden, workID, worker)
	}
	if role != "assistant" && role != "tool" {
		return Message{}, fmt.Errorf("bad role %q", role)
	}
	if role == "tool" {
		var ps []string
		json.Unmarshal([]byte(perms), &ps)
		if !Allows(ps, ToolPerm(toolName)) {
			return Message{}, fmt.Errorf("%w: sender lacks %s", ErrForbidden, ToolPerm(toolName))
		}
	}
	now := time.Now().UTC()
	r, err := s.db.Exec(`INSERT INTO coder_messages (session_id, role, content, tool_name, work_id, created_at) VALUES (?,?,?,?,?,?)`,
		sessionID, role, content, toolName, workID, now)
	if err != nil {
		return Message{}, err
	}
	id, _ := r.LastInsertId()
	return Message{ID: id, SessionID: sessionID, Role: role, Content: content, ToolName: toolName, WorkID: workID, CreatedAt: now}, nil
}

// Finish marks a claimed turn done or failed.
func (s *Store) Finish(workID int64, worker string, ok bool) error {
	st := "done"
	if !ok {
		st = "failed"
	}
	r, err := s.db.Exec(`UPDATE coder_messages SET status=? WHERE id=? AND role='user' AND status='claimed' AND worker=?`, st, workID, worker)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Lessons() ([]Lesson, error) {
	rows, err := s.db.Query(`SELECT id, text, author, created_at FROM coder_lessons ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lesson{}
	for rows.Next() {
		var l Lesson
		if err := rows.Scan(&l.ID, &l.Text, &l.Author, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) AddLesson(text, author string) (Lesson, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 400 {
		return Lesson{}, errors.New("lesson must be 1-400 chars (the agent's context is small)")
	}
	now := time.Now().UTC()
	r, err := s.db.Exec(`INSERT INTO coder_lessons (text, author, created_at) VALUES (?,?,?)`, text, author, now)
	if err != nil {
		return Lesson{}, err
	}
	id, _ := r.LastInsertId()
	return Lesson{ID: id, Text: text, Author: author, CreatedAt: now}, nil
}

func (s *Store) DeleteLesson(id int64) error {
	r, err := s.db.Exec(`DELETE FROM coder_lessons WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
