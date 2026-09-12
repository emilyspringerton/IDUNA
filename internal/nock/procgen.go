package nock

// procgen.go — procedural texture generation from real, LLM-authorable PARENA source (founder
// real-time, 2026-09-12: "can we build that into nock tools... like an api for generating
// procedurally generated textures? like written in parena... if you had an llm and you gave it
// the api and told it you need a texture for X it could just one shot something... you can save
// the texture as a file and also as the source code (think GENERA OS)").
//
// # Why the Java target, not C
//
// The founder's own next real-time message ("that sounds unsafe... maybe it needs to be done on
// the frontend") was right to worry. Checked directly, not assumed: PARENA's C emitter's
// `#target`/`inline-c` escape hatch is completely unrestricted -- any `.prn` file, LLM-authored
// or not, can embed raw C that src/emit.c's own comment says is "trusted verbatim as real C." An
// LLM-generated "texture" program compiled to C is a genuine, unsandboxed arbitrary-code-
// execution vector. "Do it on the frontend" doesn't actually solve this either -- a browser can't
// run compiled native code at all; the real safe version of that idea is a WASM target, which
// PARENA doesn't have yet.
//
// The real fix (founder's own follow-up suggestion, confirmed correct by reading the compiler
// source): compile to PARENA's Java target instead. Checked directly: src/emit_java.c has zero
// handling for `#target`/inline-anything -- the escape hatch simply isn't wired up for this
// target, and this target's own real, current construct support is narrow enough (scalar F64
// math, `if`/`not`, and the same `math/*` primitives real-lowered straight to `java.lang.Math.*`
// -- checked directly: MATH_PRIM_TABLE in emit_java.c, real for cos/sqrt/floor/log/random-f64/pi,
// unlike the C target where those same functions are still honest `0.0` placeholders) that a
// compiled texture program is provably a pure function: no defstruct/loop/match/import support
// on this target means it cannot open a file, make a network call, or spawn a process, because
// the language surface available here has no way to express any of those. Running that inside a
// real JVM (memory-safe, bytecode-verified, no raw pointers) with a capped heap and a hard
// wall-clock timeout is the same real mitigation class actual run-untrusted-code services
// (competitive-programming judges, CI runners for fork PRs) already use for exactly this shape of
// problem -- not a novel invention. `internal/nock`'s own trusted `mainJavaHarnessSrc` below (not
// LLM-authored, checked-in, reviewed) is the ONLY code in this pipeline with real file I/O
// capability -- the generated class is called from it, never the reverse.
//
// # The real, current contract an LLM (or a human) writes against
//
//	(module gentexture)
//	(import math)
//	(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
//	(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
//	(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
//
// x/y are the pixel's own coordinates, w/h the canvas size; each function returns roughly 0.0-1.0
// (clamped by the harness either way). No `let`, `loop`, `match`, `defstruct`, or any `import`
// besides `math` -- this target's own real, current construct support doesn't have the first
// four at all (checked directly against emit_java.c), and `import` is rejected outright by
// validateProcTextureSource below as a second, explicit layer of defense on top of that, not
// reliance on the emitter alone.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxProcTextureSourceBytes = 20 * 1024
	procGenCompileTimeout     = 20 * time.Second
	procGenRunTimeout         = 10 * time.Second
	procGenMaxHeap            = "128m"
)

// knownJDKBinDir is this box's own real, known full-JDK location (EINHORN_SURVIVAL/jdk25 -- the
// same real JDK SPIDERBEETLE's own build already used). Real, found-live reason this can't be
// resolved independently per-binary the way parenaBin's own single-binary lookup works: the
// system `openjdk-21-jre-headless` package has a real `java` on PATH but genuinely no `javac` at
// all -- looking up "javac" and "java" separately (each falling back to PATH first) picks javac
// from this known JDK 25 install but java from the unrelated system JRE 21, and running a class
// file compiled by one JDK's javac under a materially older JVM is a real, confirmed
// UnsupportedClassVersionError, not a hypothetical mismatch. javacBin/javaBin below always
// resolve as a pair from the same directory instead.
const knownJDKBinDir = "/home/fatbaby/EINHORN_SURVIVAL/jdk25/bin"

