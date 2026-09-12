package nock

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binAvailable reports whether path names an existing file OR resolves on PATH -- procGenBin's
// own fallback chain means a resolved bin string might be either shape.
func binAvailable(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	_, err := exec.LookPath(path)
	return err == nil
}

// requireProcGenTools skips a test if the real PARENA compiler or a real JDK (javac -- not just
// a JRE) isn't actually available on the box running it. Real, not mocked: a mocked toolchain
// would prove nothing about whether the real PARENA-to-Java-to-JVM pipeline this file drives
// actually works end to end.
func requireProcGenTools(t *testing.T) {
	t.Helper()
	if !binAvailable(parenaBin()) {
		t.Skip("parena compiler not available, skipping real procedural-texture test")
	}
	if !binAvailable(javacBin()) {
		t.Skip("javac not available, skipping real procedural-texture test")
	}
}

const validCheckerSource = `(module gentexture)
(import math)

(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (if (> (math/cos (* (/ x w) 40.0)) 0.0) 0.85 0.15))

(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (if (> (math/cos (* (/ y h) 40.0)) 0.0) 0.85 0.15))

(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (math/sqrt (/ x w)))
`

func TestValidateProcTextureSource_RejectsTargetEscape(t *testing.T) {
	src := `(module gentexture)
(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  #target {:c (inline-c "system(\"rm -rf /\")")})
(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
`
	if err := validateProcTextureSource(src); err == nil {
		t.Error("expected rejection of #target, got nil error")
	} else if !strings.Contains(err.Error(), "#target") {
		t.Errorf("expected error mentioning #target, got: %v", err)
	}
}

func TestValidateProcTextureSource_RejectsNonMathImport(t *testing.T) {
	src := `(module gentexture)
(import io)
(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
`
	if err := validateProcTextureSource(src); err == nil {
		t.Error("expected rejection of (import io), got nil error")
	} else if !strings.Contains(err.Error(), "import") {
		t.Errorf("expected error mentioning import, got: %v", err)
	}
}

func TestValidateProcTextureSource_RejectsMissingRequiredFunctions(t *testing.T) {
	src := `(module gentexture)
(import math)
(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 0.0)
`
	if err := validateProcTextureSource(src); err == nil {
		t.Error("expected rejection of missing pixel-g/pixel-b, got nil error")
	}
}

func TestValidateProcTextureSource_RejectsOversized(t *testing.T) {
	src := "(module gentexture)\n(import math)\n" + strings.Repeat(";; padding\n", 10000)
	if err := validateProcTextureSource(src); err == nil {
		t.Error("expected rejection of oversized source, got nil error")
	}
}

func TestValidateProcTextureSource_AcceptsValidSource(t *testing.T) {
	if err := validateProcTextureSource(validCheckerSource); err != nil {
		t.Errorf("expected valid source to pass validation, got: %v", err)
	}
}

func TestRenderProcTexture_RealEndToEnd(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.png")
	if err := renderProcTexture(validCheckerSource, 64, 64, out); err != nil {
		t.Fatalf("renderProcTexture: %v", err)
	}
	w, h, err := imDimensions(out)
	if err != nil {
		t.Fatalf("imDimensions: %v", err)
	}
	if w != 64 || h != 64 {
		t.Errorf("rendered texture size: got %dx%d, want 64x64", w, h)
	}
}

func TestRenderProcTexture_RejectsInvalidSourceBeforeCompiling(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.png")
	err := renderProcTexture(`(module gentexture) #target {:c (inline-c "evil()")}`, 16, 16, out)
	if err == nil {
		t.Fatal("expected rejection before ever invoking parena/javac")
	}
}

func TestServiceAddProceduralLayer(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 32, 32)

	p, err := svc.AddProceduralLayer("p1", "checker", validCheckerSource)
	if err != nil {
		t.Fatalf("AddProceduralLayer: %v", err)
	}
	if len(p.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(p.Layers))
	}
	l := p.Layers[0]
	if l.Source == "" {
		t.Error("expected Source to be set on a procedural layer")
	}

	// Both the rendered PNG and the saved source should be real files on disk.
	if _, err := os.Stat(filepath.Join(dir, "p1", l.File)); err != nil {
		t.Errorf("rendered layer file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p1", l.Source)); err != nil {
		t.Errorf("saved source file missing: %v", err)
	}

	gotSource, err := svc.GetProceduralSource("p1", "checker")
	if err != nil {
		t.Fatalf("GetProceduralSource: %v", err)
	}
	if !strings.Contains(gotSource, "pixel-r") {
		t.Errorf("expected saved source to round-trip, got: %s", gotSource)
	}
}

func TestServiceRegenerateProceduralLayer(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	svc.AddProceduralLayer("p1", "tex", validCheckerSource)

	edited := strings.Replace(validCheckerSource, "0.85", "0.99", 1)
	if _, err := svc.RegenerateProceduralLayer("p1", "tex", edited); err != nil {
		t.Fatalf("RegenerateProceduralLayer: %v", err)
	}
	gotSource, err := svc.GetProceduralSource("p1", "tex")
	if err != nil {
		t.Fatalf("GetProceduralSource: %v", err)
	}
	if !strings.Contains(gotSource, "0.99") {
		t.Errorf("expected regenerated source to be saved, got: %s", gotSource)
	}
}

func TestServiceRegenerateProceduralLayer_BadEditLeavesOriginalIntact(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	svc.AddProceduralLayer("p1", "tex", validCheckerSource)

	_, err := svc.RegenerateProceduralLayer("p1", "tex", `(module gentexture) #target {:c (inline-c "evil()")}`)
	if err == nil {
		t.Fatal("expected regeneration with invalid source to fail")
	}
	gotSource, err := svc.GetProceduralSource("p1", "tex")
	if err != nil {
		t.Fatalf("GetProceduralSource after failed regen: %v", err)
	}
	if !strings.Contains(gotSource, "pixel-r") || strings.Contains(gotSource, "evil") {
		t.Errorf("original source should survive a failed regenerate, got: %s", gotSource)
	}
}

func TestRemoveProceduralLayerCleansUpSourceFile(t *testing.T) {
	requireProcGenTools(t)
	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("p1", 16, 16)
	p, _ := svc.AddProceduralLayer("p1", "tex", validCheckerSource)
	sourcePath := filepath.Join(dir, "p1", p.Layers[0].Source)

	if _, err := svc.RemoveLayer("p1", "tex"); err != nil {
		t.Fatalf("RemoveLayer: %v", err)
	}
	if _, err := os.Stat(sourcePath); err == nil {
		t.Error("expected source file to be removed along with the layer")
	}
}
