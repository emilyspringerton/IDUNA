package statuspage

import (
	"os/exec"
	"testing"
)

func TestSystemdUnitActive_FalseForNonexistentUnit(t *testing.T) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available in this environment")
	}
	// This depends only on the unit name being bogus, not on any specific
	// unit existing/running — safe to assert everywhere systemctl exists.
	if SystemdUnitActive("definitely-not-a-real-unit-xyz123.service") {
		t.Fatal("expected a nonexistent unit to report inactive")
	}
}

func TestChecker_SystemdUnitCheck_DownForNonexistentUnit(t *testing.T) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available in this environment")
	}

	store, err := Open(t.TempDir() + "/status.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	checker := NewChecker(store, []Target{
		{Name: "ghost", Label: "Ghost Service", Type: CheckSystemdUnit, Unit: "definitely-not-a-real-unit-xyz123.service"},
	})
	checker.checkAll(nil, nil)

	up, found := store.LatestStatus("ghost")
	if !found || up {
		t.Fatalf("expected ghost unit to be down, got up=%v found=%v", up, found)
	}
}
