package nock

// edu_snippet_store.go -- CRUD + SQLite persistence for NOCK EduVM snippets (card #495): saveable EduScript
// source attached to a widget by name. Same shape as door_script_store.go minus the compile step (the EduVM
// compiles and sandboxes in the game itself).

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EduSnippetMaxSource matches EDU_MAX_SCRIPT_TEXT in SHANKPIT packages/education/edu_script.h (2048, with the NUL).
const EduSnippetMaxSource = 2047

type EduSnippet struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	WidgetName string `json:"widget_name"`
	Source     string `json:"source"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type EduSnippetStore struct {
	DB *sql.DB
}

func validateEduSnippet(name, widget, source string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateName(widget); err != nil {
		return fmt.Errorf("widget_name: %w", err)
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("nock: snippet source is empty")
	}
	if len(source) > EduSnippetMaxSource {
		return fmt.Errorf("nock: snippet source is %d bytes, max %d (the EduVM slot size)", len(source), EduSnippetMaxSource)
	}
	return nil
}

func (s *EduSnippetStore) Create(ctx context.Context, name, widget, source string) (*EduSnippet, error) {
	if err := validateEduSnippet(name, widget, source); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO nock_edu_snippets (name, widget_name, source) VALUES (?, ?, ?)`, name, widget, source)
	if err != nil {
		return nil, fmt.Errorf("nock: create edu snippet: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create edu snippet: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *EduSnippetStore) Get(ctx context.Context, id int64) (*EduSnippet, error) {
	var e EduSnippet
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, widget_name, source, created_at, updated_at FROM nock_edu_snippets WHERE id = ?`, id).
		Scan(&e.ID, &e.Name, &e.WidgetName, &e.Source, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("nock: edu snippet not found")
	}
	if err != nil {
		return nil, fmt.Errorf("nock: get edu snippet: %w", err)
	}
	return &e, nil
}

// List returns snippets oldest-first (stable slot order for the game); widget == "" means every widget.
func (s *EduSnippetStore) List(ctx context.Context, widget string) ([]EduSnippet, error) {
	q := `SELECT id, name, widget_name, source, created_at, updated_at FROM nock_edu_snippets`
	args := []any{}
	if widget != "" {
		q += ` WHERE widget_name = ?`
		args = append(args, widget)
	}
	q += ` ORDER BY id ASC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("nock: list edu snippets: %w", err)
	}
	defer rows.Close()
	out := []EduSnippet{}
	for rows.Next() {
		var e EduSnippet
		if err := rows.Scan(&e.ID, &e.Name, &e.WidgetName, &e.Source, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("nock: list edu snippets: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *EduSnippetStore) Update(ctx context.Context, id int64, source string) (*EduSnippet, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateEduSnippet(cur.Name, cur.WidgetName, source); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE nock_edu_snippets SET source = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, source, id); err != nil {
		return nil, fmt.Errorf("nock: update edu snippet: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *EduSnippetStore) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_edu_snippets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete edu snippet: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: edu snippet %d not found", id)
	}
	return nil
}
