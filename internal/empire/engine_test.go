package empire

import (
	"context"
	"testing"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

// playBots runs a whole match between two AIs and returns the finished board.
func playBots(t *testing.T, e *Engine, seed uint32, a, b int) *Sim {
	t.Helper()
	s, err := e.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if !s.Begin(seed, s.BotDeck(a), s.BotFaction(a), s.BotDeck(b), s.BotFaction(b)) {
		t.Fatal("bot decks are not valid")
	}
	s.AI(0, a)
	s.AI(1, b)
	for i := 0; i < 6000; i++ {
		if _, over := s.Step(); over {
			return s
		}
	}
	t.Fatal("a match ran away")
	return nil
}

func TestEngineRunsAMatch(t *testing.T) {
	e := newTestEngine(t)
	s := playBots(t, e, 7, 1, 2)
	r := s.Result()
	// the same match run from Node against the same wasm (PARENA tests/empire_engine_test.mjs): marrow beat quill on a fallen king at tick 1516
	if r.Ticks != 1516 || r.Winner != 0 || r.Reason != 1 || r.Crowns[0] != 3 {
		t.Fatalf("unexpected result %+v", r)
	}
	if h := s.Hash(); h != 3229431767 {
		t.Fatalf("board hash %d", h)
	}
}

func TestEngineIsDeterministicAndReplayable(t *testing.T) {
	e := newTestEngine(t)
	a := playBots(t, e, 99, 3, 6)
	// replay the recorded plays on a board with NO AIs: it must arrive at the same board
	log := a.Log()
	b, _ := e.New(context.Background())
	defer b.Close()
	b.Begin(99, b.BotDeck(3), b.BotFaction(3), b.BotDeck(6), b.BotFaction(6))
	i := 0
	for n := 0; n < 6000; n++ {
		for i < len(log) && int(log[i].Tick) == b.Tick()+1 {
			c := log[i]
			b.Play(int(c.Seat), int(c.Slot), int(c.X4), int(c.Y4))
			i++
		}
		if _, over := b.Step(); over {
			break
		}
	}
	if a.Hash() != b.Hash() || a.Tick() != b.Tick() || a.Winner() != b.Winner() {
		t.Fatalf("replay diverged: tick %d/%d hash %d/%d", a.Tick(), b.Tick(), a.Hash(), b.Hash())
	}
}

func TestSnapshotRestoresTheBoard(t *testing.T) {
	e := newTestEngine(t)
	a, _ := e.New(context.Background())
	defer a.Close()
	a.Begin(5, a.BotDeck(4), a.BotFaction(4), a.BotDeck(5), a.BotFaction(5))
	a.AI(0, 4)
	a.AI(1, 5)
	for i := 0; i < 700; i++ {
		a.Step()
	}
	snap := a.Snapshot()
	if len(snap) != 12288 {
		t.Fatalf("snapshot is %d bytes", len(snap))
	}
	if a.Units() == 0 {
		t.Fatal("nothing on the board after 35 seconds; the AIs are not playing")
	}
}
