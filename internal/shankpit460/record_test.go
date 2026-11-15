package shankpit460

import (
	"context"
	"testing"
	"time"
)

type fakeRec struct{ ch chan *Match }

func (f *fakeRec) Record(ctx context.Context, m *Match) error { f.ch <- m; return nil }

func newFakeRec() *fakeRec { return &fakeRec{ch: make(chan *Match, 8)} }

func (f *fakeRec) next(t *testing.T) *Match {
	t.Helper()
	select {
	case m := <-f.ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no match was recorded")
	}
	return nil
}

func (f *fakeRec) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-f.ch:
		t.Fatalf("nothing should be recorded, got %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

// kill b with the knife from point blank, then wait out the respawn and protection
func knifeKill(r *rig, a, b *tc, sb int) {
	r.place(a, -260, 0, -40)
	r.place(b, -260, 0, -50)
	r.run(200 * time.Millisecond)
	a.hit(sb, WKnife, false)
	r.run(4200 * time.Millisecond)
}

func TestRecordKillLimit(t *testing.T) {
	rec := newFakeRec()
	r, a, b, sa, sb := liveMatch(t, func(c *Config) { c.KillLimit = 3 }, WithRecorder(rec))
	for i := 0; i < 3 && a.last(SEnd) == nil; i++ {
		knifeKill(r, a, b, sb)
	}
	if a.last(SEnd) == nil {
		t.Fatal("no END")
	}
	m := rec.next(t)
	if m.Reason != EndKills || m.KillLimit != 3 || !m.Counted || len(m.Players) != 2 {
		t.Fatalf("match %+v", m)
	}
	w, l := m.Players[0], m.Players[1]
	if w.ID != "p-a" || w.Slot != sa || w.Kills != 3 || w.Place != 1 || !w.Winner || w.Left || w.Guest {
		t.Fatalf("winner line %+v", w)
	}
	if l.ID != "p-b" || l.Deaths != 3 || l.Place != 2 || l.Winner || l.Name != "Name-b" {
		t.Fatalf("second line %+v", l)
	}
	if !m.EndedAt.After(m.LiveAt) || m.UID == "" {
		t.Fatalf("times/uid %+v", m)
	}
	_ = b
}

func TestRecordLeaverForfeitsAndIsKept(t *testing.T) {
	rec := newFakeRec()
	r, a, b, _, sb := liveMatch(t, nil, WithRecorder(rec))
	knifeKill(r, a, b, sb)
	r.run(30 * time.Second)
	b.c.Close()
	m := rec.next(t)
	if m.Reason != EndAbandoned || !m.Counted || len(m.Players) != 2 {
		t.Fatalf("match %+v", m)
	}
	if !m.Players[0].Winner || m.Players[0].ID != "p-a" || m.Players[0].Kills != 1 {
		t.Fatalf("the one still there wins by forfeit: %+v", m.Players[0])
	}
	g := m.Players[1]
	if g.ID != "p-b" || !g.Left || g.Place != 2 || g.Deaths != 1 || g.LeftAt < 30*time.Second || g.Winner {
		t.Fatalf("the leaver keeps their line: %+v", g)
	}
}

func TestRecordShortMatchIsNotCounted(t *testing.T) {
	rec := newFakeRec()
	_, _, b, _, _ := liveMatch(t, nil, WithRecorder(rec))
	b.c.Close()
	m := rec.next(t)
	if m.Counted {
		t.Fatalf("a match abandoned after a few seconds must not count: %+v", m)
	}
}

func TestRecordTimeTieHasNoWinner(t *testing.T) {
	rec := newFakeRec()
	r, _, _, _, _ := liveMatch(t, nil, WithRecorder(rec))
	r.run(6 * time.Minute)
	m := rec.next(t)
	if m.Reason != EndTime || !m.Counted {
		t.Fatalf("match %+v", m)
	}
	for _, p := range m.Players {
		if p.Winner || p.Place != 1 {
			t.Fatalf("0-0 is a shared first place with no winner: %+v", m.Players)
		}
	}
}

func TestRecordThreeWithALeaverAndGuests(t *testing.T) {
	rec := newFakeRec()
	r := newRig(t, func(c *Config) { c.KillLimit = 2 }, nil, WithRecorder(rec))
	a := r.client("a", "")
	b := r.client("b", "")
	g := r.client("", "Visitor")
	a.queue()
	b.queue()
	g.queue()
	r.run(7 * time.Second)
	sb := slotOf(t, b.last(SMatch))
	r.run(1200 * time.Millisecond)
	r.run(25 * time.Second)
	g.c.Close() // the guest leaves; a and b play on
	if a.last(SEnd) != nil {
		t.Fatal("two people are still playing")
	}
	for i := 0; i < 2 && a.last(SEnd) == nil; i++ {
		knifeKill(r, a, b, sb)
	}
	m := rec.next(t)
	if len(m.Players) != 3 || m.Reason != EndKills {
		t.Fatalf("match %+v", m)
	}
	if m.Players[0].ID != "p-a" || !m.Players[0].Winner || m.Players[1].ID != "p-b" || m.Players[1].Place != 2 {
		t.Fatalf("order %+v", m.Players)
	}
	last := m.Players[2]
	if !last.Guest || !last.Left || last.Place != 3 || last.Name != "Visitor" {
		t.Fatalf("the guest who left is last: %+v", last)
	}
	rec.none(t)
}

func TestNoRecorderNoRecord(t *testing.T) {
	r, _, b, _, _ := liveMatch(t, nil)
	b.c.Close()
	r.run(80 * time.Millisecond)
	if r.h.rec != nil {
		t.Fatal("no recorder configured")
	}
}
