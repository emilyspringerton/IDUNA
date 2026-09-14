package nock

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// requireConvert skips a test if ImageMagick isn't actually installed on the box running it --
// real, not mocked, since a mocked ImageMagick would prove nothing about whether the real
// compositing recipes in imagemagick.go actually work.
func requireConvert(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(convertBin); err != nil {
		t.Skip("ImageMagick 'convert' not installed, skipping real image test")
	}
}

// makeSolidPNG writes a real, solid-color WxH PNG to path, for use as test fixture input.
func makeSolidPNG(t *testing.T, path, hexColor string, w, h int) {
	t.Helper()
	size := fmt.Sprintf("%dx%d", w, h)
	if err := runConvert("-size", size, "xc:"+hexColor, path); err != nil {
		t.Fatalf("makeSolidPNG: %v", err)
	}
}

func TestCreateProjectAndAddLayer(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, err := NewService(dir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	p, err := svc.CreateProject("mytex", 64, 32)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Width != 64 || p.Height != 32 {
		t.Errorf("canvas size: got %dx%d, want 64x32", p.Width, p.Height)
	}

	src := filepath.Join(dir, "src.png")
	makeSolidPNG(t, src, "#336699", 128, 128) // deliberately different size, to prove fit-to-canvas

	p, err = svc.AddLayer("mytex", "base", src)
	if err != nil {
		t.Fatalf("AddLayer: %v", err)
	}
	if len(p.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(p.Layers))
	}
	l := p.Layers[0]
	if l.Opacity != 100 || !l.Visible {
		t.Errorf("new layer defaults: got opacity=%d visible=%v, want 100/true", l.Opacity, l.Visible)
	}

	absLayer := filepath.Join(dir, "mytex", l.File)
	w, h, err := imDimensions(absLayer)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	if w != 64 || h != 32 {
		t.Errorf("layer should be fit to canvas size: got %dx%d, want 64x32", w, h)
	}
}

func TestDuplicateLayerNameRejected(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 32, 32)
	src := filepath.Join(dir, "src.png")
	makeSolidPNG(t, src, "red", 32, 32)
	if _, err := svc.AddLayer("p1", "base", src); err != nil {
		t.Fatalf("AddLayer: %v", err)
	}
	if _, err := svc.AddLayer("p1", "base", src); err == nil {
		t.Error("expected error adding duplicate layer name")
	}
}

func TestOpacityAndVisibilityAffectExport(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)

	redSrc := filepath.Join(dir, "red.png")
	makeSolidPNG(t, redSrc, "red", 16, 16)
	blueSrc := filepath.Join(dir, "blue.png")
	makeSolidPNG(t, blueSrc, "blue", 16, 16)

	if _, err := svc.AddLayer("p1", "bottom", redSrc); err != nil {
		t.Fatalf("AddLayer bottom: %v", err)
	}
	if _, err := svc.AddLayer("p1", "top", blueSrc); err != nil {
		t.Fatalf("AddLayer top: %v", err)
	}

	out := filepath.Join(dir, "out1.png")
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export: %v", err)
	}
	rgb := samplePixel(t, out)
	if !isCloseTo(rgb, [3]int{0, 0, 255}, 20) {
		t.Errorf("fully-opaque top layer should read as blue, got %v", rgb)
	}

	// Hide the top layer -- exported image should now read as the bottom (red) layer.
	if _, err := svc.SetVisible("p1", "top", false); err != nil {
		t.Fatalf("SetVisible: %v", err)
	}
	out2 := filepath.Join(dir, "out2.png")
	if err := svc.Export("p1", out2, ""); err != nil {
		t.Fatalf("Export after hide: %v", err)
	}
	rgb2 := samplePixel(t, out2)
	if !isCloseTo(rgb2, [3]int{255, 0, 0}, 20) {
		t.Errorf("hidden top layer should reveal red bottom layer, got %v", rgb2)
	}
}

func TestMoveLayerReordersStack(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 8, 8)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "black", 8, 8)
	svc.AddLayer("p1", "a", src)
	svc.AddLayer("p1", "b", src)
	svc.AddLayer("p1", "c", src)

	p, err := svc.MoveLayer("p1", "a", 2)
	if err != nil {
		t.Fatalf("MoveLayer: %v", err)
	}
	got := []string{p.Layers[0].Name, p.Layers[1].Name, p.Layers[2].Name}
	want := []string{"b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("layer order after move: got %v, want %v", got, want)
			break
		}
	}
}

