// AnimationViewer.tsx — a real, in-browser 3D preview for NOCK's animation library (founder
// real-time, 2026-09-17: "can we add a 3d scene like the shankpit level editor for the
// animations models viewer?"). Reuses the same real Three.js foundation ShankpitLevelEditor.tsx
// already established in this app (container-ref + WebGLRenderer + requestAnimationFrame render
// loop), plus three's own bundled OrbitControls addon for camera handling instead of hand-rolling
// one a second time.
//
// This is the first real JS/TS reader for GOLDENBAND's own .gskel/.gmesh/.gband binary formats
// -- every other consumer so far has been Go (gbtool) or C (the game engines). Byte layouts
// mirror GOLDENBAND/tools/gbtool's own Go writer exactly (gskel.go/gmesh.go/gband.go), which
// itself mirrors src/gskel.c/gmesh.c/gband.c field-for-field -- see those files' own header
// comments and format/*_FORMAT.md for the authoritative spec. Real skinning + animation
// playback: builds a genuine THREE.Bone hierarchy + THREE.Skeleton (with the asset's own real
// inverse-bind matrices, not recomputed ones) + THREE.SkinnedMesh, and a real THREE.AnimationClip
// from the .gband channels (falling back to each joint's own rest pose for any tick/component a
// clip doesn't animate, the same documented convention GSKEL_FORMAT.md/gseq.c already establish
// for the C engines) -- THREE's own AnimationMixer does the actual per-frame skinning, the same
// real math gpose.c performs, just GPU-side via THREE's own skinning shader instead of a second,
// independent hand-rolled implementation.
import { useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { animations, type AnimationSummary } from './api'

interface ParsedJoint {
  name: string
  parentIndex: number
  restTranslation: THREE.Vector3
  restRotation: THREE.Quaternion
  inverseBind: THREE.Matrix4
}

function parseGSkel(buf: ArrayBuffer): ParsedJoint[] {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GSKL') throw new Error(`not a real .gskel file (bad magic ${magic})`)
  const jointCount = dv.getUint32(8, true)
  const HEADER = 12
  const REC = 128
  const decoder = new TextDecoder()
  const joints: ParsedJoint[] = []
  for (let i = 0; i < jointCount; i++) {
    const off = HEADER + i * REC
    const nameBytes = new Uint8Array(buf, off, 32)
    const nul = nameBytes.indexOf(0)
    const name = decoder.decode(nameBytes.subarray(0, nul === -1 ? 32 : nul))
    const parentIndex = dv.getInt32(off + 32, true)
    const restTranslation = new THREE.Vector3(dv.getFloat32(off + 36, true), dv.getFloat32(off + 40, true), dv.getFloat32(off + 44, true))
    const restRotation = new THREE.Quaternion(dv.getFloat32(off + 48, true), dv.getFloat32(off + 52, true), dv.getFloat32(off + 56, true), dv.getFloat32(off + 60, true))
    const ibArr = new Float32Array(16)
    for (let k = 0; k < 16; k++) ibArr[k] = dv.getFloat32(off + 64 + k * 4, true)
    const inverseBind = new THREE.Matrix4().fromArray(ibArr) // column-major, matches THREE's own Matrix4 convention directly
    joints.push({ name, parentIndex, restTranslation, restRotation, inverseBind })
  }
  return joints
}

interface ParsedMesh {
  positions: Float32Array
  normals: Float32Array
  skinIndices: Uint16Array
  skinWeights: Float32Array
  indices: Uint32Array
}

function parseGMesh(buf: ArrayBuffer): ParsedMesh {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GMSH') throw new Error(`not a real .gmesh file (bad magic ${magic})`)
  const vertexCount = dv.getUint32(8, true)
  const indexCount = dv.getUint32(12, true)
  const HEADER = 16
  const REC = 52
  const positions = new Float32Array(vertexCount * 3)
  const normals = new Float32Array(vertexCount * 3)
  const skinIndices = new Uint16Array(vertexCount * 4)
  const skinWeights = new Float32Array(vertexCount * 4)
  for (let i = 0; i < vertexCount; i++) {
    const off = HEADER + i * REC
    for (let k = 0; k < 3; k++) positions[i * 3 + k] = dv.getFloat32(off + k * 4, true)
    for (let k = 0; k < 3; k++) normals[i * 3 + k] = dv.getFloat32(off + 12 + k * 4, true)
    // uv lives at off+24 (8 bytes) -- not read yet, no texturing in this v0 viewer
    for (let k = 0; k < 4; k++) skinIndices[i * 4 + k] = dv.getUint8(off + 32 + k)
    for (let k = 0; k < 4; k++) skinWeights[i * 4 + k] = dv.getFloat32(off + 36 + k * 4, true)
  }
  const idxOff = HEADER + vertexCount * REC
  const indices = new Uint32Array(indexCount)
  for (let i = 0; i < indexCount; i++) indices[i] = dv.getUint32(idxOff + i * 4, true)
  return { positions, normals, skinIndices, skinWeights, indices }
}

interface ParsedGBand {
  tickRate: number
  durationTicks: number
  numChannels: number
  data: Float32Array // durationTicks * numChannels, row-major by tick
}

function parseGBand(buf: ArrayBuffer): ParsedGBand {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GBND') throw new Error(`not a real .gband file (bad magic ${magic})`)
  const tickRate = dv.getUint32(8, true)
  const durationTicks = dv.getUint32(12, true)
  const numChannels = dv.getUint32(16, true)
  const HEADER = 84
  const count = durationTicks * numChannels
  const data = new Float32Array(count)
  for (let i = 0; i < count; i++) data[i] = dv.getFloat32(HEADER + i * 4, true)
  return { tickRate, durationTicks, numChannels, data }
}

// buildAnimationClip turns a parsed .gband + its manifest's own channel names into a real
// THREE.AnimationClip -- one position + quaternion track per joint that has at least one
// animated channel, falling back to that joint's own rest value for any tick/component the clip
// doesn't cover (GSKEL_FORMAT.md/gseq.c's own documented convention, applied here too).
function buildAnimationClip(gband: ParsedGBand, channelNames: string[], joints: ParsedJoint[]): THREE.AnimationClip {
  const { tickRate, durationTicks, numChannels, data } = gband
  const times = new Float32Array(durationTicks)
  for (let t = 0; t < durationTicks; t++) times[t] = t / tickRate

  const channelColumn = (name: string): Float32Array | null => {
    const idx = channelNames.indexOf(name)
    if (idx === -1) return null
    const out = new Float32Array(durationTicks)
    for (let t = 0; t < durationTicks; t++) out[t] = data[t * numChannels + idx]
    return out
  }

  const tracks: THREE.KeyframeTrack[] = []
  for (const j of joints) {
    const tx = channelColumn(`${j.name}.tx`)
    const ty = channelColumn(`${j.name}.ty`)
    const tz = channelColumn(`${j.name}.tz`)
    const qx = channelColumn(`${j.name}.qx`)
    const qy = channelColumn(`${j.name}.qy`)
    const qz = channelColumn(`${j.name}.qz`)
    const qw = channelColumn(`${j.name}.qw`)

    if (tx || ty || tz) {
      const values = new Float32Array(durationTicks * 3)
      for (let t = 0; t < durationTicks; t++) {
        values[t * 3 + 0] = tx ? tx[t] : j.restTranslation.x
        values[t * 3 + 1] = ty ? ty[t] : j.restTranslation.y
        values[t * 3 + 2] = tz ? tz[t] : j.restTranslation.z
      }
      tracks.push(new THREE.VectorKeyframeTrack(`${j.name}.position`, Array.from(times), Array.from(values)))
    }
    if (qx || qy || qz || qw) {
      const values = new Float32Array(durationTicks * 4)
      for (let t = 0; t < durationTicks; t++) {
        values[t * 4 + 0] = qx ? qx[t] : j.restRotation.x
        values[t * 4 + 1] = qy ? qy[t] : j.restRotation.y
        values[t * 4 + 2] = qz ? qz[t] : j.restRotation.z
        values[t * 4 + 3] = qw ? qw[t] : j.restRotation.w
      }
      tracks.push(new THREE.QuaternionKeyframeTrack(`${j.name}.quaternion`, Array.from(times), Array.from(values)))
    }
  }
  return new THREE.AnimationClip('clip', durationTicks / tickRate, tracks)
}

function buildBones(joints: ParsedJoint[]): THREE.Bone[] {
  const bones = joints.map((j) => {
    const b = new THREE.Bone()
    b.name = j.name
    b.position.copy(j.restTranslation)
    b.quaternion.copy(j.restRotation)
    return b
  })
  joints.forEach((j, i) => {
    if (j.parentIndex >= 0) bones[j.parentIndex].add(bones[i])
  })
  return bones
}

interface Props {
  animation: AnimationSummary
  onClose: () => void
}

export default function AnimationViewer({ animation, onClose }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [playing, setPlaying] = useState(true)
  const [duration, setDuration] = useState(0)
  const [time, setTime] = useState(0)
  const mixerRef = useRef<THREE.AnimationMixer | null>(null)
  const actionRef = useRef<THREE.AnimationAction | null>(null)
  const playingRef = useRef(playing)
  playingRef.current = playing

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
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
        if (actionRef.current) setTime(actionRef.current.time)
      }
      controls.update()
      renderer.render(scene, camera)
      raf = requestAnimationFrame(render)
    }
    raf = requestAnimationFrame(render)

    const disposables: Array<{ dispose: () => void }> = []

    ;(async () => {
      try {
        const [gskelBuf, gmeshBuf, gbandBuf] = await Promise.all([
          fetch(animations.downloadUrl(animation.id, 'gskel')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(animations.downloadUrl(animation.id, 'gmesh')).then((r) => (r.ok ? r.arrayBuffer() : null)),
          fetch(animations.downloadUrl(animation.id, 'gband')).then((r) => (r.ok ? r.arrayBuffer() : null)),
        ])
        if (disposed) return

        const joints = gskelBuf ? parseGSkel(gskelBuf) : null
        const mesh = gmeshBuf ? parseGMesh(gmeshBuf) : null
        if (!joints && !mesh) {
          setError('This asset has neither a mesh nor a rig to preview.')
          setLoading(false)
          return
        }

        let root: THREE.Object3D
        const material = new THREE.MeshStandardMaterial({ color: 0x8f9aa8, roughness: 0.8, metalness: 0.05 })

        if (mesh && joints) {
          const bones = buildBones(joints)
          const roots = bones.filter((_, i) => joints[i].parentIndex < 0)
          const armature = new THREE.Group()
          roots.forEach((b) => armature.add(b))
          const skeleton = new THREE.Skeleton(bones, joints.map((j) => j.inverseBind))

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

          // Real animation playback, if this asset has any -- see gbandBuf below. If not, the
          // mesh still renders in its own real rest (bind) pose, exactly the "mannequin in
          // T-pose" case the animation-library UI's own copy already describes.
          if (gbandBuf) {
            const gband = parseGBand(gbandBuf)
            const manifestRes = await fetch(animations.downloadUrl(animation.id, 'gband').replace(/\/gband$/, '/manifest'))
            const manifest = manifestRes.ok ? await manifestRes.json() : null
            const channelNames: string[] = manifest?.channels ?? []
            if (channelNames.length > 0) {
              const clip = buildAnimationClip(gband, channelNames, joints)
              const mixer = new THREE.AnimationMixer(armature)
              const action = mixer.clipAction(clip)
              action.play()
              mixerRef.current = mixer
              actionRef.current = action
              setDuration(clip.duration)
            }
          }
        } else if (mesh) {
          // Mesh with no rig -- a static, unskinned preview.
          const geometry = new THREE.BufferGeometry()
          geometry.setAttribute('position', new THREE.BufferAttribute(mesh.positions, 3))
          geometry.setAttribute('normal', new THREE.BufferAttribute(mesh.normals, 3))
          geometry.setIndex(new THREE.BufferAttribute(mesh.indices, 1))
          disposables.push(geometry)
          root = new THREE.Mesh(geometry, material)
        } else if (joints) {
          // Rig with no mesh -- a real, honest wireframe skeleton (line per bone-to-parent) so a
          // bare-rig row still shows SOMETHING, not a blank scene.
          const bones = buildBones(joints)
          const armature = new THREE.Group()
          bones.forEach((_, i) => { if (joints[i].parentIndex < 0) armature.add(bones[i]) })
          armature.updateMatrixWorld(true)
          const points: number[] = []
          bones.forEach((b, i) => {
            if (joints[i].parentIndex < 0) return
            const parent = bones[joints[i].parentIndex]
            const a = new THREE.Vector3()
            const p = new THREE.Vector3()
            b.getWorldPosition(a)
            parent.getWorldPosition(p)
            points.push(p.x, p.y, p.z, a.x, a.y, a.z)
          })
          const geometry = new THREE.BufferGeometry()
          geometry.setAttribute('position', new THREE.Float32BufferAttribute(points, 3))
          disposables.push(geometry)
          const lineMat = new THREE.LineBasicMaterial({ color: 0xffcc33 })
          root = new THREE.Group()
          root.add(new THREE.LineSegments(geometry, lineMat))
          root.add(armature)
        } else {
          return // unreachable -- guarded above
        }

        scene.add(root)

        // Real auto-frame: center + fit the camera to whatever actually got built, so a tiny
        // asset and a huge one both land on-screen without the viewer needing per-asset tuning.
        const box = new THREE.Box3().setFromObject(root)
        const size = box.getSize(new THREE.Vector3())
        const center = box.getCenter(new THREE.Vector3())
        const radius = Math.max(size.length() * 0.5, 0.05)
        camera.position.set(center.x + radius * 1.4, center.y + radius * 0.9, center.z + radius * 1.4)
        controls.target.copy(center)
        controls.update()

        setLoading(false)
      } catch (err) {
        if (!disposed) {
          setError(String(err))
          setLoading(false)
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
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally re-runs only when the asset id changes
  }, [animation.id])

  return (
    <div className="animation-viewer-overlay" onClick={onClose}>
      <div className="animation-viewer-panel" onClick={(e) => e.stopPropagation()}>
        <div className="animation-viewer-header">
          <strong>{animation.name}</strong>
          <button onClick={onClose}>Close</button>
        </div>
        <div ref={containerRef} className="animation-viewer-canvas" />
        {loading && <p className="hint">Loading…</p>}
        {error && <p className="error">{error}</p>}
        {duration > 0 && (
          <div className="animation-viewer-controls">
            <button onClick={() => setPlaying((p) => !p)}>{playing ? 'Pause' : 'Play'}</button>
            <input
              type="range"
              min={0}
              max={duration}
              step={duration / 200}
              value={time}
              onChange={(e) => {
                const t = Number(e.target.value)
                setTime(t)
                if (actionRef.current) {
                  actionRef.current.time = t
                  actionRef.current.paused = true
                  mixerRef.current?.update(0)
                  actionRef.current.paused = false
                }
                setPlaying(false)
              }}
            />
            <span className="hint">
              {time.toFixed(2)}s / {duration.toFixed(2)}s
            </span>
          </div>
        )}
      </div>
    </div>
  )
}
