package brawlpit

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newCheckpointTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE brawlpit_rl_checkpoints (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			role            TEXT NOT NULL,
			generation      INTEGER NOT NULL,
			elo             REAL NOT NULL DEFAULT 1500,
			source_location TEXT NOT NULL DEFAULT '',
			filename        TEXT NOT NULL,
			sha256          TEXT NOT NULL,
			size_bytes      INTEGER NOT NULL,
			blob_path       TEXT NOT NULL,
			name            TEXT NOT NULL DEFAULT '',
			is_active_opponent INTEGER NOT NULL DEFAULT 0,
			weights_blob_path TEXT NOT NULL DEFAULT '',
			weights_size_bytes INTEGER NOT NULL DEFAULT 0,
			weights_sha256 TEXT NOT NULL DEFAULT '',
			is_disabled     INTEGER NOT NULL DEFAULT 0,
			game            TEXT NOT NULL DEFAULT 'brawlpit',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create brawlpit_rl_checkpoints: %v", err)
	}
	return db
}

func newCheckpointTestStore(t *testing.T) *CheckpointStore {
	t.Helper()
	return &CheckpointStore{DB: newCheckpointTestDB(t), BlobDir: t.TempDir()}
}

func TestCheckpointStore_CreateAndGet(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	c, err := store.Create(ctx, "main", 3, 1550.5, "colab", "main_gen3.zip", []byte("fake ppo checkpoint bytes"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == 0 {
		t.Error("expected a real, non-zero id")
	}
	if c.Role != "main" || c.Generation != 3 || c.Elo != 1550.5 || c.SourceLocation != "colab" {
		t.Errorf("unexpected checkpoint: %+v", c)
	}
	if c.SizeBytes != int64(len("fake ppo checkpoint bytes")) {
		t.Errorf("wrong size_bytes: %d", c.SizeBytes)
	}
	if len(c.SHA256) != 64 {
		t.Errorf("expected a real 64-char hex sha256, got %q", c.SHA256)
	}

	got, err := store.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Filename != "main_gen3.zip" {
		t.Errorf("Get returned wrong data: %+v", got)
	}
}

func TestCheckpointStore_CreateWritesARealBlobFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	content := []byte("real checkpoint payload")

	c, err := store.Create(ctx, "league_exploiter", 0, 1500, "this-box", "le0.zip", content)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	blobPath := filepath.Join(store.BlobDir, "1.zip")
	on, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("expected a real blob file at %s: %v", blobPath, err)
	}
	if string(on) != string(content) {
		t.Errorf("blob file content doesn't match what was uploaded")
	}
	_ = c
}

func TestCheckpointStore_RejectsInvalidRole(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "not_a_real_role", 0, 1500, "colab", "x.zip", []byte("data")); err == nil {
		t.Error("expected an invalid role to be rejected")
	}
}

func TestCheckpointStore_RejectsEmptyFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "main", 0, 1500, "colab", "empty.zip", []byte{}); err == nil {
		t.Error("expected an empty checkpoint file to be rejected")
	}
}

func TestCheckpointStore_RejectsInvalidSourceLocation(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, "main", 0, 1500, "", "x.zip", []byte("data")); err == nil {
		t.Error("expected an empty source_location to be rejected")
	}
}

func TestCheckpointStore_ListAllAndByRole(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	store.Create(ctx, "main", 0, 1500, "this-box", "main0.zip", []byte("a"))
	store.Create(ctx, "main", 1, 1520, "colab", "main1.zip", []byte("b"))
	store.Create(ctx, "main_exploiter", 0, 1500, "colab", "me0.zip", []byte("c"))

	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 total checkpoints, got %d", len(all))
	}

	mainOnly, err := store.List(ctx, "main")
	if err != nil {
		t.Fatalf("List(main): %v", err)
	}
	if len(mainOnly) != 2 {
		t.Fatalf("expected 2 main checkpoints, got %d", len(mainOnly))
	}
	for _, c := range mainOnly {
		if c.Role != "main" {
			t.Errorf("role filter leaked a non-main row: %+v", c)
		}
	}
}

func TestCheckpointStore_ReadBlobRoundTrips(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	content := []byte("a real checkpoint's real bytes, byte for byte")

	created, err := store.Create(ctx, "main", 0, 1500, "colab", "main0.zip", content)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	c, data, err := store.ReadBlob(ctx, created.ID)
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if string(data) != string(content) {
		t.Error("ReadBlob returned different bytes than were uploaded")
	}
	if c.SHA256 != created.SHA256 {
		t.Error("ReadBlob's own metadata doesn't match the created checkpoint's")
	}
}

func TestCheckpointStore_GetUnknownIDFails(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.Get(context.Background(), 999); err == nil {
		t.Error("expected getting a nonexistent checkpoint id to fail")
	}
}

