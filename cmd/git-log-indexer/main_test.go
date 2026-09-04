package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newTestRepo creates a real git repo (not a mock) with the given commit subjects, one commit
// per subject in order, and returns its path -- real git behavior end to end, matching this
// repo's own "real, not simulated" testing convention.
func newTestRepo(t *testing.T, subjects ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for i, subj := range subjects {
		fname := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(fname, []byte(subj), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		run("add", "-A")
		run("commit", "-q", "-m", subj, "--date", "2026-01-0"+string(rune('1'+i))+"T00:00:00")
	}
	return dir
}

func TestGitLog_FullHistoryOnFirstRun(t *testing.T) {
	repo := newTestRepo(t, "first commit", "second commit", "third commit")

	commits, err := gitLog(repo, "")
	if err != nil {
		t.Fatalf("gitLog: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	// --reverse: oldest first.
	if commits[0].Subject != "first commit" || commits[2].Subject != "third commit" {
		t.Errorf("expected oldest-first order, got %v, %v, %v", commits[0].Subject, commits[1].Subject, commits[2].Subject)
	}
	for _, c := range commits {
		if c.Hash == "" || c.AuthorEmail != "test@example.com" || c.UnixTime == 0 {
			t.Errorf("commit missing expected fields: %+v", c)
		}
	}
}

func TestGitLog_SinceCursorOnlyReturnsNewCommits(t *testing.T) {
	repo := newTestRepo(t, "first commit", "second commit", "third commit")

	all, err := gitLog(repo, "")
	if err != nil {
		t.Fatalf("gitLog (full): %v", err)
	}
	firstHash := all[0].Hash

	since, err := gitLog(repo, firstHash)
	if err != nil {
		t.Fatalf("gitLog (since): %v", err)
	}
	if len(since) != 2 {
		t.Fatalf("expected 2 commits after the first, got %d", len(since))
	}
	if since[0].Subject != "second commit" || since[1].Subject != "third commit" {
		t.Errorf("unexpected commits after cursor: %v, %v", since[0].Subject, since[1].Subject)
	}
}

func TestGitLog_NoNewCommitsAtHead(t *testing.T) {
	repo := newTestRepo(t, "only commit")
	all, err := gitLog(repo, "")
	if err != nil {
		t.Fatalf("gitLog: %v", err)
	}
	headHash := all[len(all)-1].Hash

	since, err := gitLog(repo, headHash)
	if err != nil {
		t.Fatalf("gitLog (since HEAD): %v", err)
	}
	if len(since) != 0 {
		t.Fatalf("expected 0 new commits at HEAD, got %d", len(since))
	}
}

func TestIndexRepo_PostsEachCommitAndAdvancesCursor(t *testing.T) {
	repo := newTestRepo(t, "alpha", "beta")
	stateDir := t.TempDir()

	var received []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/services/collector" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Splunk test-token" {
			t.Errorf("unexpected Authorization header: %q", got)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"text": "Success", "code": 0})
	}))
	defer srv.Close()

	n, err := indexRepo(srv.Client(), srv.URL, "test-token", stateDir, repo)
	if err != nil {
		t.Fatalf("indexRepo: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 newly-indexed commits, got %d", n)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 HEC POSTs, got %d", len(received))
	}
	if received[0]["sourcetype"] != "git_commit" {
		t.Errorf("expected sourcetype=git_commit, got %v", received[0]["sourcetype"])
	}
	event, _ := received[0]["event"].(map[string]any)
	if event["subject"] != "alpha" {
		t.Errorf("expected first event subject=alpha, got %v", event["subject"])
	}

	// Idempotency: re-running with the cursor already advanced should post nothing new.
	n2, err := indexRepo(srv.Client(), srv.URL, "test-token", stateDir, repo)
	if err != nil {
		t.Fatalf("indexRepo (rerun): %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 new commits on rerun, got %d", n2)
	}
	if len(received) != 2 {
		t.Fatalf("expected no additional POSTs on rerun, total still 2, got %d", len(received))
	}
}

func TestIndexRepo_StopsOnFirstFailureWithoutAdvancingPastIt(t *testing.T) {
	repo := newTestRepo(t, "one", "two", "three")
	stateDir := t.TempDir()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"text": "Internal error", "code": 8})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"text": "Success", "code": 0})
	}))
	defer srv.Close()

	n, err := indexRepo(srv.Client(), srv.URL, "test-token", stateDir, repo)
	if err == nil {
		t.Fatal("expected an error from the failing second POST")
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 commit successfully indexed before the failure, got %d", n)
	}

	cursorPath := filepath.Join(stateDir, filepath.Base(repo)+".cursor")
	cursorBytes, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("reading cursor: %v", err)
	}
	all, _ := gitLog(repo, "")
	if string(cursorBytes) != all[0].Hash {
		t.Errorf("expected cursor to stay at the first (successful) commit %s, got %s", all[0].Hash, cursorBytes)
	}
}
