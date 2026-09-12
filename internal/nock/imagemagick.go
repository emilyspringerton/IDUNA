package nock

// imagemagick.go — every real pixel operation NOCK performs shells out to ImageMagick's
// `convert` (v6, the real binary confirmed installed on this box — `magick` v7 unification isn't
// present here, so every command below uses the classic v6 subcommand-per-verb form, not the
// `magick <verb>` v7 form). Deliberately no CGo/library binding: shelling out is the simplest
// thing that actually works today, matches the founder's own explicit "lets build it on top of
// imagemagic for now" framing, and keeps the door open to swapping the backend later (a real,
// named future phase in docs/NOCK_NORTHSTAR.md is replacing this file's own insides with a
// PARENA-native image pipeline without touching project.go/service.go's own public API at all).

import (
	"fmt"
	"os/exec"
)

// convertBin is the real ImageMagick v6 binary this package shells out to. A package var (not a
// constant) so a test can point it at a stub binary if ImageMagick is ever missing from a CI
// image — real, current CI/dev boxes all have it, so no stub exists yet, but the seam is here.
var convertBin = "convert"

func runConvert(args ...string) error {
	cmd := exec.Command(convertBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nock: convert %v: %w: %s", args, err, string(out))
	}
	return nil
}

// imFitToCanvas resizes src to fit within widthxheight preserving aspect ratio, then pads with
// transparent to land on exactly widthxheight — every layer in a NOCK project is real, always
// exactly canvas-sized, which is what keeps every later compositing step below a plain
// same-size overlay instead of needing per-layer offset/position bookkeeping (a real, honest v0
// simplification — arbitrary layer placement/transform is named, deferred, future work in
// docs/NOCK_NORTHSTAR.md, not silently assumed away).
func imFitToCanvas(src, dst string, width, height int) error {
	size := fmt.Sprintf("%dx%d", width, height)
	return runConvert(
		src,
		"-resize", size,
		"-background", "none",
		"-gravity", "center",
		"-extent", size,
		dst,
	)
}

// imBlankCanvas writes a fully transparent widthxheight PNG to dst — the starting point every
// Export flattens layers onto.
func imBlankCanvas(dst string, width, height int) error {
	size := fmt.Sprintf("%dx%d", width, height)
	return runConvert("-size", size, "xc:none", dst)
}

// imGradient writes a real linear gradient (fromHex -> toHex) sized widthxheight to dst.
// direction "vertical" (top->bottom, ImageMagick's own native gradient: orientation) or
// "horizontal" (generated sideways then rotated back to the real requested size). Arbitrary
// angles are real, named future work, not built here — v0 ships the two directions a founder
// actually asked to be able to reach quickly, not a partial/broken angle implementation.
func imGradient(dst string, width, height int, fromHex, toHex, direction string) error {
	spec := fmt.Sprintf("gradient:%s-%s", fromHex, toHex)
	switch direction {
	case "vertical", "":
		size := fmt.Sprintf("%dx%d", width, height)
		return runConvert("-size", size, spec, dst)
	case "horizontal":
		// Generate at swapped dimensions (native vertical gradient) then rotate 90 degrees --
		// a HxW vertical gradient rotated 90 degrees is a real WxH horizontal one, exact pixel
		// dimensions preserved.
		size := fmt.Sprintf("%dx%d", height, width)
		return runConvert("-size", size, spec, "-rotate", "90", dst)
	default:
		return fmt.Errorf("nock: unknown gradient direction %q (want vertical or horizontal)", direction)
	}
}

// imApplyOpacity scales an image's existing alpha channel by pct (0-100) in place (src -> dst).
// -alpha set first guarantees a real, present alpha channel to scale even for a source that
// started fully opaque (a freshly-imported JPG, or a freshly generated gradient), matching
// ImageMagick's own documented -channel A -evaluate multiply recipe for uniform opacity.
func imApplyOpacity(src, dst string, pct int) error {
	factor := float64(pct) / 100.0
	return runConvert(src, "-alpha", "set", "-channel", "A", "-evaluate", "multiply", fmt.Sprintf("%f", factor), "+channel", dst)
}

