package nock

// municipal_materials_test.go -- real coverage for the six level materials SHANKPIT's municipal
// district is built out of (2026-11-05): tile, plaster, marble, wood_oak, wood_pine, wood_mahogany.
//
// The district's levels carry only NAME, density and shader numbers -- the look is PARENA source,
// stored in this library under `shankpit-material-<material name>` by cmd/nock_gen_textures. So the
// things that can silently go wrong are all here, not in the level file:
//
//   1. the source stops validating (a bad edit to a .prn means the loader refuses to store it at all);
//   2. the surface renders FLAT -- marble with no veins is indistinguishable from a shader bug, and a
//      wall that is one solid colour reads as untextured in the editor and in game;
//   3. the three wood SPECIES render the same board. They carry three different densities (720 / 580 /
//      460 kg/m^3) and are meant to look like three different trees, so identical pixels would mean the
//      generator had collapsed them.
//
// Skips (rather than fails) when the PARENA checkout or its Java toolchain is missing, the same way
// bullet_hole_guns_test.go does; PARENA_DIR overrides the default checkout path.

import (
	"bytes"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var municipalMaterials = []string{"tile", "plaster", "marble", "wood_oak", "wood_pine", "wood_mahogany"}

// variedDivisor is how many neighbour-pairs a material must be able to differ across, as a fraction
// of the canvas (size*(size-1)/n). Tile, plaster and marble change from pixel to pixel -- grout, damp,
// stone flecks -- so they are held to one in eight.
//
// The woods are deliberately NOT held to that: a board's tone changes across the width of a ring, so
// most adjacent pixels are equal by design (oak 7238 of 65280, mahogany 3782 -- measured, not
// assumed, and it is what wood grain looks like). Holding a smooth gradient to a per-pixel-variance
// threshold would only force noise into the grain. What catches a broken wood render is the tonal
// RANGE below, plus the three species having to render differently from each other.
var variedDivisor = map[string]int{
	"tile": 8, "plaster": 8, "marble": 8,
	"wood_oak": 32, "wood_pine": 32, "wood_mahogany": 32,
}

func municipalMaterialSource(material string) string {
	name := map[string]string{
		"tile":    "institutional_tile",
		"plaster": "courthouse_plaster",
		"marble":  "courthouse_marble",
	}[material]
	if name == "" {
		name = material // wood_oak / wood_pine / wood_mahogany are their own files
	}
	return filepath.Join("stdlib", "shankpit", "textures", name+".prn")
}

func TestMunicipalMaterials_RenderDistinctSurfaces(t *testing.T) {
	requireProcGenTools(t)
	// the pipeline writes a PPM and converts it with ImageMagick, so that binary is a prerequisite in
	// the same way parena and javac are -- a box without it should SKIP this test, not fail it on a
	// missing tool it was never about.
	if _, err := exec.LookPath("convert"); err != nil {
		t.Skip("ImageMagick (convert) not available, skipping real procedural-texture render")
	}
	dir := os.Getenv("PARENA_DIR")
	if dir == "" {
		dir = "/home/fatbaby/PARENA"
	}
	const size = 256
	rendered := map[string][]byte{}
	for _, material := range municipalMaterials {
		path := filepath.Join(dir, municipalMaterialSource(material))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("PARENA checkout not available (%v)", err)
		}
		if err := validateProcTextureSource(string(src)); err != nil {
			t.Fatalf("%s: validateProcTextureSource: %v", material, err)
		}
		pngBytes, err := renderProcTextureToBytes(string(src), size, size)
		if err != nil {
			t.Fatalf("%s: render: %v", material, err)
		}
		img, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			t.Fatalf("%s: decode: %v", material, err)
		}
		// "not flat": a real surface has both tonal range and a large share of pixels that differ from
		// their right-hand neighbour. A single value, or a handful of values, means the source is not
		// drawing anything meaningful.
		lo, hi, varied := 1<<16, 0, 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a>>8 != 255 {
					t.Fatalf("%s: pixel (%d,%d) is not opaque (a=%d)", material, x, y, a>>8)
				}
				lum := int((r>>8)*30+(g>>8)*59+(b>>8)*11) / 100
				if lum < lo {
					lo = lum
				}
				if lum > hi {
					hi = lum
				}
				if x > 0 {
					pr, pg, pb, _ := img.At(x-1, y).RGBA()
					plum := int((pr>>8)*30+(pg>>8)*59+(pb>>8)*11) / 100
					if lum != plum {
						varied++
					}
				}
			}
		}
		t.Logf("%s: range=%d..%d varied=%d/%d", material, lo, hi, varied, size*(size-1))
		if hi-lo < 20 {
			t.Errorf("%s: renders flat (luminance range %d..%d)", material, lo, hi)
		}
		if varied < size*(size-1)/variedDivisor[material] {
			t.Errorf("%s: only %d of %d pixels differ from their neighbour (needs 1/%d)",
				material, varied, size*(size-1), variedDivisor[material])
		}
		// Optional artifact dump: the assertions above are about the pixels, but "look at the material"
		// is a real request too, and the only way to answer it is to leave the PNG on disk. Off by
		// default so a normal `go test` run writes nothing.
		if outDir := os.Getenv("NOCK_MATERIAL_DUMP_DIR"); outDir != "" {
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatalf("create dump dir: %v", err)
			}
			out := filepath.Join(outDir, material+".png")
			if err := os.WriteFile(out, pngBytes, 0o644); err != nil {
				t.Fatalf("write %s: %v", out, err)
			}
			t.Logf("%s: wrote %s", material, out)
		}
		rendered[material] = pngBytes
	}
	for i, a := range municipalMaterials {
		for _, b := range municipalMaterials[i+1:] {
			if rendered[a] != nil && rendered[b] != nil && bytes.Equal(rendered[a], rendered[b]) {
				t.Errorf("%s and %s render byte-identical surfaces", a, b)
			}
		}
	}
}
