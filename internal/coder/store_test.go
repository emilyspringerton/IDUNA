package coder

import (
	"errors"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWorkQueueAndToolIAM(t *testing.T) {
	s := open(t)
	s.CreateSession("s1", "alice", "t")
	// alice may use only the grep tool
	s.Send("s1", "alice", []string{PermChatSend, ToolPerm("grep")}, "find TODOs")
	w, err := s.ClaimNext("worker-1")
	if err != nil || w.Content != "find TODOs" {
		t.Fatalf("claim: %v %+v", err, w)
	}
	if _, err := s.ClaimNext("worker-2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("turn must not be claimable twice")
	}
	if _, err := s.Report(w.ID, "worker-1", "tool", "grep", "TODO x"); err != nil {
		t.Fatalf("allowed tool refused: %v", err)
	}
	if _, err := s.Report(w.ID, "worker-1", "tool", "sed", "edit"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("sed must be refused for sender without coder.tool.sed, got %v", err)
	}
	if _, err := s.Report(w.ID, "worker-2", "assistant", "", "hi"); !errors.Is(err, ErrForbidden) {
		t.Fatal("a worker that did not claim the turn must be refused")
	}
	if _, err := s.Report(w.ID, "worker-1", "assistant", "", "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(w.ID, "worker-1", true); err != nil {
		t.Fatal(err)
	}
}

func TestAdminSupersetAndLessons(t *testing.T) {
	if !Allows([]string{"iduna.admin"}, ToolPerm("sed")) || !Allows([]string{PermAdmin}, PermChatSend) {
		t.Fatal("admin roles are supersets")
	}
	if Allows([]string{PermChatSend}, ToolPerm("sed")) {
		t.Fatal("chat.send must not imply tools")
	}
	s := open(t)
	if _, err := s.AddLesson("", "x"); err == nil {
		t.Fatal("empty lesson must be rejected")
	}
	l, _ := s.AddLesson("prefer grep -n", "bob")
	ls, _ := s.Lessons()
	if len(ls) != 1 || ls[0].ID != l.ID {
		t.Fatal("lesson not stored")
	}
}
