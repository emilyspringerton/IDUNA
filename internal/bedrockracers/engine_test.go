package bedrockracers

import (
	"context"
	"os"
	"testing"
	"time"
)

// goldenCmd is the scripted command a person gives at tick t: the same closed-form stream tests/br_golden.mjs plays in Node, so the two
// engines (V8 and wazero) can be compared on one match. slot 1 drives and weaves, slot 6 brakes and boosts and holds its magnet now and then.
func goldenCmd(slot, t int) [8]byte {
	if slot == 1 {
		thr := byte(0)
		if t%40 < 25 {
			thr = 127
		}
		steer := byte(0xB0) // -80
		if t%90 < 30 {
			steer = 80
		}
		flags := byte(1)
		if t%7 == 0 {
			flags = 5
		}
		return [8]byte{thr, steer, 0, 0, 0, 0, flags, 0}
	}
	thr := byte(127)
	if t%60 >= 45 {
		thr = byte(0x81) // -127
	}
	steer := byte(0)
	if t%50 < 20 {
		steer = 100
	}
	return [8]byte{thr, steer, 0, 0, 0, byte(t % 3 * 100), byte(t%30/10) * 2, 0}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

func TestEngineModuleHygiene(t *testing.T) {
	e := newEngine(t)
	if e.Version() != 1 {
		t.Fatalf("sim version %d, want 1", e.Version())
	}
}

// the committed golden hashes come from the module run in Node (V8): tests/br_golden.mjs prints them. Equal hashes mean wazero reached
// exactly the same match state as the browsers' engine, which is what lockstep needs.
func TestEngineMatchesTheBrowsersEngine(t *testing.T) {
	e := newEngine(t)
	s, err := e.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Begin(777, 3, 1<<1|1<<6)
	golden := map[int]uint32{}
	if v := os.Getenv("BR_GOLDEN"); v != "" {
		t.Log("BR_GOLDEN is only for regenerating")
	}
	for k, v := range goldenHashes {
		golden[k] = v
	}
	last := 0
	for _, upTo := range sortedKeys(golden) {
		for tk := last; tk < upTo; tk++ {
			s.SetCmd(1, goldenCmd(1, tk))
			s.SetCmd(6, goldenCmd(6, tk))
			s.Tick()
		}
		last = upTo
		if got := s.Hash(); got != golden[upTo] {
			t.Fatalf("after %d ticks the state hash is %d, the Node engine says %d", upTo, got, golden[upTo])
		}
	}
}

func sortedKeys(m map[int]uint32) []int {
	var ks []int
	for k := range m {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return ks
}

// how fast a match steps in wazero (the hub budgets rooms from this): logged, and failed only when it is hopelessly slow.
func TestEngineTickCost(t *testing.T) {
	e := newEngine(t)
	s, err := e.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Begin(4242, 2, 1<<0|1<<4|1<<8)
	n := 3000
	st := time.Now()
	for tk := 0; tk < n; tk++ {
		for _, sl := range []int{0, 4, 8} {
			s.SetCmd(sl, goldenCmd(1, tk))
		}
		s.Tick()
	}
	per := time.Since(st) / time.Duration(n)
	t.Logf("%d ticks, %v a tick (%.1f%% of a core at 62.5 ticks a second)", n, per, float64(per)*62.5/1e9*100)
	if per > 4*time.Millisecond {
		t.Fatalf("a tick costs %v: far too slow for a room", per)
	}
}

func TestEngineResultAndForcedEnd(t *testing.T) {
	e := newEngine(t)
	s, err := e.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Begin(99, 2, 1<<2|1<<9)
	for tk := 0; tk < 2500; tk++ {
		s.SetCmd(2, goldenCmd(1, tk))
		s.SetCmd(9, goldenCmd(6, tk))
		s.Tick()
	}
	if s.Phase() != 1 || s.Roster() != (1<<2|1<<9) {
		t.Fatalf("phase %d roster %b", s.Phase(), s.Roster())
	}
	s.Drop(9)
	if s.Roster() != 1<<2 {
		t.Fatalf("the drop left roster %b", s.Roster())
	}
	s.EndNow()
	r := s.Result()
	if s.Phase() != 2 || r.Reason != ReasonClosed || r.Phase != 2 {
		t.Fatalf("forced end: phase %d reason %d result phase %d", s.Phase(), r.Reason, r.Phase)
	}
	if r.Ticks < 1000 || r.Ticks > 2500 {
		t.Fatalf("ticks %d", r.Ticks)
	}
}
