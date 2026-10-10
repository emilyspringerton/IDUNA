// Package hstracker runs the HRIP Hearthstone tracker (PARENA logic + thin C host) against a slice of
// Power.log lines and returns the parsed game record. The sources under src/ are a vendored copy of
// HRIP/tracker (same per-consuming-repo convention as internal/nock/parena_runtime); the binary is built
// on first use with the image's own parena compiler + gcc, exactly like NOCK's door-script compile.
package hstracker

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed src/*
var srcFS embed.FS

// Entity is one revealed card with its owner and the zone it first appeared in.
type Entity struct {
	ID     int    `json:"id"`
	Player int    `json:"player"`
	Card   string `json:"card"`
	Zone0  string `json:"zone0"`
	T      int    `json:"t"`
}

// Record is the tracker's per-game JSON object.
type Record struct {
	GameType string            `json:"game_type"`
	Format   string            `json:"format"`
	Player1  string            `json:"player1"`
	Player2  string            `json:"player2"`
	Result1  int               `json:"result1"`
	Result2  int               `json:"result2"`
	Turns    int               `json:"turns"`
	Complete int               `json:"complete"`
	Me       int               `json:"me"`
	Entities []Entity          `json:"entities"`
	Timeline []json.RawMessage `json:"timeline"`
}

// Runner builds the tracker once and runs it per request.
type Runner struct {
	WorkDir string // where the binary and temp files live ("" = os.TempDir()/hstracker)

	once sync.Once
	bin  string
	err  error
	sem  chan struct{}
}

func parenaBin() string {
	if v := os.Getenv("NOCK_PARENA_BIN"); v != "" {
		return v
	}
	if p, err := exec.LookPath("parena"); err == nil {
		return p
	}
	if _, err := os.Stat("/home/fatbaby/PARENA/parena"); err == nil {
		return "/home/fatbaby/PARENA/parena"
	}
	return "parena"
}

func runtimeDir() string {
	if v := os.Getenv("NOCK_PARENA_RUNTIME_DIR"); v != "" {
		return v
	}
	return "/app/parena_runtime"
}

func (r *Runner) build() (string, error) {
	dir := r.WorkDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "hstracker")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, n := range []string{"power_log.prn", "game_state.prn", "host.c"} {
		b, err := srcFS.ReadFile("src/" + n)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			return "", err
		}
	}
	pb := parenaBin()
	stdlib := os.Getenv("HRIP_PARENA_STDLIB")
	if stdlib == "" {
		stdlib = filepath.Join(filepath.Dir(pb), "stdlib")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	gen := filepath.Join(dir, "tracker_gen.c")
	if out, err := exec.CommandContext(ctx, pb, "build", filepath.Join(stdlib, "string.prn"),
		filepath.Join(dir, "power_log.prn"), filepath.Join(dir, "game_state.prn"), "-o", gen).CombinedOutput(); err != nil {
		return "", fmt.Errorf("parena build: %v: %s", err, strings.TrimSpace(string(out)))
	}
	rt := runtimeDir()
	bin := filepath.Join(dir, "tracker")
	gcc := os.Getenv("NOCK_GCC_BIN")
	if gcc == "" {
		gcc = "gcc"
	}
	if out, err := exec.CommandContext(ctx, gcc, "-std=c99", "-O2", "-DPARENA_NO_GRAPHICS", "-I", rt, "-I", dir,
		filepath.Join(dir, "host.c"), filepath.Join(rt, "parena_runtime.c"), "-o", bin, "-lm").CombinedOutput(); err != nil {
		return "", fmt.Errorf("gcc: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return bin, nil
}

// Run feeds lines (Power.log text, any line endings) to the tracker in --live mode and returns the
// records it emitted, oldest first; the last one is the game in progress (Complete==0) when there is one.
func (r *Runner) Run(ctx context.Context, lines []string) ([]Record, error) {
	r.once.Do(func() { r.bin, r.err = r.build(); r.sem = make(chan struct{}, 2) })
	if r.err != nil {
		return nil, r.err
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	f, err := os.CreateTemp(filepath.Dir(r.bin), "power-*.log")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, r.bin, f.Name(), "--live").Output()
	if err != nil {
		return nil, fmt.Errorf("tracker: %w", err)
	}
	var recs []Record
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			return nil, errors.New("tracker: bad output")
		}
		recs = append(recs, rec)
	}
	return recs, nil
}
