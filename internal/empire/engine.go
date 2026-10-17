// Package empire is IDUNA's side of EMPIRE web (WOTAN /empire/play.html): the referee.
//
// The game is ONE wasm module (PARENA/stdlib/empire/web, compiled by scripts/build_empire_wasm.sh) that the browser plays and that this
// package steps headlessly. Embedding it here is the point: the server does not re-implement the rules, it runs them. A match is
// authoritative because the server's copy is the one that decides, the AIs (which live inside the module) are hosted here, and what the
// ladder records is what the server's board said. Browsers apply each executed play as a "turn" to their own copy of the same module and
// draw it; every so often the server sends the board's hash and a client that disagrees asks for a snapshot.
//
// empire.wasm is a committed copy of WOTAN/empire/empire.wasm (scripts/sync-empire-wasm.sh). A page built from different rules would drift
// from this one, so the clients send the sim version they were built with (em_version) and the gateway refuses a mismatch.
package empire

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed empire.wasm
var wasmBytes []byte

// Engine is the compiled module; New makes a board.
type Engine struct {
	rt  wazero.Runtime
	cm  wazero.CompiledModule
	ver uint32
}

// NewEngine compiles the embedded module once.
func NewEngine(ctx context.Context) (*Engine, error) {
	return NewEngineFrom(ctx, wasmBytes)
}

// NewEngineFrom compiles a given module (tests use it to check the committed copy against a fresh build).
func NewEngineFrom(ctx context.Context, wasm []byte) (*Engine, error) {
	rt := wazero.NewRuntime(ctx)
	cm, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("empire: compile: %w", err)
	}
	if n := len(cm.ImportedFunctions()); n != 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("empire: the module imports %d functions; it must import nothing", n)
	}
	e := &Engine{rt: rt, cm: cm}
	s, err := e.New(ctx)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	e.ver = uint32(s.call("em_version"))
	s.Close()
	return e, nil
}

// Version is the sim version of the embedded module (what clients must have been built with).
func (e *Engine) Version() uint32 { return e.ver }

// Close releases the runtime.
func (e *Engine) Close(ctx context.Context) error { return e.rt.Close(ctx) }

// Sim is one board. Not safe for concurrent use except through its own methods (a mutex serialises the calls).
type Sim struct {
	ctx context.Context
	mu  sync.Mutex
	mod api.Module
	fn  map[string]api.Function
	// the log index already reported by Step
	logged int
}

// New instantiates a fresh board (4 MB of linear memory).
func (e *Engine) New(ctx context.Context) (*Sim, error) {
	mod, err := e.rt.InstantiateModule(ctx, e.cm, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("empire: instantiate: %w", err)
	}
	s := &Sim{ctx: ctx, mod: mod, fn: map[string]api.Function{}}
	s.call("em_init")
	return s, nil
}

// Close frees the board.
func (s *Sim) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mod != nil {
		_ = s.mod.Close(s.ctx)
		s.mod = nil
	}
}

func (s *Sim) call(name string, args ...uint64) int32 {
	f := s.fn[name]
	if f == nil {
		f = s.mod.ExportedFunction(name)
		if f == nil {
			panic("empire: the module has no export " + name)
		}
		s.fn[name] = f
	}
	res, err := f.Call(s.ctx, args...)
	if err != nil {
		panic(fmt.Sprintf("empire: %s trapped: %v", name, err))
	}
	if len(res) == 0 {
		return 0
	}
	return int32(uint32(res[0]))
}

func u(v int) uint64 { return uint64(uint32(int32(v))) }

// Cmd is one executed play: what a turn carries.
type Cmd struct {
	Tick uint32
	Seat uint8
	Slot uint8
	X4   uint8
	Y4   uint8
}

// Begin starts a match. False if a deck is not eight different cards.
func (s *Sim) Begin(seed uint32, deck0 uint32, fac0 int, deck1 uint32, fac1 int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logged = 0
	return s.call("em_match_begin", u(int(int32(seed))), u(int(int32(deck0))), u(fac0), u(int(int32(deck1))), u(fac1)) == 1
}

// AI makes a seat a bot (personality 1..8), or a person again with 0.
func (s *Sim) AI(seat, personality int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("em_ai_set", u(seat), u(personality))
}

// Play queues a play for the next Step. Whether it happens is the board's decision.
func (s *Sim) Play(seat, slot, x4, y4 int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("em_sim_cmd", u(seat), u(slot), u(x4), u(y4))
}

// Why is a play refused (0 = it would be accepted now): 1 not running, 2 no such slot, 3 elixir, 4 cancelled, 5 not there.
func (s *Sim) Why(seat, slot, x4, y4 int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.call("em_can_play", u(seat), u(slot), u(x4), u(y4)))
}

