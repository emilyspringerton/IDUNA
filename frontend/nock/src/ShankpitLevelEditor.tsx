import { useCallback, useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { SHANKPIT_GRID_CELL_SIZE, shankpitLevels, type ShankpitLevelSummary, type ShankpitWall } from './api'

// ShankpitLevelEditor.tsx — SHANKPIT NOCK level editor v0 frontend (EMILY/BACKLOG.md SECTION 459,
// founder real-time: "so v0 it and start working dont worry about the current levels lets just go
// full level select brawlpit repo exact model for now"). Deliberately built in the same real
// stack as the rest of this app (React/TypeScript/three.js) rather than a native PARENA/WASM core
// -- PARENA has no WASM emit target yet (a real, confirmed gap, not assumed: NOCK's own S387
// texture-generation work already looked at a WASM target for sandboxing and found it "on the
// roadmap, not built"), and the founder's own explicit direction was to ship something fast now
// ("but fuck it we need something shipped fast" / "whateer ships it the fstest" / "we can make it
// faster iterate on it if it sucks") rather than block v0 on a new compiler backend. A native
// PARENA-core rewrite stays a real, named future direction, not attempted here.
//
// Wall(x,y,z,sx,sy,sz) is CENTER-based with FULL extents (confirmed directly against
// SHANKPIT/packages/map/map.c's own collision code: bounds = center +/- size/2) -- see api.ts's
// own ShankpitWall doc comment. Y is up (SHANKPIT/packages/common/physics.h's own gravity
// decrements vy), which is three.js's own default up-axis too -- no remapping needed anywhere
// below.
//
// No orbit-controls/drag library is used -- plain pointer events on the canvas, matching
// LevelEditor.tsx's own established "no framework beyond what's already here for a v0 this small"
// precedent, just extended from 2D canvas math to a real three.js scene + raycasting.

function aDefaultWall(id: number, at: { x: number; y: number; z: number } = { x: 0, y: 2, z: 0 }): ShankpitWall {
  return { id, x: at.x, y: at.y, z: at.z, sx: 4, sy: 4, sz: 4, r: 0.6, g: 0.6, b: 0.65, friction: 0.8 }
}

function defaultSpawnerPos(): { x: number; y: number; z: number } {
  return { x: 0, y: 2, z: 0 }
}

// DEFAULT_GROUND_PLANE_SQUARES=2 -- a real, deliberate default so a brand new level starts with
// solid ground to build on ("i want there to be a plane by default that the player collides
// with... for rapid prototyping"): 2 squares * SHANKPIT_GRID_CELL_SIZE(50) = a 100x100-unit
// plane, matching this level's own default width/depth exactly.
const DEFAULT_GROUND_PLANE_SQUARES = 2

function newDefaultLevel(): {
  name: string
  width: number
  height: number
  depth: number
  groundPlaneEnabled: boolean
  groundPlaneSquares: number
  walls: ShankpitWall[]
} {
  return {
    name: '',
    width: 100,
    height: 50,
    depth: 100,
    groundPlaneEnabled: true,
    groundPlaneSquares: DEFAULT_GROUND_PLANE_SQUARES,
    walls: [aDefaultWall(1, defaultSpawnerPos())],
  }
}

function nextWallId(walls: ShankpitWall[]): number {
  return walls.reduce((m, w) => Math.max(m, w.id), 0) + 1
}

// Axis + sign identify exactly one of a box's 6 faces. faceNormal is that face's outward world
// normal -- trivial here since walls are axis-aligned (no rotation field on Wall at all), so a
// raycast hit's local face normal IS the world normal already.
// EditMode -- founder real-time: "introduce object vs face mode / start in object mode dragging a
// cube draggs it / face mode does what it does now allowing us to drag a face." Object mode drags
// the whole selected cube (or the spawner) around; Face mode is the original per-face reshape.
type EditMode = 'object' | 'face'

type Axis = 'x' | 'y' | 'z'
interface FaceHit {
  wallIndex: number
  axis: Axis
  sign: 1 | -1
}

function faceHitFromNormal(wallIndex: number, normal: THREE.Vector3): FaceHit {
  const ax = Math.abs(normal.x)
  const ay = Math.abs(normal.y)
  const az = Math.abs(normal.z)
  if (ax >= ay && ax >= az) return { wallIndex, axis: 'x', sign: normal.x >= 0 ? 1 : -1 }
  if (ay >= ax && ay >= az) return { wallIndex, axis: 'y', sign: normal.y >= 0 ? 1 : -1 }
  return { wallIndex, axis: 'z', sign: normal.z >= 0 ? 1 : -1 }
}

// closestPointOnAxisLineToRay is the real, standard closest-point-between-two-lines technique
// professional 3D gizmos use for axis-constrained dragging (Blender/Unity's own move-handle
// math) -- projecting mouse movement onto a screen-space direction breaks down the moment the
// camera is at an angle where that projected direction is ambiguous/degenerate; this instead
// finds the point on the real 3D axis line (through `linePoint`, direction `axis`) closest to the
// camera ray, which stays correct at any camera angle except looking straight down the axis
// itself (handled by the caller bailing out when `denom` is ~0).
function closestPointOnAxisLineToRay(
  rayOrigin: THREE.Vector3,
  rayDir: THREE.Vector3,
  linePoint: THREE.Vector3,
  axisDir: THREE.Vector3,
): number | null {
  // REAL, FOUND, FIXED BUG (founder real-time: "currently face dragging seems inversed i have to
  // drag away from the direction i want the face to move"): the previous version defined
  // w0 = linePoint - rayOrigin, the negation of the standard reference's own r = P1 - P2
  // (Ericson, "Real-Time Collision Detection," ClosestPtSegmentSegment specialized to infinite
  // lines: r = P1 - P2 = rayOrigin - linePoint) -- that sign flip propagates all the way through
  // to the returned parameter, so every drag moved exactly opposite the intended direction.
  // Rederived directly against that reference rather than re-guessing a sign to flip.
  const r = new THREE.Vector3().subVectors(rayOrigin, linePoint) // r = P1 - P2
  const a = rayDir.dot(rayDir)
  const e = axisDir.dot(axisDir)
  const b = rayDir.dot(axisDir)
  const c = rayDir.dot(r)
  const f = axisDir.dot(r)
  const denom = a * e - b * b
  if (Math.abs(denom) < 1e-6) return null // ray nearly parallel to the drag axis -- no stable solution
  const t = (a * f - b * c) / denom // parameter along axisDir from linePoint
  return t
}

const MIN_WALL_SIZE = 0.25

// applyFaceDrag returns a NEW wall with one face moved to `newFaceCoord` along `axis`, holding the
// OPPOSITE face fixed -- exactly the mechanism internal/shankpit's own
// TestFaceDragEditing_ReshapesCubeWithoutMovingOppositeFace proves server-side; this is that same
// math, client-side, for live visual feedback during drag.
function applyFaceDrag(wall: ShankpitWall, axis: Axis, sign: 1 | -1, newFaceCoord: number): ShankpitWall {
  const centerKey = axis
  const sizeKey = axis === 'x' ? 'sx' : axis === 'y' ? 'sy' : 'sz'
  const center = wall[centerKey]
  const size = wall[sizeKey]
  const oppositeFaceCoord = center - sign * (size / 2)
  let newSize = sign * (newFaceCoord - oppositeFaceCoord)
  if (newSize < MIN_WALL_SIZE) newSize = MIN_WALL_SIZE
  const newCenter = oppositeFaceCoord + sign * (newSize / 2)
  return { ...wall, [centerKey]: newCenter, [sizeKey]: newSize }
}

function useLevelList() {
  const [list, setList] = useState<ShankpitLevelSummary[]>([])
  const refresh = useCallback(() => {
    shankpitLevels.list().then(setList).catch(() => setList([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { list, refresh }
}

interface CameraState {
  target: THREE.Vector3
  radius: number
  theta: number // azimuth
  phi: number // polar, clamped away from the poles
}

function cameraPositionFrom(cam: CameraState): THREE.Vector3 {
  const sinPhi = Math.sin(cam.phi)
  return new THREE.Vector3(
    cam.target.x + cam.radius * sinPhi * Math.sin(cam.theta),
    cam.target.y + cam.radius * Math.cos(cam.phi),
    cam.target.z + cam.radius * sinPhi * Math.cos(cam.theta),
  )
}

function Viewport3D({
  width,
  height,
  depth,
  groundPlaneEnabled,
  groundPlaneSquares,
  walls,
  selected,
  onSelect,
  onChange,
  onCommit,
  editMode,
  spawner,
  onSpawnerChange,
}: {
  width: number
  height: number
  depth: number
  groundPlaneEnabled: boolean
  groundPlaneSquares: number
  walls: ShankpitWall[]
  selected: number | null
  onSelect: (i: number | null) => void
  onChange: (walls: ShankpitWall[]) => void
  onCommit: () => void
  editMode: EditMode
  spawner: { x: number; y: number; z: number }
  onSpawnerChange: (s: { x: number; y: number; z: number }) => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const rendererRef = useRef<THREE.WebGLRenderer | null>(null)
  const sceneRef = useRef<THREE.Scene | null>(null)
  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null)
  const meshesRef = useRef<THREE.Mesh[]>([])
  const spawnerMeshRef = useRef<THREE.Mesh | null>(null)
  const gridRef = useRef<THREE.GridHelper | null>(null)
  const wallsRef = useRef(walls)
  const selectedRef = useRef(selected)
  const editModeRef = useRef(editMode)
  const spawnerRef = useRef(spawner)
  const camStateRef = useRef<CameraState>({
    target: new THREE.Vector3(0, height / 4, 0),
    radius: Math.max(width, depth, height) * 1.1 + 5,
    theta: Math.PI / 4,
    phi: Math.PI / 3,
  })
  const dragRef = useRef<
    | { mode: 'orbit'; lastX: number; lastY: number }
    | { mode: 'face'; hit: FaceHit; linePoint: THREE.Vector3; axisDir: THREE.Vector3; startT: number; startWall: ShankpitWall }
    | { mode: 'move-wall'; wallIndex: number; plane: THREE.Plane; grabOffset: THREE.Vector3; startWall: ShankpitWall }
    | { mode: 'move-spawner'; plane: THREE.Plane; grabOffset: THREE.Vector3 }
    | null
  >(null)

  wallsRef.current = walls
  selectedRef.current = selected
  editModeRef.current = editMode
  spawnerRef.current = spawner

  // One-time scene/renderer/camera setup.
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x11141a)
    sceneRef.current = scene

    const camera = new THREE.PerspectiveCamera(55, 1, 0.1, 5000)
    cameraRef.current = camera

    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    container.appendChild(renderer.domElement)
    rendererRef.current = renderer

    scene.add(new THREE.HemisphereLight(0xffffff, 0x222233, 1.1))
    const sun = new THREE.DirectionalLight(0xffffff, 0.6)
    sun.position.set(30, 60, 20)
    scene.add(sun)

    // The real ground plane grid itself is owned by a separate effect below (driven by
    // groundPlaneEnabled/groundPlaneSquares, which can change without a full scene re-init) --
    // nothing created here.

    // The spawner -- founder real-time: "there should be a spawner object that you can move
    // around... fixes the problem of cubes spawning on eachother." A real, movable marker (not
    // level data, not saved -- purely a per-session authoring convenience, same "authoring
    // metadata only" role BRAWLPIT's own Guides play, just not persisted at all here since v0
    // doesn't need it to survive a reload). New cubes spawn at its current position.
    const spawnerMesh = new THREE.Mesh(
      new THREE.OctahedronGeometry(0.8),
      new THREE.MeshStandardMaterial({ color: 0xffcc33, emissive: 0x554400, emissiveIntensity: 0.6, wireframe: false }),
    )
    spawnerMesh.position.set(spawnerRef.current.x, spawnerRef.current.y, spawnerRef.current.z)
    scene.add(spawnerMesh)
    spawnerMeshRef.current = spawnerMesh

    let raf = 0
    const render = () => {
      const cam = camStateRef.current
      camera.position.copy(cameraPositionFrom(cam))
      camera.lookAt(cam.target)
      const rect = container.getBoundingClientRect()
      const w = Math.max(1, rect.width)
      const h = Math.max(1, rect.height)
      if (renderer.domElement.width !== Math.round(w * renderer.getPixelRatio()) || renderer.domElement.height !== Math.round(h * renderer.getPixelRatio())) {
        renderer.setSize(w, h)
        camera.aspect = w / h
        camera.updateProjectionMatrix()
      }
      renderer.render(scene, camera)
      raf = requestAnimationFrame(render)
    }
    raf = requestAnimationFrame(render)

    const raycaster = new THREE.Raycaster()
    const ndc = new THREE.Vector2()
    const setNdcFromEvent = (e: PointerEvent) => {
      const rect = container.getBoundingClientRect()
      ndc.x = ((e.clientX - rect.left) / rect.width) * 2 - 1
      ndc.y = -((e.clientY - rect.top) / rect.height) * 2 + 1
    }

    // cameraFacingPlaneThrough builds a plane facing the camera (normal = camera's own forward
    // direction) passing through `point` -- the standard "billboard drag" technique free 3D
    // object translation uses: intersecting the mouse ray against this plane each frame gives an
    // intuitive "the object follows the cursor" feel from any camera angle, unlike a
    // ground-plane-only drag which breaks down when looking straight down.
    const cameraFacingPlaneThrough = (point: THREE.Vector3) => {
      const normal = new THREE.Vector3()
      camera.getWorldDirection(normal)
      return new THREE.Plane().setFromNormalAndCoplanarPoint(normal, point)
    }

    const onPointerDown = (e: PointerEvent) => {
      container.setPointerCapture(e.pointerId)
      setNdcFromEvent(e)
      raycaster.setFromCamera(ndc, camera)

      if (editModeRef.current === 'face') {
        const hits = raycaster.intersectObjects(meshesRef.current, false)
        if (hits.length > 0 && hits[0].face) {
          const mesh = hits[0].object as THREE.Mesh
          const wallIndex = meshesRef.current.indexOf(mesh)
          const hit = faceHitFromNormal(wallIndex, hits[0].face.normal.clone())
          const wall = wallsRef.current[wallIndex]
          onSelect(wall.id)
          const axisDir = new THREE.Vector3(hit.axis === 'x' ? 1 : 0, hit.axis === 'y' ? 1 : 0, hit.axis === 'z' ? 1 : 0)
          const startT = hits[0].point.clone().dot(axisDir) // coordinate along the axis at the hit point
          dragRef.current = { mode: 'face', hit, linePoint: hits[0].point.clone(), axisDir, startT, startWall: { ...wall } }
          return
        }
        onSelect(null)
        dragRef.current = { mode: 'orbit', lastX: e.clientX, lastY: e.clientY }
        return
      }

      // object mode: whole-cube (or spawner) translation, not a face reshape
      const candidates = spawnerMeshRef.current ? [spawnerMeshRef.current, ...meshesRef.current] : meshesRef.current
      const hits = raycaster.intersectObjects(candidates, false)
      if (hits.length > 0) {
        const mesh = hits[0].object as THREE.Mesh
        if (mesh === spawnerMeshRef.current) {
          const plane = cameraFacingPlaneThrough(mesh.position.clone())
          const grabOffset = new THREE.Vector3().subVectors(mesh.position, hits[0].point)
          dragRef.current = { mode: 'move-spawner', plane, grabOffset }
          return
        }
        const wallIndex = meshesRef.current.indexOf(mesh)
        const wall = wallsRef.current[wallIndex]
        onSelect(wall.id)
        const plane = cameraFacingPlaneThrough(mesh.position.clone())
        const grabOffset = new THREE.Vector3().subVectors(mesh.position, hits[0].point)
        dragRef.current = { mode: 'move-wall', wallIndex, plane, grabOffset, startWall: { ...wall } }
        return
      }
      onSelect(null)
      dragRef.current = { mode: 'orbit', lastX: e.clientX, lastY: e.clientY }
    }

    const onPointerMove = (e: PointerEvent) => {
      const drag = dragRef.current
      if (!drag) return
      if (drag.mode === 'orbit') {
        const dx = e.clientX - drag.lastX
        const dy = e.clientY - drag.lastY
        drag.lastX = e.clientX
        drag.lastY = e.clientY
        const cam = camStateRef.current
        cam.theta -= dx * 0.008
        cam.phi = Math.min(Math.PI - 0.05, Math.max(0.05, cam.phi - dy * 0.008))
        return
      }
      if (drag.mode === 'face') {
        setNdcFromEvent(e)
        raycaster.setFromCamera(ndc, camera)
        const tc = closestPointOnAxisLineToRay(raycaster.ray.origin, raycaster.ray.direction, drag.linePoint, drag.axisDir)
        if (tc === null) return
        const newFaceCoord = drag.startT + tc
        const updated = applyFaceDrag(drag.startWall, drag.hit.axis, drag.hit.sign, newFaceCoord)
        const next = wallsRef.current.slice()
        next[drag.hit.wallIndex] = updated
        onChange(next)
        return
      }
      // move-wall / move-spawner: intersect the current ray against the drag's own camera-facing
      // plane, re-applying the original grab offset so the object doesn't jump to snap its own
      // center onto the cursor the instant a drag starts.
      setNdcFromEvent(e)
      raycaster.setFromCamera(ndc, camera)
      const hitPoint = new THREE.Vector3()
      if (!raycaster.ray.intersectPlane(drag.plane, hitPoint)) return
      const newPos = hitPoint.add(drag.grabOffset)
      if (drag.mode === 'move-spawner') {
        onSpawnerChange({ x: newPos.x, y: newPos.y, z: newPos.z })
        return
      }
      const updated = { ...drag.startWall, x: newPos.x, y: newPos.y, z: newPos.z }
      const next = wallsRef.current.slice()
      next[drag.wallIndex] = updated
      onChange(next)
    }

    const onPointerUp = (e: PointerEvent) => {
      container.releasePointerCapture(e.pointerId)
      const wasWallDrag = dragRef.current?.mode === 'face' || dragRef.current?.mode === 'move-wall'
      dragRef.current = null
      if (wasWallDrag) onCommit()
    }

    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const cam = camStateRef.current
      cam.radius = Math.max(2, cam.radius * (1 + e.deltaY * 0.001))
    }

    container.addEventListener('pointerdown', onPointerDown)
    container.addEventListener('pointermove', onPointerMove)
    container.addEventListener('pointerup', onPointerUp)
    container.addEventListener('wheel', onWheel, { passive: false })

    return () => {
      cancelAnimationFrame(raf)
      container.removeEventListener('pointerdown', onPointerDown)
      container.removeEventListener('pointermove', onPointerMove)
      container.removeEventListener('pointerup', onPointerUp)
      container.removeEventListener('wheel', onWheel)
      renderer.dispose()
      container.removeChild(renderer.domElement)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one-time setup; walls/selection are read via refs above
  }, [])

  // Rebuild wall meshes whenever the wall list itself changes shape/count; cheap to just rebuild
  // for v0's own real, small (<=100) wall counts rather than diffing.
  useEffect(() => {
    const scene = sceneRef.current
    if (!scene) return
    for (const m of meshesRef.current) {
      scene.remove(m)
      m.geometry.dispose()
      ;(m.material as THREE.Material).dispose()
    }
    meshesRef.current = walls.map((w) => {
      const geo = new THREE.BoxGeometry(w.sx, w.sy, w.sz)
      const mat = new THREE.MeshStandardMaterial({ color: new THREE.Color(w.r, w.g, w.b) })
      const mesh = new THREE.Mesh(geo, mat)
      mesh.position.set(w.x, w.y, w.z)
      scene.add(mesh)
      return mesh
    })
    applySelectionOutline()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [walls.length])

  // Keep existing meshes' transform/color in sync on every wall edit (face-drag, inspector edits)
  // without a full rebuild -- rebuild only reshuffles/recreates geometry when the COUNT changes.
  useEffect(() => {
    walls.forEach((w, i) => {
      const mesh = meshesRef.current[i]
      if (!mesh) return
      mesh.position.set(w.x, w.y, w.z)
      const params = (mesh.geometry as THREE.BoxGeometry).parameters
      if (params.width !== w.sx || params.height !== w.sy || params.depth !== w.sz) {
        mesh.geometry.dispose()
        mesh.geometry = new THREE.BoxGeometry(w.sx, w.sy, w.sz)
      }
      ;(mesh.material as THREE.MeshStandardMaterial).color.setRGB(w.r, w.g, w.b)
    })
    applySelectionOutline()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [walls, selected])

  // Sync the spawner mesh's own visual position whenever it moves (drag, or a fresh level load).
  useEffect(() => {
    spawnerMeshRef.current?.position.set(spawner.x, spawner.y, spawner.z)
  }, [spawner])

  // The real ground plane (S459-08, founder real-time: "i want there to be a plane by default
  // that the player collides with - the checkerboard in the level editor - that should
  // constitute the plane for that level... configurable in terms of size... turn on able and off
  // able per level"). Rebuilt whenever enabled/squares changes -- divisions === squares so every
  // cell is EXACTLY SHANKPIT_GRID_CELL_SIZE world units ("the squares are always the same size"),
  // matching SHANKPIT's own real, existing floor-grid convention (apps/lobby's own real
  // `#define GRID_SIZE 50.0f`) rather than an arbitrary editor-only size.
  useEffect(() => {
    const scene = sceneRef.current
    if (!scene) return
    if (gridRef.current) {
      scene.remove(gridRef.current)
      gridRef.current.geometry.dispose()
      ;(gridRef.current.material as THREE.Material).dispose()
      gridRef.current = null
    }
    if (groundPlaneEnabled && groundPlaneSquares > 0) {
      const size = groundPlaneSquares * SHANKPIT_GRID_CELL_SIZE
      const grid = new THREE.GridHelper(size, groundPlaneSquares, 0x444a58, 0x2a2e38)
      scene.add(grid)
      gridRef.current = grid
    }
  }, [groundPlaneEnabled, groundPlaneSquares])

  function applySelectionOutline() {
    walls.forEach((w, i) => {
      const mesh = meshesRef.current[i]
      if (!mesh) return
      const mat = mesh.material as THREE.MeshStandardMaterial
      mat.emissive = new THREE.Color(w.id === selected ? 0x3355ff : 0x000000)
      mat.emissiveIntensity = w.id === selected ? 0.5 : 0
    })
  }

  return <div ref={containerRef} className="shankpit-viewport" />
}

function WallInspector({ wall, onChange, onDelete }: { wall: ShankpitWall; onChange: (w: ShankpitWall) => void; onDelete: () => void }) {
  const num = (v: string) => (v === '' || v === '-' ? 0 : Number(v))
  const field = (label: string, key: keyof ShankpitWall, opts: { min?: number; step?: number } = {}) => (
    <label>
      {label}{' '}
      <input
        type="number"
        step={opts.step ?? 0.5}
        min={opts.min}
        value={wall[key] as number}
        onChange={(e) => onChange({ ...wall, [key]: opts.min !== undefined ? Math.max(opts.min, num(e.target.value)) : num(e.target.value) })}
      />
    </label>
  )
  return (
    <div className="platform-inspector">
      <h3>Selected cube</h3>
      {field('X', 'x')}
      {field('Y', 'y')}
      {field('Z', 'z')}
      {field('Size X', 'sx', { min: MIN_WALL_SIZE })}
      {field('Size Y', 'sy', { min: MIN_WALL_SIZE })}
      {field('Size Z', 'sz', { min: MIN_WALL_SIZE })}
      {field('R', 'r', { min: 0, step: 0.05 })}
      {field('G', 'g', { min: 0, step: 0.05 })}
      {field('B', 'b', { min: 0, step: 0.05 })}
      {field('Friction', 'friction', { min: 0, step: 0.05 })}
      <button className="danger" type="button" onClick={onDelete}>
        Delete cube
      </button>
    </div>
  )
}

export default function ShankpitLevelEditor() {
  const { list, refresh } = useLevelList()
  const [activeId, setActiveId] = useState<number | null>(null)
  const [draft, setDraft] = useState(newDefaultLevel())
  const [selected, setSelected] = useState<number | null>(null)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [editMode, setEditMode] = useState<EditMode>('object')
  // The spawner is deliberately NOT level data -- a per-session authoring convenience only (see
  // Viewport3D's own doc comment on the spawner mesh), so it isn't part of `draft`/persisted with
  // the level; it just resets to a sensible default on new/load.
  const [spawner, setSpawner] = useState(defaultSpawnerPos())

  const load = useCallback(async (id: number) => {
    const lvl = await shankpitLevels.get(id)
    setDraft({
      name: lvl.name,
      width: lvl.width,
      height: lvl.height,
      depth: lvl.depth,
      groundPlaneEnabled: lvl.ground_plane_enabled,
      groundPlaneSquares: lvl.ground_plane_squares,
      walls: lvl.walls,
    })
    setActiveId(id)
    setSelected(null)
    setDirty(false)
    setError(null)
    setSpawner(defaultSpawnerPos())
  }, [])

  const startNew = () => {
    setDraft(newDefaultLevel())
    setActiveId(null)
    setSelected(1)
    setDirty(true)
    setError(null)
    setSpawner(defaultSpawnerPos())
  }

  const setWalls = (walls: ShankpitWall[]) => {
    setDraft((d) => ({ ...d, walls }))
    setDirty(true)
  }

  const addCube = () => {
    const id = nextWallId(draft.walls)
    // Spawns at the spawner's own current position, not a fixed default -- founder real-time:
    // "fixes the problem of cubes spawning on eachother you can move the cube spawner if you are
    // building right in the middle."
    setWalls([...draft.walls, aDefaultWall(id, spawner)])
    setSelected(id)
  }

  const deleteSelected = () => {
    if (selected === null) return
    setWalls(draft.walls.filter((w) => w.id !== selected))
    setSelected(null)
  }

  const save = async () => {
    setError(null)
    try {
      let id = activeId
      if (id === null) {
        if (!draft.name) {
          setError('Name is required to create a new level.')
          return
        }
        const created = await shankpitLevels.create(
          draft.name,
          draft.width,
          draft.height,
          draft.depth,
          draft.groundPlaneEnabled,
          draft.groundPlaneSquares,
          draft.walls,
        )
        id = created.id
        setActiveId(id)
      } else {
        await shankpitLevels.save(
          id,
          draft.width,
          draft.height,
          draft.depth,
          draft.groundPlaneEnabled,
          draft.groundPlaneSquares,
          draft.walls,
        )
      }
      setDirty(false)
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  const doClone = async () => {
    if (activeId === null || !newName) return
    try {
      const cloned = await shankpitLevels.clone(activeId, newName)
      setNewName('')
      refresh()
      await load(cloned.id)
    } catch (err) {
      setError(String(err))
    }
  }

  const doDelete = async () => {
    if (activeId === null) return
    if (!confirm(`Delete level "${draft.name}"? This can't be undone.`)) return
    await shankpitLevels.delete(activeId)
    setActiveId(null)
    setDraft(newDefaultLevel())
    refresh()
  }

  const selectedWall = selected !== null ? draft.walls.find((w) => w.id === selected) ?? null : null

  return (
    <div className="layout">
      <aside className="project-list">
        <h2>SHANKPIT Levels</h2>
        <ul>
          {list.map((l) => (
            <li key={l.id} className={l.id === activeId ? 'active' : ''}>
              <button onClick={() => load(l.id)}>
                {l.name} <span className="hint">({l.wall_count} cubes)</span>
              </button>
            </li>
          ))}
        </ul>
        <button type="button" onClick={startNew}>
          + New level
        </button>
      </aside>

      <main>
        <div className="project-header">
          <input
            className="level-name-input"
            placeholder="Level name"
            value={draft.name}
            onChange={(e) => {
              setDraft((d) => ({ ...d, name: e.target.value }))
              setDirty(true)
            }}
          />
          <div className="dims">
            <label>
              Width{' '}
              <input
                type="number"
                min={1}
                value={draft.width}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, width: Math.max(1, Number(e.target.value)) }))
                  setDirty(true)
                }}
              />
            </label>
            <label>
              Height{' '}
              <input
                type="number"
                min={1}
                value={draft.height}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, height: Math.max(1, Number(e.target.value)) }))
                  setDirty(true)
                }}
              />
            </label>
            <label>
              Depth{' '}
              <input
                type="number"
                min={1}
                value={draft.depth}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, depth: Math.max(1, Number(e.target.value)) }))
                  setDirty(true)
                }}
              />
            </label>
          </div>
          <div className="dims">
            <label>
              <input
                type="checkbox"
                checked={draft.groundPlaneEnabled}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, groundPlaneEnabled: e.target.checked }))
                  setDirty(true)
                }}
              />{' '}
              Ground plane
            </label>
            <label>
              squares ({SHANKPIT_GRID_CELL_SIZE}u each){' '}
              <input
                type="number"
                min={1}
                disabled={!draft.groundPlaneEnabled}
                value={draft.groundPlaneSquares}
                onChange={(e) => {
                  setDraft((d) => ({ ...d, groundPlaneSquares: Math.max(1, Math.round(Number(e.target.value))) }))
                  setDirty(true)
                }}
              />
            </label>
          </div>
          <div className="mode-toggle">
            <button type="button" className={editMode === 'object' ? 'active' : ''} onClick={() => setEditMode('object')}>
              Object mode
            </button>
            <button type="button" className={editMode === 'face' ? 'active' : ''} onClick={() => setEditMode('face')}>
              Face mode
            </button>
          </div>
          <button type="button" onClick={addCube}>
            + Add cube
          </button>
          <button type="button" onClick={save} disabled={!dirty}>
            {activeId === null ? 'Create level' : 'Save changes'}
          </button>
          {activeId !== null && (
            <>
              <input placeholder="clone as..." value={newName} onChange={(e) => setNewName(e.target.value)} />
              <button type="button" onClick={doClone} disabled={!newName}>
                Clone
              </button>
              <a className="export-link" href={shankpitLevels.exportUrl(activeId)} target="_blank" rel="noreferrer">
                Export JSON
              </a>
              <button className="danger" type="button" onClick={doDelete}>
                Delete
              </button>
            </>
          )}
          {error && <span className="error">{error}</span>}
        </div>

        <div className="editor-body">
          <div className="canvas-pane">
            <Viewport3D
              width={draft.width}
              height={draft.height}
              depth={draft.depth}
              groundPlaneEnabled={draft.groundPlaneEnabled}
              groundPlaneSquares={draft.groundPlaneSquares}
              walls={draft.walls}
              selected={selected}
              onSelect={setSelected}
              onChange={setWalls}
              onCommit={() => setDirty(true)}
              editMode={editMode}
              spawner={spawner}
              onSpawnerChange={setSpawner}
            />
            <p className="hint">
              Drag empty space to orbit, scroll to zoom.{' '}
              {editMode === 'object'
                ? "Object mode: drag a cube to move it, drag the yellow spawner marker to reposition it. New cubes spawn at the marker."
                : "Face mode: drag a cube's face to reshape it."}{' '}
              Click a cube to select it.
            </p>
          </div>
          <div className="side-pane">
            {selectedWall ? (
              <WallInspector
                wall={selectedWall}
                onChange={(w) => {
                  const next = draft.walls.map((ow) => (ow.id === w.id ? w : ow))
                  setWalls(next)
                }}
                onDelete={deleteSelected}
              />
            ) : (
              <p className="hint">Select a cube to edit its exact position/size, or add a new one.</p>
            )}
          </div>
        </div>
      </main>
    </div>
  )
}
