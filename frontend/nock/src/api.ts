// api.ts — typed client for /admin/nock/api, mirroring internal/nock.Project/Layer's own real
// JSON shape exactly (see IDUNA/internal/nock/project.go). Every call goes through the same Go
// Service the CLI (cmd/nock) calls too — this file has no logic of its own beyond building the
// right HTTP request, matching the "same shape CLI and GUI" design.

export interface Layer {
  name: string
  file: string
  opacity: number
  visible: boolean
  mask?: string
  /** Present only for a layer created by generateProcedural/AddProceduralLayer -- the saved
   * PARENA source that rendered it ("think GENERA OS": the source ships with the asset). */
  source?: string
  /** x/y/scale/rotation (S416-02, "the biggest real gap between NOCK v0 and an actual
   * Photoshop-shaped tool -- every layer is forced full-canvas today"). Omitted (undefined) on
   * a layer that's never been transformed -- treat missing/0 scale as 100 (unchanged), matching
   * Layer.EffectiveScale's own real Go-side convention. */
  x?: number
  y?: number
  scale?: number
  rotation?: number
}

export interface Project {
  name: string
  width: number
  height: number
  layers: Layer[]
}

const API_BASE = '/admin/nock/api'

async function req<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> =
    opts.body && !(opts.body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: 'include',
    ...opts,
    headers: { ...headers, ...(opts.headers as Record<string, string> | undefined) },
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

const enc = encodeURIComponent

export const api = {
  listProjects: () => req<string[]>('/projects'),

  createProject: (name: string, width: number, height: number) =>
    req<Project>('/projects', { method: 'POST', body: JSON.stringify({ name, width, height }) }),

  getProject: (name: string) => req<Project>(`/projects/${enc(name)}`),

  deleteProject: (name: string) => req<void>(`/projects/${enc(name)}`, { method: 'DELETE' }),

  addLayer: (project: string, name: string, file: File) => {
    const form = new FormData()
    form.append('name', name)
    form.append('file', file)
    return req<Project>(`/projects/${enc(project)}/layers`, { method: 'POST', body: form })
  },

  removeLayer: (project: string, layer: string) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}`, { method: 'DELETE' }),

  setOpacity: (project: string, layer: string, opacity: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/opacity`, {
      method: 'PATCH',
      body: JSON.stringify({ opacity }),
    }),

  setVisible: (project: string, layer: string, visible: boolean) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/visible`, {
      method: 'PATCH',
      body: JSON.stringify({ visible }),
    }),

  moveLayer: (project: string, layer: string, delta: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/move`, {
      method: 'PATCH',
      body: JSON.stringify({ delta }),
    }),

  setTransform: (project: string, layer: string, x: number, y: number, scale: number, rotation: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/transform`, {
      method: 'PATCH',
      body: JSON.stringify({ x, y, scale, rotation }),
    }),

  setMask: (project: string, layer: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/mask`, { method: 'POST', body: form })
  },

  clearMask: (project: string, layer: string) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/mask`, { method: 'DELETE' }),

  hueSat: (project: string, layer: string, brightness: number, saturation: number, hue: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/hue-sat`, {
      method: 'PATCH',
      body: JSON.stringify({ brightness, saturation, hue }),
    }),

  sharpen: (project: string, layer: string, radius: number, sigma: number, amount: number) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/sharpen`, {
      method: 'PATCH',
      body: JSON.stringify({ radius, sigma, amount }),
    }),

  addProcedural: (project: string, name: string, source: string) =>
    req<Project>(`/projects/${enc(project)}/procedural`, {
      method: 'POST',
      body: JSON.stringify({ name, source }),
    }),

  getProceduralSource: (project: string, layer: string) =>
    req<{ source: string }>(`/projects/${enc(project)}/layers/${enc(layer)}/procedural`),

  regenerateProcedural: (project: string, layer: string, source: string) =>
    req<Project>(`/projects/${enc(project)}/layers/${enc(layer)}/procedural`, {
      method: 'PATCH',
      body: JSON.stringify({ source }),
    }),

  addGradient: (project: string, name: string, from: string, to: string, direction: string) =>
    req<Project>(`/projects/${enc(project)}/gradient`, {
      method: 'POST',
      body: JSON.stringify({ name, from, to, direction }),
    }),

  resize: (project: string, width: number, height: number) =>
    req<Project>(`/projects/${enc(project)}/resize`, {
      method: 'PATCH',
      body: JSON.stringify({ width, height }),
    }),

  exportUrl: (project: string, format: 'png' | 'jpg' = 'png') =>
    `${API_BASE}/projects/${enc(project)}/export?format=${format}&_=${Date.now()}`,
}

// GenerateResult is a discriminated result for POST .../generate: the backend returns 201 with
// the updated Project on success, or 422 with {error, source} when the model's own generated
// source failed validation or didn't compile -- a real, expected outcome (asking an LLM to
// one-shot correct code in an unusual, restricted language subset), not something to hide behind
// a generic thrown error. Handled outside the generic `req` helper above so both shapes are
// real, typed results instead of one being buried in an Error's message string.
export type GenerateResult = { ok: true; project: Project } | { ok: false; error: string; source: string }

// ---- Texture library (real, SQLite-backed CRUD -- see internal/nock/texture_store.go) ----
// A "texture" here is a standalone, independent row: many master textures, not layers inside a
// Project, and not views-with-overrides of a shared master (the founder's own explicit
// difference from CarePyre's resume-clone model). See TEXTURES_BASE calls below.

export interface Texture {
  id: number
  name: string
  width: number
  height: number
  parena_source?: string
  prompt?: string
  created_at: string
  updated_at: string
}

export interface TextureSummary {
  id: number
  name: string
  width: number
  height: number
  has_source: boolean
  prompt?: string
  created_at: string
  updated_at: string
}

const TEXTURES_BASE = '/admin/nock/api/textures'

async function treq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = opts.body ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${TEXTURES_BASE}${path}`, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export type TextureGenerateResult = { ok: true; texture: Texture } | { ok: false; error: string; source: string }

