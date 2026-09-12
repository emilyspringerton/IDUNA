package nock

import (
	"fmt"
	"os"
	"path/filepath"
)

// AddLayer imports imgPath as a new top layer named layerName, fit-and-padded to the project's
// canvas size (see imFitToCanvas's own doc comment for why every layer is always canvas-sized in
// v0). New layers start fully opaque and visible.
func (s *Service) AddLayer(projectName, layerName, imgPath string) (*Project, error) {
	if err := ValidateName(layerName); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	if _, err := s.findLayer(p, layerName); err == nil {
		return nil, fmt.Errorf("nock: layer %q already exists in project %q", layerName, projectName)
	}

	relFile := filepath.Join("layers", layerName+".png")
	absFile, err := s.layerAbsPath(projectName, relFile)
	if err != nil {
		return nil, err
	}
	if err := imFitToCanvas(imgPath, absFile, p.Width, p.Height); err != nil {
		return nil, err
	}

	p.Layers = append(p.Layers, Layer{Name: layerName, File: relFile, Opacity: 100, Visible: true})
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// AddGradientLayer creates a new top layer filled with a linear gradient (see imGradient's own
// doc comment for the real, current direction options).
func (s *Service) AddGradientLayer(projectName, layerName, fromHex, toHex, direction string) (*Project, error) {
	if err := ValidateName(layerName); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	if _, err := s.findLayer(p, layerName); err == nil {
		return nil, fmt.Errorf("nock: layer %q already exists in project %q", layerName, projectName)
	}

	relFile := filepath.Join("layers", layerName+".png")
	absFile, err := s.layerAbsPath(projectName, relFile)
	if err != nil {
		return nil, err
	}
	if err := imGradient(absFile, p.Width, p.Height, fromHex, toHex, direction); err != nil {
		return nil, err
	}

	p.Layers = append(p.Layers, Layer{Name: layerName, File: relFile, Opacity: 100, Visible: true})
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// AddProceduralLayer compiles and runs prnSource (see procgen.go's own header comment for the
// real contract and the real, checked reasoning it's compiled to Java, never C) and adds the
// result as a new top layer, keeping the generating source alongside the rendered PNG ("think
// GENERA OS" -- the founder's own framing) so it can be re-opened and tweaked later via
// RegenerateProceduralLayer.
func (s *Service) AddProceduralLayer(projectName, layerName, prnSource string) (*Project, error) {
	if err := ValidateName(layerName); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	if _, err := s.findLayer(p, layerName); err == nil {
		return nil, fmt.Errorf("nock: layer %q already exists in project %q", layerName, projectName)
	}

	relFile := filepath.Join("layers", layerName+".png")
	absFile, err := s.layerAbsPath(projectName, relFile)
	if err != nil {
		return nil, err
	}
	if err := renderProcTexture(prnSource, p.Width, p.Height, absFile); err != nil {
		return nil, err
	}

	relSource := filepath.Join("layers", layerName+".prn")
	absSource, err := s.layerAbsPath(projectName, relSource)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(absSource, []byte(prnSource), 0o644); err != nil {
		return nil, fmt.Errorf("nock: save generating source: %w", err)
	}

	p.Layers = append(p.Layers, Layer{Name: layerName, File: relFile, Opacity: 100, Visible: true, Source: relSource})
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// RegenerateProceduralLayer re-runs a procedural layer with edited source, replacing both its
// rendered PNG and its saved source in place (same layer, same position in the stack, opacity/
// visibility/mask untouched) -- the founder's own "tweak the generated PARENA code and re-run"
// loop. Errors (a compile failure, a validation rejection) leave the layer's existing render and
// source completely untouched, so a bad edit never destroys a previously-working texture.
func (s *Service) RegenerateProceduralLayer(projectName, layerName, prnSource string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	if p.Layers[idx].Source == "" {
		return nil, fmt.Errorf("nock: layer %q has no generating source to regenerate from (it wasn't created procedurally)", layerName)
	}

	absFile, err := s.layerAbsPath(projectName, p.Layers[idx].File)
	if err != nil {
		return nil, err
	}
	// Render to a scratch path first -- only overwrite the real layer file once rendering has
	// actually succeeded, so a bad edit can't leave a partially-written or missing layer image.
	scratchFile := absFile + ".regen-tmp"
	if err := renderProcTexture(prnSource, p.Width, p.Height, scratchFile); err != nil {
		os.Remove(scratchFile)
		return nil, err
	}
	if err := os.Rename(scratchFile, absFile); err != nil {
		os.Remove(scratchFile)
		return nil, fmt.Errorf("nock: finalize regenerated layer: %w", err)
	}

	absSource, err := s.layerAbsPath(projectName, p.Layers[idx].Source)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(absSource, []byte(prnSource), 0o644); err != nil {
		return nil, fmt.Errorf("nock: save regenerated source: %w", err)
	}
	return p, nil
}

// GetProceduralSource returns the saved PARENA source for a procedural layer, for a GUI/CLI to
// load into an editable text field.
func (s *Service) GetProceduralSource(projectName, layerName string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return "", err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return "", err
	}
	if p.Layers[idx].Source == "" {
		return "", fmt.Errorf("nock: layer %q has no generating source", layerName)
	}
	absSource, err := s.layerAbsPath(projectName, p.Layers[idx].Source)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(absSource)
	if err != nil {
		return "", fmt.Errorf("nock: read generating source: %w", err)
	}
	return string(data), nil
}

// RemoveLayer deletes a layer (and its mask/generating-source files, if any) from a project.
func (s *Service) RemoveLayer(projectName, layerName string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	layer := p.Layers[idx]
	if abs, err := s.layerAbsPath(projectName, layer.File); err == nil {
		_ = os.Remove(abs)
	}
	if layer.Mask != "" {
		if abs, err := s.layerAbsPath(projectName, layer.Mask); err == nil {
			_ = os.Remove(abs)
		}
	}
	if layer.Source != "" {
		if abs, err := s.layerAbsPath(projectName, layer.Source); err == nil {
			_ = os.Remove(abs)
		}
	}
	p.Layers = append(p.Layers[:idx], p.Layers[idx+1:]...)
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// SetOpacity sets a layer's opacity (0-100). The stored layer PNG itself is untouched — opacity
// is applied at Export time, not baked destructively into the file, so it stays adjustable.
func (s *Service) SetOpacity(projectName, layerName string, opacity int) (*Project, error) {
	if opacity < 0 || opacity > 100 {
		return nil, fmt.Errorf("nock: opacity must be 0-100, got %d", opacity)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	p.Layers[idx].Opacity = opacity
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// SetVisible toggles a layer's visibility.
func (s *Service) SetVisible(projectName, layerName string, visible bool) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	p.Layers[idx].Visible = visible
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// MoveLayer moves a layer earlier (toward the bottom, delta<0) or later (toward the top,
// delta>0) in the stack by |delta| positions, clamped to the stack's own bounds.
func (s *Service) MoveLayer(projectName, layerName string, delta int) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	newIdx := idx + delta
	if newIdx < 0 {
		newIdx = 0
	}
	if newIdx > len(p.Layers)-1 {
		newIdx = len(p.Layers) - 1
	}
	if newIdx == idx {
		return p, nil
	}
	layer := p.Layers[idx]
	p.Layers = append(p.Layers[:idx], p.Layers[idx+1:]...)
	p.Layers = append(p.Layers[:newIdx], append([]Layer{layer}, p.Layers[newIdx:]...)...)
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// SetMask attaches (or replaces) a grayscale mask image on a layer, fit-and-padded to canvas
// size the same way AddLayer fits a layer's own image. White = fully visible, black = fully
// hidden, matching Photoshop's own real layer-mask convention.
func (s *Service) SetMask(projectName, layerName, maskImgPath string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	relMask := filepath.Join("layers", layerName+"-mask.png")
	absMask, err := s.layerAbsPath(projectName, relMask)
	if err != nil {
		return nil, err
	}
	if err := imFitToCanvas(maskImgPath, absMask, p.Width, p.Height); err != nil {
		return nil, err
	}
	p.Layers[idx].Mask = relMask
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ClearMask removes a layer's mask, if any.
func (s *Service) ClearMask(projectName, layerName string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	if p.Layers[idx].Mask != "" {
		if abs, err := s.layerAbsPath(projectName, p.Layers[idx].Mask); err == nil {
			_ = os.Remove(abs)
		}
	}
	p.Layers[idx].Mask = ""
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// AdjustHueSaturation applies a destructive brightness/saturation/hue adjustment to a layer's
// own stored image (percentages, ImageMagick -modulate semantics: 100 = unchanged on each
// axis). Destructive (unlike opacity) because, unlike opacity, there's no cheap way to keep it
// non-destructive without a second stored "adjustment layer" concept — real, honest, named
// future work in docs/NOCK_NORTHSTAR.md, not silently pretended away.
func (s *Service) AdjustHueSaturation(projectName, layerName string, brightnessPct, saturationPct, huePct int) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	abs, err := s.layerAbsPath(projectName, p.Layers[idx].File)
	if err != nil {
		return nil, err
	}
	if err := imModulate(abs, abs, brightnessPct, saturationPct, huePct); err != nil {
		return nil, err
	}
	return p, nil
}

// Sharpen applies a destructive unsharp-mask sharpen to a layer's own stored image.
func (s *Service) Sharpen(projectName, layerName string, radius, sigma, amount float64) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	idx, err := s.findLayer(p, layerName)
	if err != nil {
		return nil, err
	}
	abs, err := s.layerAbsPath(projectName, p.Layers[idx].File)
	if err != nil {
		return nil, err
	}
	if err := imSharpen(abs, abs, radius, sigma, amount); err != nil {
		return nil, err
	}
	return p, nil
}

// ResizeCanvas hard-resizes the whole document (canvas + every layer + every mask) to a new
// size. A real, deliberate stretch/squash, not a fit-and-pad — matching a Photoshop "Image Size"
// operation, distinct from how a single newly-imported layer is fit into an existing canvas.
func (s *Service) ResizeCanvas(projectName string, width, height int) (*Project, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("nock: width/height must be positive, got %dx%d", width, height)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return nil, err
	}
	for _, l := range p.Layers {
		abs, err := s.layerAbsPath(projectName, l.File)
		if err != nil {
			return nil, err
		}
		if err := imResizeExact(abs, abs, width, height); err != nil {
			return nil, err
		}
		if l.Mask != "" {
			absMask, err := s.layerAbsPath(projectName, l.Mask)
			if err != nil {
				return nil, err
			}
			if err := imResizeExact(absMask, absMask, width, height); err != nil {
				return nil, err
			}
		}
	}
	p.Width = width
	p.Height = height
	if err := s.saveManifest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Export flattens every visible layer (bottom-to-top, respecting opacity and mask) onto a blank
// canvas and writes the result to outPath. Format is inferred from outPath's extension
// (.png keeps transparency; .jpg/.jpeg flattens onto backgroundHex first, since JPEG has no
// alpha channel at all). backgroundHex is ignored for PNG output.
func (s *Service) Export(projectName, outPath, backgroundHex string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.loadManifest(projectName)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "nock-export-*")
	if err != nil {
		return fmt.Errorf("nock: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	canvas := filepath.Join(tmpDir, "canvas.png")
	if err := imBlankCanvas(canvas, p.Width, p.Height); err != nil {
		return err
	}

	for i, l := range p.Layers {
		if !l.Visible {
			continue
		}
		abs, err := s.layerAbsPath(projectName, l.File)
		if err != nil {
			return err
		}
		working := abs
		if l.Opacity < 100 {
			opacityOut := filepath.Join(tmpDir, fmt.Sprintf("layer-%d-opacity.png", i))
			if err := imApplyOpacity(working, opacityOut, l.Opacity); err != nil {
				return err
			}
			working = opacityOut
		}
		if l.Mask != "" {
			absMask, err := s.layerAbsPath(projectName, l.Mask)
			if err != nil {
				return err
			}
			maskOut := filepath.Join(tmpDir, fmt.Sprintf("layer-%d-masked.png", i))
			if err := imApplyMask(working, absMask, maskOut, p.Width, p.Height, tmpDir); err != nil {
				return err
			}
			working = maskOut
		}
		nextCanvas := filepath.Join(tmpDir, fmt.Sprintf("canvas-%d.png", i))
		if err := imComposite(canvas, working, nextCanvas); err != nil {
			return err
		}
		canvas = nextCanvas
	}

	ext := filepath.Ext(outPath)
	if ext == ".jpg" || ext == ".jpeg" {
		if backgroundHex == "" {
			backgroundHex = "white"
		}
		return imFlattenToBackground(canvas, outPath, backgroundHex)
	}
	return runConvert(canvas, outPath)
}