func TestCheckpointStore_RejectsOversizedFile(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	// One byte over the real 200MB bound -- a real, deliberate boundary test, not a full
	// 200MB allocation (a synthetic slice header of that length is cheap to construct in Go
	// without actually touching that much memory, since make([]byte, n) zero-fills but the
	// test machine can spare 200MB briefly; kept modest here to stay fast in CI).
	huge := make([]byte, 200*1024*1024+1)
	if _, err := store.Create(ctx, "main", 0, 1500, "colab", "huge.zip", huge); err == nil {
		t.Error("expected an oversized checkpoint file to be rejected")
	}
}

func TestCheckpointStore_SetActiveOpponent(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "main", 1, 1650, "this-box", "b.zip", []byte("b"))

	if got, err := store.GetActiveOpponent(ctx); err != nil || got != nil {
		t.Fatalf("expected no active opponent yet, got %+v, err=%v", got, err)
	}

	activated, err := store.SetActiveOpponent(ctx, a.ID)
	if err != nil {
		t.Fatalf("SetActiveOpponent: %v", err)
	}
	if !activated.IsActiveOpponent {
		t.Error("the just-activated checkpoint's own returned struct should report is_active_opponent=true")
	}

	active, err := store.GetActiveOpponent(ctx)
	if err != nil || active == nil || active.ID != a.ID {
		t.Fatalf("expected checkpoint %d active, got %+v, err=%v", a.ID, active, err)
	}

	// Switching to b must clear a's own flag -- exactly one active opponent at a time.
	if _, err := store.SetActiveOpponent(ctx, b.ID); err != nil {
		t.Fatalf("SetActiveOpponent(b): %v", err)
	}
	active, _ = store.GetActiveOpponent(ctx)
	if active == nil || active.ID != b.ID {
		t.Fatalf("expected checkpoint %d active after switching, got %+v", b.ID, active)
	}
	refetchedA, _ := store.Get(ctx, a.ID)
	if refetchedA.IsActiveOpponent {
		t.Error("activating b must clear a's own is_active_opponent flag")
	}
}

func TestCheckpointStore_SetActiveOpponentUnknownIDFails(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.SetActiveOpponent(context.Background(), 999); err == nil {
		t.Error("expected activating a nonexistent checkpoint id to fail")
	}
}

func TestCheckpointStore_SetDisabled(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	if c.IsDisabled {
		t.Fatal("a freshly created checkpoint should not start disabled")
	}

	disabled, err := store.SetDisabled(ctx, c.ID, true)
	if err != nil {
		t.Fatalf("SetDisabled(true): %v", err)
	}
	if !disabled.IsDisabled {
		t.Error("the just-disabled checkpoint's own returned struct should report is_disabled=true")
	}
	refetched, _ := store.Get(ctx, c.ID)
	if !refetched.IsDisabled {
		t.Error("is_disabled should persist across a fresh Get")
	}

	// Real, reversible -- re-enabling clears the flag, the row/blob stay intact throughout.
	enabled, err := store.SetDisabled(ctx, c.ID, false)
	if err != nil {
		t.Fatalf("SetDisabled(false): %v", err)
	}
	if enabled.IsDisabled {
		t.Error("re-enabling should clear is_disabled")
	}
}

func TestCheckpointStore_SetDisabledIsIndependentPerRow(t *testing.T) {
	// Unlike SetActiveOpponent, disabling one checkpoint must NOT affect any other -- no global
	// single-selection invariant here.
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "main", 1, 1500, "this-box", "b.zip", []byte("b"))

	if _, err := store.SetDisabled(ctx, a.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	refetchedB, _ := store.Get(ctx, b.ID)
	if refetchedB.IsDisabled {
		t.Error("disabling a must not disable b")
	}
}

func TestCheckpointStore_SetDisabledUnknownIDFails(t *testing.T) {
	store := newCheckpointTestStore(t)
	if _, err := store.SetDisabled(context.Background(), 999, true); err == nil {
		t.Error("expected disabling a nonexistent checkpoint id to fail")
	}
}

