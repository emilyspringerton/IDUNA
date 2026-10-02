package shankpit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"iduna/internal/shankpit"
)

func TestSnapshotName(t *testing.T) {
	at := time.Date(2026, 10, 2, 15, 30, 45, 0, time.UTC)
	if got := shankpit.SnapshotName("CITY", at); got != "CITY_20261002T153045Z" {
		t.Fatalf("got %q", got)
	}
	if got := shankpit.SnapshotName("CITY_20250101T000000Z", at); got != "CITY_20261002T153045Z" {
		t.Fatalf("chained snapshot name: %q", got)
	}
	long := shankpit.SnapshotName(string(make([]byte, 0))+"A234567890123456789012345678901234567890123456789012345678901234", at)
	if len(long) > 64 || shankpit.ValidateName(long) != nil {
		t.Fatalf("name too long/invalid: %q (%d)", long, len(long))
	}
}

func TestSnapshotLevel_DamageAndSameSecondDebounce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	src, err := s.CreateLevel(ctx, "CITY", 100, 50, 100, true, 2, []shankpit.Wall{{X: 0, Y: 5, Z: 0, SX: 20, SY: 10, SZ: 4, R: 1, G: 1, B: 1}}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 15, 30, 45, 0, time.UTC)
	dmg := []shankpit.BrickCell{{Wall: 0, Key: 1<<20 | 2<<10 | 3, HP: 0}, {Wall: 0, Key: 5, HP: 40}}
	snap, err := s.SnapshotLevel(ctx, src.ID, dmg, at)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Name != "CITY_20261002T153045Z" || len(snap.BrickDamage) != 2 || snap.BrickDamage[0].Key != dmg[0].Key {
		t.Fatalf("snapshot wrong: %+v", snap)
	}
	if _, err := s.SnapshotLevel(ctx, src.ID, dmg, at); !errors.Is(err, shankpit.ErrSnapshotExists) {
		t.Fatalf("same-second repeat must be shankpit.ErrSnapshotExists, got %v", err)
	}
	if _, err := s.SnapshotLevel(ctx, src.ID, []shankpit.BrickCell{{Wall: 9, Key: 1, HP: 0}}, at.Add(time.Second)); err == nil {
		t.Fatal("out-of-range wall must be rejected")
	}
	exp, err := s.Export(ctx, snap.ID)
	if err != nil || len(exp.BrickDamage) != 2 {
		t.Fatalf("export lost damage: %v %+v", err, exp)
	}
	// a snapshot of the snapshot reuses the base name, and the source is untouched
	s2, err := s.SnapshotLevel(ctx, snap.ID, nil, at.Add(2*time.Second))
	if err != nil || s2.Name != "CITY_20261002T153047Z" || len(s2.BrickDamage) != 0 {
		t.Fatalf("snapshot of snapshot: %v %+v", err, s2)
	}
	if orig, _ := s.GetLevel(ctx, src.ID); len(orig.BrickDamage) != 0 {
		t.Fatal("source level was modified")
	}
}

func TestZombieRepository(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateLevel(ctx, "nextown", 100, 50, 100, true, 2, []shankpit.Wall{{X: 0, Y: 5, Z: 0, SX: 20, SY: 10, SZ: 4, R: 1, G: 1, B: 1}}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// "set for zombies" on a main-registry level copies it into the zombies repository.
	z, err := st.SetZombieDefaultLevel(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if z.ID == src.ID || z.Collection != shankpit.CollectionZombies || !z.IsZombieDefault {
		t.Fatalf("expected a flagged copy in zombies, got %+v", z)
	}
	if lv, _ := st.ListLevels(ctx); len(lv) != 1 || lv[0].ID != src.ID {
		t.Fatalf("main registry should still hold only the original: %+v", lv)
	}

	// A zombies-mode snapshot lands in zombies and becomes the new default; the old one is unflagged.
	snap, err := st.SnapshotLevelTo(ctx, z.ID, []shankpit.BrickCell{{Wall: 0, Key: 5, HP: 0}}, time.Date(2026, 10, 2, 15, 30, 45, 0, time.UTC), shankpit.CollectionZombies, true)
	if err != nil {
		t.Fatal(err)
	}
	zl, _ := st.ListLevelsIn(ctx, shankpit.CollectionZombies)
	defaults := 0
	for _, l := range zl {
		if l.IsZombieDefault {
			defaults++
			if l.ID != snap.ID {
				t.Fatalf("default should be the snapshot, got %d", l.ID)
			}
		}
	}
	if len(zl) != 2 || defaults != 1 {
		t.Fatalf("want 2 zombie levels with exactly one default, got %d / %d", len(zl), defaults)
	}
	// A plain snapshot must not leak into zombies or take the flag.
	plain, err := st.SnapshotLevelTo(ctx, src.ID, nil, time.Date(2026, 10, 2, 15, 30, 46, 0, time.UTC), "bogus", true)
	if err != nil || plain.Collection != shankpit.CollectionLevels || plain.IsZombieDefault {
		t.Fatalf("plain snapshot leaked: %+v err=%v", plain, err)
	}
}
