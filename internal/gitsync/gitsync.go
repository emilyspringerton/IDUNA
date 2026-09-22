// Package gitsync holds the one real, shared "git add already staged → commit → push, retry
// once after a rebase on rejection" idiom every git-backed subsystem in this codebase reuses
// (Apples → APPLES repo, kanban → EMILY/BACKLOG.md, and now internal/modelgit → each RL
// checkpoint model repository's own sibling repo). Extracted 2026-09-22 from internal/http/
// handlers/apples.go's own gitPushWithRetry (already shared by kanban.go within that same
// package) into its own leaf package with zero internal dependencies, so internal/modelgit can
// reuse it too without an import cycle (handlers already imports internal/brawlpit and
// internal/shankpit, which now import this package — handlers importing modelgit, or modelgit
// importing handlers, would cycle; this package importing neither does not).
package gitsync

import (
	"fmt"
	"log"
	"os/exec"
)

// PushWithRetry pushes gitDir's current branch. On rejection (most likely non-fast-forward), it
// pulls with --rebase and retries once. Caller must already hold its own real sync mutex scoped
// to gitDir specifically (apples.go's own gitSyncMu, kanban.go's own backlogFileMu, modelgit's
// own per-RepoDir mutex map — never the same lock shared across two distinct working trees).
// logPrefix names which real caller this is for in the log line.
func PushWithRetry(logPrefix, gitDir string, gitEnv []string) error {
	pushCmd := exec.Command("git", "-C", gitDir, "push")
	pushCmd.Env = gitEnv
	if out, err := pushCmd.CombinedOutput(); err == nil {
		return nil
	} else {
		log.Printf("[%s] git push rejected, retrying after rebase: %v\n%s", logPrefix, err, out)
	}

	pullCmd := exec.Command("git", "-C", gitDir, "pull", "--rebase")
	pullCmd.Env = gitEnv
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pull --rebase: %w\n%s", err, out)
	}

	retryCmd := exec.Command("git", "-C", gitDir, "push")
	retryCmd.Env = gitEnv
	if out, err := retryCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("push retry: %w\n%s", err, out)
	}
	return nil
}