// Resign ends the match in the opponent's favour.
func (s *Sim) Resign(seat int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("em_sim_resign", u(seat))
}

// Step advances one tick and returns the plays that were executed in it (humans' first, then the AIs'), and whether the match is over.
func (s *Sim) Step() (cmds []Cmd, over bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	over = s.call("em_sim_step") != 0
	n := int(s.call("em_cmdlog_n"))
	if n > s.logged {
		base := uint32(s.call("em_cmdlog_addr"))
		buf, ok := s.mod.Memory().Read(base+uint32(s.logged)*8, uint32(n-s.logged)*8)
		if ok {
			for i := 0; i+8 <= len(buf); i += 8 {
				tick := binary.LittleEndian.Uint32(buf[i:])
				p := binary.LittleEndian.Uint32(buf[i+4:])
				cmds = append(cmds, Cmd{Tick: tick, Seat: uint8(p & 15), Slot: uint8(p >> 4 & 15), X4: uint8(p >> 8), Y4: uint8(p >> 16)})
			}
		}
		s.logged = n
	}
	return cmds, over
}

func (s *Sim) get(name string, args ...int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := make([]uint64, len(args))
	for i, v := range args {
		a[i] = u(v)
	}
	return int(s.call(name, a...))
}

func (s *Sim) Tick() int            { return s.get("em_tick") }
func (s *Sim) Over() bool           { return s.get("em_over") != 0 }
func (s *Sim) Winner() int          { return s.get("em_winner") }
func (s *Sim) Reason() int          { return s.get("em_reason") }
func (s *Sim) Crowns(seat int) int  { return s.get("em_crowns", seat) }
func (s *Sim) Elixir(seat int) int  { return s.get("em_elixir", seat) }
func (s *Sim) Decorum(seat int) int { return s.get("em_decorum", seat) }
func (s *Sim) Hand(seat, k int) int { return s.get("em_hand", seat, k) }
func (s *Sim) Stat(seat, k int) int { return s.get("em_stat", seat, k) }
func (s *Sim) Plays(seat, c int) int {
	return s.get("em_plays", seat, c)
}
func (s *Sim) TowerHP(t int) int    { return s.get("em_tower_hp", t) }
func (s *Sim) TowerAlive(t int) int { return s.get("em_tower_alive", t) }
func (s *Sim) Units() int           { return s.get("em_unit_count") }
func (s *Sim) Hash() uint32         { return uint32(s.get("em_sim_hash")) }

// BotDeck / BotFaction are the AIs' own decks, from the module (so there is one list).
func (s *Sim) BotDeck(p int) uint32 { return uint32(s.get("em_bot_deck", p)) }
func (s *Sim) BotFaction(p int) int { return s.get("em_bot_faction", p) }

// Snapshot is the whole board (a client restores it with a memcpy).
func (s *Sim) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	addr, n := uint32(s.call("em_sim_addr")), uint32(s.call("em_sim_len"))
	b, ok := s.mod.Memory().Read(addr, n)
	if !ok {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Log is every play executed so far.
func (s *Sim) Log() []Cmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int(s.call("em_cmdlog_n"))
	base := uint32(s.call("em_cmdlog_addr"))
	buf, ok := s.mod.Memory().Read(base, uint32(n)*8)
	if !ok {
		return nil
	}
	out := make([]Cmd, 0, n)
	for i := 0; i+8 <= len(buf); i += 8 {
		tick := binary.LittleEndian.Uint32(buf[i:])
		p := binary.LittleEndian.Uint32(buf[i+4:])
		out = append(out, Cmd{Tick: tick, Seat: uint8(p & 15), Slot: uint8(p >> 4 & 15), X4: uint8(p >> 8), Y4: uint8(p >> 16)})
	}
	return out
}

// Result is what the board says about a finished match.
type Result struct {
	Winner  int // 0, 1, or 2 for a draw
	Reason  int
	Ticks   int
	Crowns  [2]int
	TowerHP [2]int
	Towers  [2]int
	Stats   [2][16]int
	Plays   [2][16]int
	Decorum [2]int
}

// Result reads the board's verdict.
func (s *Sim) Result() Result {
	var r Result
	r.Winner, r.Reason, r.Ticks = s.Winner(), s.Reason(), s.Tick()
	for seat := 0; seat < 2; seat++ {
		r.Crowns[seat] = s.Crowns(seat)
		r.Decorum[seat] = s.Decorum(seat)
		for t := 0; t < 3; t++ {
			r.TowerHP[seat] += s.TowerHP(seat*3 + t)
			r.Towers[seat] += s.TowerAlive(seat*3 + t)
		}
		for k := 0; k < 16; k++ {
			r.Stats[seat][k] = s.Stat(seat, k)
			r.Plays[seat][k] = s.Plays(seat, k)
		}
	}
	return r
}