export const textures = {
  list: () => treq<TextureSummary[]>(''),
  get: (id: number) => treq<Texture>(`/${id}`),
  imageUrl: (id: number) => `${TEXTURES_BASE}/${id}/image?_=${Date.now()}`,
  rename: (id: number, name: string) => treq<Texture>(`/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  delete: (id: number) => treq<void>(`/${id}`, { method: 'DELETE' }),
  clone: (id: number, name: string) => treq<Texture>(`/${id}/clone`, { method: 'POST', body: JSON.stringify({ name }) }),
  regenerate: (id: number, source: string) =>
    treq<Texture>(`/${id}/regenerate`, { method: 'PATCH', body: JSON.stringify({ source }) }),

  async generate(name: string, prompt: string, width: number, height: number): Promise<TextureGenerateResult> {
    const res = await fetch(`${TEXTURES_BASE}/generate`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, prompt, width, height }),
    })
    const body = await res.json().catch(() => ({}))
    if (res.status === 201) return { ok: true, texture: body as Texture }
    if (res.status === 422) return { ok: false, error: body.error ?? 'generation failed', source: body.source ?? '' }
    throw new Error(`${res.status}: ${JSON.stringify(body)}`)
  },
}

// ---- BRAWLPIT online level editor (S415-02/03, founder real-time: "get the brawlpit level
// editor online - web technologies - we already started building nock - can we finish building
// out some of that interface so we can kind of parlay it into an online brawlpit level editor?")
// ----
// Platform mirrors BRAWLPIT/packages/common/protocol.h's own real Platform2D struct exactly
// (x/y/w/h are world-unit floats; type 0=SOLID, 1=PASSTHROUGH) -- see
// IDUNA/internal/brawlpit/level_store.go's own matching Go struct.

export interface Platform {
  x: number
  y: number
  w: number
  h: number
  type: 0 | 1
}

// Guide mirrors IDUNA/internal/brawlpit/level_store.go's own real Guide struct exactly (S418-01,
// "NOCK — Guide-Based Snapping"). Guides are real level data (they save/load/clone with the
// level, see LevelStore.SaveGuides), but they are AUTHORING METADATA ONLY -- notice ExportDoc
// below has no guides field at all, matching the requirements doc's own explicit 1.4 contract
// ("the game client must never load or care about them"). Never add guides to ExportDoc.
export interface Guide {
  axis: 'horizontal' | 'vertical'
  coord: number
  locked: boolean
  is_mirror_axis: boolean
}

export interface Level {
  id: number
  name: string
  width: number
  height: number
  platforms: Platform[]
  guides: Guide[]
  created_at: string
  updated_at: string
}

export interface LevelSummary {
  id: number
  name: string
  width: number
  height: number
  platform_count: number
  created_at: string
  updated_at: string
}

const LEVELS_BASE = '/admin/nock/api/brawlpit-levels'

async function lreq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = opts.body ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${LEVELS_BASE}${path}`, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const levels = {
  list: () => lreq<LevelSummary[]>(''),
  get: (id: number) => lreq<Level>(`/${id}`),
  create: (name: string, width: number, height: number, platforms: Platform[]) =>
    lreq<Level>('', { method: 'POST', body: JSON.stringify({ name, width, height, platforms }) }),
  save: (id: number, width: number, height: number, platforms: Platform[]) =>
    lreq<Level>(`/${id}`, { method: 'PUT', body: JSON.stringify({ width, height, platforms }) }),
  rename: (id: number, name: string) => lreq<Level>(`/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  delete: (id: number) => lreq<void>(`/${id}`, { method: 'DELETE' }),
  clone: (id: number, name: string) => lreq<Level>(`/${id}/clone`, { method: 'POST', body: JSON.stringify({ name }) }),
  exportUrl: (id: number) => `${LEVELS_BASE}/${id}/export?_=${Date.now()}`,
  // saveGuides is a real, separate call from `save` above -- ruler/guide edits are a genuinely
  // independent action from moving/resizing platforms (matches LevelStore.SaveGuides' own
  // separate-endpoint design on the Go side).
  saveGuides: (id: number, guides: Guide[]) => lreq<Level>(`/${id}/guides`, { method: 'PUT', body: JSON.stringify({ guides }) }),
}

// ---- SHANKPIT NOCK level editor v0 (EMILY/BACKLOG.md SECTION 459, founder real-time: "so v0 it
// and start working dont worry about the current levels lets just go full level select brawlpit
// repo exact model for now") ----
// Wall mirrors SHANKPIT/packages/map/map.h's own real Wall struct exactly (center x/y/z, FULL
// extents sx/sy/sz -- confirmed against map.c's own collision code, not min/max corners) -- see
// IDUNA/internal/shankpit/level_store.go's own matching Go struct.

export interface ShankpitWall {
  id: number
  x: number
  y: number
  z: number
  sx: number
  sy: number
  sz: number
  r: number
  g: number
  b: number
  friction: number
  // material (S459-16, founder real-time: "i think it makes sense to abstract into material
  // first so it cleanly translates into papercraft ... the default material is brick") -- empty
  // resolves to "brick" server-side (see internal/shankpit.DefaultMaterialName), matching every
  // pre-S459-16 wall's own real, existing data (no "material" key at all).
  material?: string
}

// ShankpitMaterial mirrors IDUNA/internal/shankpit.Material exactly (S459-16, founder real-time:
// "we will need the ability to add new materials and set their textures" / "registries for
// everything"). shader_name is a real, named reference into SHANKPIT's own compiled-in shader
// registry (packages/render/material_shaders.h) -- NEVER GLSL source stored here (founder: "build
// the shaders in to the native and then refer to the shaders from NOCK directly ... for now it
// will just be like the ffi names").
export interface ShankpitMaterial {
  id: number
  name: string
  shader_name: string
  texture_id?: number
  specular: number
  shininess: number
  created_at: string
  updated_at: string
}

const SHANKPIT_MATERIALS_BASE = '/admin/nock/api/shankpit-materials'

async function mreq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = opts.body ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${SHANKPIT_MATERIALS_BASE}${path}`, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const shankpitMaterials = {
  list: () => mreq<ShankpitMaterial[]>(''),
  create: (name: string, specular: number, shininess: number, textureId: number | null, shaderName: string = 'standard') =>
    mreq<ShankpitMaterial>('', {
      method: 'POST',
      body: JSON.stringify({ name, specular, shininess, texture_id: textureId, shader_name: shaderName }),
    }),
  update: (id: number, specular: number, shininess: number, textureId: number | null, shaderName: string = 'standard') =>
    mreq<ShankpitMaterial>(`/${id}`, {
      method: 'PUT',
      body: JSON.stringify({ specular, shininess, texture_id: textureId, shader_name: shaderName }),
    }),
  delete: (id: number) => mreq<void>(`/${id}`, { method: 'DELETE' }),
}

// SHANKPIT_GRID_CELL_SIZE is the real, fixed, constant world-unit size of one ground-plane grid
// square -- matches IDUNA/internal/shankpit.GridCellSize (Go) and SHANKPIT's own real, already-
// existing `#define GRID_SIZE 50.0f` (apps/lobby/src/main.c) exactly, kept in sync by hand across
// this Go/C/TS boundary. See that Go constant's own doc comment for the full rationale (founder:
// "the squares are always the same size" / "shankpit has it built in that the grid lights up
// when you touch it... it would be great if we integrated with that").
export const SHANKPIT_GRID_CELL_SIZE = 50

// ShankpitLevelObject mirrors IDUNA/internal/shankpit.LevelObject exactly -- a level placed as a
// child object inside another level (S459-15, founder real-time: "i have this level 2222 -
// already i want to use it as an object - the whole level ... a map is a composition of levels",
// then "really a level and a map is the same thing - its like a smart document in photoshop where
// you have like a photoshop doc in a photoshop doc"). There is no separate Map type: any
// ShankpitLevel can hold objects. rot_y is snapped to 0/90/180/270 (founder: "snap rotate 90
// degree turns is good for now"). plane_visible/plane_solid are real, stored, forward-compatible
// per-instance overrides for the referenced level's own ground plane -- both default false
// (founder: "DEFAULTS TO OFF") and are NOT yet acted on by the backend's own flatten/export pass,
// see level_store.go's own flattenObjects doc comment for why.
export interface ShankpitLevelObject {
  id: number
  ref_level_id: number
  x: number
  y: number
  z: number
  rot_y: 0 | 90 | 180 | 270
  plane_visible: boolean
  plane_solid: boolean
}

export interface ShankpitLevel {
  id: number
  name: string
  width: number
  height: number
  depth: number
  ground_plane_enabled: boolean
  ground_plane_squares: number
  walls: ShankpitWall[]
  objects: ShankpitLevelObject[]
  // is_default_queue (S459-41, founder real-time: "need to add an option to shankpit levels to
  // set a level as default for queue") -- exactly one level is the real, global QUEUE default at
  // a time, same real shape ShankpitSpray's own is_default already uses.
  is_default_queue: boolean
  created_at: string
  updated_at: string
}

export interface ShankpitLevelSummary {
  id: number
  name: string
  width: number
  height: number
  depth: number
  ground_plane_enabled: boolean
  ground_plane_squares: number
  wall_count: number
  object_count: number
  is_default_queue: boolean
  created_at: string
  updated_at: string
}

const SHANKPIT_LEVELS_BASE = '/admin/nock/api/shankpit-levels'

async function sreq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = opts.body ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${SHANKPIT_LEVELS_BASE}${path}`, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const shankpitLevels = {
  list: () => sreq<ShankpitLevelSummary[]>(''),
  get: (id: number) => sreq<ShankpitLevel>(`/${id}`),
  create: (
    name: string,
    width: number,
    height: number,
    depth: number,
    groundPlaneEnabled: boolean,
    groundPlaneSquares: number,
    walls: ShankpitWall[],
    objects: ShankpitLevelObject[],
  ) =>
    sreq<ShankpitLevel>('', {
      method: 'POST',
      body: JSON.stringify({
        name,
        width,
        height,
        depth,
        ground_plane_enabled: groundPlaneEnabled,
        ground_plane_squares: groundPlaneSquares,
        walls,
        objects,
      }),
    }),
  save: (
    id: number,
    width: number,
    height: number,
    depth: number,
    groundPlaneEnabled: boolean,
    groundPlaneSquares: number,
    walls: ShankpitWall[],
    objects: ShankpitLevelObject[],
  ) =>
    sreq<ShankpitLevel>(`/${id}`, {
      method: 'PUT',
      body: JSON.stringify({
        width,
        height,
        depth,
        ground_plane_enabled: groundPlaneEnabled,
        ground_plane_squares: groundPlaneSquares,
        walls,
        objects,
      }),
    }),
  rename: (id: number, name: string) => sreq<ShankpitLevel>(`/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  delete: (id: number) => sreq<void>(`/${id}`, { method: 'DELETE' }),
  clone: (id: number, name: string) => sreq<ShankpitLevel>(`/${id}/clone`, { method: 'POST', body: JSON.stringify({ name }) }),
  exportUrl: (id: number) => `${SHANKPIT_LEVELS_BASE}/${id}/export?_=${Date.now()}`,
  setDefaultQueue: (id: number) => sreq<ShankpitLevel>(`/${id}/default-queue`, { method: 'PATCH' }),
}

// ---- SHANKPIT sprays (S459-19, founder real-time: "can we implement sprays? ... export to spray
// goes to sprays registry same treatment ... we need a nock sprays interface right now just to
// set the default") ----

export interface ShankpitSpray {
  id: number
  name: string
  width: number
  height: number
  is_default: boolean
  created_at: string
  updated_at: string
}

const SHANKPIT_SPRAYS_BASE = '/admin/nock/api/shankpit-sprays'

async function sprreq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = opts.body ? { 'Content-Type': 'application/json' } : {}
  const res = await fetch(`${SHANKPIT_SPRAYS_BASE}${path}`, { credentials: 'include', ...opts, headers })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const shankpitSprays = {
  list: () => sprreq<ShankpitSpray[]>(''),
  imageUrl: (id: number) => `${SHANKPIT_SPRAYS_BASE}/${id}/image?_=${Date.now()}`,
  create: (name: string, width: number, height: number, pngBase64: string) =>
    sprreq<ShankpitSpray>('', { method: 'POST', body: JSON.stringify({ name, width, height, png_base64: pngBase64 }) }),
  setDefault: (id: number) => sprreq<ShankpitSpray>(`/${id}/default`, { method: 'PATCH' }),
  delete: (id: number) => sprreq<void>(`/${id}`, { method: 'DELETE' }),

  // exportProjectToSpray -- the real target of the Projects tab's own "Export to Spray" button:
  // fetches the project's already-rendered composite PNG (the exact same export the "Export PNG"
  // link downloads) and posts it straight to the sprays registry as a new, independent row.
  async exportProjectToSpray(projectName: string, width: number, height: number, sprayName: string): Promise<ShankpitSpray> {
    const res = await fetch(api.exportUrl(projectName, 'png'), { credentials: 'include' })
    if (!res.ok) throw new Error(`${res.status}: failed to fetch project export`)
    const blob = await res.blob()
    const buf = await blob.arrayBuffer()
    let binary = ''
    const bytes = new Uint8Array(buf)
    for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
    const base64 = btoa(binary)
    return shankpitSprays.create(sprayName, width, height, base64)
  },
}

export async function generateProcedural(project: string, name: string, prompt: string): Promise<GenerateResult> {
  const res = await fetch(`${API_BASE}/projects/${enc(project)}/generate`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, prompt }),
  })
  const body = await res.json().catch(() => ({}))
  if (res.status === 201) return { ok: true, project: body as Project }
  if (res.status === 422) return { ok: false, error: body.error ?? 'generation failed', source: body.source ?? '' }
  throw new Error(`${res.status}: ${JSON.stringify(body)}`)
}

