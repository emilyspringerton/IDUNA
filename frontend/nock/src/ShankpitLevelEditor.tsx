import { useCallback, useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import {
  SHANKPIT_GRID_CELL_SIZE,
  shankpitLevels,
  shankpitMaterials,
  type ShankpitLevelObject,
  type ShankpitLevelSummary,
  type ShankpitMaterial,
  type ShankpitWall,
} from './api'

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
  objects: ShankpitLevelObject[]
} {
  return {
    name: '',
    width: 100,
    height: 50,
    depth: 100,
    groundPlaneEnabled: true,
    groundPlaneSquares: DEFAULT_GROUND_PLANE_SQUARES,
    walls: [aDefaultWall(1, defaultSpawnerPos())],
    objects: [],
  }
}

function nextWallId(walls: ShankpitWall[]): number {
  return walls.reduce((m, w) => Math.max(m, w.id), 0) + 1
}

function nextObjectId(objects: ShankpitLevelObject[]): number {
  return objects.reduce((m, o) => Math.max(m, o.id), 0) + 1
}

// ROT_Y_STEPS -- "snap rotate 90 degree turns is good for now" (founder, real-time, S459-15).
const ROT_Y_STEPS = [0, 90, 180, 270] as const

function rotateY90Step(rotY: number): 0 | 90 | 180 | 270 {
  const idx = ROT_Y_STEPS.indexOf(rotY as (typeof ROT_Y_STEPS)[number])
  return ROT_Y_STEPS[(idx + 1) % ROT_Y_STEPS.length]
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

// useMaterialList -- S459-16, founder real-time: "we will need the ability to add new materials
// and set their textures" / "registries for everything". Same real shape as useLevelList above.
function useMaterialList() {
  const [materials, setMaterials] = useState<ShankpitMaterial[]>([])
  const refresh = useCallback(() => {
    shankpitMaterials.list().then(setMaterials).catch(() => setMaterials([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { materials, refresh }
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
  constrainY,
  onDragStart,
  objects,
  levelSummaries,
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
  constrainY: boolean
  onDragStart: () => void
  objects: ShankpitLevelObject[]
  levelSummaries: ShankpitLevelSummary[]
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const rendererRef = useRef<THREE.WebGLRenderer | null>(null)
  const sceneRef = useRef<THREE.Scene | null>(null)
  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null)
  const meshesRef = useRef<THREE.Mesh[]>([])
  const objectMeshesRef = useRef<THREE.Group[]>([])
  const spawnerMeshRef = useRef<THREE.Mesh | null>(null)
  const gridRef = useRef<THREE.GridHelper | null>(null)
  const wallsRef = useRef(walls)
  const selectedRef = useRef(selected)
  const editModeRef = useRef(editMode)
  const spawnerRef = useRef(spawner)
  const onSpawnerChangeRef = useRef(onSpawnerChange)
  const constrainYRef = useRef(constrainY)
  const onDragStartRef = useRef(onDragStart)
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
  onSpawnerChangeRef.current = onSpawnerChange
  constrainYRef.current = constrainY
  onDragStartRef.current = onDragStart

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

    // "i need the blocks to notfly up and down when i drag them around unless i uncheck the
    // constrain z or y or whatever box" -- a horizontal (Y-up-normal) plane through the object's
    // current position instead of the camera-facing plane. Intersecting the mouse ray against
    // this plane keeps the object's Y fixed for the whole drag; only X/Z ever change.
    const horizontalPlaneThrough = (point: THREE.Vector3) => {
      return new THREE.Plane().setFromNormalAndCoplanarPoint(new THREE.Vector3(0, 1, 0), point)
    }
    const dragPlaneThrough = (point: THREE.Vector3) =>
      constrainYRef.current ? horizontalPlaneThrough(point) : cameraFacingPlaneThrough(point)

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
          onDragStartRef.current()
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
          const plane = dragPlaneThrough(mesh.position.clone())
          const grabOffset = new THREE.Vector3().subVectors(mesh.position, hits[0].point)
          dragRef.current = { mode: 'move-spawner', plane, grabOffset }
          return
        }
        const wallIndex = meshesRef.current.indexOf(mesh)
        const wall = wallsRef.current[wallIndex]
        onSelect(wall.id)
        const plane = dragPlaneThrough(mesh.position.clone())
        const grabOffset = new THREE.Vector3().subVectors(mesh.position, hits[0].point)
        onDragStartRef.current()
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

  // WASD moves the spawner -- founder real-time: "in the shankpit level editor can you have wasd
  // move around the spawner?" Camera-relative (matches every FPS/editor convention: W is "toward
  // what the camera is looking at," not a fixed world axis), horizontal-only (spawner.y is
  // untouched, same "constrain Y" spirit already established for object dragging). Skips entirely
  // while a text field has focus so it never steals keystrokes from the level-name/dims/material
  // inputs elsewhere on this page.
  useEffect(() => {
    const SPAWNER_MOVE_STEP = 2
    const onKeyDown = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement | null)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
      const key = e.key.toLowerCase()
      if (key !== 'w' && key !== 'a' && key !== 's' && key !== 'd') return
      e.preventDefault()
      const theta = camStateRef.current.theta
      // Derived from cameraPositionFrom's own convention (camera sits at
      // target + radius*(sinTheta, _, cosTheta)) via forward = normalize(target - camera),
      // right = normalize(cross(forward, up)) -- see this block's own PR description for the
      // by-hand derivation, verified at theta=0 (forward=(0,0,-1), right=(1,0,0), the standard
      // "looking down -Z, right is +X" check).
      const forwardX = -Math.sin(theta)
      const forwardZ = -Math.cos(theta)
      const rightX = Math.cos(theta)
      const rightZ = -Math.sin(theta)
      let dx = 0
      let dz = 0
      if (key === 'w') { dx += forwardX; dz += forwardZ }
      if (key === 's') { dx -= forwardX; dz -= forwardZ }
      if (key === 'd') { dx += rightX; dz += rightZ }
      if (key === 'a') { dx -= rightX; dz -= rightZ }
      const s = spawnerRef.current
      onSpawnerChangeRef.current({ x: s.x + dx * SPAWNER_MOVE_STEP, y: s.y, z: s.z + dz * SPAWNER_MOVE_STEP })
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  // Level objects (S459-15, "a map is a composition of levels"): a real, non-interactive-for-v0
  // wireframe preview of each placed child level's own footprint (its real width/height/depth,
  // looked up from the already-fetched level list -- no extra fetch needed) at its placed
  // position + 90-degree Y rotation. Position/rotation are edited via the inspector panel, not
  // 3D-dragged, matching this feature's own real v0 scope. Rebuilt whenever the object list's own
  // shape changes; kept in sync on every edit via the effect just below.
  useEffect(() => {
    const scene = sceneRef.current
    if (!scene) return
    for (const g of objectMeshesRef.current) {
      scene.remove(g)
      g.children.forEach((c) => {
        if (c instanceof THREE.LineSegments) {
          c.geometry.dispose()
          ;(c.material as THREE.Material).dispose()
        }
      })
    }
    objectMeshesRef.current = objects.map((o) => {
      const ref = levelSummaries.find((l) => l.id === o.ref_level_id)
      const w = ref?.width ?? SHANKPIT_GRID_CELL_SIZE, h = ref?.height ?? SHANKPIT_GRID_CELL_SIZE, d = ref?.depth ?? SHANKPIT_GRID_CELL_SIZE
      const geo = new THREE.BoxGeometry(w, h, d)
      const wire = new THREE.LineSegments(new THREE.EdgesGeometry(geo), new THREE.LineBasicMaterial({ color: 0xffaa33 }))
      geo.dispose()
      const group = new THREE.Group()
      group.add(wire)
      group.position.set(o.x, o.y + h / 2, o.z) // Y offset so the wireframe sits ON o.y (its own floor), not straddling it
      group.rotation.y = -(o.rot_y * Math.PI) / 180
      scene.add(group)
      return group
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [objects.length, levelSummaries.length])

  useEffect(() => {
    objects.forEach((o, i) => {
      const group = objectMeshesRef.current[i]
      if (!group) return
      const ref = levelSummaries.find((l) => l.id === o.ref_level_id)
      const h = ref?.height ?? SHANKPIT_GRID_CELL_SIZE
      group.position.set(o.x, o.y + h / 2, o.z)
      group.rotation.y = -(o.rot_y * Math.PI) / 180
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [objects, levelSummaries])

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

function WallInspector({
  wall,
  onChange,
  onDelete,
  materials,
}: {
  wall: ShankpitWall
  onChange: (w: ShankpitWall) => void
  onDelete: () => void
  materials: ShankpitMaterial[]
}) {
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
      <label>
        Material{' '}
        <select value={wall.material || 'brick'} onChange={(e) => onChange({ ...wall, material: e.target.value })}>
          {materials.length === 0 && <option value="brick">brick</option>}
          {materials.map((m) => (
            <option key={m.id} value={m.name}>
              {m.name}
            </option>
          ))}
        </select>
      </label>
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

// ObjectInspector -- S459-15, founder real-time: "a map is a composition of levels" / "we are
// going to need to be able to rotate objects (levels) in the map, snap rotate 90 degree turns is
// good for now" / "THEIR PLANE NEEDS TO BE TOGGALABLE ON THE MAP SIDE (toggle on and off AND
// toggle visual off - it can be on but invisible) DEFAULTS TO OFF." Position/rotation edited here
// numerically rather than 3D-dragged -- a real, deliberate v0 scope call (see Viewport3D's own
// object-rendering effect doc comment), not a placeholder for missing functionality.
function ObjectInspector({
  obj,
  refName,
  onChange,
  onDelete,
}: {
  obj: ShankpitLevelObject
  refName: string
  onChange: (o: ShankpitLevelObject) => void
  onDelete: () => void
}) {
  const num = (v: string) => (v === '' ? 0 : Number(v))
  return (
    <div className="platform-inspector">
      <h4>{refName}</h4>
      <label>
        X <input type="number" step={0.5} value={obj.x} onChange={(e) => onChange({ ...obj, x: num(e.target.value) })} />
      </label>
      <label>
        Y <input type="number" step={0.5} value={obj.y} onChange={(e) => onChange({ ...obj, y: num(e.target.value) })} />
      </label>
      <label>
        Z <input type="number" step={0.5} value={obj.z} onChange={(e) => onChange({ ...obj, z: num(e.target.value) })} />
      </label>
      <div className="mode-toggle">
        <button type="button" onClick={() => onChange({ ...obj, rot_y: rotateY90Step(obj.rot_y) })}>
          Rotate 90° (now {obj.rot_y}°)
        </button>
      </div>
      <label>
        <input type="checkbox" checked={obj.plane_visible} onChange={(e) => onChange({ ...obj, plane_visible: e.target.checked })} /> Plane
        visible
      </label>
      <label>
        <input type="checkbox" checked={obj.plane_solid} onChange={(e) => onChange({ ...obj, plane_solid: e.target.checked })} /> Plane solid
      </label>
      <button className="danger" type="button" onClick={onDelete}>
        Remove object
      </button>
    </div>
  )
}

// MaterialsPanel -- S459-16, founder real-time: "we will need the ability to add new materials
// and set their textures" / "we will be able to add materials via Nock and set the texture of
// the material from the texture library." Texture-override picking from NOCK's own texture
// library is real, deliberate follow-up (this panel edits specular/shininess -- the real, working
// VS0 shading parameters -- and leaves texture_id null, meaning "use the native procedural
// default for this name"); named here, not silently promised.
function MaterialsPanel({ materials, refresh }: { materials: ShankpitMaterial[]; refresh: () => void }) {
  const [name, setName] = useState('')
  const [specular, setSpecular] = useState(0.05)
  const [shininess, setShininess] = useState(8)
  const [shaderName, setShaderName] = useState('standard')
  const [error, setError] = useState<string | null>(null)

  const add = async () => {
    if (!name) return
    setError(null)
    try {
      await shankpitMaterials.create(name, specular, shininess, null, shaderName)
      setName('')
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  return (
    <div className="material-panel">
      <h2>Materials</h2>
      <ul>
        {materials.map((m) => (
          <li key={m.id}>
            <div className="material-panel-row">
              <span className="material-panel-title">{m.name}</span>
            </div>
            <span className="hint">
              spec {m.specular}, shin {m.shininess}, shader {m.shader_name}
            </span>
          </li>
        ))}
      </ul>
      <input placeholder="new material name" value={name} onChange={(e) => setName(e.target.value)} />
      <label>
        Specular{' '}
        <input type="number" min={0} max={1} step={0.05} value={specular} onChange={(e) => setSpecular(Number(e.target.value))} />
      </label>
      <label>
        Shininess{' '}
        <input type="number" min={1} max={256} step={1} value={shininess} onChange={(e) => setShininess(Number(e.target.value))} />
      </label>
      <label>
        Shader{' '}
        <select value={shaderName} onChange={(e) => setShaderName(e.target.value)}>
          <option value="standard">standard (Blinn-Phong)</option>
          <option value="ips_light">ips_light (emissive panel)</option>
          <option value="hps_light">hps_light (flickering sodium lamp)</option>
        </select>
      </label>
      <button type="button" onClick={add} disabled={!name}>
        + Add material
      </button>
      {error && <span className="error">{error}</span>}
    </div>
  )
}

export default function ShankpitLevelEditor() {
  const { list, refresh } = useLevelList()
  const { materials, refresh: refreshMaterials } = useMaterialList()
  const [activeId, setActiveId] = useState<number | null>(null)
  const [draft, setDraft] = useState(newDefaultLevel())
  const [selected, setSelected] = useState<number | null>(null)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [newName, setNewName] = useState('')
  const [objectPickLevelId, setObjectPickLevelId] = useState<number | ''>('')
  const [editMode, setEditMode] = useState<EditMode>('object')
  // Constrain Y while dragging in object mode -- founder real-time: "i need the blocks to notfly
  // up and down when i drag them around unless i uncheck the constrain z or y or whatever box" /
  // "thats really important." Checked (constrained) by default -- dragging a cube glides it along
  // its own current height instead of free 3D movement, so leveling walls to the ground doesn't
  // require fighting the drag plane.
  const [constrainY, setConstrainY] = useState(true)
  // The spawner is deliberately NOT level data -- a per-session authoring convenience only (see
  // Viewport3D's own doc comment on the spawner mesh), so it isn't part of `draft`/persisted with
  // the level; it just resets to a sensible default on new/load.
  const [spawner, setSpawner] = useState(defaultSpawnerPos())

  // Undo/redo (founder real-time: "I ALSO NEED REDO... thats really important"). A plain, real
  // history-stack of past draft snapshots -- deliberately NOT Redux: this app has an explicit,
  // already-established "no framework beyond React itself for a v0 this small" precedent
  // (NOCK_NORTHSTAR.md's own real "no Redux" call, matching LevelEditor.tsx's own doc comment),
  // and undo/redo needs nothing Redux provides that a plain array + two setState calls doesn't
  // already give here. draftRef mirrors the established wallsRef/selectedRef pattern elsewhere in
  // this file -- lets pushHistory (passed into Viewport3D's own one-time-setup effect) always read
  // the CURRENT draft at call time without needing the callback reference itself to be stable.
  const [history, setHistory] = useState<typeof draft[]>([])
  const [future, setFuture] = useState<typeof draft[]>([])
  const draftRef = useRef(draft)
  draftRef.current = draft

  const pushHistory = useCallback(() => {
    setHistory((h) => [...h, draftRef.current])
    setFuture([])
  }, [])

  const undo = useCallback(() => {
    setHistory((h) => {
      if (h.length === 0) return h
      const prev = h[h.length - 1]
      setFuture((f) => [draftRef.current, ...f])
      setDraft(prev)
      setDirty(true)
      return h.slice(0, -1)
    })
  }, [])

  const redo = useCallback(() => {
    setFuture((f) => {
      if (f.length === 0) return f
      const next = f[0]
      setHistory((h) => [...h, draftRef.current])
      setDraft(next)
      setDirty(true)
      return f.slice(1)
    })
  }, [])

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.key.toLowerCase() !== 'z') return
      e.preventDefault()
      if (e.shiftKey) redo()
      else undo()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [undo, redo])

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
      objects: lvl.objects,
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
    pushHistory()
    const id = nextWallId(draft.walls)
    // Spawns at the spawner's own current position, not a fixed default -- founder real-time:
    // "fixes the problem of cubes spawning on eachother you can move the cube spawner if you are
    // building right in the middle."
    setWalls([...draft.walls, aDefaultWall(id, spawner)])
    setSelected(id)
  }

  const deleteSelected = () => {
    if (selected === null) return
    pushHistory()
    setWalls(draft.walls.filter((w) => w.id !== selected))
    setSelected(null)
  }

  const setObjects = (objects: ShankpitLevelObject[]) => {
    setDraft((d) => ({ ...d, objects }))
    setDirty(true)
  }

  // addObject -- S459-15, founder real-time: "i have this level 2222 ... i want to use it as an
  // object - the whole level" / "a map is a composition of levels." Placed at the spawner's own
  // current position (same "no accidental overlap" convenience addCube already gives cubes),
  // unrotated -- rotate afterward via the inspector's own "Rotate 90°" button.
  const addObject = (refLevelId: number) => {
    pushHistory()
    const id = nextObjectId(draft.objects)
    setObjects([
      ...draft.objects,
      { id, ref_level_id: refLevelId, x: spawner.x, y: spawner.y, z: spawner.z, rot_y: 0, plane_visible: false, plane_solid: false },
    ])
  }

  const updateObject = (updated: ShankpitLevelObject) => {
    setObjects(draft.objects.map((o) => (o.id === updated.id ? updated : o)))
  }

  const deleteObject = (id: number) => {
    pushHistory()
    setObjects(draft.objects.filter((o) => o.id !== id))
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
          draft.objects,
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
          draft.objects,
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
        <MaterialsPanel materials={materials} refresh={refreshMaterials} />
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
          <label className="constrain-y">
            <input type="checkbox" checked={constrainY} onChange={(e) => setConstrainY(e.target.checked)} />{' '}
            Constrain Y while dragging
          </label>
          <div className="mode-toggle">
            <button type="button" onClick={undo} disabled={history.length === 0} title="Undo (Ctrl+Z)">
              Undo
            </button>
            <button type="button" onClick={redo} disabled={future.length === 0} title="Redo (Ctrl+Shift+Z)">
              Redo
            </button>
          </div>
          <button type="button" onClick={addCube}>
            + Add cube
          </button>
          <div className="mode-toggle">
            <select value={objectPickLevelId} onChange={(e) => setObjectPickLevelId(e.target.value === '' ? '' : Number(e.target.value))}>
              <option value="">Add level as object...</option>
              {list
                .filter((l) => l.id !== activeId)
                .map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                  </option>
                ))}
            </select>
            <button
              type="button"
              disabled={objectPickLevelId === ''}
              onClick={() => {
                if (objectPickLevelId !== '') addObject(objectPickLevelId)
              }}
            >
              + Add object
            </button>
          </div>
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
              constrainY={constrainY}
              onDragStart={pushHistory}
              objects={draft.objects}
              levelSummaries={list}
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
                materials={materials}
              />
            ) : (
              <p className="hint">Select a cube to edit its exact position/size, or add a new one.</p>
            )}
            {draft.objects.length > 0 && (
              <div className="object-list">
                <h3>Objects (levels placed as objects)</h3>
                {draft.objects.map((o) => (
                  <ObjectInspector
                    key={o.id}
                    obj={o}
                    refName={list.find((l) => l.id === o.ref_level_id)?.name ?? `level ${o.ref_level_id}`}
                    onChange={updateObject}
                    onDelete={() => deleteObject(o.id)}
                  />
                ))}
              </div>
            )}
          </div>
        </div>
      </main>
    </div>
  )
}
