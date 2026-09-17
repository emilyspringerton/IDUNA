# NOCK procedural texture examples

Real, working `.prn` sources you can paste straight into NOCK's texture editor (Texture Library →
Generate → "manual source" / the inline source editor on an existing procedural texture), or
into `internal/nock/procgen.go`'s own `renderProcTexture`/`cmd/nock proc-add` paths. Each one
satisfies the real contract `validateProcTextureSource` enforces: `(module gentexture)`,
`(import math)` only, `pixel-r`/`pixel-g`/`pixel-b` defined, no `#target`.

## `bullet_hole.prn`

Founder real-time (2026-09-17): "when you shoot the wall it leaves a bullet hole (decal) can we
write a parena script for a basic bullet hole (slightly asymmetrical)... use sin or cos or
something with a multiplier or modulator that let me generate multiple versions by tweaking a
parameter."

A small, asymmetric scorch-mark decal. The one real tweakable parameter is `asym-seed` (the
first `defn` in the file) — edit that single number and re-render (NOCK's Regenerate button, or
`cmd/nock proc-edit`) to get a genuinely different hole shape; both the wobble's amplitude
character and its frequency derive from it. Roughly `-1.0..1.0` is the useful range; `0.0` gives
a nearly-perfect circle.

**Real, deliberate design choice — no alpha channel, background is pure white on purpose.**
NOCK's procedural pipeline only ever produces flat RGB (`Main.java`'s own harness writes a PPM,
no alpha channel anywhere in this path) — a founder follow-up caught this directly ("it needs
transparent edges too no? unless you can chroma it out or something") and a second follow-up
("something something photoshop blend mode") named the real fix: rather than adding alpha/
chroma-keying, this decal paints its untouched background pure white and only ever gets *darker*
toward the hole, so drawing it with a **Multiply** blend (`glBlendFunc(GL_DST_COLOR, GL_ZERO)` in
a real GL renderer, or ImageMagick's `-compose Multiply` for a static composite) makes the white
background vanish on any wall texture with zero alpha-channel work needed anywhere in this
pipeline. Verified live: `convert wall.png bullet_hole.png -compose Multiply -composite` produces
a correctly-composited wall with only the scorch mark visible, background fully invisible.

Real, live-verified this pass: compiles via the real `parena` binary (Java target), passes
`validateProcTextureSource`, and renders end-to-end through `renderProcTexture` (PARENA → javac →
java → PPM → PNG) at 128x128 — see `internal/nock/procgen_examples_test.go`. Three different
`asym-seed` values (-0.9, 0.0, 0.9) were rendered side-by-side and visually confirmed to produce
three distinctly different hole shapes, not just brightness/color changes.

**Real, honest, not built yet**: an actual in-game decal-drawing code path (SHANKPIT/PAPERCRAFT
don't yet have "spawn a decal on the hit surface when a bullet impacts a wall" wired up) — this
asset is ready to be dropped in once that exists, not proof that it's wired up already.
