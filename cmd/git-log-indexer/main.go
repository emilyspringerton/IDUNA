// git-log-indexer: real, minimal v0 for kanban cruise-queue card IUS-001 ("can we add git
// indexing to iduna unified search"). Walks one or more local git repos' commit history and
// ingests each commit as a real event into IDUNA's own unified logging backend
// (internal/http/handlers/logs.go) via its Splunk-HEC-shaped POST /services/collector endpoint
// -- the exact same ingest path any other event source uses, so no new search-side code is
// needed: commits are immediately searchable through the existing
// GET /services/search/jobs / /portal/logs UI with `type=git_commit`, `source=<repo>`, or a
// free-text `q=` match against the commit subject/hash/author.
//
// Real, deliberately narrow v0 scope, matching this monorepo's own "bounded, honest slice"
// convention (see EMILY/BACKLOG.md's own repeated Principle 19 usage): indexes HEAD only (not
// every branch/worktree — this monorepo's own convention is one long-lived default branch per
// repo, see /home/fatbaby/CLAUDE.md's own repo table), and is fully idempotent via a small local
// cursor file per repo (the last-indexed commit hash) so re-running on a cron/timer only ever
// costs work proportional to what's new since the last run — the same idempotent-cron shape
// cmd/promptoverse-thumbnails already established in this same cmd/ directory.
//
// Usage:
//
//	IDUNA_HEC_TOKEN=... git-log-indexer -iduna-url http://localhost:8080 /home/fatbaby/EMILY /home/fatbaby/IDUNA ...
//
// Real, honest, not attempted here: indexing every repo in the monorepo automatically (the
// caller passes explicit repo paths — a wrapper script naming the full repo list is real,
// separate, follow-up work), and non-HEAD branches/tags.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const fieldSep = "\x1f" // ASCII unit separator -- won't collide with real commit message text

type commit struct {
	Hash        string
	AuthorName  string
	AuthorEmail string
	UnixTime    int64
	Subject     string
}

func main() {
	idunaURL := flag.String("iduna-url", getenvDefault("IDUNA_BASE_URL", "http://localhost:8080"), "IDUNA base URL")
	stateDir := flag.String("state-dir", getenvDefault("GIT_LOG_INDEXER_STATE_DIR", filepath.Join(os.Getenv("HOME"), ".cache", "iduna-git-indexer")), "directory holding per-repo cursor files")
	flag.Parse()

	repos := flag.Args()
	if len(repos) == 0 {
		fmt.Fprintln(os.Stderr, "usage: git-log-indexer [-iduna-url URL] [-state-dir DIR] REPO_PATH [REPO_PATH ...]")
		os.Exit(2)
	}

	hecToken := os.Getenv("IDUNA_HEC_TOKEN")
	if hecToken == "" {
		log.Fatal("IDUNA_HEC_TOKEN must be set (same token the collector endpoint checks)")
	}

	if err := os.MkdirAll(*stateDir, 0700); err != nil {
		log.Fatalf("creating state dir: %v", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	totalIndexed := 0
	for _, repoPath := range repos {
		n, err := indexRepo(client, *idunaURL, hecToken, *stateDir, repoPath)
		if err != nil {
			log.Printf("repo %s: %v", repoPath, err)
			continue
		}
		totalIndexed += n
		fmt.Printf("%s: indexed %d new commit(s)\n", filepath.Base(repoPath), n)
	}
	fmt.Printf("done: %d total new commit(s) indexed across %d repo(s)\n", totalIndexed, len(repos))
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// indexRepo returns the count of newly-indexed commits. Real idempotency: a per-repo cursor
// file remembers the last commit hash successfully ingested, and only advances past a commit
// once its HEC POST has actually succeeded -- a mid-run failure (server down, bad token) leaves
// the cursor at the last real success, so a rerun picks back up from there instead of either
// re-posting already-ingested commits or silently skipping ones that failed.
func indexRepo(client *http.Client, idunaURL, hecToken, stateDir, repoPath string) (int, error) {
	repoName := filepath.Base(strings.TrimRight(repoPath, "/"))
	cursorPath := filepath.Join(stateDir, repoName+".cursor")

	var since string
	if b, err := os.ReadFile(cursorPath); err == nil {
		since = strings.TrimSpace(string(b))
	}

	commits, err := gitLog(repoPath, since)
	if err != nil {
		return 0, fmt.Errorf("git log: %w", err)
	}
	if len(commits) == 0 {
		return 0, nil
	}

	indexed := 0
	for _, c := range commits {
		if err := postCommitEvent(client, idunaURL, hecToken, repoName, c); err != nil {
			// Real, deliberate stop-on-first-failure: the cursor file was already left at the
			// last commit that actually succeeded (written at the end of the previous loop
			// iteration, or untouched if this is the very first commit) -- do NOT write
			// anything here, or a rerun would wrongly treat this failed commit as indexed and
			// skip re-posting it, see the function's own doc comment above.
			return indexed, fmt.Errorf("post commit %s: %w", c.Hash, err)
		}
		if err := os.WriteFile(cursorPath, []byte(c.Hash), 0600); err != nil {
			return indexed, fmt.Errorf("write cursor after commit %s: %w", c.Hash, err)
		}
		indexed++
	}
	return indexed, nil
}

// gitLog returns commits strictly after `since` (exclusive), oldest first, on HEAD only. When
// since is empty, returns the repo's entire HEAD history (oldest first) -- the real first-run
// case.
func gitLog(repoPath, since string) ([]commit, error) {
	rev := "HEAD"
	if since != "" {
		rev = since + "..HEAD"
	}
	// --reverse: oldest first, so the cursor always advances monotonically through real history
	// and a partial run's cursor genuinely means "everything up to and including this commit is
	// indexed", not an arbitrary point in an unordered set.
	cmd := exec.Command("git", "-C", repoPath, "log", "--reverse",
		"--format=%H"+fieldSep+"%an"+fieldSep+"%ae"+fieldSep+"%at"+fieldSep+"%s", rev)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}

	var commits []commit
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // commit subjects can be long
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, fieldSep, 5)
		if len(parts) != 5 {
			continue // malformed line -- skip rather than fail the whole batch
		}
		ts, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			continue
		}
		commits = append(commits, commit{
			Hash:        parts[0],
			AuthorName:  parts[1],
			AuthorEmail: parts[2],
			UnixTime:    ts,
			Subject:     parts[4],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return commits, nil
}

// postCommitEvent ingests one commit via IDUNA's real Splunk-HEC-shaped collector endpoint --
// see internal/http/handlers/logs.go's own HandleCollector for the exact contract this mirrors.
func postCommitEvent(client *http.Client, idunaURL, hecToken, repoName string, c commit) error {
	body := map[string]any{
		"sourcetype": "git_commit",
		"source":     repoName,
		"time":       float64(c.UnixTime),
		"event": map[string]any{
			"repo":         repoName,
			"hash":         c.Hash,
			"author_name":  c.AuthorName,
			"author_email": c.AuthorEmail,
			"subject":      c.Subject,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(idunaURL, "/")+"/services/collector", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Splunk "+hecToken)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var respBody bytes.Buffer
		respBody.ReadFrom(resp.Body)
		return fmt.Errorf("HEC returned %d: %s", resp.StatusCode, respBody.String())
	}
	return nil
}
