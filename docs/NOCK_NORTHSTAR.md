# NOCK — NORTHSTAR

Real scoping + v0 implementation for the founder's own ask (2026-09-12, several real-time
messages folded into one): "we are gonna need to build our own tools to create the textures...
same affordances same interface layers layer masks opacity png and jpg gradient tool hue
saturation sharpening resolution exporting etc lets build it on top of imagemagic for now and
add cli affordances for everything in our app FIRST... lets yolo it into iduna... this project
is called NOCK." Per Principle 19 ("a big, unscoped ask gets scoped, not swallowed whole") this
doc names the real, huge surface the founder's own message covers (a Photoshop-equivalent editor,
a 3D modeler, a level editor, PARENA integration, a project-switcher) and draws a real, working
v0 line through the smallest useful slice of it — then a second explicit instruction landed
mid-build ("just get it working in whatever technology will get it working fastest") which is
why v0 below is a real, working, tested engine + CLI + minimal web GUI, not a plan document with
no code.

## Why "not too coupled to GFD"

Founder, explicit: "this is for shankpit it will be used for GFD we build it for shankpit first
to engineify it we need it to not be tooooo coupled to GFD we need it to be built out for a
different game first to make sure we have extension points." Concretely honored: `internal/nock`
imports nothing from GoblinFoxDragon, has no GFD-specific concept anywhere in its data model, and
the only real "project switcher" data structure so far — `internal/nock.Service.DataDir`, one
subdirectory per named project — is generic on purpose. Whether GFD (or any other game) plugs in
later as its own set of NOCK projects, or gets a real, separate per-repo `DataDir`, is left open;
either shape is reachable from what's built without a rewrite.

## Real, current v0 (this pass, 2026-09-12)

**Engine** (`internal/nock/`, zero GFD/game-specific code):
- A `Project` is a fixed canvas size (`Width`/`Height`) + an ordered `[]Layer` stack.
- Each `Layer`: `Name`, `File` (its own real PNG, always fit-and-padded to the canvas size — see
  the real, honest v0 simplification named in `imagemagick.go`'s own doc comment: no per-layer
  position/offset yet, every layer is full-canvas), `Opacity` (0-100), `Visible`, optional `Mask`
  (grayscale PNG, white = visible).
- Every real pixel operation shells out to ImageMagick 6's `convert` (the real binary installed
  on this box — no `magick` v7 unification here). `imagemagick.go` is the one file that would
  need to change to swap backends later (a real, named future phase, not built now).
- Real, working operations: create/list/delete project; add layer (from an uploaded/local image,
  or a generated gradient — vertical/horizontal only, arbitrary angle is real future work);
  remove/reorder/toggle-visibility/opacity a layer; attach/clear a grayscale mask; destructive
  hue/saturation/brightness adjust; destructive unsharp-mask sharpen; whole-canvas resize
  (stretches every layer + mask); export — flattens every visible layer bottom-to-top, honoring
  opacity and mask, to PNG (keeps transparency) or JPEG (flattens onto a background color first,
  since JPEG has no alpha channel).
- 10 real tests in `internal/nock/service_test.go`, run against the real, installed ImageMagick
  binary (not mocked) — including a real, found-live footgun documented in the test file itself:
  `convert -crop WxH+X+Y` retains the original image's "virtual canvas" page offset unless
  `+repage` is given, so a naive crop-then-flatten silently samples the wrong region.

**CLI** (`cmd/nock/`) — the founder's own explicit "CLI affordances for everything... FIRST":
a thin `flag`-based dispatcher over the exact same `internal/nock.Service` the HTTP API calls.
Every real operation above has a real subcommand (`nock init`, `layer-add`, `gradient`, `hue-sat`,
`sharpen`, `resize`, `export`, etc. — `nock help` lists all of them). Live end-to-end verified
(not just unit-tested): a real run creating a project, importing a photo as a base layer, adding
a gradient layer, setting opacity, adjusting hue/saturation, sharpening, attaching a directional
gradient mask, and exporting to both PNG and JPEG — the exported composite visually shows the
mask correctly blending the two layers.

**HTTP API + web GUI** (`internal/http/handlers/nock.go` + `nock_page.go`, mounted at
`/admin/nock` in IDUNA, gated by the same `iduna.admin` permission every other admin surface
uses): a JSON API exposing every `Service` operation (multipart upload for the two operations
that take an image file — layer-add, layer-mask), plus `GET .../export` streaming a real
flattened PNG/JPEG directly, so a browser can point an `<img>` straight at it for a live preview
or a `download` link for a real export, with no separate job/polling step. `frontend/nock/` is a
real React 19 + TypeScript app (Vite-built, function components + hooks throughout, no class
components, no Redux — deliberately not adopted yet; the state this app manages today is small
enough that reaching for a global-state library preemptively would be the wrong "newest idiom"
call, not the right one) — project list/create/delete, a layer panel (visibility, opacity slider,
reorder, mask upload/clear, delete), an "add layer from image" upload, a gradient-layer form, and
a hue/saturation/sharpen panel for the selected layer, all driving the same real API the CLI
calls. Built output (`frontend/nock/dist/`) is `go:embed`-ed into the IDUNA binary via a small
co-located `frontend/nock/embed.go` package (`go:embed` can't reach outside its own directory
tree, which is why the embed lives next to `dist/` rather than in `internal/http/handlers`) — no
Node process needed at runtime, matching this monorepo's general bias against extra runtime
dependencies in production. Real, honest, deliberately NOT ignored in git: `frontend/nock/dist/`
is a committed build artifact, not a directory Go/CI regenerates — a frontend source change needs
a real `npm run build` + commit before it reaches a running IDUNA binary; a real CI build step is
real, named future follow-up, not built in this pass.

