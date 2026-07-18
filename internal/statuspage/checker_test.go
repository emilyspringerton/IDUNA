package statuspage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestChecker_RecordsUpAndDown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	checker := NewChecker(store, []Target{
		{Name: "healthy", Label: "Healthy Service", CheckURL: up.URL},
		{Name: "unreachable", Label: "Unreachable Service", CheckURL: "http://127.0.0.1:1/nope"},
	})
	checker.checkAll(context.Background(), nil)

	gotUp, found := store.LatestStatus("healthy")
	if !found || !gotUp {
		t.Fatalf("expected healthy target to be up, got up=%v found=%v", gotUp, found)
	}
	gotUp, found = store.LatestStatus("unreachable")
	if !found || gotUp {
		t.Fatalf("expected unreachable target to be down, got up=%v found=%v", gotUp, found)
	}
}

func TestChecker_Non2xxCountsAsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	checker := NewChecker(store, []Target{{Name: "erroring", Label: "x", CheckURL: srv.URL}})
	checker.checkAll(context.Background(), nil)

	gotUp, found := store.LatestStatus("erroring")
	if !found || gotUp {
		t.Fatalf("expected 500 response to count as down, got up=%v found=%v", gotUp, found)
	}
}

func TestStore_LatestStatusNoChecksYet(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	_, found := store.LatestStatus("never-checked")
	if found {
		t.Fatal("expected found=false for a target with no history")
	}
}

func TestStore_UptimePercent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// 3 up, 1 down = 75%.
	store.record("svc", true, 10)
	store.record("svc", true, 10)
	store.record("svc", true, 10)
	store.record("svc", false, 10)

	pct, n := store.UptimePercent("svc", time.Now().Add(-time.Hour))
	if n != 4 {
		t.Fatalf("expected 4 samples, got %d", n)
	}
	if pct != 75.0 {
		t.Fatalf("expected 75%%, got %.1f%%", pct)
	}
}

func TestStore_UptimePercentNoSamples(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	pct, n := store.UptimePercent("never-checked", time.Now().Add(-time.Hour))
	if n != 0 || pct != 0 {
		t.Fatalf("expected 0/0 for no samples, got %.1f%%/%d", pct, n)
	}
}

func TestStore_UptimePercentRespectsWindow(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// A very old failure outside the window shouldn't count.
	store.db.Exec(`INSERT INTO checks (target, up, latency_ms, checked_at) VALUES (?, 0, 5, ?)`,
		"svc", time.Now().Add(-48*time.Hour))
	store.record("svc", true, 10)

	pct, n := store.UptimePercent("svc", time.Now().Add(-time.Hour))
	if n != 1 {
		t.Fatalf("expected only the recent sample to count, got n=%d", n)
	}
	if pct != 100.0 {
		t.Fatalf("expected 100%% within window, got %.1f%%", pct)
	}
}

func TestChecker_Run_DelaysFirstCheck(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	store, err := Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	checker := NewChecker(store, []Target{{Name: "svc", Label: "x", CheckURL: up.URL}})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go checker.runDelayed(ctx, time.Hour, 500*time.Millisecond, nil)

	// Context expires before the startup grace elapses — no check should
	// have run yet, proving the delay is real, not a no-op.
	<-ctx.Done()
	time.Sleep(20 * time.Millisecond)
	if _, found := store.LatestStatus("svc"); found {
		t.Fatal("expected no check to have run before the startup grace period elapsed")
	}
}
