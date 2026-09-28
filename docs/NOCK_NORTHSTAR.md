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
  remove/reorder/toggle-visibility/opacity a layer; attach/clear a grayscale mask; real, NON-
  destructive position/scale/rotation (S416-02) and hue/saturation/brightness/unsharp-sharpen
  adjustment (S416-04) — both stored as manifest metadata, applied fresh to a disposable working
  copy on every export, never baked into the layer's own stored file; whole-canvas resize
  (stretches every layer + mask); export — flattens every visible layer bottom-to-top, honoring
  opacity, transform, adjustments, and mask, to PNG (keeps transparency) or JPEG (flattens onto a
  background color first, since JPEG has no alpha channel).
- 10+ real tests in `internal/nock/service_test.go`, run against the real, installed ImageMagick
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

## Real re-scope: texture library (SQLite CRUD) before manual editing polish

Founder real-time, direct continuation: "alright work on the IDUNA side affordances we are
making a texture generator and manager so it needs to have CRUD and all that and also we are
gonna want to save them in sqlite or whatever with their parena src we can build the human
manual photoshop affordances after we get some basic texture management primitives built into
the iduna nock console." Then, modeled on CarePyre's own real master-resume/clone feature
(`CarePyre/docs/COMMUNITY_TOOLS_RESUME_NORTHSTAR.md`): "same model as the carepyre resumes with
like the ability to clone the main texture except the resumes there is a master resume there
wont be just one master texture there will be many master textures."

**Real, deliberate re-scope, not an addition alongside the old design unchanged:** the
Project/Layer flat-file compositing engine documented above stays exactly as it is (the future
"manual Photoshop affordances" layer), but the *primary* managed entity going forward is a
standalone **Texture** row in a new `nock_textures` SQLite table
(`migrations/truestore/202609120001_nock_textures.sql`), not a Project. `internal/nock/
texture_store.go` is the real CRUD layer: Create (from an uploaded image or from PARENA source,
rendered server-side), Get/GetByName, List (a real lightweight summary shape -- no PNG bytes/full
source in a listing response), Rename, Regenerate (edit the saved source, re-render, replace in
place -- a failed edit leaves the existing row untouched, same guarantee the file-backed
`RegenerateProceduralLayer` already gives), Delete, and **Clone**.

**The real, named difference from CarePyre's resume-clone model, per the founder's own
distinction:** a resume has exactly one master per user with many derived *target views*
(`IncludedWorkIDs`/`SummaryOverride`-style subset-and-override rows referencing that one shared
master). A texture has no single master at all -- `CloneTexture` is therefore a full,
independent row copy (its own new id, its own future edits never touching the source it was
cloned from), not a view-with-overrides. There is deliberately no `parent_id`/clone-provenance
column in the schema -- if that's ever needed it's real, separate, future work, not assumed away.

