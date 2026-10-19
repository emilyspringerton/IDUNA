package coder

// Fine-grained permissions. Each API route requires exactly one of these.
// "coder.admin" and "iduna.admin" are superset roles (hold everything below).
const (
	PermSessionCreate  = "coder.session.create"   // POST   sessions
	PermSessionRead    = "coder.session.read"     // GET    own sessions + messages
	PermSessionReadAll = "coder.session.read.all" // GET    anyone's sessions + messages
	PermChatSend       = "coder.chat.send"        // POST   sessions/{id}/messages
	PermToolsRead      = "coder.tools.read"       // GET    tools
	PermLessonsRead    = "coder.lessons.read"     // GET    lessons
	PermLessonsWrite   = "coder.lessons.write"    // POST/DELETE lessons
	PermWorkerClaim    = "coder.worker.claim"     // POST   work/claim   (agent worker M2M identity)
	PermWorkerReport   = "coder.worker.report"    // POST   work/{id}/events, work/{id}/finish
	PermAdmin          = "coder.admin"
)

// Tools is the agent's tool catalogue: name -> description. Each is gated by "coder.tool.<name>".
// All tools are emily.cli subcommands (the agent is powered by emily.cli).
var Tools = map[string]string{
	"observe":      "emily observe — post a founder/ops observation",
	"apples":       "emily apples — post/list Apples (completion proofs)",
	"changelog":    "emily changelog add — record a CHANGELOG entry",
	"backlog":      "emily backlog — read the backlog / promote items",
	"status":       "emily status — cluster/system status",
	"files":        "read/list/write files in the agent workdir",
	"go":           "go build/vet/test in the agent workdir",
	"grep":         "grep -rn over files in the agent workdir (read-only search)",
	"sed":          "sed -n (print ranges) and sed -i (in-place edit, workdir only)",
	"claude_files": "read CLAUDE.md/GEMINI.md instruction files (walks up from workdir)",
	"git":          "git status/diff/add/commit (never push) in the agent workdir",
	"finish":       "declare a task done (verified)",
}

func ToolPerm(tool string) string { return "coder.tool." + tool }

func Allows(perms []string, want string) bool {
	for _, p := range perms {
		if p == want || p == PermAdmin || p == "iduna.admin" {
			return true
		}
	}
	return false
}
