package redgardenplay

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestAgainstTheRealArena runs the REAL REDGARDEN matchmaker, arena server and bot pool (built from
// github.com/emilyspringerton/REDGARDEN; point REDGARDEN_BIN_DIR at a directory holding `matchmaker`, `arena_server`
// and `arena_bot`) and plays a real 10v10 through the gateway: the gateway's byte offsets, ticket, command packets and
// result detection are checked against the actual C implementation, not against this package's own idea of it.
//
//	REDGARDEN_BIN_DIR=/path go test -run TestAgainstTheRealArena -v ./internal/redgardenplay/
func TestAgainstTheRealArena(t *testing.T) {
	dir := os.Getenv("REDGARDEN_BIN_DIR")
	if dir == "" {
		t.Skip("REDGARDEN_BIN_DIR is not set")
	}
	secret := "real-arena-test-secret"
	base := 20000 + os.Getpid()%2000
	mmPort := base
	first := base + 10
	env := append(os.Environ(), "REDGARDEN_TICKET_SECRET="+secret)
	start := func(name string, args ...string) *exec.Cmd {
		c := exec.Command(filepath.Join(dir, name), args...)
		c.Env = env
		if os.Getenv("REAL_LOG") != "" {
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
		}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
		return c
	}
	start("matchmaker", "--listen-port", fmt.Sprint(mmPort), "--lobby-size", "20", "--server-bin", filepath.Join(dir, "arena_server"),
		"--first-game-port", fmt.Sprint(first), "--game-port-range", "10")
	time.Sleep(300 * time.Millisecond)
	for i := 1; i <= 19; i++ {
		start("arena_bot", "--host", "127.0.0.1", "--index", fmt.Sprint(i), "--matchmaker-port", fmt.Sprint(mmPort))
	}
	time.Sleep(3000 * time.Millisecond)

	rec := &memRecorder{}
	cfg := DefaultConfig()
	cfg.Pools = []Pool{{Name: "real-10v10", Mode: 1, Addr: fmt.Sprintf("127.0.0.1:%d", mmPort), Lobby: 20}}
	cfg.Secret = []byte(secret)
	h, err := NewHub(cfg, okResolver, rec)
	if err != nil {
		t.Fatal(err)
	}
	tr := &fakeTransport{}
	c := h.Connect(tr)
	defer c.Close()
	c.Handle(hello("good-token"))
	waitForLong(t, "welcome", 5*time.Second, func() bool { return len(tr.of(SWelcome)) == 1 })
	c.Handle([]byte{CQueue, 1})
	waitForLong(t, "MATCH from the real matchmaker + arena", 30*time.Second, func() bool { return len(tr.of(SMatch)) == 1 })
	m := tr.of(SMatch)[0]
	owner := int(m[1])
	t.Logf("matched: slot %d of %d, team %d", owner, m[2], m[3])

	last := func() *Snap {
		sn := tr.of(SSnap)
		if len(sn) == 0 {
			return nil
		}
		f := sn[len(sn)-1]
		return &Snap{World: f[1 : 1+worldLen], Heroes: f[1+worldLen:]}
	}
	waitForLong(t, "the draft", 20*time.Second, func() bool { s := last(); return s != nil && s.Phase() == PhaseDraft })
	s := last()
	if s.Count() != 20 {
		t.Fatalf("lobby of %d, want 20", s.Count())
	}
	c.Handle([]byte{CPick, 9})
	waitForLong(t, "the match to go live", 20*time.Second, func() bool { s := last(); return s != nil && s.Phase() == PhaseLive })

	pos := func() (float32, float32, int) {
		s := last()
		h := s.hero(owner)
		return getF32(h, hX), getF32(h, hZ), int(h[hHeroID])
	}
	x0, z0, hero := pos()
	if hero != 9 {
		t.Fatalf("picked hero 9, snapshot says %d", hero)
	}
	if math.Abs(float64(x0)) > 60 || math.Abs(float64(z0)) > 60 {
		t.Fatalf("hero starts at %.1f,%.1f: not a plausible arena position (offsets wrong?)", x0, z0)
	}
	tx, tz := float32(0), float32(0)
	c.Handle(append([]byte{CMove}, f32le(tx, tz)...))
	d0 := math.Hypot(float64(x0-tx), float64(z0-tz))
	waitForLong(t, "the hero to walk toward the target", 20*time.Second, func() bool {
		x, z, _ := pos()
		return math.Hypot(float64(x-tx), float64(z-tz)) < d0-6
	})
	x1, z1, _ := pos()
	t.Logf("hero walked %.1f,%.1f -> %.1f,%.1f on a real arena server", x0, z0, x1, z1)

	// the snapshot's other heroes are real too: 6 alive heroes with HP and distinct hero ids
	seen := map[int]bool{}
	for i := 0; i < 20; i++ {
		hr := last().hero(i)
		if binary.LittleEndian.Uint16(hr[hHP:]) == 0 || hr[hAlive] != 1 {
			t.Fatalf("hero %d is not alive with hp in a fresh match", i)
		}
		seen[int(hr[hHeroID])] = true
	}
	if len(seen) < 3 {
		t.Fatalf("only %d distinct heroes in the lobby; offsets look wrong", len(seen))
	}
	c.Handle([]byte{CLeave})
	waitForLong(t, "abandon record", 5*time.Second, func() bool { return len(rec.all()) == 1 })
	if r := rec.all()[0]; r.Result != ResAbandoned || r.HeroID != 9 || r.Lobby != 20 {
		t.Fatalf("leaving a live match: %+v", r)
	}
	_ = context.Background
}

func waitForLong(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out (%v) waiting for %s", d, what)
}
