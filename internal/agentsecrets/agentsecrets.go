// Package agentsecrets reads and writes IDUNA's plaintext agent-secrets file
// (var/agent-secrets.env), the ONLY place an M2M agent's plaintext credential
// is ever recorded -- the `agents` table only ever holds a one-way SHA-256
// hash (see internal/store's own hashAgentSecret), by deliberate design
// (cmd/create-admin-agent's own doc comment: "prints the plaintext secret
// once -- it's never retrievable again").
//
// Founder real-time, 2026-09-25: "in the IDUNA BACKOFFICE i need an interface
// for the training keys it needs to reveal them to me like an admin in
// carepyre can summon the email password out of the void." CarePyre's own
// reveal-password (internal/mailaccounts, IDUNA_PRO) works because that
// mailbox password is stored reversibly-encrypted at rest. Agent secrets are
// NOT -- the hash can never be turned back into the plaintext -- so
// AdminHandler's own "reveal" route (admin.go) reads it from THIS file
// instead, the one place it still exists in plaintext.
//
// This logic is a deliberate, careful port of cmd/bootstrap/main.go's own
// private writeSecretsEnv/readExistingSecretLines (same merge-safe write:
// S141-04 found live that a naive overwrite of this file silently destroyed
// several agents' only recorded plaintext -- EMIREE/JON/BOB/TYLER had to be
// rotated because of it). Kept as a SEPARATE copy rather than refactoring
// cmd/bootstrap to import this package: bootstrap is a fragile, one-shot
// provisioning tool with its own already-incident-scarred history, and this
// pass's real job is the admin reveal feature, not a refactor of it -- a
// real, named tradeoff (duplicated logic vs. touching bootstrap), not an
// oversight. Both copies now need to stay in sync if the file format ever
// changes.
package agentsecrets

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// EnvKeyForName derives the IDUNA_SECRET_<NAME> env var key for an agent
// name, matching cmd/bootstrap's own derivation exactly (uppercase, "-" ->
// "_" -- no agent name in this codebase contains a literal underscore today,
// same caveat cmd/bootstrap's own comment names).
func EnvKeyForName(name string) string {
	return "IDUNA_SECRET_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

var secretLineRE = regexp.MustCompile(`^export (IDUNA_SECRET_[A-Z0-9_]+)=(\S+)$`)

// ReadAll parses `export IDUNA_SECRET_<KEY>=<value>` lines from path,
// returning envKey -> plaintext. Returns an empty map if the file doesn't
// exist or can't be parsed -- never an error, matching cmd/bootstrap's own
// readExistingSecretLines (a missing/corrupt file just means nothing to
// read, not a failure).
func ReadAll(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		m := secretLineRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		out[m[1]] = m[2]
	}
	return out
}

// Lookup returns the current recorded plaintext for a given agent name, and
// whether one was found at all. A miss means either this agent has never
// had its secret provisioned/rotated since agentsecrets started tracking it,
// or (a real, honest, named gap) its secret WAS rotated but only through a
// code path that doesn't call WriteMerged -- the caller should say so rather
// than implying the secret doesn't exist.
func Lookup(path, agentName string) (string, bool) {
	v, ok := ReadAll(path)[EnvKeyForName(agentName)]
	return v, ok
}

type entry struct {
	envKey      string
	plaintext   string
	displayName string
}

// WriteMerged writes/updates one or more agent secrets into path, MERGING
// with whatever plaintext is already recorded there rather than replacing
// the file outright -- see this package's own doc comment for why that
// matters (S141-04). updates is agentName -> new plaintext.
func WriteMerged(path string, updates map[string]string) error {
	merged := map[string]*entry{}
	var order []string

	for envKey, plaintext := range ReadAll(path) {
		guess := strings.ReplaceAll(strings.TrimPrefix(envKey, "IDUNA_SECRET_"), "_", "-")
		merged[envKey] = &entry{envKey: envKey, plaintext: plaintext, displayName: guess}
		order = append(order, envKey)
	}
	for name, plaintext := range updates {
		envKey := EnvKeyForName(name)
		if _, ok := merged[envKey]; !ok {
			order = append(order, envKey)
		}
		merged[envKey] = &entry{envKey: envKey, plaintext: plaintext, displayName: name}
	}
	sort.Strings(order)

	var sb strings.Builder
	sb.WriteString("# IDUNA agent secrets -- see cmd/bootstrap and internal/agentsecrets\n")
	sb.WriteString("# Source this file before starting agents: source var/agent-secrets.env\n")
	sb.WriteString("# DO NOT COMMIT THIS FILE. It is git-ignored by default.\n")
	sb.WriteString(fmt.Sprintf("# Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	for _, envKey := range order {
		sb.WriteString(fmt.Sprintf("export %s=%s\n", envKey, merged[envKey].plaintext))
	}

	sb.WriteString("\n# Convenience aliases (match agent IDUNA_AGENT_SECRET env var convention):\n")
	for _, envKey := range order {
		e := merged[envKey]
		sb.WriteString(fmt.Sprintf("# Agent %s: set IDUNA_AGENT_SECRET=${%s} when starting that agent\n", e.displayName, e.envKey))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}
