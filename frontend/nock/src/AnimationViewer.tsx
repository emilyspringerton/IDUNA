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
import { parseGSkel, parseGMesh, parseGBand, buildAnimationClip, buildBones } from './goldenband'

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
