# NOCK — Character Pipeline NORTHSTAR (meshes, rigs, animation, scriptable objects)

Real scoping doc for the founder's own multi-message arc (2026-09-16/17): "let's start iterating
towards nock tools modeler (blender) and golden band we need to be able to import quaternion
animations into nock golden band" → a real 422 hitting a rigged-mesh-only upload ("i have this
manequin im guessing its a rigged mesh? so we need like 3 things meshes rigs and rigged meshes?
im not sure but i want to piggyback off of this stuff for now but i really want to make it so we
can build our own too so we need to figure out those affordances") → "nock tools is gonna end up
having rigging and animating interfaces" / "and basic modeling" → "we need some affordances for
getting this into a game... lets get the player character meshed rigged and animated" → "yolo an
NPC into one of the games" → "we want animations in game and we want nicer models so we want to
at least build the affordances to start building the animations into the games" → "not sure how
to add scripts to actual objects and then place those objects into the world need affordances in
nock for that... i guess we need browsers and editors for animations, meshes, and rigs, and also
some way to rig meshes" → "we are importing assets to get us started because its hard to build
the whole pipeline out without assets to see how it fits together and that it actually works...
the real goal is to bring it all in house as we can we need the tools to edit and compose at each
level."

This is a real, deliberate SIBLING doc to `docs/NOCK_NORTHSTAR.md`, not a replacement or a merge
into it — that doc's own "explicitly out of scope" §2 ("A low-poly 3D modeler... not scoped or
started here") is the exact line this doc now picks up, since 3D character work has genuinely
started. `NOCK_NORTHSTAR.md` stays the texture/2D-compositing engine's own doc; this one covers
everything mesh/rig/animation/scriptable-object shaped.

## The real pipeline, soup to nuts

1. **Model a mesh** — geometry only (no bones).
2. **Model a rig** — a joint hierarchy (parent → child bones), independent of any mesh.
3. **Rig the mesh** (skinning / weight painting) — assign each mesh vertex to one or more joints
   with blend weights. This is the step that turns "a shape" + "a skeleton" into one object that
   can bend.
4. **Animate the rig** — record bone motion over time. The mesh is never touched again after
   step 3; skinning drags it along automatically.
5. **Compose** — pick a rigged mesh + zero or more compatible clips, preview it moving, save it
   as one real, placeable character asset.
