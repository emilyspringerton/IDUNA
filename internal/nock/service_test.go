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

func TestAdjustHueSaturationChangesPixels(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 8, 8)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "gray", 8, 8)
	svc.AddLayer("p1", "a", src)

	// Desaturating gray does nothing visible, so instead crank brightness way down and confirm
	// the stored layer file itself actually changed (destructive adjustment, per this method's
	// own doc comment).
	before := samplePixel(t, filepath.Join(dir, "p1", "layers", "a.png"))
	if _, err := svc.AdjustHueSaturation("p1", "a", 20, 100, 100); err != nil {
		t.Fatalf("AdjustHueSaturation: %v", err)
	}
	after := samplePixel(t, filepath.Join(dir, "p1", "layers", "a.png"))
	if isCloseTo(after, before, 5) {
		t.Errorf("expected brightness=20 to visibly darken the layer, got before=%v after=%v", before, after)
	}
}

func TestSharpenRunsWithoutError(t *testing.T) {
	requireConvert(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	src := filepath.Join(dir, "a.png")
	makeSolidPNG(t, src, "gray", 16, 16)
	svc.AddLayer("p1", "a", src)
	if _, err := svc.Sharpen("p1", "a", 0, 1, 1); err != nil {
		t.Fatalf("Sharpen: %v", err)
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
	// Output line looks like: "0,0: (255,0,0) #FF0000 red" -- parse the (r,g,b) tuple.
	s := string(out)
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
