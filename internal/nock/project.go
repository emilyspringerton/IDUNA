// Package nock implements NOCK — a from-scratch, ImageMagick-backed layered image editor
// (Photoshop-equivalent affordances: layers, layer masks, opacity, gradients, hue/saturation,
// sharpening, resize, PNG/JPG export). Built for SHANKPIT's own texture needs first (founder
// real-time, 2026-09-12: "we are gonna need to build our own tools to create the textures...
// lets build it on top of imagemagic for now and add cli affordances for everything in our app
// FIRST"), deliberately kept independent of GoblinFoxDragon/GFD so it stays a real, reusable
// engine tool rather than a GFD-only feature — GFD is a real, planned second consumer, not the
// first (founder: "this is for shankpit it will be used for GFD we build it for shankpit first
// to engineify it we need it to not be tooooo coupled to GFD").
//
// v0 scope (this pass): a real, working layer/composite engine callable from both a CLI
// (cmd/nock) and an HTTP JSON API (internal/http/handlers/nock.go), sharing this one package as
// the single source of truth — "same shape CLI and GUI," the founder's own framing. Real, honest,
// deliberately deferred: PARENA integration (macros, eventually replacing ImageMagick as the
// backend), a 3D modeler, a level editor, drag-and-drop layer reordering, arbitrary-angle
// gradients (vertical/horizontal only for now). See docs/NOCK_NORTHSTAR.md for the full phased
// plan and what's real vs. deferred.
package nock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Layer is one row of a Project's layer stack, bottom-to-top by index in Project.Layers.
type Layer struct {
	Name    string `json:"name"`
	File    string `json:"file"`           // path relative to the project dir, e.g. "layers/base.png"
	Opacity int    `json:"opacity"`        // 0-100
	Visible bool   `json:"visible"`
	Mask    string `json:"mask,omitempty"` // path relative to the project dir, grayscale PNG; "" = no mask
	// X/Y/Scale/Rotation (S416-02, "the biggest real gap between NOCK v0 and an actual
	// Photoshop-shaped tool -- every layer is forced full-canvas today"). A real, deliberate
	// backward-compatible shape: a layer that never sets these (every layer from before this
	// change, and the Go zero value for any new layer that genuinely doesn't need them) behaves
	// EXACTLY as before -- Scale's own zero value is special-cased to mean 100 (unchanged) rather
	// than "0% size" everywhere it's actually used (effectiveScale below), so an old manifest.json
	// on disk with no x/y/scale/rotation keys at all still composites pixel-identical to today.
	X        int     `json:"x,omitempty"`        // pixels from canvas left, top-left of the (possibly scaled/rotated) layer
	Y        int     `json:"y,omitempty"`        // pixels from canvas top
	Scale    float64 `json:"scale,omitempty"`    // percent, 100 = unchanged; 0 (omitted/legacy) also means unchanged, see effectiveScale
	Rotation float64 `json:"rotation,omitempty"` // degrees, clockwise, 0 = unchanged
	// Brightness/Saturation/Hue/SharpenRadius/SharpenSigma/SharpenAmount (S416-04, "a real
	// adjustment-layer concept instead of baking hue/saturation/sharpen destructively into the
	// stored file"). Real, deliberate shape change from v0: AdjustHueSaturation/Sharpen used to
	// overwrite the layer's own stored PNG in place, permanently -- re-tuning meant starting from
	// an already-altered image (real, cumulative quality loss on repeated adjustment, and no way
	// back to the original once applied). These fields now hold the adjustment as real, editable
	// METADATA instead; Export applies them to a disposable working COPY at composite time, every
	// time, so the stored layer file itself never changes and re-tuning always starts from the
	// same real original. Same "0/omitted means unchanged" convention Scale above already
	// established: Brightness/Saturation/Hue default to 100 (imModulate's own real "unchanged"
	// value) via EffectiveBrightness/EffectiveSaturation/EffectiveHue, not their Go zero value.
	Brightness     int     `json:"brightness,omitempty"`
	Saturation     int     `json:"saturation,omitempty"`
	Hue            int     `json:"hue,omitempty"`
	SharpenRadius  float64 `json:"sharpen_radius,omitempty"`
	SharpenSigma   float64 `json:"sharpen_sigma,omitempty"`
	SharpenAmount  float64 `json:"sharpen_amount,omitempty"`
	// Source is the real PARENA program that generated this layer's own File, when the layer
	// was created by AddProceduralLayer rather than imported from an image -- "think GENERA OS,"
	// the founder's own framing for keeping a generated asset's real source alongside its
	// rendered output rather than only the rendered pixels. "" for an ordinary imported/gradient
	// layer (no generating program to keep).
	Source string `json:"source,omitempty"` // path relative to the project dir, a .prn file
}

// EffectiveScale is l.Scale with the real "0/omitted means unchanged" convention applied -- see
// the Layer.Scale field's own doc comment for why a bare zero value can't mean "0% size" here.
func (l Layer) EffectiveScale() float64 {
	if l.Scale <= 0 {
		return 100
	}
	return l.Scale
}