// imApplyMask multiplies an image's alpha channel by a grayscale mask (white = fully visible,
// black = fully hidden) using ImageMagick's own real, documented CopyOpacity-via-multiply-first
// pattern: since the source may already carry a real (non-255) alpha from imApplyOpacity above,
// a plain CopyOpacity composite would overwrite rather than combine it, so the mask is first
// multiplied against the existing alpha channel (via a temp mask scaled to canvas size), then
// applied as the new alpha via -compose CopyOpacity.
func imApplyMask(src, maskSrc, dst string, width, height int, tmpDir string) error {
	fittedMask := tmpDir + "/mask-fitted.png"
	if err := imFitToCanvas(maskSrc, fittedMask, width, height); err != nil {
		return fmt.Errorf("nock: fit mask: %w", err)
	}
	// Combine: new_alpha = old_alpha * mask_gray. Extract src's own alpha as a grayscale image,
	// multiply against the fitted mask, then recombine as src's new alpha channel.
	oldAlpha := tmpDir + "/old-alpha.png"
	if err := runConvert(src, "-alpha", "extract", oldAlpha); err != nil {
		return fmt.Errorf("nock: extract alpha: %w", err)
	}
	combinedAlpha := tmpDir + "/combined-alpha.png"
	if err := runConvert(oldAlpha, fittedMask, "-compose", "multiply", "-composite", combinedAlpha); err != nil {
		return fmt.Errorf("nock: combine mask: %w", err)
	}
	return runConvert(src, combinedAlpha, "-alpha", "off", "-compose", "CopyOpacity", "-composite", dst)
}

// imComposite composites layerPath over basePath ("over" = normal alpha blending, the only real
// blend mode v0 supports — additional ImageMagick -compose modes are a real, easy, named future
// addition, not built here since the founder's own list didn't ask for blend modes specifically).
func imComposite(basePath, layerPath, dst string) error {
	return runConvert(basePath, layerPath, "-compose", "over", "-composite", dst)
}

// imModulate applies brightness/saturation/hue adjustment in place (ImageMagick's own real
// -modulate semantics: 100 = unchanged for each of the three; brightness doubles as NOCK's own
// "lightness" control since -modulate has no separate lightness axis).
func imModulate(src, dst string, brightnessPct, saturationPct, huePct int) error {
	spec := fmt.Sprintf("%d,%d,%d", brightnessPct, saturationPct, huePct)
	return runConvert(src, "-modulate", spec, dst)
}

// imSharpen applies an unsharp mask. radius/sigma control the blur kernel (ImageMagick's own
// real -unsharp parameters), amount is the real, literal effect strength (0 = no-op).
func imSharpen(src, dst string, radius, sigma, amount float64) error {
	spec := fmt.Sprintf("%gx%g+%g+0", radius, sigma, amount)
	return runConvert(src, "-unsharp", spec, dst)
}

// imFlattenToBackground composites src onto a solid backgroundHex color and drops the alpha
// channel entirely — required before writing JPEG, which has no alpha channel at all.
func imFlattenToBackground(src, dst, backgroundHex string) error {
	return runConvert(src, "-background", backgroundHex, "-alpha", "remove", "-alpha", "off", dst)
}

// imResizeExact hard-resizes (no aspect preservation) src to exactly widthxheight -- used for
// ResizeCanvas, which is a real, deliberate stretch/squash of every existing layer to the new
// size (matching a whole-document canvas resize), distinct from imFitToCanvas's own
// fit-and-pad behavior used when importing a single new layer into an existing canvas.
func imResizeExact(src, dst string, width, height int) error {
	size := fmt.Sprintf("%dx%d!", width, height)
	return runConvert(src, "-resize", size, dst)
}

// imDimensions returns the real, actual pixel width/height of an image file, via `identify`.
func imDimensions(path string) (width, height int, err error) {
	cmd := exec.Command("identify", "-format", "%w %h", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("nock: identify %s: %w", path, err)
	}
	_, err = fmt.Sscanf(string(out), "%d %d", &width, &height)
	if err != nil {
		return 0, 0, fmt.Errorf("nock: parse identify output %q: %w", string(out), err)
	}
	return width, height, nil
}
