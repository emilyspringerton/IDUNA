package nock

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// testdata/ur5e.* are GOLDENBAND's real committed UR5e artifacts (robots/ur5e.grobot.json and
// the `gbtool robot compile` output in assets/robots/), copied verbatim.
func loadUR5e(t *testing.T) (spec, grobot, gskel []byte) {
	t.Helper()
	read := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return read("ur5e.grobot.json"), read("ur5e.grobot"), read("ur5e.gskel")
}

func newRobotTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Apply the REAL migration, not a hand-copied schema.
	mig, err := os.ReadFile(filepath.Join("..", "..", "migrations", "truestore", "202609270001_nock_robots.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(mig)); err != nil {
		t.Fatalf("migration: %v", err)
	}
	return db
}

func TestParseGRobotUR5e(t *testing.T) {
	_, grobot, _ := loadUR5e(t)
	info, err := ParseGRobot(grobot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "ur5e" || len(info.Joints) != 6 || info.TCPJoint != 5 {
		t.Fatalf("header: %+v", info)
	}
	// Universal Robots' published values (joint_limits.yaml / physical_parameters.yaml).
	want := []struct {
		name         string
		effort, mass float64
	}{
		{"shoulder_pan_joint", 150, 3.761}, {"shoulder_lift_joint", 150, 8.058}, {"elbow_joint", 150, 2.846},
		{"wrist_1_joint", 28, 1.37}, {"wrist_2_joint", 28, 1.3}, {"wrist_3_joint", 28, 0.365},
	}
	for i, w := range want {
		j := info.Joints[i]
		if j.Name != w.name || j.Effort != w.effort || j.Mass != w.mass || math.Abs(j.Velocity-math.Pi) > 1e-12 {
			t.Errorf("joint %d: got %s effort %v mass %v vel %v", i, j.Name, j.Effort, j.Mass, j.Velocity)
		}
	}
	for _, j := range info.Joints {
		if j.Axis != [3]float64{0, 0, 1} {
			t.Errorf("%s axis %v, UR joints turn about their frame's +Z", j.Name, j.Axis)
		}
	}
	if info.Joints[2].Upper != math.Pi || info.Joints[0].Upper != 2*math.Pi {
		t.Errorf("limits: elbow %v (want pi), pan %v (want 2pi)", info.Joints[2].Upper, info.Joints[0].Upper)
	}
	if _, err := ParseGRobot(grobot[:len(grobot)-1]); err == nil {
		t.Error("a truncated .grobot must be rejected")
	}
}

func TestRobotStoreRoundTripAndProvenance(t *testing.T) {
	db := newRobotTestDB(t)
	store := &RobotStore{DB: db}
	ctx := context.Background()
	spec, grobot, gskel := loadUR5e(t)

	r, err := store.CreateRobot(ctx, spec, grobot, gskel, "GOLDENBAND scripts/robot_assets.sh")
	if err != nil {
		t.Fatalf("CreateRobot: %v", err)
	}
	if r.Name != "ur5e" || r.Manufacturer != "Universal Robots" || r.Model != "UR5e" || r.JointCount != 6 || !r.HasSkel {
		t.Fatalf("row: %+v", r)
	}
	if math.Abs(r.MovingMassKg-17.7) > 1e-9 {
		t.Errorf("moving mass %v, want UR's published 17.700 kg", r.MovingMassKg)
	}
	if r.Rig == nil || r.Rig.Joints[1].Name != "shoulder_lift_joint" {
		t.Fatalf("rig not decoded on get")
	}
	got, err := store.GetRobot(ctx, r.ID)
	if err != nil || !bytes.Equal(got.GRobotData, grobot) || !bytes.Equal(got.GSkelData, gskel) || got.SpecJSON != string(spec) {
		t.Fatalf("blobs did not round-trip: %v", err)
	}
	list, err := store.ListRobots(ctx)
	if err != nil || len(list) != 1 || list[0].SpecJSON != "" || list[0].GRobotData != nil {
		t.Fatalf("list should be lightweight: %v %+v", err, list)
	}
	if _, err := store.CreateRobot(ctx, spec, grobot, nil, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate name must be rejected, got %v", err)
	}

	// Provenance: a spec edited after compilation no longer matches the rig's embedded hash.
	tampered := bytes.Replace(spec, []byte(`"effort": 150`), []byte(`"effort": 300`), 1)
	if bytes.Equal(tampered, spec) {
		t.Fatal("test setup: tamper did not change the spec")
	}
	if _, err := store.CreateRobot(ctx, tampered, grobot, nil, ""); err == nil || !strings.Contains(err.Error(), "not compiled from this spec") {
		t.Errorf("a doctored spec must be rejected, got %v", err)
	}
	// A spec with an uncited joint is rejected before any hash check.
	uncited := bytes.Replace(spec, []byte(`"source": "ur-ros2-description",
      "note": "effort`), []byte(`"source": "a-guess",
      "note": "effort`), 1)
	if bytes.Equal(uncited, spec) {
		t.Fatal("test setup: uncited replacement did not apply")
	}
	if _, err := store.CreateRobot(ctx, uncited, grobot, nil, ""); err == nil || !strings.Contains(err.Error(), "undeclared source") {
		t.Errorf("an uncited joint must be rejected, got %v", err)
	}
	if err := store.DeleteRobot(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRobot(ctx, r.ID); err == nil {
		t.Error("deleted robot still readable")
	}
}

// fakeGoldenband lays out a GOLDENBAND-shaped working tree (robots/ + assets/robots/ + a .git
// HEAD) from the real UR5e testdata.
func fakeGoldenband(t *testing.T) string {
	t.Helper()
	spec, grobot, gskel := loadUR5e(t)
	dir := t.TempDir()
	for p, b := range map[string][]byte{
		"robots/ur5e.grobot.json":          spec,
		"assets/robots/ur5e.grobot":        grobot,
		"assets/robots/ur5e.gskel":         gskel,
		".git/HEAD":                        []byte("ref: refs/heads/main\n"),
		".git/refs/heads/main":             []byte("0123456789abcdef0123456789abcdef01234567\n"),
		"robots/sources/README-not-a-spec": []byte("x"),
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRobotSyncFromGit(t *testing.T) {
	db := newRobotTestDB(t)
	store := &RobotStore{DB: db}
	ctx := context.Background()
	dir := fakeGoldenband(t)

	rep, err := store.SyncFromGit(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Revision != "0123456789abcdef0123456789abcdef01234567" || len(rep.Items) != 1 || rep.Items[0].Action != "created" {
		t.Fatalf("first sync: %+v", rep)
	}
	list, _ := store.ListRobots(ctx)
	if len(list) != 1 || list[0].SourceLocation != "git:GOLDENBAND@0123456789ab" || !list[0].HasSkel {
		t.Fatalf("row: %+v", list)
	}

	rep, _ = store.SyncFromGit(ctx, dir)
	if rep.Items[0].Action != "unchanged" {
		t.Fatalf("second sync should be a no-op: %+v", rep.Items)
	}

	// git changes (the skeleton is re-exported) -> the git-owned row is updated in place.
	os.Remove(filepath.Join(dir, "assets", "robots", "ur5e.gskel"))
	rep, _ = store.SyncFromGit(ctx, dir)
	if rep.Items[0].Action != "updated" {
		t.Fatalf("changed tree should update: %+v", rep.Items)
	}
	if r, _ := store.GetRobot(ctx, list[0].ID); r.HasSkel {
		t.Error("update did not take the new (skeleton-less) state")
	}

	// A spec edited without recompiling fails provenance and is reported, not imported.
	specPath := filepath.Join(dir, "robots", "ur5e.grobot.json")
	b, _ := os.ReadFile(specPath)
	os.WriteFile(specPath, bytes.Replace(b, []byte(`"effort": 150`), []byte(`"effort": 999`), 1), 0o644)
	rep, _ = store.SyncFromGit(ctx, dir)
	if rep.Items[0].Action != "error" || !strings.Contains(rep.Items[0].Detail, "not compiled from this spec") {
		t.Fatalf("stale compile must be rejected: %+v", rep.Items)
	}
	os.WriteFile(specPath, b, 0o644)

	// A hand-uploaded robot with the same name is never overwritten by git.
	db2 := newRobotTestDB(t)
	store2 := &RobotStore{DB: db2}
	spec, grobot, _ := loadUR5e(t)
	if _, err := store2.CreateRobot(ctx, spec, grobot, nil, "NOCK upload"); err != nil {
		t.Fatal(err)
	}
	rep, _ = store2.SyncFromGit(ctx, dir)
	if rep.Items[0].Action != "skipped" {
		t.Fatalf("hand upload must win: %+v", rep.Items)
	}

	// Removed from git -> reported, left in place.
	os.Remove(specPath)
	os.WriteFile(filepath.Join(dir, "robots", "other.grobot.json"), []byte("{}"), 0o644)
	rep, _ = store.SyncFromGit(ctx, dir)
	if len(rep.NotInGit) != 1 || rep.NotInGit[0] != "ur5e" {
		t.Fatalf("not_in_git: %+v", rep)
	}
	// A real checkout on a branch without the robot data says so, naming the branch.
	bare := t.TempDir()
	os.MkdirAll(filepath.Join(bare, ".git", "refs", "heads"), 0o755)
	os.WriteFile(filepath.Join(bare, ".git", "HEAD"), []byte("ref: refs/heads/master\n"), 0o644)
	os.WriteFile(filepath.Join(bare, ".git", "refs", "heads", "master"), []byte("52c62a630118ab1b8089c78e0d0183f46b89a8b9\n"), 0o644)
	if _, err := store.SyncFromGit(ctx, bare); err == nil || !strings.Contains(err.Error(), "branch master @ 52c62a630118") || !strings.Contains(err.Error(), "has no robot data") {
		t.Errorf("no-data checkout must name the branch and say what's missing, got %v", err)
	}
	if _, err := store.SyncFromGit(ctx, filepath.Join(bare, "nope")); err == nil || !strings.Contains(err.Error(), "no GOLDENBAND checkout at") {
		t.Errorf("missing checkout must say so, got %v", err)
	}
}