**Real, honest, not yet done — checked, not silently skipped:**
- Live-booting a real IDUNA instance end-to-end and hitting `/admin/nock` over HTTP with a
  browser. The engine (10 tests) and the HTTP handler layer (3 more tests, via `httptest`, no
  real network listener) are both real and passing; what's *not* independently verified is the
  full `main.go` route/middleware wiring against a live process — IDUNA's own port (`:8080`) is
  hardcoded with no env override, and the real, currently-running production IDUNA instance on
  this box is a live, shared service ("the central trust authority") that this pass deliberately
  did not stop/restart to test against. Deploying this (restarting the live service to pick up
  the new binary) is a real, separate step for whoever runs that process, not done here.
- Layer transforms (move/scale/rotate a layer within the canvas) — every layer is full-canvas
  only in v0.
- Non-destructive hue/sat/sharpen (both bake directly into the stored layer file today).
- Arbitrary-angle gradients (vertical/horizontal only).
- Additional blend modes beyond normal "over" compositing.
- Drag-and-drop layer reordering (up/down buttons only).
- Undo/redo of any kind.

## Procedural texture generation via PARENA + Vertex AI (same day, second real slice)

Founder real-time, direct continuation: "can we build that into nock tools... like an api for
generating procedurally generated textures? like written in parena maybe so if you had an llm
and you gave it the api and told it you need a texture for X it could just one shot something...
you can save the texture as a file and also as the source code (think GENERA OS)." Then, mid-
build, a real security concern raised and worth its own callout: "we can farm the parena to
texture generation off to the backend? that sounds unsafe i dunno lol maybe it needs to be done
on the frontend i dunno" -- then, after a real investigation surfaced the actual mechanism (below),
the founder's own follow-up landed on the real fix: "we could write it in BURROW the go emitter -
or we can run it on our JAVA server" / "yea run it in JAVA?"

**The real safety problem, checked directly, not assumed:** PARENA's C emitter's `#target`/
`inline-c` escape hatch is completely unrestricted -- any `.prn` file, LLM-authored or not, can
embed raw C, and `src/emit.c`'s own comment says that string "is trusted verbatim as real C." An
LLM-generated texture program compiled to C would have been a genuine, unsandboxed arbitrary-
code-execution vector. "Do it on the frontend" doesn't actually fix this either -- a browser
can't execute compiled native code at all; the real safe version of that idea is a WASM target,
which PARENA doesn't have yet (on the roadmap per `PARENA/CLAUDE.md`, not built).

**The real fix, following the founder's own suggestion:** compile to PARENA's Java target
instead. Checked directly: `src/emit_java.c` has zero handling for `#target`/inline-anything at
all -- the escape hatch simply isn't wired up for this target. Combined with this target's own
real, current construct support being narrow (scalar `F64` math, `if`/`not`, and the `math/*`
primitives genuinely lowered straight to `java.lang.Math.*` -- confirmed via `MATH_PRIM_TABLE` in
`emit_java.c`, real for `cos`/`sqrt`/`floor`/`log`/`random-f64`/`pi`, unlike the C target where
those same functions are still honest `0.0` placeholders in `stdlib/math/math.prn`), a compiled
texture program is provably a pure function: no `defstruct`/`loop`/`match`/`import`-of-io-or-net
support on this target means it cannot open a file, make a network call, or spawn a process,
because the language surface available here has no way to express any of those. Running that
inside a real JVM (memory-safe, bytecode-verified) with a capped heap (`-Xmx128m`) and a hard
wall-clock timeout on every subprocess is the same real mitigation class actual run-untrusted-
code services (competitive-programming judges, CI runners for fork PRs) already use for this
exact shape of problem. `internal/nock/procgen.go`'s own header comment has the full real
argument; `validateProcTextureSource` is a second, independent static layer on top (rejects
`#target` and any non-`math` import outright, regardless of what the emitter does or doesn't
support) rather than relying on the emitter alone.

**Real contract a program (LLM- or human-written) must satisfy:**
```clojure
(module gentexture)
(import math)
(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
```
No `let`/`loop`/`match`/`defstruct` (this target's own real construct support doesn't have them),
no import besides `math`, no `#target`. A checked-in, trusted, never-generated `Main.java`
harness (the *only* code in this pipeline with real file I/O) calls the three functions once per
pixel and writes a PPM, which `internal/nock`'s existing `convert` wrapper turns into a real PNG
NOCK already knows how to treat as a layer.

