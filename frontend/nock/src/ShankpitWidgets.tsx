import { useCallback, useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import {
  doorScripts,
  shankpitMaterials,
  shankpitWidgets,
  type DoorScriptSummary,
  type ShankpitDoor,
  type ShankpitMaterial,
  type ShankpitWall,
  type ShankpitWidgetSummary,
} from './api'
import { WallInspector, cameraPositionFrom, type CameraState } from './ShankpitLevelEditor'

// ShankpitWidgets.tsx -- S482, founder real-time, direct correction of the earlier S479-follow-up
// door-composition work: "i still dont know how to add a door... i keep asking for an actual
// object viewer i dont want to make doors be levels please - make widget or something they are
// both objects but widgets just dont show up in the levels menu and the geometry of the widget
// shows up not the geometry of the underlying level under the widget - there should be no ground
// plane and no dimension in the widget - a level is a dimension - a widget is just a widget."
//
// A real, separate top-level NOCK tab (not the Levels menu) -- a Widget is walls+doors only, no
// dimension, no ground plane, no Objects of its own. Reuses WallInspector/cameraPositionFrom
// directly from ShankpitLevelEditor.tsx (identical wall-editing concerns) rather than forking
// them. Real, deliberate v0 scope cut from the level editor's own viewport: object-mode
// select-and-drag only, no face-reshape-by-dragging (a widget's own cube dimensions are edited
// numerically via WallInspector instead) -- a widget typically holds a handful of small pieces
// (a door plus its frame), not a whole level's worth of geometry, so the extra interaction
// complexity isn't earning its own keep yet.

function nextWallId(walls: ShankpitWall[]): number {
  return walls.reduce((m, w) => Math.max(m, w.id), 0) + 1
}

function aDefaultWall(id: number): ShankpitWall {
  return { id, x: 0, y: 2, z: 0, sx: 4, sy: 4, sz: 4, r: 0.6, g: 0.6, b: 0.65, friction: 0.3 }
}

function useWidgetList() {
  const [list, setList] = useState<ShankpitWidgetSummary[]>([])
  const refresh = useCallback(() => {
    shankpitWidgets.list().then(setList).catch(() => setList([]))
  }, [])
  useEffect(() => {
    refresh()
  }, [refresh])
  return { list, refresh }
}

function useMaterialList() {
  const [materials, setMaterials] = useState<ShankpitMaterial[]>([])
  useEffect(() => {
    shankpitMaterials.list().then(setMaterials).catch(() => setMaterials([]))
  }, [])
  return materials
}

function useDoorScriptList() {
  const [scripts, setScripts] = useState<DoorScriptSummary[]>([])
  useEffect(() => {
    doorScripts.list().then(setScripts).catch(() => setScripts([]))
  }, [])
  return scripts
}

function WidgetViewport3D({
  walls,
  selected,
  onSelect,
  onChange,
  onCommit,
}: {
  walls: ShankpitWall[]
  selected: number | null
  onSelect: (id: number | null) => void
  onChange: (walls: ShankpitWall[]) => void
  onCommit: () => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const sceneRef = useRef<THREE.Scene | null>(null)
  const meshesRef = useRef<THREE.Mesh[]>([])
  const wallsRef = useRef(walls)
  const selectedRef = useRef(selected)
  const camStateRef = useRef<CameraState>({ target: new THREE.Vector3(0, 2, 0), radius: 20, theta: Math.PI / 4, phi: Math.PI / 3 })
  const dragRef = useRef<
    | { mode: 'orbit'; lastX: number; lastY: number }
    | { mode: 'move-wall'; wallIndex: number; plane: THREE.Plane; grabOffset: THREE.Vector3 }
    | null
  >(null)

  wallsRef.current = walls
  selectedRef.current = selected

  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x11141a)
    sceneRef.current = scene

    const camera = new THREE.PerspectiveCamera(55, 1, 0.1, 5000)
    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    container.appendChild(renderer.domElement)

    scene.add(new THREE.HemisphereLight(0xffffff, 0x222233, 1.1))
    const sun = new THREE.DirectionalLight(0xffffff, 0.6)
    sun.position.set(30, 60, 20)
    scene.add(sun)

    const grid = new THREE.GridHelper(40, 8, 0x334455, 0x223344)
    scene.add(grid)

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
    const cameraFacingPlaneThrough = (point: THREE.Vector3) => {
      const normal = new THREE.Vector3()
      camera.getWorldDirection(normal)
      return new THREE.Plane().setFromNormalAndCoplanarPoint(normal, point)
    }

    const onPointerDown = (e: PointerEvent) => {
      container.setPointerCapture(e.pointerId)
      setNdcFromEvent(e)
      raycaster.setFromCamera(ndc, camera)
      const hits = raycaster.intersectObjects(meshesRef.current, false)
      if (hits.length > 0) {
        const mesh = hits[0].object as THREE.Mesh
        const wallIndex = meshesRef.current.indexOf(mesh)
        const wall = wallsRef.current[wallIndex]
        onSelect(wall.id)
        const plane = cameraFacingPlaneThrough(mesh.position.clone())
        const grabOffset = new THREE.Vector3().subVectors(mesh.position, hits[0].point)
        onCommit()
        dragRef.current = { mode: 'move-wall', wallIndex, plane, grabOffset }
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
      setNdcFromEvent(e)
      raycaster.setFromCamera(ndc, camera)
      const hitPoint = new THREE.Vector3()
      if (!raycaster.ray.intersectPlane(drag.plane, hitPoint)) return
      const newPos = hitPoint.add(drag.grabOffset)
      const next = wallsRef.current.slice()
      next[drag.wallIndex] = { ...next[drag.wallIndex], x: newPos.x, y: newPos.y, z: newPos.z }
      onChange(next)
    }

    const onPointerUp = (e: PointerEvent) => {
      container.releasePointerCapture(e.pointerId)
      dragRef.current = null
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [walls.length])

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
      ;(mesh.material as THREE.MeshStandardMaterial).emissive.setHex(w.id === selected ? 0x333333 : 0x000000)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [walls, selected])

  return <div ref={containerRef} className="shankpit-viewport" />
}

export default function ShankpitWidgets() {
  const { list, refresh } = useWidgetList()
  const materials = useMaterialList()
  const doorScriptList = useDoorScriptList()

  const [activeId, setActiveId] = useState<number | null>(null)
  const [name, setName] = useState('')
  const [walls, setWallsState] = useState<ShankpitWall[]>([])
  const [doors, setDoorsState] = useState<ShankpitDoor[]>([])
  const [selected, setSelected] = useState<number | null>(null)
  const [dirty, setDirty] = useState(false)
  const [newName, setNewName] = useState('')
  const [error, setError] = useState<string | null>(null)

  const load = async (id: number) => {
    setError(null)
    const w = await shankpitWidgets.get(id)
    setActiveId(w.id)
    setName(w.name)
    setWallsState(w.walls)
    setDoorsState(w.doors)
    setSelected(null)
    setDirty(false)
  }

  const setWalls = (next: ShankpitWall[]) => {
    setWallsState(next)
    setDirty(true)
  }
  const setDoors = (next: ShankpitDoor[]) => {
    setDoorsState(next)
    setDirty(true)
  }

  const createWidget = async () => {
    if (!newName) return
    setError(null)
    try {
      const w = await shankpitWidgets.create(newName, [], [])
      setNewName('')
      refresh()
      await load(w.id)
    } catch (err) {
      setError(String(err))
    }
  }

  const save = async () => {
    if (activeId === null) return
    setError(null)
    try {
      await shankpitWidgets.save(activeId, walls, doors)
      setDirty(false)
      refresh()
    } catch (err) {
      setError(String(err))
    }
  }

  const deleteActive = async () => {
    if (activeId === null) return
    if (!window.confirm(`Delete widget "${name}"? Any level object still referencing it will fail to export.`)) return
    await shankpitWidgets.delete(activeId)
    setActiveId(null)
    refresh()
  }

  const addCube = () => {
    const id = nextWallId(walls)
    setWalls([...walls, aDefaultWall(id)])
    setSelected(id)
  }

  const deleteSelected = () => {
    if (selected === null) return
    setWalls(walls.filter((w) => w.id !== selected))
    setDoors(doors.filter((d) => d.wall_id !== selected))
    setSelected(null)
  }

  const setWallDoor = (wallId: number, scriptId: number | null) => {
    const existing = doors.find((d) => d.wall_id === wallId)
    if (scriptId === null) {
      if (existing) setDoors(doors.filter((d) => d.wall_id !== wallId))
      return
    }
    if (existing) {
      setDoors(doors.map((d) => (d.wall_id === wallId ? { ...d, script_id: scriptId } : d)))
    } else {
      const id = doors.reduce((m, d) => Math.max(m, d.id), 0) + 1
      setDoors([...doors, { id, wall_id: wallId, script_id: scriptId }])
    }
  }

  const selectedWall = walls.find((w) => w.id === selected) ?? null

  return (
    <div className="layout">
      <aside className="project-list">
        <h2>Widgets</h2>
        <p className="hint">
          Real, reusable geometry+doors -- no dimension, no ground plane, never listed under Levels. Place one into a
          level via that level's own Objects panel.
        </p>
        <ul>
          {list.map((w) => (
            <li key={w.id} className={activeId === w.id ? 'active' : ''}>
              <button onClick={() => load(w.id)}>
                {w.name}{' '}
                <span className="hint">
                  ({w.wall_count} cube{w.wall_count === 1 ? '' : 's'}, {w.door_count} door{w.door_count === 1 ? '' : 's'})
                </span>
              </button>
            </li>
          ))}
        </ul>
        <input placeholder="new widget name" value={newName} onChange={(e) => setNewName(e.target.value)} />
        <button type="button" onClick={createWidget} disabled={!newName}>
          + New widget
        </button>
      </aside>

      <main>
        {activeId === null ? (
          <p className="hint">Select or create a widget.</p>
        ) : (
          <>
            <div className="project-header">
              <h2>{name}</h2>
              <button type="button" onClick={addCube}>
                + Add cube
              </button>
              <button type="button" onClick={save} disabled={!dirty}>
                {dirty ? 'Save*' : 'Saved'}
              </button>
              <button className="danger" type="button" onClick={deleteActive}>
                Delete widget
              </button>
              {error && <span className="error">{error}</span>}
            </div>
            <div className="editor-body">
              <div className="canvas-pane">
                <WidgetViewport3D walls={walls} selected={selected} onSelect={setSelected} onChange={setWalls} onCommit={() => setDirty(true)} />
              </div>
              {selectedWall && (
                <WallInspector
                  wall={selectedWall}
                  onChange={(w) => setWalls(walls.map((ow) => (ow.id === w.id ? w : ow)))}
                  onDelete={deleteSelected}
                  materials={materials}
                  door={doors.find((d) => d.wall_id === selectedWall.id) ?? null}
                  doorScriptList={doorScriptList}
                  onDoorChange={(scriptId) => setWallDoor(selectedWall.id, scriptId)}
                />
              )}
            </div>
          </>
        )}
      </main>
    </div>
  )
}
