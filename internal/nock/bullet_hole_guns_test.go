package nock

// bullet_hole_guns_test.go -- real regression coverage for SHANKPIT's per-gun bullet-hole decals
// (PARENA/stdlib/shankpit/textures/bullet_hole_<gun>.prn, founder real-time 2026-10-01: "shooting
// a wall causes a bullet hole, different per gun"). Each must pass NOCK's own validator, render
// through the real PARENA -> javac -> java pipeline, keep a pure-white background (the decal is
// drawn with a MULTIPLY blend, so white must be exactly white), have a dark core, and differ
// from every other gun's decal.

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

var bulletHoleGuns = []string{"magnum", "ar", "shotgun", "sniper"}

func TestBulletHoleGuns_RenderDistinctWhiteBackedDecals(t *testing.T) {
	requireProcGenTools(t)
	const size = 64
	rendered := map[string][]byte{}
	for _, gun := range bulletHoleGuns {
		path := "/home/fatbaby/PARENA/stdlib/shankpit/textures/bullet_hole_" + gun + ".prn"
		src, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("PARENA checkout not available (%v)", err)
		}
		if err := validateProcTextureSource(string(src)); err != nil {
			t.Fatalf("%s: validateProcTextureSource: %v", gun, err)
		}
		pngBytes, err := renderProcTextureToBytes(string(src), size, size)
		if err != nil {
			t.Fatalf("%s: render: %v", gun, err)
		}
		img, err := png.Decode(bytes.NewReader(pngBytes))
		if err != nil {
			t.Fatalf("%s: decode: %v", gun, err)
		}
		cr, cg, cb, _ := img.At(size/2, size/2).RGBA()
		if cr>>8 > 60 || cg>>8 > 60 || cb>>8 > 60 {
			t.Errorf("%s: center pixel not dark: %d,%d,%d", gun, cr>>8, cg>>8, cb>>8)
		}
		for _, p := range [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}} {
			r, g, b, _ := img.At(p[0], p[1]).RGBA()
			if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
				t.Errorf("%s: corner %v not pure white: %d,%d,%d", gun, p, r>>8, g>>8, b>>8)
			}
		}
		rendered[gun] = pngBytes
	}
	for i, a := range bulletHoleGuns {
		for _, b := range bulletHoleGuns[i+1:] {
			if rendered[a] != nil && rendered[b] != nil && bytes.Equal(rendered[a], rendered[b]) {
				t.Errorf("%s and %s decals are byte-identical", a, b)
			}
		}
	}
}