Exposed via `internal/http/handlers/nock_textures.go` (`GET/POST /admin/nock/api/textures`,
`GET/PATCH/DELETE .../textures/{id}`, `GET .../textures/{id}/image`, `POST .../textures/{id}/
clone`, `PATCH .../textures/{id}/regenerate`, `POST .../textures/generate` -- the real Vertex
one-shot path, mirroring the Project-scoped `.../generate` endpoint but landing in the texture
library instead of a Project's layer stack) and `cmd/nock`'s new `texture-*` subcommands
(`texture-list`/`create`/`generate`/`get`/`image`/`rename`/`delete`/`clone`/`regenerate`),
which connect to the SAME real SQLite DB the running IDUNA server itself uses by default
(`NOCK_DB_PATH`, defaulting to `var/iduna.db`) -- a texture created via the CLI shows up in the
web GUI and vice versa. A real, lightweight "Texture Library" tab was added to the React app
(generate-from-prompt form, a grid of real thumbnails via `.../image`, per-card
clone/rename/delete, and an inline source editor + re-run for a generated texture) -- kept
deliberately minimal, since the founder's own framing is "primitives first," not full GUI polish
yet. 20 new tests (10 in `internal/nock/texture_store_test.go`, plus handler tests), all real
(in-memory SQLite, real ImageMagick/PARENA/JVM for the generation-path tests). `go build/vet/
test ./...` clean.

## Real aside: PARENA has no web-UI/JSX generation capability today (checked, corrected)

Mid-build, the founder asked about generating the NOCK React frontend itself from PARENA source
("drop a parena file next to the file its supposed to generate... the parena can call into the
standard library to abstract widget patterns"), and separately floated Tailwind-as-a-cross-
target styling language ("build tailwind into parena if needed... totally makes sense as a ui
language beyond the web like for c games too"). Checked directly rather than assumed: `src/
emit_ts.c` (PARENA's real TypeScript emitter) has zero JSX/React/props/component handling
anywhere -- it emits plain scalar TypeScript functions, the same narrow shape as the Java
emitter (`MATH_PRIM_TABLE`-style primitive lowering), not component trees. `stdlib/editor/
ui.prn`/`widget.prn` (a real, if narrow, "UI widget" system) are for PARENA's *own* native SDL2
editor shell (the PITVIPER/DUNG lineage) -- unrelated to web/DOM UI, and not something `emit_ts.c`
even touches.

Resolved via `AskUserQuestion`: keep hand-writing the NOCK frontend directly in React/TypeScript
(+ real Tailwind, added this same pass -- `@tailwindcss/vite`, CSS-first `@theme` tokens in
`index.css`, existing component classes in `App.css` re-expressed as real `@apply` compositions
rather than hand-rolled property lists) for now. Both the PARENA-generates-web-components idea
and the Tailwind-as-a-cross-target-UI-language idea are real, named, deliberately deferred future
directions -- explicitly NOT scoped or designed in this pass (the founder's own "not sure what
that looks like if its dumb... just note it for later"), not silently dropped.

## Explicitly out of scope for this pass (real future phases, not forgotten)

1. **PARENA as the backend.** Founder: "we build it into the PARENA EDITOR PE new command line
   tool... start to replace the backend imagemagic stuff as you can." `imagemagick.go` is the
   one real seam this would replace — every other file (`project.go`, `service.go`, the HTTP/CLI
   layers) already only calls the functions in that one file, not `convert` directly.
2. **A low-poly 3D modeler.** Named explicitly by the founder. **Real update (2026-09-17):** the
   mesh/rig/animation side of NOCK has since started for real (animation repository, glTF
   import, a general N-joint runtime) — see `docs/NOCK_CHARACTER_PIPELINE_NORTHSTAR.md` for the
   real, phased plan. The modeler itself (authoring mesh geometry from scratch) is still real,
   deliberately unscoped future work within that doc's own Phase 3, not started.
3. **A level editor** for SHANKPIT (placing/arranging objects in a scene) and **object builders
   (premades)** — both named, both real, separate future tools. **Real update (2026-09-17):**
   scriptable object placement (attaching a script to a placed object, generalizing the Story
   System's own door-script pattern) is now scoped as Phase 2 of
   `docs/NOCK_CHARACTER_PIPELINE_NORTHSTAR.md`; a general map-editor placement UI itself is still
   real, unscoped future work within that doc.
4. **PARENA Editor (PE) macro recording** — founder: "the parena editor will even allow us to
   program macros repeatable written in parena." No macro/scripting layer exists anywhere in
   `internal/nock` yet. **Real correction (2026-09-14)**: checked directly and confirmed this was
   mis-homed — "PE" (the PARENA Editor) is `DUNG`, not `internal/nock`; this bullet was filed here
   only because it shared a founder quote with #1 above. Real scoping pass moved to
   `DUNG/NORTHSTAR.md`'s own "Real scoping pass: PE macro recording" section — the real hook
   (`PARENA/stdlib/editor/events.prn`'s `subscribe`, `plugin.prn`'s `register-command`), the
   founder's own "written in PARENA" constraint, and the one real open playback-mechanism question
   all live there now. Nothing for NOCK to build here.
5. **A native Windows client.** Founder, explicit and decisive: "i am on windows so im just
   thinking that instead of shipping the tech to me... what am i gonna do with it on my end...
   hit a sync button? ... lets build it into iduna for now... this is the easiest way to get a
   tool." This is *why* v0 is a web app inside IDUNA rather than a native desktop build — a real,
   deliberate, founder-made call, not a default this doc invented.
6. **Redux**, floated in an earlier message, then implicitly superseded by "whatever the newest
   react idioms are" — v0 has no global-state library; revisit only if `App.tsx`'s own local
   `useState` calls genuinely stop scaling, not preemptively.

## Visual style

See `docs/NOCK_STYLE_GUIDE.md` (S459-20) for the real daisyUI theme setup (cupcake light /
dracula dark, both stock, both colorful) and the one rule every new NOCK component should follow
(never hardcode a color — use the semantic tokens so it re-themes automatically).

## Real, phased next steps

1. Live-deploy and browser-verify `/admin/nock` against a real running IDUNA instance (the one
   real gap named above). Partial (2026-09-14): the real, currently-running IDUNA process was
   confirmed to serve and correctly auth-gate `/admin/nock/` and its own built static assets
   (401 without a session, not a 500 or an accidental public leak) — server-side plumbing is
   real and verified. A full logged-in browser walkthrough still needs the founder's own real
   admin cookie session (deliberately not bypassed or self-granted); left open.
2. ~~Layer position/scale/rotate~~ — done (S416-02, 2026-09-14). `Layer.X/Y/Scale/Rotation`,
   `imCompositeTransformed` (resize→rotate→position order), `Service.SetTransform` +
   `PATCH .../layers/:name/transform`, frontend `LayerTransformPanel`. Backward-compatible: an
   untransformed layer composites pixel-identical to before.
3. NOCK project-switcher: today "one `DataDir`, flat list of named projects" — a real per-game
   namespace (matching the founder's own "like you can switch a repo on github integrations")
   is the natural next data-model change, sequenced whenever a second real game project (GFD)
   actually needs its own NOCK space.
4. ~~Non-destructive adjustments~~ — done (S416-04, 2026-09-14). `Layer.Brightness/Saturation/
   Hue/SharpenRadius/SharpenSigma/SharpenAmount` are now real manifest metadata, applied to a
   disposable working copy on every export — the layer's own stored file is never touched, and
   re-tuning always starts from the same real original (no cumulative quality loss).
5. PARENA backend migration (see "explicitly out of scope" #1) — sequenced after PARENA's own
   image/raster stdlib support exists, which it does not today (checked: no `stdlib/image` or
   equivalent anywhere in `PARENA/stdlib/`).

## Video editor + phone uploads (2026-09-27)

Founder real-time: "blue ocean we need a nock video editor that can take uploads from any phone via
nock." Frame-break read: the general pattern is **"capture on any device, author in NOCK"** — a
short-lived capability link turns any phone into an input device for NOCK without an app or a
login on it. Video is the first consumer; the same link shape could later take photos → texture
library, or audio.

**Built (v0)**, `internal/nock/video_store.go` + `handlers/nock_videos.go` + `frontend/nock/src/VideoEditor.tsx`:
- **Phone link**: admin mints a token (15 min–1 day, optional upload cap, revocable), shown as a QR
  code. `/nock/upload/<token>` is a self-contained mobile page (`accept="video/*" multiple`,
  sequential uploads with progress). Slots are reserved atomically before the body streams (two
  phones can't overrun a capped link) and released if the upload fails.
- **Clips**: streamed to disk (never buffered in memory or /tmp), ffprobe-validated (a non-video is
  rejected and deleted — the path is reachable without login), rotation-aware dimensions, background
  720p H.264 proxy + thumbnail so iPhone HEVC plays in any browser. Range-capable serving for seek.
- **Timelines**: EDL = output size/fps + ordered `{clip_id, in_ms, out_ms}` segments. Render
  normalizes each segment (scale + pad, fps, yuv420p, H.264, 48 kHz stereo AAC, silent track synth
  for audio-less clips), then concat-demuxes with stream copy. Editing after a render marks it
  `stale`; a clip used by a timeline can't be deleted; a render interrupted by a restart is marked
  failed on startup (`VideoStore.Recover`).

**Not built (named)**: transitions, titles/text, audio mixing/music beds, multiple tracks, speed
changes, a visual waveform/filmstrip scrubber, render queue beyond one-at-a-time, disk quotas or
retention for clips, exporting a render into the texture/animation libraries, a native NOCK mobile
app. Untested on physical phones (Playwright iPhone emulation + real HEVC/rotated fixtures only).

**Deploy checklist**: `ffmpeg`/`ffprobe` on the IDUNA host; `NOCK_VIDEO_DIR`; `NOCK_VIDEO_MAX_MB`;
nginx `client_max_body_size` ≥ that for `/nock/upload/` and `/admin/nock/api/videos` (and generous
`proxy_read_timeout`/`proxy_request_buffering off` so multi-GB phone uploads stream through).

## MIXFORGE EDITOR: PARENA-wasm non-linear editing + MPC clip capture (2026-09-28)

Founder real-time, three messages folded into one real pass: "ensure NOCK tools video editor is
PARENA wasm powered and shares components with MIXFORGE's shared components... ensure we have
full non linear video editing for the video and audio clips via nock tools we need a full
documentary editing booth"; "keep the mixforge branding in nock call it the MIXFORGE EDITOR"; "the
mpc should work off of video streams to pull clips in addition to the traditional import and
manual snip workflow." Per Principle 19 ("a big, unscoped ask gets scoped, not swallowed whole"):
"full non-linear editing" as a category covers multi-track compositing, titles, motion graphics,
color grading, speed ramps and more — none of that is what got built. What's real below is a
specific, honest slice: crossfade transitions + fade envelopes, computed once by PARENA and
applied identically in a live client-side preview and the final server render, plus a third,
faster clip-acquisition path (MPC pad capture) alongside the two that already existed (desktop/
phone upload, manual mark-in/mark-out).

**"Shares components with MIXFORGE" — literal, not aspirational.** `frontend/nock/src/video/
nleEngine.ts`'s `instantiateDsp` and `frontend/nock/src/video/waveform.ts` are direct ports of
`MIXFORGE/web/engine.mjs`'s own `instantiateDsp` and `MIXFORGE/web/waveform.mjs` (the exact same
offscreen-canvas peak-render + playhead-repaint pattern). Not a cross-repo import — MIXFORGE is a
build-step-free static site in a separate repo, NOCK is a separate Vite/React/TS app — the same
real pattern, ported by hand, same as every other cross-repo idiom-sharing in this monorepo
(`internal/modelgit.Syncer` from `internal/gitsync.PushWithRetry`, CONSTRUCT generation, etc.).
`dj.html`/`multiplayer.html` themselves are completely untouched.

**Real, new PARENA module**: `PARENA/stdlib/video/nle.prn` — equal-power crossfade curves
(`xfade-out-gain`/`xfade-in-gain`, literally the same `quarter-cos` construction
`stdlib/mixforge/mixer.prn`'s own `pan-left`/`pan-right` already use — no cross-import between the
two modules, same "each module stands alone" precedent mixer.prn/sampler.prn already set), a
per-clip fade-in/fade-out envelope (`fade-envelope`) independent of any adjacent transition, and
MPC pad-capture window math (`pad-capture-in`/`pad-capture-out`, time-based rather than
sampler.prn's own beat-synced `capture-frames` — documentary footage has no BPM grid). Compiled to
`frontend/nock/src/video/nle.wasm` via the identical pipeline `MIXFORGE/scripts/build_dsp_wasm.sh`
established (`parena build` → LLVM IR → `llc -mtriple=wasm32-unknown-unknown` → `wasm-ld`), see
`frontend/nock/scripts/build_nle_wasm.sh`. Real, live-verified: 14/14 kernel checks
(`frontend/nock/scripts/nle_test.mjs`) — equal-power crossfade sweep, fade-envelope ramps at every
boundary, pad-capture min-length/preroll clamping — all pass against the actual built `nle.wasm`,
not mocked. Found and fixed a real, live, unrelated bug while first running this pipeline: the
checked-out `PARENA/parena` binary predated a same-day `emit_llvm.c` fix `stdlib/mixforge/
{mixer,sampler}.prn` themselves needed — `make build` in PARENA picked it up (see MIXFORGE's own
`CHANGELOG.md`, same-day Bazel entry).

**Live, client-side, non-linear preview** (`frontend/nock/src/video/TimelinePreview.tsx`): the
literal "non-linear" deliverable — seek anywhere across the WHOLE assembled multi-clip timeline
via a scrub bar and see/hear the real composited crossfade, without waiting on a server render.
Two hidden `<video>` elements ping-pong between "current" and "previous" segment during a
transition window; a `<canvas>` composites them with `globalAlpha` from `xfade_out_gain`/
`xfade_in_gain`/`fade_envelope`; two `GainNode`s (via `MediaElementAudioSourceNode`) cross-fade the
audio identically. The exact same offset/duration accumulation math
(`running = running + dur[i] - transition[i]`) is used independently on both sides of the Go/TS
boundary — `internal/nock/video_store.go`'s `renderWithTransitions` (the ffmpeg side) and
`TimelinePreview.tsx`'s `computeGeometry` (the preview side) — checked by hand to agree, not by a
shared schema (none exists across this boundary, same as every other Go/TS/C convention in this
monorepo). Honest limits, same "roughly synchronized" spirit as MIXFORGE's own room playback: a
150ms drift threshold before a video element is reseeked; single video track only, no
picture-in-picture/overlay tracks.

**Real EDL extension** (`internal/nock/video_store.go`): `Segment` gained `TransitionMS`/
`FadeInMS`/`FadeOutMS` (all optional, 0/absent = old cuts-only behavior, so every already-saved
timeline is unaffected). `validateEDL` clamps rather than rejects an over-long value (a transition
longer than either adjacent clip, or fades that together exceed a clip's own length) and writes
the clamped number back, so a saved timeline never claims a longer effect than what actually
plays — same clamp rule the frontend preview and PARENA's own `fade-envelope` boundary condition
use. Negative values are real, rejected errors (not silently clamped to 0), matching this file's
existing "out must be after in" rejection style. **Real server-side render**: a per-segment
`fade=`/`afade=` ffmpeg filter bakes in each clip's own fade during the existing per-segment
normalize step (composes fine with either render path); when any segment requests a transition, the
whole render switches from the old concat-demuxer/stream-copy path to a `filter_complex` chain of
`xfade`(video)/`acrossfade`(audio) across every normalized part
(`VideoStore.renderWithTransitions`). Named, real simplification: within that chain, a cut with no
requested transition gets a 50ms floor rather than a true zero-duration crossfade — `xfade`/
`acrossfade` don't accept zero — imperceptible, not hidden (`minXfadeSec`'s own doc comment).
Real tests: `TestEDLClampsTransitionFadeAndRejectsNegative`/`TestFadeFilters` (pure Go, no ffmpeg
needed) plus `TestTimelineRenderWithCrossfade` (real ffmpeg, same `requireFFmpeg`-skip convention
every other real-media test in this file already uses — **skipped in this sandbox, no ffmpeg
installed here**, same pre-existing constraint `TestTimelineRenderMixedSources` already lived
with before this change; both are real and will run in any environment with ffmpeg present, not
new fakes).

**Real third acquisition path: MPC pad capture** (`ClipViewer` in `VideoEditor.tsx`) — founder:
"the mpc should work off of video streams to pull clips in addition to the traditional import and
manual snip workflow." Four quick-grab pads ("−2s"/"−5s"/"−10s"/"−30s" — "grab the last N seconds
ending at the current playhead," for the reaction-shot-you-just-noticed-was-good case) plus one
press-and-hold pad (press = mark in, release = mark out, extended to a minimum length for a too-
quick tap) sit under the existing clip viewer, both wired through `pad_capture_in`/
`pad_capture_out` and the SAME `onAdd` callback the manual mark-in/mark-out flow already calls —
a third route onto the timeline, not a parallel, separate one. No backend change needed for this
part: the pad math runs entirely client-side against the clip already loaded in the viewer.

**Branding**: the NOCK tab previously labeled "Video" is now "MIXFORGE EDITOR" (founder: "keep the
mixforge branding in nock"); the component itself carries a matching in-page heading. The
internal tab id (`'video'`) is unchanged — only the visible label and the file's own doc comment
changed, so no stored/URL state breaks.

**Real, honest, not done** (named, not silently skipped): a full logged-in browser walkthrough of
the new preview/pad-capture UI — same real, already-standing limitation this doc names generally
for `/admin/nock` (needs the founder's own real admin cookie session, deliberately not bypassed).
Verified instead via: `tsc -b && vite build` clean, the real 14/14 `nle_test.mjs` kernel suite
against the actual built `nle.wasm`, `go build/vet/test ./internal/nock/...` clean (18 pre-existing
+ 3 new tests, two of the three new ones ffmpeg-gated and skipped in this sandbox as noted above).
Also not done, real and named: multiple video tracks/picture-in-picture, titles/text overlays,
speed ramps, color grading, transition types beyond a single equal-power dissolve (`xfade=fade`),
and live-stream (webcam/getUserMedia) capture — "streams" here means an already-uploaded clip
playing back in the browser, not a live camera feed; a real, separate future phase if wanted.