// ---- BRAWLPIT RL checkpoint registry (S420/S421) ----
// A "checkpoint" here is a real, uploaded RL training snapshot -- see
// IDUNA/internal/brawlpit/checkpoint_store.go's own doc comment. List/download are real, public
// GETs at /api/v1/brawlpit-checkpoints (a different base path than the admin /admin/nock/api/
// surface every other NOCK feature uses, matching S420's own real trust-level split: uploading
// is an M2M training-pipeline action, but BROWSING the registry and SELECTING an opponent are a
// real, human, in-NOCK action -- see AiOpponents.tsx).

export interface Checkpoint {
  id: number
  name: string
  role: string
  generation: number
  elo: number
  source_location: string
  filename: string
  sha256: string
  size_bytes: number
  is_active_opponent: boolean
  has_weights: boolean
  weights_size_bytes: number
  weights_sha256: string
  // S428, founder real-time: "i want to reset training but not include certain models from the
  // registry - can you add a checkbox to the registry backend to disable those models from the
  // league?" -- a real, reversible, per-checkpoint exclusion flag, distinct from
  // is_active_opponent (one global in-game selection).
  is_disabled: boolean
  created_at: string
}

const CHECKPOINTS_BASE = '/api/v1/brawlpit-checkpoints'
const CHECKPOINTS_ADMIN_BASE = '/admin/nock/api/brawlpit-checkpoints'