// jdkBinDir resolves the one real directory both javacBin and javaBin are derived from: an env
// override, else the directory containing a real PATH-resolved `javac` (so a normal deployment
// with a real JDK on PATH never touches the fallback below), else knownJDKBinDir.
func jdkBinDir() string {
	if v := os.Getenv("NOCK_JDK_BIN_DIR"); v != "" {
		return v
	}
	if p, err := exec.LookPath("javac"); err == nil {
		return filepath.Dir(p)
	}
	return knownJDKBinDir
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
	return "parena" // let exec.Command's own "not found" error surface naturally
}

func javacBin() string { return filepath.Join(jdkBinDir(), "javac") }
func javaBin() string  { return filepath.Join(jdkBinDir(), "java") }

// validateProcTextureSource is the first, static layer of defense: reject anything that could
// even attempt the C-target-shaped attack (there is no target selection here at all -- this
// pipeline always compiles to .java -- but rejecting the literal token means a future refactor
// that accidentally lets the target become selectable can't silently reopen the hole), and
// reject any import besides `math`, so a generated program can't even name an io/net/shell/sdl2
// module, regardless of what that target does or doesn't implement for it.
func validateProcTextureSource(src string) error {
	if len(src) == 0 {
		return fmt.Errorf("nock: procedural texture source is empty")
	}
	if len(src) > maxProcTextureSourceBytes {
		return fmt.Errorf("nock: procedural texture source too large (%d bytes, max %d)", len(src), maxProcTextureSourceBytes)
	}
	if strings.Contains(src, "#target") {
		return fmt.Errorf("nock: procedural texture source may not use #target")
	}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "(import ") && trimmed != "(import math)" {
			return fmt.Errorf("nock: procedural texture source may only import 'math', found: %s", trimmed)
		}
	}
	for _, fn := range []string{"pixel-r", "pixel-g", "pixel-b"} {
		if !strings.Contains(src, "(defn "+fn+" ") {
			return fmt.Errorf("nock: procedural texture source must define (defn %s ...)", fn)
		}
	}
	return nil
}

// mainJavaHarnessSrc is real, trusted, checked-in Go-embedded Java source -- never generated,
// never touched by an LLM. It is the only piece of this pipeline with real file I/O: it calls
// the untrusted GenTexture class once per pixel and writes a plain PPM (P6) file, which
// imagemagick.go's own existing `convert` wrapper turns into a real PNG the rest of `nock`
// already knows how to treat as a layer.
const mainJavaHarnessSrc = `import java.io.FileOutputStream;
import java.io.IOException;

public final class Main {
    public static void main(String[] args) throws IOException {
        int w = Integer.parseInt(args[0]);
        int h = Integer.parseInt(args[1]);
        String outPath = args[2];
        byte[] pixels = new byte[w * h * 3];
        int idx = 0;
        for (int y = 0; y < h; y++) {
            for (int x = 0; x < w; x++) {
                double r = clamp(GenTexture.pixelR(x, y, w, h));
                double g = clamp(GenTexture.pixelG(x, y, w, h));
                double b = clamp(GenTexture.pixelB(x, y, w, h));
                pixels[idx++] = (byte) Math.round(r * 255.0);
                pixels[idx++] = (byte) Math.round(g * 255.0);
                pixels[idx++] = (byte) Math.round(b * 255.0);
            }
        }
        try (FileOutputStream fos = new FileOutputStream(outPath)) {
            fos.write(("P6\n" + w + " " + h + "\n255\n").getBytes("US-ASCII"));
            fos.write(pixels);
        }
    }

    private static double clamp(double v) {
        if (Double.isNaN(v)) return 0.0;
        if (v < 0.0) return 0.0;
        if (v > 1.0) return 1.0;
        return v;
    }
}
`

