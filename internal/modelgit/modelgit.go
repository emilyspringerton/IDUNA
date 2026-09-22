// Package modelgit — real, shared "model repository" git integration (founder real-time,
// 2026-09-22: "we need to integrate the model repository with git lfs and each model repository
// should have a git integration that can be turned off (on by default)").
//
// A "model repository" here is one of this monorepo's real RL checkpoint registries
// (internal/brawlpit.CheckpointStore -- shared by BRAWLPIT and DEADWEIGHT via its own Game
// field -- and internal/shankpit.CheckpointStore) -- each already a real, working SQLite-
// metadata + on-disk-blob store, with no git backing of any kind before this. Checkpoint blobs
// are exactly git-lfs's own real use case (large, opaque binary files that would otherwise bloat
// a plain git history), so this syncs each new checkpoint into a real, already-existing sibling
// repo (SHANKPIT/BRAWLPIT/DEADWEIGHT are all real, already-cloned checkouts under this same
// monorepo root -- the same "sibling checkouts on this box" precedent internal/http/handlers/
// kanban.go's own BACKLOG.md sync and internal/http/handlers/gfd_items.go's own items.json write
// already established), git-lfs-tracked, rather than standing up new, separate GitHub repos
// (which would need a human to create empty upstreams first, the same real gate WOTAN/LO/
// SPIDERBEETLE/BIG_O/SLOWBOT_LEAGUE's own upstream repos all needed).
//
// "can be turned off (on by default)": Syncer.Disabled is a plain bool. Go's own zero value for
// an unset bool is false, so a CheckpointStore constructed without explicitly setting Disabled
// gets sync ON automatically -- matching the founder's own "on by default" requirement without
// needing a constructor function at every one of this codebase's many direct-struct-literal
// call sites (main.go's own established convention, not a Syncer-specific choice).
//
// Sync is always best-effort, fire-and-forget (a git/network failure never fails or blocks the
// real checkpoint upload a caller is waiting on) -- same real discipline apples.go's own
// syncAppleToGit and kanban.go's own syncNewItemToBacklogGitIfMissing already established for
// every other git-backed subsystem in this codebase. gitPushWithRetry itself (add→commit→push,
// retry once after a rebase on rejection) is reused directly from internal/http/handlers/
// apples.go rather than copied a third time.
package modelgit

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"

	"iduna/internal/gitsync"
)

// Syncer syncs one model repository's checkpoint blobs into a real sibling git working tree.
type Syncer struct {
	// RepoDir is the real, already-cloned sibling repo's root (e.g. "/home/fatbaby/SHANKPIT")
	// -- must already have a real `origin` remote configured; this package never runs `git
	// clone` or `git remote add` itself.
	RepoDir string
	// RelDir is the subdirectory within RepoDir checkpoint blobs are copied into (e.g.
	// "models/rl-checkpoints"), created on first use if missing.
	RelDir string
	// Disabled turns the git integration off for this store. Zero value (false) means ON --
	// see this package's own doc comment for why that's the correct default without a
	// constructor.
	Disabled bool
	// LogPrefix names this syncer in log lines (e.g. "shankpit-model-git",
	// "brawlpit-model-git") -- gitPushWithRetry's own real, already-existing parameter,
	// distinguishing which real subsystem a push failure came from (same reason kanban.go's own
	// reuse of it passes "kanban-git" rather than apples.go's hardcoded "apples-git").
	LogPrefix string
}

// validLFSFilename guards against a filename containing characters that would make it unsafe to
// interpolate into a `git lfs track` glob pattern or a shell-adjacent path join -- checkpoint
// filenames are server-generated (see brawlpit/shankpit CheckpointStore's own Create, always
// "<id>.zip"/"<id>.pth"-shaped), never raw user input, but this is real, cheap, defense in depth
// rather than trusting that invariant blindly.
var validLFSFilename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

// modelGitSyncMu — one mutex per real RepoDir (never a single global lock across
// SHANKPIT/BRAWLPIT/DEADWEIGHT's three distinct working trees, matching gitPushWithRetry's own
// documented "two distinct working trees, never the same lock" rule) so two concurrent syncs
// into the SAME repo serialize, while syncs into different repos never block each other.
var (
	modelGitSyncMuMap   = map[string]*sync.Mutex{}
	modelGitSyncMuMapMu sync.Mutex
)