func TestGradientLayerAndExportToJPEG(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 32, 32)
	if _, err := svc.AddGradientLayer("p1", "sky", "#000033", "#3399ff", "vertical"); err != nil {
		t.Fatalf("AddGradientLayer: %v", err)
	}
	out := filepath.Join(dir, "out.jpg")
	if err := svc.Export("p1", out, "black"); err != nil {
		t.Fatalf("Export to jpg: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected export output file to exist: %v", err)
	}
	w, h, err := imDimensions(out)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	if w != 32 || h != 32 {
		t.Errorf("exported jpeg size: got %dx%d, want 32x32", w, h)
	}
}

func TestMaskHidesPartOfLayer(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)

	redSrc := filepath.Join(dir, "red.png")
	makeSolidPNG(t, redSrc, "red", 16, 16)
	blueSrc := filepath.Join(dir, "blue.png")
	makeSolidPNG(t, blueSrc, "blue", 16, 16)
	svc.AddLayer("p1", "bottom", redSrc)
	svc.AddLayer("p1", "top", blueSrc)

	// A fully black mask on the top layer should hide it entirely, revealing red underneath.
	maskSrc := filepath.Join(dir, "mask.png")
	makeSolidPNG(t, maskSrc, "black", 16, 16)
	if _, err := svc.SetMask("p1", "top", maskSrc); err != nil {
		t.Fatalf("SetMask: %v", err)
	}

	out := filepath.Join(dir, "out.png")
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export: %v", err)
	}
	rgb := samplePixel(t, out)
	if !isCloseTo(rgb, [3]int{255, 0, 0}, 20) {
		t.Errorf("fully-black mask should hide top layer entirely, got %v", rgb)
	}
}

// TestAdjustHueSaturationIsNonDestructive -- S416-04, "a real adjustment-layer concept instead of
// baking hue/saturation/sharpen destructively into the stored file." Real, end-to-end
// verification of the NEW contract (v0's old contract -- the stored file itself changes --
// verified the opposite on purpose; see this test's own git history for that prior version): the
// layer's own stored PNG never changes, the EXPORTED output reflects the current adjustment, and
// re-tuning to a different value always starts fresh from the same real original (no cumulative
// drift from repeated adjustment, the real problem non-destructive editing exists to solve).
func TestAdjustHueSaturationIsNonDestructive(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 8, 8)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "gray", 8, 8)
	svc.AddLayer("p1", "a", src)

	storedPath := filepath.Join(dir, "p1", "layers", "a.png")
	beforeStored := samplePixel(t, storedPath)

	if _, err := svc.AdjustHueSaturation("p1", "a", 20, 100, 100); err != nil {
		t.Fatalf("AdjustHueSaturation: %v", err)
	}

	// The STORED layer file must be untouched -- that's the whole real point of this being
	// non-destructive now.
	afterStored := samplePixel(t, storedPath)
	if !isCloseTo(afterStored, beforeStored, 2) {
		t.Errorf("stored layer file changed after AdjustHueSaturation (should be non-destructive): before=%v after=%v", beforeStored, afterStored)
	}

	// The EXPORTED output, on the other hand, must actually reflect the adjustment.
	out := filepath.Join(dir, "out1.png")
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export: %v", err)
	}
	exported := samplePixel(t, out)
	if isCloseTo(exported, beforeStored, 5) {
		t.Errorf("expected brightness=20 to visibly darken the EXPORTED output, got original=%v exported=%v", beforeStored, exported)
	}

	// Re-tuning back to brightness=100 (unchanged) must start fresh from the real original --
	// not compound on top of the previous darkened export, the real "no cumulative quality loss"
	// property destructive baking doesn't have.
	if _, err := svc.AdjustHueSaturation("p1", "a", 100, 100, 100); err != nil {
		t.Fatalf("AdjustHueSaturation (reset): %v", err)
	}
	out2 := filepath.Join(dir, "out2.png")
	if err := svc.Export("p1", out2, ""); err != nil {
		t.Fatalf("Export (reset): %v", err)
	}
	resetExported := samplePixel(t, out2)
	if !isCloseTo(resetExported, beforeStored, 5) {
		t.Errorf("expected brightness=100 (reset) to match the real original: original=%v got=%v", beforeStored, resetExported)
	}
}