func TestCheckpointStore_CreateGeneratesARealNameWithRoleAndTimestamp(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	before := time.Now().UTC()

	c, err := store.Create(ctx, "main_exploiter", 2, 1500, "this-box", "x.zip", []byte("data"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(c.Name, "main_exploiter_") {
		t.Fatalf("expected name to start with the real archetype, got %q", c.Name)
	}
	// "main_exploiter_YYYYMMDD_HHMMSS" -- confirm it parses back as a real, recent UTC time.
	tsPart := strings.TrimPrefix(c.Name, "main_exploiter_")
	parsed, err := time.Parse("20060102_150405", tsPart)
	if err != nil {
		t.Fatalf("expected a real, parseable to-the-second timestamp suffix, got %q: %v", tsPart, err)
	}
	if parsed.Before(before.Add(-2*time.Second)) || parsed.After(time.Now().UTC().Add(2*time.Second)) {
		t.Errorf("timestamp in name isn't close to real creation time: %v", parsed)
	}
}

func TestCheckpointStore_SetWeightsAndReadWeights(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()

	c, _ := store.Create(ctx, "main", 0, 1500, "this-box", "main0.zip", []byte("zip data"))
	if c.HasWeights {
		t.Error("a freshly created checkpoint must not report HasWeights before SetWeights is called")
	}

	weights := []byte("BPMW-fake-weights-payload")
	updated, err := store.SetWeights(ctx, c.ID, weights)
	if err != nil {
		t.Fatalf("SetWeights: %v", err)
	}
	if !updated.HasWeights {
		t.Error("HasWeights must be true after a real SetWeights call")
	}
	if updated.WeightsSizeBytes != int64(len(weights)) {
		t.Errorf("wrong WeightsSizeBytes: %d", updated.WeightsSizeBytes)
	}
	if len(updated.WeightsSHA256) != 64 {
		t.Errorf("expected a real 64-char hex sha256 for the weights blob, got %q", updated.WeightsSHA256)
	}

	_, data, err := store.ReadWeights(ctx, c.ID)
	if err != nil {
		t.Fatalf("ReadWeights: %v", err)
	}
	if string(data) != string(weights) {
		t.Error("ReadWeights returned different bytes than SetWeights stored")
	}
}

func TestCheckpointStore_ReadWeightsFailsWhenNoneSet(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, "main", 0, 1500, "this-box", "main0.zip", []byte("data"))
	if _, _, err := store.ReadWeights(ctx, c.ID); err == nil {
		t.Error("expected reading weights from a checkpoint with none set to fail")
	}
}

func TestCheckpointStore_SetWeightsRejectsEmptyAndOversized(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	c, _ := store.Create(ctx, "main", 0, 1500, "this-box", "main0.zip", []byte("data"))

	if _, err := store.SetWeights(ctx, c.ID, []byte{}); err == nil {
		t.Error("expected an empty weights file to be rejected")
	}
	huge := make([]byte, 20*1024*1024+1)
	if _, err := store.SetWeights(ctx, c.ID, huge); err == nil {
		t.Error("expected an oversized weights file to be rejected")
	}
}

func TestCheckpointStore_RecordMatchResultUpdatesBothSides(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "league_exploiter", 0, 1500, "this-box", "b.zip", []byte("b"))

	updatedA, updatedB, err := store.RecordMatchResult(ctx, a.ID, b.ID, 1.0)
	if err != nil {
		t.Fatalf("RecordMatchResult: %v", err)
	}
	if updatedA.Elo <= 1500 {
		t.Errorf("winner's Elo should have increased, got %v", updatedA.Elo)
	}
	if updatedB.Elo >= 1500 {
		t.Errorf("loser's Elo should have decreased, got %v", updatedB.Elo)
	}

	// Re-read independently -- proves this is real, durable persisted state.
	refetchedA, _ := store.Get(ctx, a.ID)
	refetchedB, _ := store.Get(ctx, b.ID)
	if refetchedA.Elo != updatedA.Elo || refetchedB.Elo != updatedB.Elo {
		t.Error("Elo changes from RecordMatchResult must actually persist")
	}
}

func TestCheckpointStore_RecordMatchResultIsZeroSum(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "main", 1, 1600, "this-box", "b.zip", []byte("b"))

	updatedA, updatedB, err := store.RecordMatchResult(ctx, a.ID, b.ID, 1.0)
	if err != nil {
		t.Fatalf("RecordMatchResult: %v", err)
	}
	deltaA := updatedA.Elo - 1500
	deltaB := updatedB.Elo - 1600
	if math.Abs(deltaA+deltaB) > 1e-6 {
		t.Errorf("expected a real zero-sum Elo update, got deltaA=%v deltaB=%v", deltaA, deltaB)
	}
}

func TestCheckpointStore_RecordMatchResultDrawChangesNothingBetweenEquals(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "main", 1, 1500, "this-box", "b.zip", []byte("b"))

	updatedA, updatedB, err := store.RecordMatchResult(ctx, a.ID, b.ID, 0.5)
	if err != nil {
		t.Fatalf("RecordMatchResult: %v", err)
	}
	if math.Abs(updatedA.Elo-1500) > 1e-6 || math.Abs(updatedB.Elo-1500) > 1e-6 {
		t.Errorf("a real draw between equally-rated checkpoints should change nothing, got %v/%v", updatedA.Elo, updatedB.Elo)
	}
}

func TestCheckpointStore_RecordMatchResultRejectsInvalidScore(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	b, _ := store.Create(ctx, "main", 1, 1500, "this-box", "b.zip", []byte("b"))
	if _, _, err := store.RecordMatchResult(ctx, a.ID, b.ID, 1.5); err == nil {
		t.Error("expected an out-of-range score_a to be rejected")
	}
}

func TestCheckpointStore_RecordMatchResultRejectsSelfMatch(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	if _, _, err := store.RecordMatchResult(ctx, a.ID, a.ID, 1.0); err == nil {
		t.Error("expected a checkpoint playing itself to be rejected")
	}
}

func TestCheckpointStore_RecordMatchResultUnknownIDFails(t *testing.T) {
	store := newCheckpointTestStore(t)
	ctx := context.Background()
	a, _ := store.Create(ctx, "main", 0, 1500, "this-box", "a.zip", []byte("a"))
	if _, _, err := store.RecordMatchResult(ctx, a.ID, 999, 1.0); err == nil {
		t.Error("expected a nonexistent opponent id to fail")
	}
}