// EffectiveBrightness/EffectiveSaturation/EffectiveHue are l.Brightness/Saturation/Hue with the
// same real "0/omitted means unchanged" convention EffectiveScale already established -- a fresh
// layer that's never had hue/saturation adjusted composites at real, unchanged 100 for each,
// matching imModulate's own real "100 = no-op" semantics, not a bare zero value's literal "all
// channels crushed to black" meaning.
func (l Layer) EffectiveBrightness() int {
	if l.Brightness <= 0 {
		return 100
	}
	return l.Brightness
}

func (l Layer) EffectiveSaturation() int {
	if l.Saturation <= 0 {
		return 100
	}
	return l.Saturation
}

func (l Layer) EffectiveHue() int {
	if l.Hue <= 0 {
		return 100
	}
	return l.Hue
}

// HasSharpen answers whether this layer has a real, non-no-op sharpen adjustment set -- radius=0
// is imSharpen's own real no-op ("0 = no-op" per its own doc comment), so Export can skip the
// extra convert invocation entirely for the very common "never sharpened" case.
func (l Layer) HasSharpen() bool {
	return l.SharpenRadius > 0
}

// Project is one NOCK document: a fixed canvas size and an ordered layer stack.
type Project struct {
	Name   string  `json:"name"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Layers []Layer `json:"layers"`
}

// Service is the real, stateful entry point every caller (CLI, HTTP handler) goes through.
// DataDir holds one subdirectory per project. Safe for concurrent use — a single mutex
// serializes manifest read-modify-write cycles, matching GfdItemsHandler's own real precedent
// (a hand-edited-JSON-file store doesn't need anything heavier at NOCK's own real, current scale).
type Service struct {
	DataDir string
	mu      sync.Mutex
}

// NewService returns a Service rooted at dataDir, creating it if it doesn't exist.
func NewService(dataDir string) (*Service, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("nock: create data dir: %w", err)
	}
	return &Service{DataDir: dataDir}, nil
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ValidateName rejects anything that isn't a plain, filesystem-safe identifier — both project
// names and layer names go through this, since both become real path components on disk.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("nock: invalid name %q (must match %s)", name, validName.String())
	}
	return nil
}

func (s *Service) projectDir(name string) string {
	return filepath.Join(s.DataDir, name)
}

func (s *Service) manifestPath(name string) string {
	return filepath.Join(s.projectDir(name), "manifest.json")
}

func (s *Service) layersDir(name string) string {
	return filepath.Join(s.projectDir(name), "layers")
}

// loadManifest reads a project's manifest.json. Caller must hold s.mu.
func (s *Service) loadManifest(name string) (*Project, error) {
	data, err := os.ReadFile(s.manifestPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("nock: project %q not found", name)
		}
		return nil, fmt.Errorf("nock: read manifest: %w", err)
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("nock: parse manifest: %w", err)
	}
	return &p, nil
}

// saveManifest writes a project's manifest.json atomically (temp file + rename) so a crash
// mid-write never leaves a half-written, unparseable manifest behind. Caller must hold s.mu.
func (s *Service) saveManifest(p *Project) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("nock: marshal manifest: %w", err)
	}
	final := s.manifestPath(p.Name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("nock: write manifest: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("nock: finalize manifest: %w", err)
	}
	return nil
}

// CreateProject makes a new, empty project with the given canvas size.
func (s *Service) CreateProject(name string, width, height int) (*Project, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("nock: width/height must be positive, got %dx%d", width, height)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.projectDir(name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("nock: project %q already exists", name)
	}
	if err := os.MkdirAll(s.layersDir(name), 0o755); err != nil {
		return nil, fmt.Errorf("nock: create project dir: %w", err)
	}
	p := &Project{Name: name, Width: width, Height: height, Layers: []Layer{}}
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// GetProject returns the current manifest for name.
func (s *Service) GetProject(name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadManifest(name)
}

// ListProjects returns every project name under DataDir, alphabetically.
func (s *Service) ListProjects() ([]string, error) {
	entries, err := os.ReadDir(s.DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("nock: list projects: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.DataDir, e.Name(), "manifest.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// DeleteProject permanently removes a project and every layer/mask file it owns.
func (s *Service) DeleteProject(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.projectDir(name)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		return fmt.Errorf("nock: project %q not found", name)
	}
	return os.RemoveAll(dir)
}

func (s *Service) findLayer(p *Project, name string) (int, error) {
	for i := range p.Layers {
		if p.Layers[i].Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("nock: layer %q not found in project %q", name, p.Name)
}

// layerAbsPath resolves a layer's relative File/Mask path to an absolute one under the
// project's own directory. Rejects a value containing ".." so a manifest can never be crafted
// to read/write outside the project directory.
func (s *Service) layerAbsPath(projectName, rel string) (string, error) {
	if strings.Contains(rel, "..") {
		return "", fmt.Errorf("nock: invalid path %q", rel)
	}
	return filepath.Join(s.projectDir(projectName), rel), nil
}