// TestSharpenIsNonDestructive -- same real S416-04 contract TestAdjustHueSaturationIsNonDestructive
// verifies for hue/saturation: Sharpen must never touch the layer's own stored file.
func TestSharpenIsNonDestructive(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "gray", 16, 16)
	svc.AddLayer("p1", "a", src)

	storedPath := filepath.Join(dir, "p1", "layers", "a.png")
	beforeStored := samplePixel(t, storedPath)

	if _, err := svc.Sharpen("p1", "a", 2, 1, 3); err != nil {
		t.Fatalf("Sharpen: %v", err)
	}
	afterStored := samplePixel(t, storedPath)
	if !isCloseTo(afterStored, beforeStored, 2) {
		t.Errorf("stored layer file changed after Sharpen (should be non-destructive): before=%v after=%v", beforeStored, afterStored)
	}

	out := filepath.Join(dir, "out.png")
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export: %v", err)
	}
}

func TestDeleteProjectRemovesIt(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 8, 8)
	names, err := svc.ListProjects()
	if err != nil || len(names) != 1 {
		t.Fatalf("ListProjects before delete: %v %v", names, err)
	}
	if err := svc.DeleteProject("p1"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	names, err = svc.ListProjects()
	if err != nil || len(names) != 0 {
		t.Fatalf("ListProjects after delete: got %v, want empty (err=%v)", names, err)
	}
}

func TestResizeCanvasResizesLayers(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "green", 16, 16)
	svc.AddLayer("p1", "a", src)

	p, err := svc.ResizeCanvas("p1", 32, 8)
	if err != nil {
		t.Fatalf("ResizeCanvas: %v", err)
	}
	if p.Width != 32 || p.Height != 8 {
		t.Errorf("manifest size after resize: got %dx%d, want 32x8", p.Width, p.Height)
	}
	abs := filepath.Join(dir, "p1", p.Layers[0].File)
	w, h, err := imDimensions(abs)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	if w != 32 || h != 8 {
		t.Errorf("layer size after ResizeCanvas: got %dx%d, want 32x8", w, h)
	}
}

// TestSetTransformPositionsScalesAndRotatesLayer -- S416-02, "the biggest real gap between NOCK
// v0 and an actual Photoshop-shaped tool -- every layer is forced full-canvas today." Real,
// end-to-end verification via actual pixel sampling (not just "the API call didn't error").
//
// AddLayer's own existing, unchanged v0 behavior fits every imported image to the FULL canvas
// size (imFitToCanvas) before it's ever stored -- a real, found-live fact this test's own first
// draft got wrong (assumed a small imported square would stay small on disk; it doesn't, it's
// resized to fill the canvas the moment it's imported, so a same-aspect-ratio 10x10 square on a
// 40x40 canvas becomes a full 40x40 solid-red layer file). This transform feature doesn't change
// that import-time behavior (a real, separate, riskier change -- imFitToCanvas's own fit-and-pad
// shape is also relied on by mask application's own same-size-assumption, not touched here) --
// it transforms whatever the layer file already is, same as a real image editor still lets you
// scale/move/rotate a layer that happens to already be canvas-sized.
func TestSetTransformPositionsScalesAndRotatesLayer(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 40, 40)

	base := filepath.Join(dir, "base.png")
	makeSolidPNG(t, base, "blue", 40, 40)
	svc.AddLayer("p1", "base", base)

	red := filepath.Join(dir, "red.png")
	makeSolidPNG(t, red, "red", 40, 40) // imports as a full 40x40 red layer (see doc comment above)
	svc.AddLayer("p1", "red", red)

	// Scale the (now 40x40) red layer down to 50% (20x20) at origin (0,0) -- it should now only
	// cover the top-left quadrant, leaving the rest of the canvas showing the blue base beneath.
	if _, err := svc.SetTransform("p1", "red", 0, 0, 50, 0); err != nil {
		t.Fatalf("SetTransform (scale): %v", err)
	}
	out := filepath.Join(dir, "out.png")
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export: %v", err)
	}
	insideShrunk := samplePixelAt(t, out, 5, 5)
	if insideShrunk[0] < 200 || insideShrunk[2] > 50 {
		t.Errorf("pixel at (5,5) (inside the shrunk 20x20 red square at origin) should be red: got RGB %v", insideShrunk)
	}
	outsideShrunk := samplePixelAt(t, out, 30, 30)
	if outsideShrunk[0] > 50 || outsideShrunk[2] < 200 {
		t.Errorf("pixel at (30,30) (outside the shrunk 20x20 square) should show the blue base beneath: got RGB %v", outsideShrunk)
	}

	// Now also move that same shrunk (20x20) layer to (15,15) -- (5,5) should revert to blue
	// (the square isn't there anymore) and (20,20) (inside the moved square) should be red.
	if _, err := svc.SetTransform("p1", "red", 15, 15, 50, 0); err != nil {
		t.Fatalf("SetTransform (scale+move): %v", err)
	}
	if err := svc.Export("p1", out, ""); err != nil {
		t.Fatalf("Export after move: %v", err)
	}
	nowBlue := samplePixelAt(t, out, 5, 5)
	if nowBlue[0] > 50 || nowBlue[2] < 200 {
		t.Errorf("pixel at (5,5) (the square moved away from origin) should now be blue: got RGB %v", nowBlue)
	}
	nowRed := samplePixelAt(t, out, 20, 20)
	if nowRed[0] < 200 || nowRed[2] > 50 {
		t.Errorf("pixel at (20,20) (inside the moved 20x20 square at 15,15) should be red: got RGB %v", nowRed)
	}
}

