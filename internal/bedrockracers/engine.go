package bedrockracers

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// bedrockracers.wasm is a committed copy of WOTAN/bedrockracers/bedrockracers.wasm (scripts/sync-bedrockracers-wasm.sh). A page built from
// different rules would drift from this copy, so browsers send the sim version they were built with (br_version) and the hub refuses a mismatch.
//
//go:embed bedrockracers.wasm
var wasmBytes []byte

// Engine is the compiled module; New makes a match.
type Engine struct {
	rt  wazero.Runtime
	cm  wazero.CompiledModule
	ver uint32
}

// NewEngine compiles the embedded module once.
func NewEngine(ctx context.Context) (*Engine, error) { return NewEngineFrom(ctx, wasmBytes) }

// NewEngineFrom compiles a given module (tests check the committed copy against a fresh build with it).
func NewEngineFrom(ctx context.Context, wasm []byte) (*Engine, error) {
	rt := wazero.NewRuntime(ctx)
	cm, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("bedrockracers: compile: %w", err)
	}
	if n := len(cm.ImportedFunctions()); n != 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("bedrockracers: the module imports %d functions; it must import nothing", n)
	}
	e := &Engine{rt: rt, cm: cm}
	s, err := e.New(ctx)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	e.ver = uint32(s.call("br_version"))
	s.Close()
	return e, nil
}

// Version is the sim version of the embedded module (what browsers must have been built with).
func (e *Engine) Version() uint32 { return e.ver }

// Close releases the runtime.
func (e *Engine) Close(ctx context.Context) error { return e.rt.Close(ctx) }

// Sim is one match. A mutex serialises the calls; one goroutine (the room's) drives it.
type Sim struct {
	ctx  context.Context
	mu   sync.Mutex
	mod  api.Module
	fn   map[string]api.Function
	pend uint32
}

// New instantiates a fresh module (8 MB of linear memory, mostly never touched) and initialises it for simulation only: the road map, nothing to draw.
func (e *Engine) New(ctx context.Context) (*Sim, error) {
	mod, err := e.rt.InstantiateModule(ctx, e.cm, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("bedrockracers: instantiate: %w", err)
	}
	s := &Sim{ctx: ctx, mod: mod, fn: map[string]api.Function{}}
	s.call("br_init_sim")
	s.pend = uint32(s.call("br_net_pend_addr"))
	return s, nil
}

// Close frees the match's module.
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
			panic("bedrockracers: the module has no export " + name)
		}
		s.fn[name] = f
	}
	res, err := f.Call(s.ctx, args...)
	if err != nil {
		panic(fmt.Sprintf("bedrockracers: %s trapped: %v", name, err))
	}
	if len(res) == 0 {
		return 0
	}
	return int32(uint32(res[0]))
}

func u(v int) uint64 { return uint64(uint32(int32(v))) }

// Begin starts a match: seed, bot level 1..3, and a bit per slot that is a person.
func (s *Sim) Begin(seed uint32, level, humans int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	me := 0
	for i := 0; i < 12; i++ {
		if humans>>uint(i)&1 != 0 {
			me = i
			break
		}
	}
	s.call("br_net_begin", u(int(int32(seed))), u(level), u(humans), u(me))
}

// SetCmd is what a person's car is told to do on the next Tick (eight bytes: thr steer pitch roll strafe lift flags 0).
func (s *Sim) SetCmd(slot int, cmd [8]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot < 0 || slot > 11 {
		return
	}
	s.mod.Memory().Write(s.pend+uint32(slot)*8, cmd[:])
}

// Drop turns a person into a bot, from the next Tick.
func (s *Sim) Drop(slot int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("br_net_drop", u(slot))
}

// Tick plays one 16 ms tick (the kickoff countdown's ticks included).
func (s *Sim) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("br_net_tick")
}

// EndNow ends a match that has gone on too long: the leading team wins, reason 4 (closed).
func (s *Sim) EndNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("br_net_end_now")
}

func (s *Sim) get(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.call(name))
}

// Phase is 1 while the match is on, 2 once it is over.
func (s *Sim) Phase() int { return s.get("br_net_phase") }

// Ticks is the match clock: ticks of play, not counting the kickoff countdowns.
func (s *Sim) Ticks() int { return s.get("br_net_ticks") }

// Roster is a bit per slot that is still a person.
func (s *Sim) Roster() int { return s.get("br_net_roster") }

// Hash is the match state's hash (the browsers send theirs every 250 ticks).
func (s *Sim) Hash() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint32(s.call("br_net_hash"))
}

// SlotStats is what one car did.
type SlotStats struct {
	Goals, Assists, OwnGoals, Steals, Catches, Throws, Passes, Fires int
	Touches, TopSpeed, AirUsed                                       int
}

// Result is the match as the module counts it.
type Result struct {
	Winner   int // team 0..3, or -1
	Reason   int
	Ticks    int
	Overtime bool
	Phase    int
	Scores   [4]int
	Conceded [4]int
	Slots    [12]SlotStats
}

// Result reads the result block.
func (s *Sim) Result() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	addr := uint32(s.call("br_net_result"))
	b, _ := s.mod.Memory().Read(addr, 16+12*16)
	var r Result
	if len(b) < 16+12*16 {
		return r
	}
	r.Winner = int(b[0])
	if b[0] == 255 {
		r.Winner = -1
	}
	r.Reason = int(b[1])
	r.Ticks = int(binary.LittleEndian.Uint32(b[2:]))
	r.Overtime = b[6] != 0
	r.Phase = int(b[7])
	for t := 0; t < 4; t++ {
		r.Scores[t] = int(b[8+t])
		r.Conceded[t] = int(b[12+t])
	}
	for sl := 0; sl < 12; sl++ {
		o := 16 + sl*16
		r.Slots[sl] = SlotStats{Goals: int(b[o]), Assists: int(b[o+1]), OwnGoals: int(b[o+2]), Steals: int(b[o+3]), Catches: int(b[o+4]),
			Throws: int(b[o+5]), Passes: int(b[o+6]), Fires: int(b[o+7]), Touches: int(binary.LittleEndian.Uint16(b[o+8:])),
			TopSpeed: int(binary.LittleEndian.Uint16(b[o+10:])), AirUsed: int(binary.LittleEndian.Uint16(b[o+12:]))}
	}
	return r
}
