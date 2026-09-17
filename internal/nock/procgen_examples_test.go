package nock

// procgen_examples_test.go — real regression coverage for internal/nock/examples/*.prn, the
// checked-in library of ready-to-paste procedural texture sources (see examples/README.md).
// go:embed keeps the test and the shipped example byte-identical -- no risk of the test quietly
// drifting from what a founder actually copies into NOCK's editor.

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"testing"
)

//go:embed examples/bullet_hole.prn
var bulletHoleExampleSource string

// TestExample_BulletHole_RealEndToEnd is the real regression test for the founder's own ask
// (2026-09-17): "can we write a parena script for a basic bullet hole (slightly asymetrical)...
// use sin or cos or something with a multiplier or modulator that let me generate multiple
// versions by tweaking a parameter." Confirms the shipped example still compiles, passes NOCK's
// own real validator, and renders end-to-end through the exact pipeline a founder-triggered
// generate/regenerate call would use.
func TestExample_BulletHole_RealEndToEnd(t *testing.T) {
	requireProcGenTools(t)
	if err := validateProcTextureSource(bulletHoleExampleSource); err != nil {
		t.Fatalf("validateProcTextureSource: %v", err)
	}
	dir := t.TempDir()
	out := dir + "/bullet_hole.png"
	if err := renderProcTexture(bulletHoleExampleSource, 128, 128, out); err != nil {
		t.Fatalf("renderProcTexture: %v", err)
	}
	w, h, err := imDimensions(out)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	if w != 128 || h != 128 {
		t.Errorf("rendered decal size: got %dx%d, want 128x128", w, h)
	}
}

// TestExample_BulletHole_AsymSeedProducesDifferentShapes is the real regression test for the
// tweakable-parameter requirement specifically: two different asym-seed values must render to
// genuinely different images, not just a cosmetic no-op.
func TestExample_BulletHole_AsymSeedProducesDifferentShapes(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()

	renderWithSeed := func(seed, outName string) string {
		src := replaceAsymSeed(t, bulletHoleExampleSource, seed)
		out := dir + "/" + outName
		if err := renderProcTexture(src, 64, 64, out); err != nil {
			t.Fatalf("renderProcTexture (seed=%s): %v", seed, err)
		}
		return out
	}

	outA := renderWithSeed("-0.9", "a.png")
	outB := renderWithSeed("0.9", "b.png")

	same, err := filesByteIdentical(outA, outB)
	if err != nil {
		t.Fatalf("comparing rendered outputs: %v", err)
	}
	if same {
		t.Error("expected different asym-seed values to render different images, got byte-identical output")
	}
}

// replaceAsymSeed swaps the example's own default asym-seed literal for a test-supplied value --
// a real, narrow, single-line source edit (the same real "edit one number, regenerate" workflow
// the example's own README documents), not a general template system.
func replaceAsymSeed(t *testing.T, src, seed string) string {
	t.Helper()
	const marker = "(defn asym-seed [] : F64 0.35)"
	if !strings.Contains(src, marker) {
		t.Fatalf("expected to find %q in the example source to substitute a test seed", marker)
	}
	return strings.Replace(src, marker, fmt.Sprintf("(defn asym-seed [] : F64 %s)", seed), 1)
}

func filesByteIdentical(a, b string) (bool, error) {
	ba, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ba, bb), nil
}