func lockFor(repoDir string) *sync.Mutex {
	modelGitSyncMuMapMu.Lock()
	defer modelGitSyncMuMapMu.Unlock()
	mu, ok := modelGitSyncMuMap[repoDir]
	if !ok {
		mu = &sync.Mutex{}
		modelGitSyncMuMap[repoDir] = mu
	}
	return mu
}

// SyncBlob copies srcBlobPath into RepoDir/RelDir/destFilename, ensures that directory is
// git-lfs-tracked, and commits+pushes. No-op (not an error) when Disabled or RepoDir/RelDir are
// unset -- a CheckpointStore in a test harness, or one deliberately configured without a target
// repo, behaves exactly as it did before this package existed. SyncBlob itself is synchronous
// (does the real git work inline, easy to test deterministically); it has no return value on
// purpose, so a caller wanting fire-and-forget behavior (never blocking or failing the real DB
// write it's following) invokes it as `go syncer.SyncBlob(...)`, the same real "goroutine at the
// call site" idiom apples.go's own syncAppleToGit and kanban.go's own
// syncNewItemToBacklogGitIfMissing already use.
func (s *Syncer) SyncBlob(srcBlobPath, destFilename, commitMsg string) {
	if s == nil || s.Disabled || s.RepoDir == "" || s.RelDir == "" {
		return
	}
	if !validLFSFilename.MatchString(destFilename) {
		log.Printf("[%s] refusing unsafe destination filename %q", s.logPrefix(), destFilename)
		return
	}

	mu := lockFor(s.RepoDir)
	mu.Lock()
	defer mu.Unlock()

	relTargetDir := s.RelDir
	targetDir := filepath.Join(s.RepoDir, relTargetDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		log.Printf("[%s] mkdir %s: %v", s.logPrefix(), targetDir, err)
		return
	}

	data, err := os.ReadFile(srcBlobPath)
	if err != nil {
		log.Printf("[%s] read source blob %s: %v", s.logPrefix(), srcBlobPath, err)
		return
	}
	destPath := filepath.Join(targetDir, destFilename)
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		log.Printf("[%s] write %s: %v", s.logPrefix(), destPath, err)
		return
	}

	if err := s.ensureLFSTracked(relTargetDir); err != nil {
		log.Printf("[%s] git lfs track: %v", s.logPrefix(), err)
		return
	}

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=iduna", "GIT_AUTHOR_EMAIL=iduna@einhorn.internal",
		"GIT_COMMITTER_NAME=iduna", "GIT_COMMITTER_EMAIL=iduna@einhorn.internal",
	)
	addCmd := exec.Command("git", "-C", s.RepoDir, "add", "-A", "--", ".gitattributes", relTargetDir)
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("[%s] git add: %v\n%s", s.logPrefix(), err, out)
		return
	}
	commitCmd := exec.Command("git", "-C", s.RepoDir, "commit", "-m", commitMsg)
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		// A real, common, non-error case: nothing changed (e.g. an identical blob re-synced).
		log.Printf("[%s] git commit (may be a real no-op, not necessarily an error): %v\n%s", s.logPrefix(), err, out)
		return
	}
	if err := gitsync.PushWithRetry(s.logPrefix(), s.RepoDir, gitEnv); err != nil {
		log.Printf("[%s] git push failed after retry: %v", s.logPrefix(), err)
		return
	}
	log.Printf("[%s] synced %s → %s/%s", s.logPrefix(), srcBlobPath, relTargetDir, destFilename)
}

func (s *Syncer) logPrefix() string {
	if s.LogPrefix != "" {
		return s.LogPrefix
	}
	return "model-git"
}

// ensureLFSTracked runs `git lfs install --local` (idempotent -- safe to call on every sync) and
// `git lfs track "<relDir>/**"` (also idempotent: git-lfs itself no-ops re-adding an
// already-tracked pattern to .gitattributes) so every file this Syncer ever writes under relDir
// is LFS-tracked from its very first commit, not retrofitted after the fact.
func (s *Syncer) ensureLFSTracked(relDir string) error {
	installCmd := exec.Command("git", "-C", s.RepoDir, "lfs", "install", "--local")
	if out, err := installCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lfs install: %w\n%s", err, out)
	}
	pattern := relDir + "/**"
	trackCmd := exec.Command("git", "-C", s.RepoDir, "lfs", "track", pattern)
	if out, err := trackCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lfs track %q: %w\n%s", pattern, err, out)
	}
	return nil
}
