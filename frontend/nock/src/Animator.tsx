// Animator.tsx — keyframe animation authoring in NOCK (founder real-time, 2026-09-27: "continue to
// evolve NOCK tools into a total blender replacement we need a way to animate in NOCK").
//
// Works like Blender's pose mode + dope sheet, on any library row that has a rig:
//   - click a joint (the dots) or pick it from the list; drag the gizmo to pose it
//     (R rotate, G move, like Blender);
//   - with auto-key on (default) every pose change keys that joint at the current tick; with it
//     off, press I to insert a key;
//   - the timeline shows every key; click to jump, X/Delete removes the selected joint's key at
//     the playhead, the dropdown sets how the segment after a key interpolates;
//   - Space plays, ←/→ step a tick, Home/End jump to the ends.
//
// The keyframe doc is the source of truth. The server bakes it to a .gband and stores the doc
// next to it (internal/nock/keyframes.go + anim_rig.go), so an authored clip reopens with its
// keys. An imported clip opens with keys derived from its baked data (one every 5 ticks), and is
// only ever saved as a NEW row, so imports are never overwritten. Preview posing uses
// keyframes.ts, the browser twin of the Go baker, sampled per integer tick like the bake.
import { useCallback, useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { TransformControls } from 'three/examples/jsm/controls/TransformControls.js'
import { animations, type AnimationSummary } from './api'
import { buildBones, parseGMesh, parseGSkel, type ParsedJoint } from './goldenband'
import {
  clampToDuration,
  deleteKeysAt,
  evalRotation,
  evalTranslation,
  keyTicks,
  setInterpAt,
  setKey,
  type Interp,
  type KeyframeDoc,
} from './keyframes'

interface Props {
  animation: AnimationSummary
  onClose: () => void
  onSaved: () => void
}

const RAD2DEG = 180 / Math.PI
const DEG2RAD = Math.PI / 180
const JOINT_COLOR = 0x5ab0ff
const JOINT_SELECTED = 0xffb040
const JOINT_KEYED = 0x7ee07e

export default function Animator({ animation, onClose, onSaved }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [phase, setPhase] = useState<'loading' | 'ready' | 'error'>('loading')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [joints, setJoints] = useState<ParsedJoint[]>([])
  const [doc, setDoc] = useState<KeyframeDoc>({ tick_rate: 30, duration_ticks: 60, tracks: [] })
  const [source, setSource] = useState<'stored' | 'derived' | 'empty'>('empty')
  const [rowId, setRowId] = useState(animation.id)
  const [rowName, setRowName] = useState(animation.name)
  const [tick, setTick] = useState(0)
  const [playing, setPlaying] = useState(false)
  const [selected, setSelected] = useState(0)
  const [mode, setMode] = useState<'rotate' | 'translate'>('rotate')
  const [autoKey, setAutoKey] = useState(true)
  const [xray, setXray] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [poseRev, setPoseRev] = useState(0) // bumps when the gizmo moves a bone, to refresh the numeric panel

  const bonesRef = useRef<THREE.Bone[]>([])
  const spheresRef = useRef<THREE.Mesh[]>([])
  const tcRef = useRef<TransformControls | null>(null)
  const materialRef = useRef<THREE.MeshStandardMaterial | null>(null)
  const draggingRef = useRef(false)
  const clipboardRef = useRef<{ q: THREE.Quaternion; p: THREE.Vector3 }[] | null>(null)

  // Refs mirror state for the render loop and three.js event handlers.
  const docRef = useRef(doc)
  docRef.current = doc
  const tickRef = useRef(tick)
  tickRef.current = tick
  const playingRef = useRef(playing)
  playingRef.current = playing
  const selectedRef = useRef(selected)
  selectedRef.current = selected
  const autoKeyRef = useRef(autoKey)
  autoKeyRef.current = autoKey
  const modeRef = useRef(mode)
  modeRef.current = mode
  const jointsRef = useRef(joints)
  jointsRef.current = joints

  const updateDoc = useCallback((next: KeyframeDoc) => {
    setDoc(next)
    setDirty(true)
  }, [])

  // applyPose poses every bone from the doc at a tick (rest where a joint has no keys).
  const applyPose = useCallback((d: KeyframeDoc, t: number) => {
    const bones = bonesRef.current
    const js = jointsRef.current
    const byJoint = new Map(d.tracks.map((tr) => [tr.joint, tr]))
    bones.forEach((b, i) => {
      const tr = byJoint.get(js[i].name)
      if (tr?.rotation?.length) evalRotation(tr.rotation, t, b.quaternion)
      else b.quaternion.copy(js[i].restRotation)
      if (tr?.translation?.length) evalTranslation(tr.translation, t, b.position)
      else b.position.copy(js[i].restTranslation)
    })
  }, [])

  // Scene + loading.
  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    let disposed = false

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x11141a)
    const camera = new THREE.PerspectiveCamera(45, 1, 0.01, 2000)
    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    container.appendChild(renderer.domElement)
    scene.add(new THREE.HemisphereLight(0xffffff, 0x222233, 1.1))
    const sun = new THREE.DirectionalLight(0xffffff, 0.7)
    sun.position.set(3, 6, 2)
    scene.add(sun)
    scene.add(new THREE.GridHelper(4, 16, 0x444455, 0x2a2a33))
    const orbit = new OrbitControls(camera, renderer.domElement)
    orbit.enableDamping = true

    const tc = new TransformControls(camera, renderer.domElement)
    tc.setSpace('local')
    tc.setSize(0.8)
    tc.setMode(modeRef.current)
    scene.add(tc.getHelper())
    tcRef.current = tc
    tc.addEventListener('dragging-changed', (e) => {
      const dragging = Boolean((e as unknown as { value: boolean }).value)
      orbit.enabled = !dragging
      draggingRef.current = dragging
    })
    tc.addEventListener('objectChange', () => {
      const i = selectedRef.current
      const bone = bonesRef.current[i]
      if (!bone) return
      setPoseRev((r) => r + 1)
      if (!autoKeyRef.current) return
      const name = jointsRef.current[i].name
      const next =
        modeRef.current === 'rotate'
          ? setKey(docRef.current, name, tickRef.current, bone.quaternion.clone())
          : setKey(docRef.current, name, tickRef.current, undefined, bone.position.clone())
      docRef.current = next
      setDoc(next)
      setDirty(true)
    })

    // Click a joint dot to select it (ignored while hovering/dragging the gizmo).
    const raycaster = new THREE.Raycaster()
    const pointer = new THREE.Vector2()
    const onPointerDown = (ev: PointerEvent) => {
      if (draggingRef.current || tc.axis !== null) return
      const rect = renderer.domElement.getBoundingClientRect()
      pointer.set(((ev.clientX - rect.left) / rect.width) * 2 - 1, -((ev.clientY - rect.top) / rect.height) * 2 + 1)
      raycaster.setFromCamera(pointer, camera)
      const hit = raycaster.intersectObjects(spheresRef.current, false)[0]
      if (hit) setSelected(hit.object.userData.jointIndex as number)
    }
    renderer.domElement.addEventListener('pointerdown', onPointerDown)

    const clock = new THREE.Clock()
    let playAcc = 0
    let raf = 0
    const render = () => {
      const rect = container.getBoundingClientRect()
      const w = Math.max(1, rect.width)
      const h = Math.max(1, rect.height)
      if (renderer.domElement.width !== Math.round(w * renderer.getPixelRatio()) || renderer.domElement.height !== Math.round(h * renderer.getPixelRatio())) {
        renderer.setSize(w, h)
        camera.aspect = w / h
        camera.updateProjectionMatrix()
      }
      const dt = clock.getDelta()
      if (playingRef.current) {
        const d = docRef.current
        playAcc += dt * d.tick_rate
        if (playAcc >= 1) {
          const step = Math.floor(playAcc)
          playAcc -= step
          const next = (tickRef.current + step) % d.duration_ticks
          tickRef.current = next
          setTick(next)
          applyPose(d, next)
        }
      } else {
        playAcc = 0
      }
      orbit.update()
      renderer.render(scene, camera)
      raf = requestAnimationFrame(render)
    }
    raf = requestAnimationFrame(render)

    const disposables: Array<{ dispose: () => void }> = []
    ;(async () => {
      try {
        const [gskelBuf, gmeshBuf, kf] = await Promise.all([
          fetch(animations.downloadUrl(animation.id, 'gskel')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(animations.downloadUrl(animation.id, 'gmesh')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          animations.getKeyframes(animation.id),
        ])
        if (disposed) return
        if (!gskelBuf) throw new Error('This asset has no rig -- there is nothing to animate.')
        const parsed = parseGSkel(gskelBuf)
        const mesh = gmeshBuf ? parseGMesh(gmeshBuf) : null
        jointsRef.current = parsed
        setJoints(parsed)
        const loaded: KeyframeDoc = { ...kf.keyframes, tracks: kf.keyframes.tracks ?? [] }
        setDoc(loaded)
        docRef.current = loaded
        setSource(kf.source)

        const bones = buildBones(parsed)
        bonesRef.current = bones
        const armature = new THREE.Group()
        bones.filter((_, i) => parsed[i].parentIndex < 0).forEach((b) => armature.add(b))

        let root: THREE.Object3D = armature
        const material = new THREE.MeshStandardMaterial({ color: 0x8f9aa8, roughness: 0.8, metalness: 0.05 })
        materialRef.current = material
        disposables.push(material)
        if (mesh) {
          const skeleton = new THREE.Skeleton(bones, parsed.map((j) => j.inverseBind))
          const geometry = new THREE.BufferGeometry()
          geometry.setAttribute('position', new THREE.BufferAttribute(mesh.positions, 3))
          geometry.setAttribute('normal', new THREE.BufferAttribute(mesh.normals, 3))
          geometry.setAttribute('skinIndex', new THREE.Uint16BufferAttribute(mesh.skinIndices, 4))
          geometry.setAttribute('skinWeight', new THREE.BufferAttribute(mesh.skinWeights, 4))
          geometry.setIndex(new THREE.BufferAttribute(mesh.indices, 1))
          disposables.push(geometry)
          const skinned = new THREE.SkinnedMesh(geometry, material)
          skinned.add(armature)
          skinned.bind(skeleton)
          root = skinned
        }
        scene.add(root)
        const helper = new THREE.SkeletonHelper(armature)
        scene.add(helper)

        root.updateMatrixWorld(true)
        const box = new THREE.Box3()
        bones.forEach((b) => box.expandByPoint(b.getWorldPosition(new THREE.Vector3())))
        if (mesh) box.union(new THREE.Box3().setFromObject(root))
        const size = box.getSize(new THREE.Vector3())
        const radius = Math.max(size.length() * 0.5, 0.05)

        // Joint dots, parented to their bones so they follow the pose.
        const dotGeo = new THREE.SphereGeometry(radius * 0.022, 10, 8)
        disposables.push(dotGeo)
        spheresRef.current = bones.map((b, i) => {
          const m = new THREE.Mesh(dotGeo, new THREE.MeshBasicMaterial({ color: JOINT_COLOR, depthTest: false, transparent: true, opacity: 0.9 }))
          m.renderOrder = 999
          m.userData.jointIndex = i
          b.add(m)
          disposables.push(m.material as THREE.Material)
          return m
        })

        const center = box.getCenter(new THREE.Vector3())
        camera.position.set(center.x + radius * 1.2, center.y + radius * 0.5, center.z + radius * 2.2)
        orbit.target.copy(center)
        orbit.update()

        applyPose(loaded, 0)
        setPhase('ready')
        if (kf.source === 'derived') setNotice('Imported clip: keys were derived from its baked motion (one every 5 ticks). Saving creates a new clip; the import is left untouched.')
      } catch (err) {
        if (!disposed) {
          setError(String(err))
          setPhase('error')
        }
      }
    })()

    return () => {
      disposed = true
      cancelAnimationFrame(raf)
      renderer.domElement.removeEventListener('pointerdown', onPointerDown)
      tc.detach()
      tc.dispose()
      orbit.dispose()
      disposables.forEach((d) => d.dispose())
      renderer.dispose()
      container.removeChild(renderer.domElement)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [animation.id])

  // Re-pose whenever the doc or playhead changes (not mid-drag: the gizmo owns the bone then).
  useEffect(() => {
    if (phase !== 'ready' || draggingRef.current) return
    applyPose(doc, tick)
    setPoseRev((r) => r + 1)
  }, [doc, tick, phase, applyPose])

  // Gizmo follows the selected joint and mode; dot colours show selected / keyed joints.
  useEffect(() => {
    const tc = tcRef.current
    const bone = bonesRef.current[selected]
    if (tc && bone) {
      tc.attach(bone)
      tc.setMode(mode)
    }
    const keyed = new Set(doc.tracks.map((t) => t.joint))
    spheresRef.current.forEach((s, i) => {
      const color = i === selected ? JOINT_SELECTED : keyed.has(joints[i]?.name) ? JOINT_KEYED : JOINT_COLOR
      ;(s.material as THREE.MeshBasicMaterial).color.setHex(color)
    })
  }, [selected, mode, doc, joints, phase])

  useEffect(() => {
    const m = materialRef.current
    if (!m) return
    m.transparent = xray
    m.opacity = xray ? 0.35 : 1
    m.depthWrite = !xray
    m.needsUpdate = true
  }, [xray, phase])

  const selectedName = joints[selected]?.name ?? ''

  const insertKey = useCallback(() => {
    const bone = bonesRef.current[selectedRef.current]
    const j = jointsRef.current[selectedRef.current]
    if (!bone || !j) return
    const tr = docRef.current.tracks.find((t) => t.joint === j.name)
    const withPos = j.parentIndex < 0 || (tr?.translation?.length ?? 0) > 0 || modeRef.current === 'translate'
    updateDoc(setKey(docRef.current, j.name, tickRef.current, bone.quaternion.clone(), withPos ? bone.position.clone() : undefined))
  }, [updateDoc])

  const deleteKey = useCallback(() => {
    const j = jointsRef.current[selectedRef.current]
    if (j) updateDoc(deleteKeysAt(docRef.current, j.name, tickRef.current))
  }, [updateDoc])

  const copyPose = () => {
    clipboardRef.current = bonesRef.current.map((b) => ({ q: b.quaternion.clone(), p: b.position.clone() }))
    setNotice(`Copied the pose at tick ${tick}.`)
  }

  // pastePose keys every joint whose copied pose differs from rest, or that is already keyed.
  const pastePose = () => {
    const clip = clipboardRef.current
    if (!clip) return
    let d = docRef.current
    joints.forEach((j, i) => {
      const tr = d.tracks.find((t) => t.joint === j.name)
      const rotMoved = Math.abs(clip[i].q.dot(j.restRotation)) < 1 - 1e-6
      const posMoved = clip[i].p.distanceTo(j.restTranslation) > 1e-6
      const rot = rotMoved || (tr?.rotation?.length ?? 0) > 0 ? clip[i].q : undefined
      const pos = posMoved || (tr?.translation?.length ?? 0) > 0 ? clip[i].p : undefined
      if (rot || pos) d = setKey(d, j.name, tick, rot, pos)
    })
    updateDoc(d)
  }

  const resetJoint = () => {
    const j = joints[selected]
    const bone = bonesRef.current[selected]
    if (!j || !bone) return
    bone.quaternion.copy(j.restRotation)
    bone.position.copy(j.restTranslation)
    const tr = doc.tracks.find((t) => t.joint === j.name)
    updateDoc(setKey(doc, j.name, tick, j.restRotation.clone(), (tr?.translation?.length ?? 0) > 0 ? j.restTranslation.clone() : undefined))
  }

  // Keyboard shortcuts (Blender-style), ignored while typing in a field.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement | null)?.tagName
      if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA') return
      const dur = docRef.current.duration_ticks
      switch (e.key) {
        case 'r':
          setMode('rotate')
          break
        case 'g':
          setMode('translate')
          break
        case 'i':
          insertKey()
          break
        case 'x':
        case 'Delete':
          deleteKey()
          break
        case ' ':
          e.preventDefault()
          setPlaying((p) => !p)
          break
        case 'ArrowLeft':
          setPlaying(false)
          setTick((t) => (t - 1 + dur) % dur)
          break
        case 'ArrowRight':
          setPlaying(false)
          setTick((t) => (t + 1) % dur)
          break
        case 'Home':
          setTick(0)
          break
        case 'End':
          setTick(dur - 1)
          break
        default:
          return
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [insertKey, deleteKey])

  const save = async (asNew: boolean) => {
    let name = ''
    if (asNew) {
      const suggested = source === 'stored' ? `${rowName}_v2` : `${rowName}_anim`
      const input = window.prompt('Name for the new clip:', suggested)
      if (!input) return
      name = input
    }
    setSaving(true)
    setError(null)
    try {
      const row = await animations.saveKeyframes(rowId, doc, asNew ? { name } : { replace: true })
      setRowId(row.id)
      setRowName(row.name)
      setSource('stored')
      setDirty(false)
      setNotice(asNew ? `Saved as "${row.name}". Further saves update it.` : `Saved "${row.name}".`)
      onSaved()
    } catch (err) {
      setError(String(err))
    } finally {
      setSaving(false)
    }
  }

  const setDuration = (n: number) => {
    if (!Number.isFinite(n) || n < 1) return
    updateDoc(clampToDuration(doc, Math.min(Math.round(n), 36000)))
    setTick((t) => Math.min(t, Math.round(n) - 1))
  }

  const selTrack = doc.tracks.find((t) => t.joint === selectedName)
  const selKeyHere = [...(selTrack?.rotation ?? []), ...(selTrack?.translation ?? [])].find((k) => k.tick === tick)
  const depthOf = (i: number): number => (joints[i].parentIndex < 0 ? 0 : 1 + depthOf(joints[i].parentIndex))

  return (
    <div className="animation-viewer-overlay" onClick={dirty ? undefined : onClose}>
      <div className="animation-viewer-panel animator-panel" onClick={(e) => e.stopPropagation()}>
        <div className="animation-viewer-header">
          <strong>
            Animate: {rowName}
            {dirty && ' •'}
          </strong>
          <span className="hint">
            {source === 'stored' ? 'NOCK-authored clip' : source === 'derived' ? 'keys derived from an imported clip' : 'new clip'}
          </span>
          <button
            onClick={() => {
              if (dirty && !confirm('Discard unsaved keys?')) return
              onClose()
            }}
          >
            Close
          </button>
        </div>
        {error && <p className="error">{error}</p>}
        {notice && <p className="hint">{notice}</p>}
        <div className="animator-body">
          <div ref={containerRef} className="animation-viewer-canvas animator-canvas" />
          {phase === 'ready' && (
            <aside className="animator-side">
              <label>
                Joint
                <select value={selected} onChange={(e) => setSelected(Number(e.target.value))}>
                  {joints.map((j, i) => (
                    <option key={j.name} value={i}>
                      {'  '.repeat(depthOf(i))}
                      {j.name}
                      {doc.tracks.some((t) => t.joint === j.name) ? ' ◆' : ''}
                    </option>
                  ))}
                </select>
              </label>
              <div className="animator-row animator-mode">
                <button className={mode === 'rotate' ? 'active' : ''} onClick={() => setMode('rotate')} title="R">
                  Rotate (R)
                </button>
                <button className={mode === 'translate' ? 'active' : ''} onClick={() => setMode('translate')} title="G">
                  Move (G)
                </button>
              </div>
              <label className="animator-check">
                <input type="checkbox" checked={autoKey} onChange={(e) => setAutoKey(e.target.checked)} /> Auto-key
              </label>
              <label className="animator-check">
                <input type="checkbox" checked={xray} onChange={(e) => setXray(e.target.checked)} /> X-ray mesh
              </label>
              <JointNumeric
                key={`${selected}-${tick}-${poseRev}`}
                bone={bonesRef.current[selected]}
                onCommit={(q, p) => {
                  const bone = bonesRef.current[selected]
                  if (!bone) return
                  bone.quaternion.copy(q)
                  bone.position.copy(p)
                  if (autoKey) {
                    const hasPos = (selTrack?.translation?.length ?? 0) > 0 || !p.equals(joints[selected].restTranslation)
                    updateDoc(setKey(doc, selectedName, tick, q, hasPos ? p : undefined))
                  }
                }}
              />
              <div className="animator-row">
                <button onClick={insertKey} title="I">
                  Key (I)
                </button>
                <button onClick={deleteKey} disabled={!selKeyHere} title="X">
                  Delete key (X)
                </button>
              </div>
              <label>
                Interpolation after this key
                <select
                  value={selKeyHere?.interp ?? 'linear'}
                  disabled={!selKeyHere}
                  onChange={(e) => updateDoc(setInterpAt(doc, selectedName, tick, e.target.value as Interp))}
                >
                  <option value="linear">linear</option>
                  <option value="smooth">smooth (ease in/out)</option>
                  <option value="step">step (hold)</option>
                </select>
              </label>
              <div className="animator-row">
                <button onClick={resetJoint}>Reset joint to rest</button>
              </div>
              <div className="animator-row">
                <button onClick={copyPose}>Copy pose</button>
                <button onClick={pastePose} disabled={!clipboardRef.current}>
                  Paste pose
                </button>
              </div>
              <hr />
              <label>
                Length (ticks)
                <input type="number" min={1} value={doc.duration_ticks} onChange={(e) => setDuration(Number(e.target.value))} />
              </label>
              <label>
                Ticks / second
                <input
                  type="number"
                  min={1}
                  max={1000}
                  value={doc.tick_rate}
                  onChange={(e) => {
                    const v = Math.round(Number(e.target.value))
                    if (v >= 1 && v <= 1000) updateDoc({ ...doc, tick_rate: v })
                  }}
                />
              </label>
              <span className="hint">{(doc.duration_ticks / doc.tick_rate).toFixed(2)} s</span>
              <hr />
              <div className="animator-row">
                {source === 'stored' && (
                  <button disabled={!dirty || saving} onClick={() => save(false)}>
                    {saving ? 'Saving…' : 'Save'}
                  </button>
                )}
                <button disabled={saving || doc.tracks.length === 0} onClick={() => save(true)}>
                  Save as new clip…
                </button>
              </div>
            </aside>
          )}
        </div>
        {phase === 'ready' && (
          <Timeline
            doc={doc}
            tick={tick}
            playing={playing}
            selectedJoint={selectedName}
            onTick={(t) => {
              setPlaying(false)
              setTick(t)
            }}
            onSelectJoint={(name) => {
              const i = joints.findIndex((j) => j.name === name)
              if (i >= 0) setSelected(i)
            }}
            onTogglePlay={() => setPlaying((p) => !p)}
          />
        )}
        {phase === 'ready' && <p className="hint animator-keys">R rotate · G move · I key · X delete key · Space play · ←/→ step · click a dot to pick a joint</p>}
      </div>
    </div>
  )
}

// JointNumeric edits the selected joint's current local pose as Euler degrees + position.
function JointNumeric({ bone, onCommit }: { bone?: THREE.Bone; onCommit: (q: THREE.Quaternion, p: THREE.Vector3) => void }) {
  const e0 = bone ? new THREE.Euler().setFromQuaternion(bone.quaternion) : new THREE.Euler()
  const [rot, setRot] = useState<[number, number, number]>([e0.x * RAD2DEG, e0.y * RAD2DEG, e0.z * RAD2DEG])
  const [pos, setPos] = useState<[number, number, number]>(bone ? [bone.position.x, bone.position.y, bone.position.z] : [0, 0, 0])
  if (!bone) return null
  const commit = (r: [number, number, number], p: [number, number, number]) => {
    onCommit(new THREE.Quaternion().setFromEuler(new THREE.Euler(r[0] * DEG2RAD, r[1] * DEG2RAD, r[2] * DEG2RAD)), new THREE.Vector3(...p))
  }
  return (
    <div className="animator-numeric">
      <span className="hint">Rotation (°)</span>
      <div className="animator-row">
        {[0, 1, 2].map((i) => (
          <input
            key={i}
            type="number"
            step={1}
            value={rot[i].toFixed(1)}
            onChange={(e) => {
              const next: [number, number, number] = [...rot]
              next[i] = Number(e.target.value)
              setRot(next)
              commit(next, pos)
            }}
          />
        ))}
      </div>
      <span className="hint">Position</span>
      <div className="animator-row">
        {[0, 1, 2].map((i) => (
          <input
            key={i}
            type="number"
            step={0.01}
            value={pos[i].toFixed(3)}
            onChange={(e) => {
              const next: [number, number, number] = [...pos]
              next[i] = Number(e.target.value)
              setPos(next)
              commit(rot, next)
            }}
          />
        ))}
      </div>
    </div>
  )
}

// Timeline is a compact dope sheet: a summary row with every key, then one row per keyed joint
// (plus the selected joint). Click anywhere to move the playhead; click a row to select its joint.
function Timeline({
  doc,
  tick,
  playing,
  selectedJoint,
  onTick,
  onSelectJoint,
  onTogglePlay,
}: {
  doc: KeyframeDoc
  tick: number
  playing: boolean
  selectedJoint: string
  onTick: (t: number) => void
  onSelectJoint: (name: string) => void
  onTogglePlay: () => void
}) {
  const last = Math.max(1, doc.duration_ticks - 1)
  const pct = (t: number) => `${(t / last) * 100}%`
  const rows = doc.tracks.map((t) => t.joint)
  if (selectedJoint && !rows.includes(selectedJoint)) rows.unshift(selectedJoint)
  const scrub = (e: React.MouseEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    const u = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
    onTick(Math.round(u * last))
  }
  const labelEvery = Math.max(1, Math.ceil(doc.duration_ticks / 12))
  const labels = []
  for (let t = 0; t <= last; t += labelEvery) labels.push(t)

  return (
    <div className="timeline">
      <div className="timeline-head">
        <button onClick={onTogglePlay}>{playing ? 'Pause' : 'Play'}</button>
        <span className="hint">
          tick {tick} / {last}
        </span>
      </div>
      <div className="timeline-grid">
        <div className="timeline-label" />
        <div className="timeline-lane timeline-ruler" onMouseDown={scrub}>
          {labels.map((t) => (
            <span key={t} className="timeline-tick-label" style={{ left: pct(t) }}>
              {t}
            </span>
          ))}
          <div className="timeline-playhead" style={{ left: pct(tick) }} />
        </div>
        <div className="timeline-label">All keys</div>
        <div className="timeline-lane" onMouseDown={scrub}>
          {keyTicks(doc).map((t) => (
            <span key={t} className="timeline-key summary" style={{ left: pct(t) }} />
          ))}
          <div className="timeline-playhead" style={{ left: pct(tick) }} />
        </div>
        {rows.map((name) => (
          <TimelineRow key={name} name={name} doc={doc} selected={name === selectedJoint} pct={pct} tick={tick} onSelect={() => onSelectJoint(name)} onScrub={scrub} />
        ))}
      </div>
    </div>
  )
}

function TimelineRow({
  name,
  doc,
  selected,
  pct,
  tick,
  onSelect,
  onScrub,
}: {
  name: string
  doc: KeyframeDoc
  selected: boolean
  pct: (t: number) => string
  tick: number
  onSelect: () => void
  onScrub: (e: React.MouseEvent<HTMLDivElement>) => void
}) {
  return (
    <>
      <div className={`timeline-label${selected ? ' selected' : ''}`} onClick={onSelect} title={name}>
        {name}
      </div>
      <div
        className={`timeline-lane${selected ? ' selected' : ''}`}
        onMouseDown={(e) => {
          onSelect()
          onScrub(e)
        }}
      >
        {keyTicks(doc, name).map((t) => (
          <span key={t} className="timeline-key" style={{ left: pct(t) }} />
        ))}
        <div className="timeline-playhead" style={{ left: pct(tick) }} />
      </div>
    </>
  )
}
