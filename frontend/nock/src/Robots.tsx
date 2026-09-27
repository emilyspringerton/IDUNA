import { useCallback, useEffect, useRef, useState } from 'react'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { robots, type Robot, type RobotSyncReport } from './api'
import { buildBones, parseGSkel, type ParsedJoint } from './goldenband'

// Robots.tsx -- NOCK's robot registry (founder real-time, 2026-09-27: "upgrade shankpit and nock
// to formal rigid body physics we need to get goldenband rigged up with real robot data from
// industrial data sheets"). Lists GOLDEN BAND datasheet robot rigs, shows every joint's
// manufacturer limits (range, max speed, max torque) and link mass with the cited sources, and a
// 3D view of the rig you can pose with sliders clamped to those same datasheet ranges. Uploads
// need all three GOLDEN BAND files; the server rejects a compiled rig that wasn't built from the
// spec it's uploaded with, or a spec that cites no sources.

const DEG = 180 / Math.PI

function RigView({ robot }: { robot: Robot }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const bonesRef = useRef<{ bones: THREE.Bone[]; joints: ParsedJoint[] } | null>(null)
  const [angles, setAngles] = useState<number[]>(() => (robot.rig?.joints ?? []).map(() => 0))
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container || !robot.has_skel) return
    let disposed = false
    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x11141a)
    const camera = new THREE.PerspectiveCamera(45, 1, 0.01, 100)
    camera.position.set(1.4, 1.1, 1.6)
    const renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
    container.appendChild(renderer.domElement)
    scene.add(new THREE.HemisphereLight(0xffffff, 0x222233, 1.2))
    scene.add(new THREE.GridHelper(2, 20, 0x444455, 0x2a2a33))
    const controls = new OrbitControls(camera, renderer.domElement)
    controls.target.set(0, 0.3, 0)
    controls.enableDamping = true
    let raf = 0
    const render = () => {
      const rect = container.getBoundingClientRect()
      const w = Math.max(1, rect.width), h = Math.max(1, rect.height)
      if (renderer.domElement.width !== Math.round(w * renderer.getPixelRatio())) {
        renderer.setSize(w, h)
        camera.aspect = w / h
        camera.updateProjectionMatrix()
      }
      controls.update()
      renderer.render(scene, camera)
      raf = requestAnimationFrame(render)
    }
    raf = requestAnimationFrame(render)
    fetch(robots.downloadUrl(robot.id, 'gskel'))
      .then((r) => (r.ok ? r.arrayBuffer() : Promise.reject(new Error(`gskel: ${r.status}`))))
      .then((buf) => {
        if (disposed) return
        const joints = parseGSkel(buf)
        const bones = buildBones(joints)
        // Robot rigs are z-up (URDF convention); the viewer is y-up.
        const base = new THREE.Group()
        base.rotation.x = -Math.PI / 2
        bones.filter((_, i) => joints[i].parentIndex < 0).forEach((b) => base.add(b))
        // A sphere per joint and a link cylinder from each joint to its parent.
        const jointMat = new THREE.MeshStandardMaterial({ color: 0xf2b134, roughness: 0.5 })
        const linkMat = new THREE.MeshStandardMaterial({ color: 0x8fa3b8, roughness: 0.7 })
        bones.forEach((b, i) => {
          b.add(new THREE.Mesh(new THREE.SphereGeometry(0.035, 16, 12), jointMat))
          const p = joints[i].parentIndex
          if (p >= 0) {
            const len = joints[i].restTranslation.length()
            if (len > 1e-4) {
              const cyl = new THREE.Mesh(new THREE.CylinderGeometry(0.025, 0.025, len, 12), linkMat)
              const mid = joints[i].restTranslation.clone().multiplyScalar(0.5)
              cyl.position.copy(mid)
              cyl.quaternion.setFromUnitVectors(new THREE.Vector3(0, 1, 0), joints[i].restTranslation.clone().normalize())
              bones[p].add(cyl)
            }
          }
        })
        scene.add(base)
        bonesRef.current = { bones, joints }
      })
      .catch((e) => setError(String(e)))
    return () => {
      disposed = true
      cancelAnimationFrame(raf)
      controls.dispose()
      renderer.dispose()
      container.removeChild(renderer.domElement)
      bonesRef.current = null
    }
  }, [robot.id, robot.has_skel])

  // Pose: skeleton joint i+1 is robot joint i (gbtool robot compile's convention); its local
  // rotation is the rest (origin) rotation followed by the joint's own axis rotation.
  useEffect(() => {
    const cur = bonesRef.current
    if (!cur || !robot.rig) return
    robot.rig.joints.forEach((j, i) => {
      const bone = cur.bones[i + 1]
      if (!bone) return
      const axis = new THREE.Vector3(...j.axis).normalize()
      bone.quaternion.copy(cur.joints[i + 1].restRotation).multiply(new THREE.Quaternion().setFromAxisAngle(axis, angles[i] ?? 0))
    })
  }, [angles, robot.rig])

  if (!robot.has_skel) return <p className="hint">No display skeleton (.gskel) uploaded for this robot.</p>
  return (
    <div>
      {error && <p className="error">{error}</p>}
      <div ref={containerRef} style={{ width: '100%', height: 360 }} />
      {robot.rig?.joints.map((j, i) => {
        const lo = j.continuous ? -2 * Math.PI : j.lower
        const hi = j.continuous ? 2 * Math.PI : j.upper
        return (
          <label key={j.name} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ width: 170 }}>{j.name}</span>
            <input type="range" min={lo} max={hi} step={0.01} value={angles[i] ?? 0}
              onChange={(e) => setAngles((a) => a.map((v, k) => (k === i ? Number(e.target.value) : v)))} />
            <span className="hint">{((angles[i] ?? 0) * DEG).toFixed(0)}°</span>
          </label>
        )
      })}
    </div>
  )
}

