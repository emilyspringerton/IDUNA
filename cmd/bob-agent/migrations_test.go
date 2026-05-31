package main

import (
	"strings"
	"testing"
)

func TestSplitSQL_BasicStatements(t *testing.T) {
	src := `CREATE TABLE a (id INT);
CREATE TABLE b (id INT);`
	stmts := splitSQL(src)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
}

func TestSplitSQL_SemicolonInString(t *testing.T) {
	src := `INSERT INTO t VALUES ('a;b;c');`
	stmts := splitSQL(src)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement (semicolons inside string), got %d", len(stmts))
	}
}

func TestSplitSQL_LineComment(t *testing.T) {
	src := `-- this is a comment; fake split
CREATE TABLE t (id INT);`
	stmts := splitSQL(src)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE in statement, got: %q", stmts[0])
	}
}

func TestSplitSQL_BlockComment(t *testing.T) {
	src := `/* comment; with; semis */
CREATE TABLE t (id INT);`
	stmts := splitSQL(src)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
}

func TestSplitSQL_EmptyInput(t *testing.T) {
	if got := splitSQL(""); len(got) != 0 {
		t.Errorf("empty input should return 0 statements, got %d", len(got))
	}
}

func TestSplitSQL_TrailingSemicolon(t *testing.T) {
	src := `CREATE TABLE t (id INT);`
	stmts := splitSQL(src)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
}

func TestSplitSQL_MultilineStatement(t *testing.T) {
	src := `CREATE TABLE IF NOT EXISTS users (
  id VARCHAR(36) NOT NULL PRIMARY KEY,
  email VARCHAR(255) NOT NULL UNIQUE
) ENGINE=InnoDB;
CREATE INDEX idx ON users(email);`
	stmts := splitSQL(src)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], "CREATE TABLE") {
		t.Errorf("first statement should contain CREATE TABLE, got: %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "CREATE INDEX") {
		t.Errorf("second statement should contain CREATE INDEX, got: %q", stmts[1])
	}
}
