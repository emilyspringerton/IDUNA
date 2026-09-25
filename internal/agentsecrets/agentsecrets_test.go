package agentsecrets

import (
	"path/filepath"
	"testing"
)

func TestEnvKeyForName(t *testing.T) {
	cases := map[string]string{
		"EMILY":       "IDUNA_SECRET_EMILY",
		"shankpit-rl": "IDUNA_SECRET_SHANKPIT_RL",
		"D2-SERVER":   "IDUNA_SECRET_D2_SERVER",
	}
	for name, want := range cases {
		if got := EnvKeyForName(name); got != want {
			t.Errorf("EnvKeyForName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestWriteMergedThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	if err := WriteMerged(path, map[string]string{"EMILY": "secret-one"}); err != nil {
		t.Fatalf("WriteMerged: %v", err)
	}
	got, ok := Lookup(path, "EMILY")
	if !ok || got != "secret-one" {
		t.Fatalf("Lookup(EMILY) = %q, %v; want secret-one, true", got, ok)
	}
}

// TestWriteMergedPreservesUnrelatedEntries is a direct regression test for
// S141-04: a run that only rotates/provisions SOME agents must never destroy
// the plaintext already on file for agents not in that run's update set.
func TestWriteMergedPreservesUnrelatedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	if err := WriteMerged(path, map[string]string{
		"EMILY-PRIME":   "prime-secret",
		"FATBABY-EMILY": "fatbaby-secret",
	}); err != nil {
		t.Fatalf("first WriteMerged: %v", err)
	}

	// A second run only updates NORN -- EMILY-PRIME/FATBABY-EMILY are absent
	// from this update set, exactly the shape of the real incident.
	if err := WriteMerged(path, map[string]string{"NORN": "norn-secret"}); err != nil {
		t.Fatalf("second WriteMerged: %v", err)
	}

	for name, want := range map[string]string{
		"EMILY-PRIME":   "prime-secret",
		"FATBABY-EMILY": "fatbaby-secret",
		"NORN":          "norn-secret",
	} {
		got, ok := Lookup(path, name)
		if !ok || got != want {
			t.Errorf("Lookup(%q) after second write = %q, %v; want %q, true", name, got, ok, want)
		}
	}
}

func TestWriteMergedRotateOverwritesOnlyThatAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-secrets.env")

	if err := WriteMerged(path, map[string]string{"BOB": "old-secret"}); err != nil {
		t.Fatalf("first WriteMerged: %v", err)
	}
	if err := WriteMerged(path, map[string]string{"BOB": "rotated-secret"}); err != nil {
		t.Fatalf("second WriteMerged: %v", err)
	}
	got, ok := Lookup(path, "BOB")
	if !ok || got != "rotated-secret" {
		t.Fatalf("Lookup(BOB) after rotation = %q, %v; want rotated-secret, true", got, ok)
	}
}

func TestLookupMissingReturnsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.env")
	if _, ok := Lookup(path, "GHOST"); ok {
		t.Fatal("Lookup on a nonexistent file should return ok=false")
	}
}
