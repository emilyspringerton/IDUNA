package coder

import (
	"os"
	"strings"
	"testing"
)

// IDUNA refuses to grant a permission missing from the permissions table, so every coder.*
// permission and every coder.tool.<name> must be seeded by the migration.
func TestPermissionsAreSeeded(t *testing.T) {
	b, err := os.ReadFile("../../migrations/truestore/202610190001_coder_permissions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	want := []string{PermAdmin, PermSessionCreate, PermSessionRead, PermSessionReadAll, PermChatSend, PermToolsRead,
		PermLessonsRead, PermLessonsWrite, PermWorkerClaim, PermWorkerReport, PermWorkerProvision}
	for tool := range Tools {
		want = append(want, ToolPerm(tool))
	}
	for _, p := range want {
		if !strings.Contains(sql, "'"+p+"'") {
			t.Errorf("permission %q is not seeded by the migration", p)
		}
	}
}

// A stray apostrophe in a description breaks the whole migration (and IDUNA exits at startup).
func TestMigrationQuotingIsBalanced(t *testing.T) {
	b, _ := os.ReadFile("../../migrations/truestore/202610190001_coder_permissions.sql")
	for i, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "--") || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "INSERT") {
			continue
		}
		if strings.Count(ln, "'") != 6 {
			t.Errorf("line %d: want exactly 3 quoted values (6 quotes), got %d: %s", i+1, strings.Count(ln, "'"), ln)
		}
	}
}
