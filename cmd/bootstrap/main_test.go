package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression test for the bug found live during S141-04 (registering the
// NORN agent): writeSecretsEnv used to overwrite the whole file with only
// the newly-provisioned secrets, silently destroying the plaintext for
// every previously-provisioned agent. The database's api_key_hash was
// untouched (so those agents kept working), but their plaintext was gone
// from the only place it's ever written — this file is git-ignored,
// generated once, never backed up elsewhere by design. EMILY-PRIME's was
// recovered from a live process's environment; five others
// (FATBABY-EMILY/EMIREE/JON/BOB/TYLER) were not recoverable and had to be
// rotated in the running database.

func TestWriteSecretsEnv_PreservesExistingEntriesNotInThisRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	// First run: two agents provisioned.
	if err := writeSecretsEnv(path, []agentSecret{
		{Name: "EMILY-PRIME", Plaintext: "secret-a"},
		{Name: "FATBABY-EMILY", Plaintext: "secret-b"},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Second run: only ONE new agent provisioned (the common real-world
	// case — e.g. adding NORN). EMILY-PRIME and FATBABY-EMILY must survive
	// even though they aren't in this run's secrets slice.
	if err := writeSecretsEnv(path, []agentSecret{
		{Name: "NORN", Plaintext: "secret-c"},
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"export IDUNA_SECRET_EMILY_PRIME=secret-a",
		"export IDUNA_SECRET_FATBABY_EMILY=secret-b",
		"export IDUNA_SECRET_NORN=secret-c",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected file to contain %q after merge, got:\n%s", want, content)
		}
	}
}

func TestWriteSecretsEnv_RotationOverwritesOnlyThatAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	if err := writeSecretsEnv(path, []agentSecret{
		{Name: "EMILY-PRIME", Plaintext: "old-secret"},
		{Name: "BOB", Plaintext: "bob-secret"},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Rotate EMILY-PRIME only.
	if err := writeSecretsEnv(path, []agentSecret{
		{Name: "EMILY-PRIME", Plaintext: "new-secret"},
	}); err != nil {
		t.Fatalf("rotation write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "old-secret") {
		t.Error("rotated secret's old plaintext should not survive")
	}
	if !strings.Contains(content, "export IDUNA_SECRET_EMILY_PRIME=new-secret") {
		t.Error("expected the rotated value to be present")
	}
	if !strings.Contains(content, "export IDUNA_SECRET_BOB=bob-secret") {
		t.Error("expected BOB's untouched secret to survive rotating a different agent")
	}
}

func TestWriteSecretsEnv_NoExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist-yet.env")
	if err := writeSecretsEnv(path, []agentSecret{{Name: "EMILY-PRIME", Plaintext: "s1"}}); err != nil {
		t.Fatalf("write with no existing file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "export IDUNA_SECRET_EMILY_PRIME=s1") {
		t.Error("expected fresh file to contain the new secret")
	}
}

func TestWriteSecretsEnv_PreservedEntryDisplayNameReconstructed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	if err := writeSecretsEnv(path, []agentSecret{
		{Name: "EDIS-CUSTODIAN", Plaintext: "s1"},
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second run doesn't touch EDIS-CUSTODIAN — its comment line must
	// display "EDIS-CUSTODIAN", not the raw env key
	// "IDUNA_SECRET_EDIS_CUSTODIAN".
	if err := writeSecretsEnv(path, []agentSecret{{Name: "NORN", Plaintext: "s2"}}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "# Agent EDIS-CUSTODIAN: set") {
		t.Errorf("expected reconstructed display name 'EDIS-CUSTODIAN', got:\n%s", content)
	}
	if strings.Contains(content, "# Agent IDUNA_SECRET_EDIS_CUSTODIAN:") {
		t.Error("display name should not be the raw env key")
	}
}

func TestReadExistingSecretLines_MissingFileReturnsEmpty(t *testing.T) {
	got := readExistingSecretLines(filepath.Join(t.TempDir(), "nope.env"))
	if len(got) != 0 {
		t.Errorf("expected empty map for missing file, got %v", got)
	}
}

func TestReadExistingSecretLines_ParsesExportLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")
	content := "# comment\nexport IDUNA_SECRET_EMILY_PRIME=abc123\nexport IDUNA_SECRET_BOB=def456\n# Agent BOB: set IDUNA_AGENT_SECRET=${IDUNA_SECRET_BOB}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := readExistingSecretLines(path)
	if got["IDUNA_SECRET_EMILY_PRIME"] != "abc123" {
		t.Errorf("EMILY_PRIME = %q, want abc123", got["IDUNA_SECRET_EMILY_PRIME"])
	}
	if got["IDUNA_SECRET_BOB"] != "def456" {
		t.Errorf("BOB = %q, want def456", got["IDUNA_SECRET_BOB"])
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 parsed entries (comment lines must not match), got %d: %v", len(got), got)
	}
}