6. **Place + script** — put a composed character (or any other object) into a level, optionally
   attach behavior to it (the same real shape SHANKPIT's Story System doors already prove out).

GOLDENBAND's own `.gmesh`/`.gskel`/`.gband` trilogy already maps directly onto steps 1/3, 2, and
4 respectively (see `GOLDENBAND/CLAUDE.md`). What's built, what's missing, and the real order to
close the gap follow below.

## Phase 0 — DONE: import pipeline + a general runtime that can actually play these assets back

Real, live, shipped this pass — the reason phases 1+ below are buildable on a real foundation
instead of a guess:

- **NOCK animation repository** (`internal/nock/anim_store.go`, S459-79/95) — SQLite-backed CRUD
  for `.gmesh`/`.gskel`/`.gband` assets, drag-and-drop glTF import
  (`internal/nock/gltf_convert.go`, server-side, no local `gbtool` run needed). **Real, load-
  bearing fix (S459-95, 2026-09-17):** a row no longer requires animation data — a bare mesh, a
  bare rig, or a rigged mesh with no baked animation yet (the founder's own real
  `Mannequin_F.glb` upload) is a real, legitimate asset on its own now, matching GOLDENBAND's own
  three-independent-parts model instead of forcing every upload to have all three.
- **`GSKEL_MAX_JOINTS` 64 → 128** (S459-93) — a real 65-joint industry-standard rig (a common
  shape once individual finger bones are included) hit the old cap on first real use; raised
  everywhere it's mirrored (GOLDENBAND source, Go tooling, IDUNA server-side compile path, and —
  found stale during S459-97 — SHANKPIT's own vendored copy, now re-synced).
- **General N-joint forward kinematics + mesh skinning** (`GOLDENBAND/src/gpose.h/.c`, S459-96) —
  closes the real gap that SHANKPIT's existing player-body renderer (`gband_mesh_rig.c`) is
  hardcoded to Tyler's own 5-joint rig with manually-indexed animation channels, unusable for any
  other skeleton. `gpose.c` is a general, engine-agnostic (no engine `Mat4` dependency — raw
  column-major `float[16]`) FK+skinning module: walks any `GSkel` joint hierarchy from a sampled
  pose, produces skin matrices, flattens a `GMesh` into a render-ready triangle list. 4 real,
  hand-derived unit tests (a 2-joint chain, root rotated 90°, checked against exact expected
  output) pass.
- **Real proof it works**: confirmed live (joint-by-joint diff of the two `.gskel` blobs) that
  the founder's own imported mannequin mesh+rig and the `UAL2_Standard_RM` mocap clip share the
  *exact same* 65-joint skeleton — no retargeting needed. Spawned as a real NPC in SHANKPIT
  (S459-97, `packages/goldenband/gband_skel_npc.c/.h`, a deliberate sibling to
  `gband_mesh_rig.c` rather than a rewrite of it — Tyler's own rendering is untouched). `make
  lobby` builds clean. **Real, honest, not yet done**: full on-screen visual confirmation — this
  sandbox's headless GL context can't load modern GL entry points even with
  `LIBGL_ALWAYS_SOFTWARE=1` (the same pre-existing limitation that already disables Tyler's own
  mesh here), so real visual verification needs a machine with a working GL driver.

## Phase 1 — NEXT: browse + compose UI (no new authoring capability, just real glue)

The cheapest real next step, and useful even while every asset is still Blender-imported: today
NOCK's animation library is a flat list of independent rows (S459-79's own v0 scope, explicitly
undecided on this). A composed *character* — "this mesh, this rig, these clips, ready to place in
a game" — doesn't exist as its own real thing yet; pairing a mesh with a clip and checking they
actually fit (same skeleton) is done by hand today (exactly what this doc's own Phase 0 section
did manually via `sqlite3` + a Python joint-name diff — that check needs to be a real, one-click
UI affordance, not something only Claude Code can do from a shell).

Real, concrete shape:
- A **skeleton compatibility check**, surfaced in the UI, not just possible via manual DB
  inspection — GOLDENBAND's own manifest already carries a `skeleton_hash` per animation
  (`gband_convert.go`'s `gbandManifest.SkeletonHash`); a real `.gskel` content hash on the mesh
  side (not currently stored/exposed) is the missing other half of a real "does this clip fit
  this rig" check.
- A **Character** as a new, real, composed entity (name, one mesh+rig pair, an ordered list of
  compatible clips with real names — "idle", "walk", "wave" — same real "many independent
  masters" convention `TextureStore`/`AnimStore`'s own `Clone*` already establish, not a
  synthesized-from-nothing new pattern).
- A **live preview**: sample a clip against the paired rig+mesh and render it moving, in-browser.
  Real, honest gap named up front: today's playback proof (Phase 0) is server-side C in a game
  client — there is no browser-side WebGL/Three.js viewer anywhere in NOCK yet. This is real,
  non-trivial new frontend work (a WebGL skinned-mesh renderer, not a reuse of anything that
  exists), not a small addition.

## Phase 2 — scriptable object placement (extends the Story System, not a new system)

Founder: "not sure how to add scripts to actual objects and then place those objects into the
world need affordances in nock for that." SHANKPIT's Story System already proves the real, exact
shape needed — it's just currently narrower than "any object":

- **What already exists** (`SHANKPIT/docs/STORY_SYSTEM_NORTHSTAR.md`, S459-81/82): a level's
  `doors` array names a `box_index` + a `script_url` pointing at a NOCK-hosted, PARENA-source,
  server-compiled `.so` (`internal/nock/door_script_compile.go`, real PARENA→C→`.so` pipeline,
  same real compile step `procgen.go`'s Java pipeline established). `story_doors.c` downloads and
  `dlopen`s it at level load. This is real, working, shipped — just scoped to doors specifically
  (`door-tick(dist-to-player, state) -> state`), not any placed object.
- **What Phase 2 needs to add**: (a) a general "attach a script to any placed object" contract,
  not just the door-shaped one — likely a small family of real script *kinds* (the Story System
  doc already names door/ladder/screen/character/trigger as real, distinct kinds with their own
  shapes, not one universal signature); (b) a real NOCK-hosted script *library* mirroring
  `DoorScriptStore`'s own shape but generalized across kinds, so a script is authored/compiled
  once and attached to many placed instances; (c) a real map-editor affordance to place a
  Character (Phase 1's own composed entity) into a level and pick which script (if any) drives
  it — today, level placement is still hand-authored JSON (`examples/story-doors/demo_level/
  level.json`), not a NOCK UI.
- **Deliberately not re-designed here**: the actual compile pipeline, the `dlopen` runtime
  loading model, and the per-kind script contracts are all real and already correct — this phase
  is about *reach* (any object, not just doors) and *authoring UI* (a NOCK placement affordance
  instead of hand-written JSON), not a new execution model.

## Phase 3 — authoring tools (modeling, rigging, animating) — the real "bring it in house" work

The founder's own explicit long-term goal ("the real goal is to bring it all in house as we can")
and the biggest remaining lift by far — each of the three is a genuinely separate, substantial
editor, not one feature:

1. **Rigging tool** (build a skeleton + paint skin weights on an existing mesh) — real, concrete
   next candidate after modeling, since Phase 0/1 already assume rigged assets exist; a rigging
   tool that works against an *imported* mesh (skip modeling dependency) is reachable sooner than
   a full from-scratch modeler.
2. **Animation tool** (pose a rig over time, produce a real `.gband` clip in-app) — natural
   pairing with the rigging tool, since both operate on the same skeleton data; `gseq.c`'s own
   real crossfade/stitching model (already built, S144-XX) is the real target format this tool
   would author *into*, not a new format.
3. **Modeling tool** (author mesh geometry from scratch) — named explicitly by the founder
   ("basic modeling"), the largest, most speculative piece; real, deliberately NOT scoped further
   in this pass — sequenced last because Phases 0-2 and rigging/animation authoring are all real,
   valuable, and buildable against imported meshes without it.

No code for Phase 3 exists yet. This doc names the real order (rigging+animation before
modeling) and the real reason (imported assets already unblock everything upstream of modeling),
not a designed implementation.

## Explicitly out of scope for this doc

- **Physics/rigid-body object interaction** (a Half-Life 2-style teeter-totter, picking up a
  cinder block) — the founder's own explicit call, scoped OUT of the Story System doc as a
  separate future engine addition; same exclusion applies here.
- **PAPERCRAFT player-character integration** — real, named next step after this pipeline is
  further along (SHANKPIT was chosen as the first proof surface specifically because it already
  has a working render harness; PAPERCRAFT has none yet — see `PAPERCRAFT/docs/
  NORTHSTAR_MODULAR_BUILDING.md`). Not designed here.
- **NPCs-are-scriptable-objects, PAPERCRAFT's own two-tier structural/Paper-Engine-object split**
  — real, already-answered-in-conversation ("yes, tying to PAPERCRAFT's structural/Paper-Engine-
  object two-tier model and SHANKPIT's character-tick/story_ai/humanness systems") but not
  re-derived or expanded here; see `PAPERCRAFT/docs/NORTHSTAR_MODULAR_BUILDING.md` and
  `SHANKPIT/docs/HUMANNESS_NORTHSTAR.md`.

## Real, phased next steps (summary)

1. Phase 1: skeleton-compatibility check + a Character (composed mesh+rig+clips) entity +
   in-browser preview.
2. Phase 2: generalize scriptable-object attachment past doors + a real map-editor placement UI.
3. Phase 3: rigging tool, then animation tool, then (last, most speculative) modeling tool.

Each phase is real, separately shippable work — not a single big-bang rewrite.
