package nock

// door_script_compile.go -- the real, server-side PARENA->C->.so compile pipeline for SHANKPIT
// Story System door scripts (SHANKPIT/docs/STORY_SYSTEM_NORTHSTAR.md Part 2, S459-81's own real
// Phase 1). Founder real-time, closing the gap named at the very start of that whole design
// thread ("via the nock tools"): S459-81 proved the PARENA->C->dlopen mechanism works end to end,
// but required a human to run `parena build` + `gcc` by hand and reference the result by a local
// filesystem path. This is the missing "NOCK compiles it for you" half -- same real shape as
// procgen.go's own PARENA->Java->javac pipeline for procedural textures, targeting C instead.
//
// # Why the C target is the right choice here, not a compromise (already argued in the
// NORTHSTAR doc, restated briefly): door scripts are hand-authored by a trusted NOCK-admin-gated
// map designer, not LLM-generated -- the same trust level as any other server-side code an admin
// ships. procgen.go's own avoidance of the C target was specifically about *LLM-generated*
// texture source being a real, unsandboxed code-exec vector via #target/inline-C; that concern
// doesn't apply to a human deliberately writing a door script for their own server.
//
// # The real, current contract a map designer writes against (docs/STORY_SYSTEM_NORTHSTAR.md
// Part 2, S459-81's own real, live-verified example at SHANKPIT's examples/story-doors/
// door_tick.prn):
//
//	(module doorscript)
//	(import math)
//	(defn door-tick [(dist-to-player : F64) (state : F64)] : F64 ...)
//
// Returns the door's own new state (0.0 = closed, 1.0 = open; SHANKPIT's own story_doors.h
// treats >= 0.5 as open). Validated the same "reject #target, restrict imports, require the
// exact function signature" way procgen.go's validateProcTextureSource already does, as a real,
// explicit second layer of defense on top of whatever the emitter itself does or doesn't allow.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maxDoorScriptSourceBytes = 20 * 1024
	doorScriptCompileTimeout = 20 * time.Second
)

// validateDoorScriptSource is the first, static layer of defense -- same real posture
// validateProcTextureSource already takes: reject the literal #target token (the C target's own
// real inline-C escape hatch, see emit.c's own doc comment), restrict imports to math only, and
// require the exact real door-tick contract by name.
func validateDoorScriptSource(src string) error {
	if len(src) == 0 {
		return fmt.Errorf("nock: door script source is empty")
	}
	if len(src) > maxDoorScriptSourceBytes {
		return fmt.Errorf("nock: door script source too large (%d bytes, max %d)", len(src), maxDoorScriptSourceBytes)
	}
	if strings.Contains(src, "#target") {
		return fmt.Errorf("nock: door script source may not use #target")
	}
	if strings.Contains(src, "inline-c") {
		return fmt.Errorf("nock: door script source may not use inline-c")
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "(import ") && trimmed != "(import math)" {
			return fmt.Errorf("nock: door script source may only import 'math', found: %s", trimmed)
		}
	}
	if !strings.Contains(src, "(defn door-tick ") {
		return fmt.Errorf("nock: door script source must define (defn door-tick ...)")
	}
	return nil
}

// parenaRuntimeDir is where this package's own vendored copy of PARENA's runtime.h/.c lives --
// checked in under this same package (internal/nock/parena_runtime/), same real "vendored
// per-consuming-repo" convention PAPERCRAFT/ECOWAR/WEAKNIGHT_BEDROCK_RACERS already established
// (the SAGA-audited research this doc's own design was grounded in found this precedent
// directly, not assumed) -- IDUNA has no dependency on the PARENA repo itself.
func parenaRuntimeDir() string {
	if v := os.Getenv("NOCK_PARENA_RUNTIME_DIR"); v != "" {
		return v
	}
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "parena_runtime")
}

func gccBin() string {
	if v := os.Getenv("NOCK_GCC_BIN"); v != "" {
		return v
	}
	return "gcc"
}

// compileDoorScript runs the real, two-real-step compile (parena build -> gcc -shared) and
// returns the compiled .so's own real bytes. Every subprocess runs with an explicit timeout and
// a minimal environment, same defense-in-depth posture renderProcTexture's own runWithTimeout
// already established for the Java-target pipeline.
func compileDoorScript(prnSource string) ([]byte, error) {
	if err := validateDoorScriptSource(prnSource); err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "nock-doorscript-*")
	if err != nil {
		return nil, fmt.Errorf("nock: create scratch dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prnPath := filepath.Join(tmpDir, "doorscript.prn")
	lines := strings.Split(prnSource, "\n")
	replacedModule := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "(module ") {
			lines[i] = "(module doorscript)"
			replacedModule = true
			break
		}
	}
	forcedSrc := strings.Join(lines, "\n")
	if !replacedModule {
		forcedSrc = "(module doorscript)\n" + forcedSrc
	}
	if err := os.WriteFile(prnPath, []byte(forcedSrc), 0o644); err != nil {
		return nil, fmt.Errorf("nock: write source: %w", err)
	}

	cPath := filepath.Join(tmpDir, "doorscript.c")
	if err := runWithTimeout(doorScriptCompileTimeout, tmpDir, parenaBin(), "build", prnPath, "-o", cPath); err != nil {
		return nil, fmt.Errorf("nock: parena build failed: %w", err)
	}

	rtDir := parenaRuntimeDir()
	rtC := filepath.Join(rtDir, "parena_runtime.c")
	rtH := filepath.Join(rtDir, "parena_runtime.h")
	if _, err := os.Stat(rtC); err != nil {
		return nil, fmt.Errorf("nock: parena_runtime.c not found at %s: %w", rtC, err)
	}
	// gcc needs parena_runtime.h on its include path -- the generated .c does
	// #include "parena_runtime.h" (a relative include), so copy it alongside doorscript.c in the
	// scratch dir rather than passing -I (keeps this identical in spirit to how a real `gbtool`-
	// style local build would lay files out).
	rtHBytes, err := os.ReadFile(rtH)
	if err != nil {
		return nil, fmt.Errorf("nock: read parena_runtime.h: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "parena_runtime.h"), rtHBytes, 0o644); err != nil {
		return nil, fmt.Errorf("nock: stage parena_runtime.h: %w", err)
	}

	soPath := filepath.Join(tmpDir, "doorscript.so")
	if err := runWithTimeout(doorScriptCompileTimeout, tmpDir, gccBin(), "-shared", "-fPIC", "-o", soPath, cPath, rtC, "-lm"); err != nil {
		return nil, fmt.Errorf("nock: gcc failed: %w", err)
	}

	return os.ReadFile(soPath)
}