// samplePixelAt reads the RGB of one specific pixel via ImageMagick, the same real "no Go image
// decoding dependency" approach samplePixel below already established, just parameterized on
// position instead of always sampling dead center.
func samplePixelAt(t *testing.T, path string, x, y int) [3]int {
	t.Helper()
	crop := fmt.Sprintf("1x1+%d+%d", x, y)
	cmd := exec.Command(convertBin, path, "-crop", crop, "+repage", "-flatten", "txt:-")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("samplePixelAt: %v", err)
	}
	return parsePixelTxt(t, string(out))
}

// samplePixel reads the RGB of the exact center pixel of a PNG/JPEG via ImageMagick `convert
// ... txt:-`, for cheap real-output assertions without pulling in a Go image-decoding dependency.
func samplePixel(t *testing.T, path string) [3]int {
	t.Helper()
	w, h, err := imDimensions(path)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	cx, cy := w/2, h/2
	crop := fmt.Sprintf("1x1+%d+%d", cx, cy)
	// +repage after -crop drops the cropped region's retained "virtual canvas" page offset --
	// without it, -flatten re-expands the 1x1 crop back onto the ORIGINAL image's full page
	// size (positioned at cx,cy), silently sampling the wrong thing (a real footgun found live
	// writing this test, not a hypothetical one).
	cmd := exec.Command(convertBin, path, "-crop", crop, "+repage", "-flatten", "txt:-")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sample pixel: %v", err)
	}
	return parsePixelTxt(t, string(out))
}

// parsePixelTxt parses ImageMagick's own `txt:-` output for a single pixel, e.g.
// "0,0: (255,0,0) #FF0000 red", into an (r,g,b) tuple -- shared by samplePixel/samplePixelAt.
func parsePixelTxt(t *testing.T, s string) [3]int {
	t.Helper()
	start := strings.IndexByte(s, '(')
	end := strings.IndexByte(s, ')')
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("unexpected identify txt output: %q", s)
	}
	parts := strings.Split(s[start+1:end], ",")
	if len(parts) < 3 {
		t.Fatalf("expected at least 3 comma-separated channels in %q", s[start+1:end])
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		// Q16 ImageMagick builds print txt: channel values as floats (e.g. "254.016") even for
		// an 8-bit-range PNG -- ParseFloat handles both that and a plain integer.
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
		if err != nil {
			t.Fatalf("parse channel %q: %v", parts[i], err)
		}
		rgb[i] = int(v + 0.5)
	}
	return rgb
}

func isCloseTo(got, want [3]int, tol int) bool {
	for i := 0; i < 3; i++ {
		d := got[i] - want[i]
		if d < 0 {
			d = -d
		}
		if d > tol {
			return false
		}
	}
	return true
}
