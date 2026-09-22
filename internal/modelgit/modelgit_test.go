package modelgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a real, throwaway local git repo (a bare "remote" + a working clone) so
// SyncBlob's own real git add/commit/push path is exercised against real git and real git-lfs,
// not mocked -- no network, no GitHub, entirely disposable under t.TempDir().
func initTestRepo(t *testing.T) (workDir string) {
	t.Helper()
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs not installed in this environment")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	run(bare, "init", "--bare")

	run(root, "clone", bare, work)
	run(work, "config", "user.email", "test@example.com")
	run(work, "config", "user.name", "Test")
	// An initial commit so `git push` (no upstream branch yet otherwise) and `pull --rebase`
	// both have a real base to work from.
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("test repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "add", "README.md")
	run(work, "commit", "-m", "init")
	// Push whatever branch `git init`/`git clone` actually created (init.defaultBranch varies
	// by git config -- "main" here, but not guaranteed in every environment this runs in) rather
	// than hardcoding a name.
	branchCmd := exec.Command("git", "-C", work, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, err := branchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse --abbrev-ref HEAD: %v\n%s", err, branchOut)
	}
	branch := string(branchOut)
	for len(branch) > 0 && (branch[len(branch)-1] == '\n' || branch[len(branch)-1] == '\r') {
		branch = branch[:len(branch)-1]
	}
	run(work, "push", "-u", "origin", branch)

	return work
}

func TestSyncBlob_DisabledIsNoOp(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{RepoDir: work, RelDir: "models", Disabled: true}
	s.SyncBlob(src, "1.bin", "test commit")
	// No-op means: nothing written into RepoDir at all (it isn't even a real git repo here).
	if _, err := os.Stat(filepath.Join(work, "models", "1.bin")); err == nil {
		t.Fatal("expected no file written when Disabled")
	}
}

func TestSyncBlob_ZeroValueIsEnabledByDefault(t *testing.T) {
	// The real point of the founder's own "on by default" requirement: a Syncer{} literal with
	// Disabled never explicitly set must behave as enabled, not disabled.
	var s Syncer
	if s.Disabled {
		t.Fatal("zero-value Syncer must be enabled (Disabled == false) by default")
	}
}

func TestSyncBlob_UnsetRepoDirIsNoOp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{RelDir: "models"} // RepoDir left empty
	// Must not panic and must not attempt any git operation against an empty path.
	s.SyncBlob(src, "1.bin", "test commit")
}

func TestSyncBlob_RejectsUnsafeFilename(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Syncer{RepoDir: work, RelDir: "models"}
	s.SyncBlob(src, "../../etc/passwd", "test commit")
	if _, err := os.Stat(filepath.Join(work, "models")); err == nil {
		t.Fatal("expected no directory created for an unsafe destination filename")
	}
}

// TestSyncBlob_RealEndToEnd -- real git + real git-lfs, no mocks: a blob synced via SyncBlob
// must land in the working tree, be committed, be LFS-tracked (.gitattributes updated), and be
// pushed to the real (local, throwaway) remote -- verified by cloning the remote fresh and
// checking the file is there.
func TestSyncBlob_RealEndToEnd(t *testing.T) {
	work := initTestRepo(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "checkpoint.zip")
	content := []byte("fake checkpoint bytes for a real end-to-end sync test")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Syncer{RepoDir: work, RelDir: "models/rl-checkpoints", LogPrefix: "test-model-git"}
	s.SyncBlob(src, "42.zip", "model: sync checkpoint 42")

	// SyncBlob runs synchronously (the goroutine wrapping is the CALLER's responsibility, per
	// its own doc comment) -- no sleep/poll needed, the git operations are already done by the
	// time SyncBlob returns.

	// 1. The file really exists in the working tree.
	destPath := filepath.Join(work, "models", "rl-checkpoints", "42.zip")
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("expected synced blob at %s: %v", destPath, err)
	}
	if string(got) != string(content) {
		t.Fatal("synced blob content does not match source")
	}

	// 2. .gitattributes really has the LFS tracking line for this dir.
	attrs, err := os.ReadFile(filepath.Join(work, ".gitattributes"))
	if err != nil {
		t.Fatalf("expected .gitattributes to exist: %v", err)
	}
	if !containsLFSFilter(string(attrs)) {
		t.Fatalf(".gitattributes does not contain a real LFS filter line: %s", attrs)
	}

	// 3. It was really committed (git log shows the commit).
	logCmd := exec.Command("git", "-C", work, "log", "--oneline", "-1")
	out, err := logCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !contains(string(out), "sync checkpoint 42") {
		t.Fatalf("expected the sync commit at HEAD, got: %s", out)
	}

	// 4. It was really pushed -- clone the remote fresh into a new dir and confirm the file
	// (as a real LFS pointer, resolved back to real content via lfs smudge on checkout) is there.
	freshClone := filepath.Join(t.TempDir(), "fresh-clone")
	cloneCmd := exec.Command("git", "clone", filepath.Join(filepath.Dir(work), "remote.git"), freshClone)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone fresh: %v\n%s", err, out)
	}
	freshContent, err := os.ReadFile(filepath.Join(freshClone, "models", "rl-checkpoints", "42.zip"))
	if err != nil {
		t.Fatalf("expected the synced blob in a fresh clone of the real pushed remote: %v", err)
	}
	if string(freshContent) != string(content) {
		t.Fatalf("fresh clone's blob content does not match source (real LFS round-trip failed): got %d bytes, want %d", len(freshContent), len(content))
	}
}

func containsLFSFilter(s string) bool {
	return contains(s, "filter=lfs")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
