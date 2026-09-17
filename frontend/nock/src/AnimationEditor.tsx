// AnimationEditor.tsx — real, in-browser "clone then edit" workflow for an existing animation
// clip (founder real-time, 2026-09-17: "lets build the animations editor - clone then edit
// workflow"). Clones the source row first (AnimStore.CloneAnimation, already real) so every edit
// lands on a brand-new, independent row -- the original a founder started from is never touched.
// Reuses goldenband.ts's own real parse/encode functions (shared with AnimationViewer.tsx) and
// the same Three.js foundation (container-ref + WebGLRenderer + OrbitControls + rAF loop).
//
// Real, deliberate v0 scope, named honestly rather than silently assumed: editing changes VALUES
// within the clip's own existing channels (tick x joint x component), never the channel set
// itself -- a joint with no tx/ty/tz or no qx/qy/qz/qw channel in the source clip can't have that
// component authored from scratch here (it stays locked to its own real rest-pose value, shown
// but disabled). Adding brand-new joint animation from nothing is real, separate, future work
// (NOCK_CHARACTER_PIPELINE_NORTHSTAR.md's own Phase 3 "animation tool"); this tool adjusts an
// already-imported trick's own poses, which is what "clone then edit" an existing clip means.
import { useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { animations, type Animation, type AnimationSummary } from './api'
import { parseGSkel, parseGMesh, parseGBand, buildAnimationClip, buildBones, encodeGBand, gbandDataBytes, sha256Hex, type ParsedJoint, type ParsedGBand } from './goldenband'

interface Props {
  animation: AnimationSummary
  onClose: () => void
  onSaved: () => void
}

const RAD2DEG = 180 / Math.PI
const DEG2RAD = Math.PI / 180

export default function AnimationEditor({ animation, onClose, onSaved }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [phase, setPhase] = useState<'cloning' | 'loading' | 'ready' | 'error'>('cloning')
  const [error, setError] = useState<string | null>(null)
  const [clone, setClone] = useState<Animation | null>(null)
  const [joints, setJoints] = useState<ParsedJoint[]>([])
  const [channelNames, setChannelNames] = useState<string[]>([])
  const [tick, setTick] = useState(0)
  const [selectedJoint, setSelectedJoint] = useState(0)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [playing, setPlaying] = useState(false)

  const gbandRef = useRef<ParsedGBand | null>(null)
  const skeletonRef = useRef<THREE.Skeleton | null>(null)
  const armatureRef = useRef<THREE.Object3D | null>(null)
  const mixerRef = useRef<THREE.AnimationMixer | null>(null)
  const actionRef = useRef<THREE.AnimationAction | null>(null)
  const playingRef = useRef(playing)
  playingRef.current = playing

  // Step 1: clone the source row -- every edit below operates on this new, independent id only.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const defaultName = window.prompt('Name for the edited copy:', `${animation.name}_edit`)
        if (!defaultName) {
          onClose()
          return
        }
        const created = await animations.clone(animation.id, defaultName)
        if (cancelled) return
        setClone(created)
        setPhase('loading')
      } catch (err) {
        if (!cancelled) {
          setError(String(err))
          setPhase('error')
        }
      }
    })()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [animation.id])

  // Step 2: real Three.js scene, mirrors AnimationViewer's own setup.
  useEffect(() => {
    if (phase !== 'loading' && phase !== 'ready') return
    const container = containerRef.current
    if (!container || !clone) return
    let disposed = false

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x11141a)
    const camera = new THREE.PerspectiveCamera(50, 1, 0.01, 2000)
    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    container.appendChild(renderer.domElement)
    scene.add(new THREE.HemisphereLight(0xffffff, 0x222233, 1.1))
    const sun = new THREE.DirectionalLight(0xffffff, 0.7)
    sun.position.set(3, 6, 2)
    scene.add(sun)
    scene.add(new THREE.GridHelper(4, 16, 0x444455, 0x2a2a33))
    const controls = new OrbitControls(camera, renderer.domElement)
    controls.enableDamping = true

    const clock = new THREE.Clock()
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
      if (mixerRef.current && playingRef.current) {
        mixerRef.current.update(dt)
        if (actionRef.current && gbandRef.current) {
          setTick(Math.round(actionRef.current.time * gbandRef.current.tickRate) % gbandRef.current.durationTicks)
        }
      }
      controls.update()
      renderer.render(scene, camera)
      raf = requestAnimationFrame(render)
    }
    raf = requestAnimationFrame(render)

    const disposables: Array<{ dispose: () => void }> = []

    ;(async () => {
      try {
        const [gskelBuf, gmeshBuf, gbandBuf, manifestRes] = await Promise.all([
          fetch(animations.downloadUrl(clone.id, 'gskel')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(animations.downloadUrl(clone.id, 'gmesh')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(animations.downloadUrl(clone.id, 'gband')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(`/admin/nock/api/animations/${clone.id}/manifest`),
        ])
        if (disposed) return
        if (!gskelBuf || !gbandBuf) {
          setError('This asset needs both a rig and animation data to edit -- nothing to adjust here.')
          setPhase('error')
          return
        }
        const parsedJoints = parseGSkel(gskelBuf)
        const mesh = gmeshBuf ? parseGMesh(gmeshBuf) : null
        const gband = parseGBand(gbandBuf)
        const manifest = manifestRes.ok ? await manifestRes.json() : null
        const channels: string[] = manifest?.channels ?? []
        gbandRef.current = gband
        setJoints(parsedJoints)
        setChannelNames(channels)

        const bones = buildBones(parsedJoints)
        const roots = bones.filter((_, i) => parsedJoints[i].parentIndex < 0)
        const armature = new THREE.Group()
        roots.forEach((b) => armature.add(b))
        armatureRef.current = armature

        let root: THREE.Object3D
        const material = new THREE.MeshStandardMaterial({ color: 0x8f9aa8, roughness: 0.8, metalness: 0.05 })
        if (mesh) {
          const skeleton = new THREE.Skeleton(bones, parsedJoints.map((j) => j.inverseBind))
          skeletonRef.current = skeleton
          const geometry = new THREE.BufferGeometry()
          geometry.setAttribute('position', new THREE.BufferAttribute(mesh.positions, 3))
          geometry.setAttribute('normal', new THREE.BufferAttribute(mesh.normals, 3))
          geometry.setAttribute('skinIndex', new THREE.Uint16BufferAttribute(mesh.skinIndices, 4))
          geometry.setAttribute('skinWeight', new THREE.BufferAttribute(mesh.skinWeights, 4))
          geometry.setIndex(new THREE.BufferAttribute(mesh.indices, 1))
          disposables.push(geometry)
          const skinnedMesh = new THREE.SkinnedMesh(geometry, material)
          skinnedMesh.add(armature)
          skinnedMesh.bind(skeleton)
          root = skinnedMesh
        } else {
          root = armature
        }
        scene.add(root)

        const clip = buildAnimationClip(gband, channels, parsedJoints)
        const mixer = new THREE.AnimationMixer(armature)
        const action = mixer.clipAction(clip)
        action.play()
        action.paused = true
        mixerRef.current = mixer
        actionRef.current = action

        const box = new THREE.Box3().setFromObject(root)
        const size = box.getSize(new THREE.Vector3())
        const center = box.getCenter(new THREE.Vector3())
        const radius = Math.max(size.length() * 0.5, 0.05)
        camera.position.set(center.x + radius * 1.4, center.y + radius * 0.9, center.z + radius * 1.4)
        controls.target.copy(center)
        controls.update()

        setPhase('ready')
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
      controls.dispose()
      disposables.forEach((d) => d.dispose())
      renderer.dispose()
      container.removeChild(renderer.domElement)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase === 'loading' || phase === 'ready', clone?.id])

  // Keep the mixer's own displayed pose in sync with the selected tick whenever it changes
  // (scrubbing, or after an edit) -- real, immediate visual feedback.
  useEffect(() => {
    const gband = gbandRef.current
    const action = actionRef.current
    const mixer = mixerRef.current
    if (!gband || !action || !mixer) return
    action.time = tick / gband.tickRate
    mixer.update(0)
  }, [tick, dirty])

  const joint = joints[selectedJoint]
  const channelIndex = (suffix: string) => (joint ? channelNames.indexOf(`${joint.name}.${suffix}`) : -1)
  const gband = gbandRef.current
  const valueAt = (suffix: string, fallback: number) => {
    const idx = channelIndex(suffix)
    if (idx === -1 || !gband) return fallback
    return gband.data[tick * gband.numChannels + idx]
  }

  const applyValue = (suffix: string, value: number) => {
    const idx = channelIndex(suffix)
    if (idx === -1 || !gband) return // locked -- this joint/component has no real channel to edit
    gband.data[tick * gband.numChannels + idx] = value
    setDirty(true)
  }

  const save = async () => {
    if (!clone || !gband) return
    setSaving(true)
    setError(null)
    try {
      const dataBytes = gbandDataBytes(gband.data)
      const contentHash = await sha256Hex(dataBytes)
      const encoded = encodeGBand(gband.tickRate, gband.durationTicks, gband.numChannels, clone.skeleton_hash ?? '', contentHash, gband.data)
      const gbandDataBase64 = btoa(String.fromCharCode(...new Uint8Array(encoded)))
      const manifest = {
        gband_version: 1,
        skeleton_hash: clone.skeleton_hash ?? '',
        content_hash: contentHash,
        tick_rate: gband.tickRate,
        duration_ticks: gband.durationTicks,
        channels: channelNames,
        authorship: { kind: 'edited', who: 'nock animation editor' },
        intent_tags: [],
        loop_points: { start_tick: 0, end_tick: gband.durationTicks },
        safety: { max_joint_velocity: null, max_joint_torque: null },
      }
      await animations.saveEditedGBand(clone.id, gbandDataBase64, JSON.stringify(manifest))
      setDirty(false)
      onSaved()
      onClose()
    } catch (err) {
      setError(String(err))
    } finally {
      setSaving(false)
    }
  }

  const durationTicks = gband?.durationTicks ?? 1

  return (
    <div className="animation-viewer-overlay" onClick={dirty ? undefined : onClose}>
      <div className="animation-viewer-panel" onClick={(e) => e.stopPropagation()}>
        <div className="animation-viewer-header">
          <strong>{clone ? `Editing: ${clone.name}` : 'Cloning…'}</strong>
          <button
            onClick={() => {
              if (dirty && !confirm('Discard unsaved edits?')) return
              onClose()
            }}
          >
            Close
          </button>
        </div>
        {phase === 'cloning' && <p className="hint">Cloning the source clip…</p>}
        {phase === 'loading' && <p className="hint">Loading…</p>}
        {error && <p className="error">{error}</p>}
        {(phase === 'loading' || phase === 'ready') && <div ref={containerRef} className="animation-viewer-canvas" />}
        {phase === 'ready' && gband && (
          <>
            <div className="animation-viewer-controls">
              <button onClick={() => setPlaying((p) => !p)}>{playing ? 'Pause' : 'Play'}</button>
              <input
                type="range"
                min={0}
                max={durationTicks - 1}
                step={1}
                value={tick}
                onChange={(e) => {
                  setPlaying(false)
                  setTick(Number(e.target.value))
                }}
              />
              <span className="hint">
                tick {tick} / {durationTicks - 1}
              </span>
            </div>
            <div className="animation-attach-row">
              <span>Joint:</span>
              <select value={selectedJoint} onChange={(e) => setSelectedJoint(Number(e.target.value))}>
                {joints.map((j, i) => (
                  <option key={j.name} value={i}>
                    {j.name}
                  </option>
                ))}
              </select>
            </div>
            {joint && (
              <JointEditor
                key={`${selectedJoint}-${tick}`}
                joint={joint}
                hasPosition={channelIndex('tx') !== -1}
                hasRotation={channelIndex('qx') !== -1}
                position={[valueAt('tx', joint.restTranslation.x), valueAt('ty', joint.restTranslation.y), valueAt('tz', joint.restTranslation.z)]}
                quaternion={[valueAt('qx', joint.restRotation.x), valueAt('qy', joint.restRotation.y), valueAt('qz', joint.restRotation.z), valueAt('qw', joint.restRotation.w)]}
                onChangePosition={(p) => {
                  applyValue('tx', p[0])
                  applyValue('ty', p[1])
                  applyValue('tz', p[2])
                }}
                onChangeQuaternion={(q) => {
                  applyValue('qx', q[0])
                  applyValue('qy', q[1])
                  applyValue('qz', q[2])
                  applyValue('qw', q[3])
                }}
              />
            )}
            <div className="animation-attach-row">
              <button disabled={!dirty || saving} onClick={save}>
                {saving ? 'Saving…' : 'Save edits'}
              </button>
              {dirty && <span className="hint">unsaved changes</span>}
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// JointEditor -- numeric position + Euler-angle rotation editing for one joint at the currently
// selected tick. Euler degrees (not raw quaternion components) on purpose -- editable, intuitive
// numbers for a founder who isn't a graphics engineer, converted to/from the real quaternion the
// underlying channel data actually stores.
function JointEditor({
  joint,
  hasPosition,
  hasRotation,
  position,
  quaternion,
  onChangePosition,
  onChangeQuaternion,
}: {
  joint: ParsedJoint
  hasPosition: boolean
  hasRotation: boolean
  position: [number, number, number]
  quaternion: [number, number, number, number]
  onChangePosition: (p: [number, number, number]) => void
  onChangeQuaternion: (q: [number, number, number, number]) => void
}) {
  const euler = new THREE.Euler().setFromQuaternion(new THREE.Quaternion(...quaternion))
  const [eulerDeg, setEulerDeg] = useState<[number, number, number]>([euler.x * RAD2DEG, euler.y * RAD2DEG, euler.z * RAD2DEG])
  const [pos, setPos] = useState<[number, number, number]>(position)

  const applyEuler = (next: [number, number, number]) => {
    setEulerDeg(next)
    const q = new THREE.Quaternion().setFromEuler(new THREE.Euler(next[0] * DEG2RAD, next[1] * DEG2RAD, next[2] * DEG2RAD))
    onChangeQuaternion([q.x, q.y, q.z, q.w])
  }
  const applyPos = (next: [number, number, number]) => {
    setPos(next)
    onChangePosition(next)
  }

  return (
    <div className="animation-attach-panel hint">
      <p>
        <strong>{joint.name}</strong>
        {!hasPosition && !hasRotation && ' — not animated in this clip (locked to its own rest pose; authoring brand-new joint animation isn\'t supported here yet).'}
      </p>
      <div className="animation-attach-row">
        <span>Position:</span>
        {(['X', 'Y', 'Z'] as const).map((axis, i) => (
          <input
            key={axis}
            type="number"
            step={0.1}
            disabled={!hasPosition}
            value={pos[i].toFixed(3)}
            onChange={(e) => {
              const next: [number, number, number] = [...pos]
              next[i] = Number(e.target.value)
              applyPos(next)
            }}
          />
        ))}
      </div>
      <div className="animation-attach-row">
        <span>Rotation (deg):</span>
        {(['X', 'Y', 'Z'] as const).map((axis, i) => (
          <input
            key={axis}
            type="number"
            step={1}
            disabled={!hasRotation}
            value={eulerDeg[i].toFixed(1)}
            onChange={(e) => {
              const next: [number, number, number] = [...eulerDeg]
              next[i] = Number(e.target.value)
              applyEuler(next)
            }}
          />
        ))}
      </div>
    </div>
  )
}