**Real, working, tested (11 new tests in `internal/nock/procgen_test.go` +
`gen_vertex_test.go`, 2 more in `internal/http/handlers/nock_test.go`):** `Service.
AddProceduralLayer`/`RegenerateProceduralLayer`/`GetProceduralSource` (the "tweak the code and
re-run" loop -- a failed re-run leaves the layer's existing render and source completely
untouched); `cmd/nock proc-add`/`proc-edit`/`proc-show`; HTTP `POST .../procedural` (manual
source), `GET`/`PATCH .../layers/{name}/procedural` (fetch/regenerate), and `POST .../generate`
(the real Vertex AI one-shot path -- `internal/nock/gen_vertex.go`, same real ADC-credential/
project/region pattern `gfd_item_proposals.go` already uses); a real GUI panel (prompt + generate
button, an editable source textarea + Re-run for an existing procedural layer). A real, live,
manually-verified end-to-end run (PARENA source -> `parena build -o X.java` -> `javac` -> `java`
-> real PPM -> real PNG) produced a correct checkerboard-style pattern from real `Math.cos`/
`Math.sqrt` calls. The Vertex AI call itself is NOT live-tested in this session (no active
`gcloud` account in this sandbox) -- `TestGenerateProceduralTextureSource_RealVertexCall` skips
honestly rather than faking a pass, same real precedent `gfd_item_proposals_test.go` already set.

**Real, honest, not done:** non-destructive/layered procedural generation (a regenerate replaces
the layer's own render outright); no seed/variation parameter in the contract (a program that
wants variety calls `(math/random-f64)` itself, so re-renders of the same source aren't
guaranteed pixel-identical -- a real, accepted tradeoff, not a bug); BURROW (Go emitter) as a
possible third target was floated by the founder and not pursued this pass, since the Java path
already closed the real safety gap and BURROW's own current capability is narrower still (per
`LO/NORTHSTAR.md`'s own capability audit: scalar+flat-struct only) -- a real, live comparison is
separate future work if the Java path's own real limits (no local bindings at all makes complex
patterns visually deep) turn out to matter in practice; no resource quota/rate-limiting on the
Vertex endpoint (a real, separate hardening item for whenever this is opened up beyond the
current `iduna.admin`-gated audience).

## Explicitly out of scope for this pass (real future phases, not forgotten)

1. **PARENA as the backend.** Founder: "we build it into the PARENA EDITOR PE new command line
   tool... start to replace the backend imagemagic stuff as you can." `imagemagick.go` is the
   one real seam this would replace — every other file (`project.go`, `service.go`, the HTTP/CLI
   layers) already only calls the functions in that one file, not `convert` directly.
2. **A low-poly 3D modeler.** Named explicitly by the founder, not scoped or started here — a
   genuinely separate tool (geometry, not raster images) with no real overlap with `internal/nock`
   beyond possibly living under the same `/admin/nock`-branded umbrella later.
3. **A level editor** for SHANKPIT (placing/arranging objects in a scene) and **object builders
   (premades)** — both named, both real, separate future tools.
4. **PARENA Editor (PE) macro recording** — founder: "the parena editor will even allow us to
   program macros repeatable written in parena." No macro/scripting layer exists anywhere in
   `internal/nock` yet.
5. **A native Windows client.** Founder, explicit and decisive: "i am on windows so im just
   thinking that instead of shipping the tech to me... what am i gonna do with it on my end...
   hit a sync button? ... lets build it into iduna for now... this is the easiest way to get a
   tool." This is *why* v0 is a web app inside IDUNA rather than a native desktop build — a real,
   deliberate, founder-made call, not a default this doc invented.
6. **Redux**, floated in an earlier message, then implicitly superseded by "whatever the newest
   react idioms are" — v0 has no global-state library; revisit only if `App.tsx`'s own local
   `useState` calls genuinely stop scaling, not preemptively.

## Real, phased next steps

1. Live-deploy and browser-verify `/admin/nock` against a real running IDUNA instance (the one
   real gap named above).
2. Layer position/scale/rotate — the real, biggest gap between v0 and an actual Photoshop-shaped
   tool (every layer being forced full-canvas is a genuine, felt limitation the moment someone
   wants to place a small decal, not just stack full-size tiles).
3. NOCK project-switcher: today "one `DataDir`, flat list of named projects" — a real per-game
   namespace (matching the founder's own "like you can switch a repo on github integrations")
   is the natural next data-model change, sequenced whenever a second real game project (GFD)
   actually needs its own NOCK space.
4. Non-destructive adjustments (a real adjustment-layer concept, not baking hue/sat/sharpen into
   the stored file).
5. PARENA backend migration (see "explicitly out of scope" #1) — sequenced after PARENA's own
   image/raster stdlib support exists, which it does not today (checked: no `stdlib/image` or
   equivalent anywhere in `PARENA/stdlib/`).