async function creq<T>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`${CHECKPOINTS_BASE}${path}`, { credentials: 'include', ...opts })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`${res.status}: ${text}`)
  }
  return (await res.json()) as T
}

export const checkpoints = {
  list: (role?: string) => creq<Checkpoint[]>(role ? `?role=${enc(role)}` : ''),
  getActive: () => creq<Checkpoint | null>('/active'),
  // activate is the one write action a human makes through this UI -- admin-gated, a genuinely
  // different route/trust level from the public list/getActive reads above (see main.go's own
  // real routing split).
  activate: (id: number) =>
    fetch(`${CHECKPOINTS_ADMIN_BASE}/${id}/activate`, { method: 'PATCH', credentials: 'include' }).then(async (res) => {
      if (!res.ok) throw new Error(`${res.status}: ${await res.text().catch(() => res.statusText)}`)
      return (await res.json()) as Checkpoint
    }),
  // setDisabled is the real checkbox backend (S428) -- same admin-gated trust level as activate.
  // A disabled checkpoint stays fully visible/re-enable-able here; training/resume/bot-pool logic
  // is what actually skips it (scripts/rl_train_packet.py, scripts/rl_bot_pool.py).
  setDisabled: (id: number, disabled: boolean) =>
    fetch(`${CHECKPOINTS_ADMIN_BASE}/${id}/disable`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ disabled }),
    }).then(async (res) => {
      if (!res.ok) throw new Error(`${res.status}: ${await res.text().catch(() => res.statusText)}`)
      return (await res.json()) as Checkpoint
    }),
}