export default function Robots() {
  const [list, setList] = useState<Robot[]>([])
  const [selected, setSelected] = useState<Robot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sync, setSync] = useState<RobotSyncReport | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [files, setFiles] = useState<{ spec: File | null; grobot: File | null; gskel: File | null }>({ spec: null, grobot: null, gskel: null })

  const refresh = useCallback(() => {
    robots.list().then(setList).catch(() => setList([]))
  }, [])
  useEffect(() => refresh(), [refresh])

  const open = async (id: number) => {
    setError(null)
    try {
      setSelected(await robots.get(id))
    } catch (e) {
      setError(String(e))
    }
  }

  const upload = async () => {
    if (!files.spec || !files.grobot) return setError('Pick the .grobot.json spec and the compiled .grobot (the .gskel is optional).')
    setError(null)
    try {
      const r = await robots.upload(files.spec, files.grobot, files.gskel)
      refresh()
      open(r.id)
    } catch (e) {
      setError(String(e))
    }
  }

  const syncFromGit = async () => {
    setError(null)
    setSyncing(true)
    try {
      setSync(await robots.syncFromGit())
      refresh()
    } catch (e) {
      setError(String(e))
    } finally {
      setSyncing(false)
    }
  }

  const remove = async (r: Robot) => {
    if (!confirm(`Delete robot "${r.name}"?`)) return
    try {
      await robots.delete(r.id)
      if (selected?.id === r.id) setSelected(null)
      refresh()
    } catch (e) {
      setError(String(e))
    }
  }

  let sources: { id: string; title: string; url: string }[] = []
  try {
    sources = selected?.spec_json ? JSON.parse(selected.spec_json).sources ?? [] : []
  } catch {
    sources = []
  }

  return (
    <div className="layout-single">
      <h2>Robots</h2>
      <p className="hint">
        Real industrial robot rigs for GOLDEN BAND physics: every mass, inertia, joint range, max speed and max torque
        comes from the manufacturer's published data and cites its source. Build them with GOLDENBAND's{' '}
        <code>gbtool robot import-ur</code> / <code>gbtool robot compile</code> (see <code>format/GROBOT_FORMAT.md</code>).
      </p>
      {error && <p className="error">{error}</p>}
      <p>
        <button type="button" onClick={syncFromGit} disabled={syncing}>
          {syncing ? 'Syncing…' : 'Sync from git (GOLDENBAND)'}
        </button>{' '}
        <span className="hint">
          Git is the source of truth: IDUNA also re-imports GOLDENBAND's robots/ + assets/robots/ on every start, and{' '}
          <code>nock robots-sync</code> does the same from a shell. Hand uploads are never overwritten.
        </span>
      </p>
      {sync && (
        <div className="hint">
          Synced from <code>{sync.dir}</code>{sync.revision && <> @ <code>{sync.revision.slice(0, 12)}</code></>}:{' '}
          {sync.items.map((it) => `${it.name} ${it.action}${it.detail ? ` (${it.detail})` : ''}`).join(' · ')}
          {sync.not_in_git && sync.not_in_git.length > 0 && <> · no longer in git: {sync.not_in_git.join(', ')}</>}
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        <label>spec (.grobot.json) <input type="file" accept=".json" onChange={(e) => setFiles((f) => ({ ...f, spec: e.target.files?.[0] ?? null }))} /></label>
        <label>rig (.grobot) <input type="file" accept=".grobot" onChange={(e) => setFiles((f) => ({ ...f, grobot: e.target.files?.[0] ?? null }))} /></label>
        <label>skeleton (.gskel) <input type="file" accept=".gskel" onChange={(e) => setFiles((f) => ({ ...f, gskel: e.target.files?.[0] ?? null }))} /></label>
        <button type="button" onClick={upload}>Upload robot</button>
      </div>
      <table className="robot-table">
        <thead>
          <tr><th>Robot</th><th>Manufacturer</th><th>Joints</th><th>Moving mass</th><th /></tr>
        </thead>
        <tbody>
          {list.map((r) => (
            <tr key={r.id} className={selected?.id === r.id ? 'active' : ''}>
              <td><button type="button" onClick={() => open(r.id)}>{r.model}</button> <span className="hint">{r.source_location?.startsWith('git:') ? r.source_location : 'uploaded'}</span></td>
              <td>{r.manufacturer}</td>
              <td>{r.joint_count}</td>
              <td>{r.moving_mass_kg.toFixed(3)} kg</td>
              <td><button className="danger" type="button" onClick={() => remove(r)}>Delete</button></td>
            </tr>
          ))}
          {list.length === 0 && (
            <tr><td colSpan={5} className="hint">No robots yet -- press “Sync from git” (or upload a spec + compiled rig).</td></tr>
          )}
        </tbody>
      </table>
      {selected?.rig && (
        <div>
          <h3>{selected.manufacturer} {selected.model} — datasheet envelope</h3>
          <table className="robot-table">
            <thead>
              <tr><th>Joint</th><th>Range</th><th>Max speed</th><th>Max torque</th><th>Link mass</th></tr>
            </thead>
            <tbody>
              {selected.rig.joints.map((j) => (
                <tr key={j.name}>
                  <td>{j.name}</td>
                  <td>{j.continuous ? 'continuous' : `${(j.lower * DEG).toFixed(0)}° … ${(j.upper * DEG).toFixed(0)}°`}</td>
                  <td>{(j.velocity * DEG).toFixed(0)}°/s</td>
                  <td>{j.effort.toFixed(0)} N·m</td>
                  <td>{j.mass.toFixed(3)} kg</td>
                </tr>
              ))}
            </tbody>
          </table>
          <h4>Sources</h4>
          <ul>
            {sources.map((s) => (
              <li key={s.id}><a href={s.url} target="_blank" rel="noopener noreferrer">{s.title}</a></li>
            ))}
          </ul>
          <p className="hint">
            spec sha256 <code>{selected.spec_hash.slice(0, 16)}…</code> · downloads:{' '}
            <a href={robots.downloadUrl(selected.id, 'spec')}>spec</a> · <a href={robots.downloadUrl(selected.id, 'grobot')}>rig</a>
            {selected.has_skel && <> · <a href={robots.downloadUrl(selected.id, 'gskel')}>skeleton</a></>}
          </p>
          <RigView key={selected.id} robot={selected} />
        </div>
      )}
    </div>
  )
}