// renderProcTexture compiles prnSource (the module must be named "gentexture", enforced by
// always writing it to gentexture.prn regardless of what the caller's own source declares) and
// runs it in a fresh scratch directory, producing a real PNG at pngOut. Every subprocess runs
// with an explicit timeout and a stripped, minimal environment (no inherited secrets/env vars
// beyond PATH) -- real, defense-in-depth on top of the Java target's own structural safety
// argued in this file's own header comment, not a substitute for it.
func renderProcTexture(prnSource string, width, height int, pngOut string) error {
	if err := validateProcTextureSource(prnSource); err != nil {
		return err
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("nock: width/height must be positive, got %dx%d", width, height)
	}

	tmpDir, err := os.MkdirTemp("", "nock-procgen-*")
	if err != nil {
		return fmt.Errorf("nock: create scratch dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prnPath := filepath.Join(tmpDir, "gentexture.prn")
	// The module name is always forced to "gentexture" here, independent of whatever the source
	// text itself declares -- the compiled class name comes from the -o path below, not the
	// module declaration, but keeping the two in visible agreement avoids real confusion reading
	// a failed build's own error output.
	lines := strings.Split(prnSource, "\n")
	replacedModule := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "(module ") {
			lines[i] = "(module gentexture)"
			replacedModule = true
			break
		}
	}
	forcedSrc := strings.Join(lines, "\n")
	if !replacedModule {
		forcedSrc = "(module gentexture)\n" + forcedSrc
	}
	if err := os.WriteFile(prnPath, []byte(forcedSrc), 0o644); err != nil {
		return fmt.Errorf("nock: write source: %w", err)
	}

	javaPath := filepath.Join(tmpDir, "GenTexture.java")
	if err := runWithTimeout(procGenCompileTimeout, tmpDir, parenaBin(), "build", prnPath, "-o", javaPath); err != nil {
		return fmt.Errorf("nock: parena build failed: %w", err)
	}

	mainPath := filepath.Join(tmpDir, "Main.java")
	if err := os.WriteFile(mainPath, []byte(mainJavaHarnessSrc), 0o644); err != nil {
		return fmt.Errorf("nock: write harness: %w", err)
	}
	if err := runWithTimeout(procGenCompileTimeout, tmpDir, javacBin(), javaPath, mainPath); err != nil {
		return fmt.Errorf("nock: javac failed: %w", err)
	}

	ppmPath := filepath.Join(tmpDir, "out.ppm")
	runArgs := []string{"-Xmx" + procGenMaxHeap, "-cp", tmpDir, "Main", strconv.Itoa(width), strconv.Itoa(height), ppmPath}
	if err := runWithTimeout(procGenRunTimeout, tmpDir, javaBin(), runArgs...); err != nil {
		return fmt.Errorf("nock: java execution failed: %w", err)
	}

	return runConvert(ppmPath, pngOut)
}

// renderProcTextureToBytes is renderProcTexture, but returns the rendered PNG's own real bytes
// instead of writing to a caller-given path -- what TextureStore.RegenerateTexture and
// GenerateProceduralTextureSource's own callers need, since a Texture row keeps its PNG as a
// real BLOB in SQLite, not a file on disk.
func renderProcTextureToBytes(prnSource string, width, height int) ([]byte, error) {
	tmp, err := os.CreateTemp("", "nock-procgen-out-*.png")
	if err != nil {
		return nil, fmt.Errorf("nock: create temp output file: %w", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	if err := renderProcTexture(prnSource, width, height, tmp.Name()); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp.Name())
}

// runWithTimeout runs bin with args, cwd set to dir, a hard context timeout, and a minimal
// environment (PATH only -- no inherited credentials, tokens, or other env vars a compiled
// texture program has no legitimate reason to see even though the Java target itself already
// can't express reading them).
func runWithTimeout(timeout time.Duration, dir, bin string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s: %s", timeout, string(out))
		}
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
