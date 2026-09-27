// RigTools.tsx — move a mesh or a clip onto a different rig (founder real-time, 2026-09-27: "we
// need the primatives for remapping a mesh onto a new rig"). A panel on a character-library
// card, driving IDUNA's internal/nock rig primitives (rig_bonemap.go / rig_remap.go /
// rig_retarget.go):
//   1. pick the target rig: any other library row that has one;
//   2. review the automatic bone map (how each joint was matched, what's left unmapped) and
//      override any joint by hand;
//   3. "Remap mesh" re-skins this row's mesh onto the target rig, and/or "Retarget clip" moves
//      this row's animation onto it. Each saves a NEW row; this row is never modified.
import { useEffect, useMemo, useState } from 'react'
import { animations, type AnimationSummary, type BoneMapReport, type RemapReport, type RetargetReport } from './api'
import { parseGSkel } from './goldenband'

interface Props {
  source: AnimationSummary
  library: AnimationSummary[]
  onDone: () => void
}

export default function RigTools({ source, library, onDone }: Props) {
  const targets = library.filter((a) => a.has_skel && a.id !== source.id)
  const [targetId, setTargetId] = useState<number | ''>('')
  const [report, setReport] = useState<BoneMapReport | null>(null)
  const [dstJoints, setDstJoints] = useState<string[]>([])
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const [mode, setMode] = useState<'auto' | 'names' | 'proximity'>('auto')
  const [fit, setFit] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<{ kind: 'remap'; r: RemapReport; name: string } | { kind: 'retarget'; r: RetargetReport; name: string } | null>(null)
  const [showAll, setShowAll] = useState(false)

  const target = targets.find((t) => t.id === targetId)

  const pickTarget = (id: number | '') => {
    setTargetId(id)
    setReport(null)
    setOverrides({})
    setResult(null)
    setError(null)
  }

  useEffect(() => {
    if (targetId === '' || !source.has_skel) return
    let cancelled = false
    Promise.all([
      animations.boneMap(source.id, targetId),
      fetch(animations.downloadUrl(targetId, 'gskel')).then((r) => r.arrayBuffer()),
    ])
      .then(([rep, buf]) => {
        if (cancelled) return
        setReport(rep)
        setDstJoints(parseGSkel(buf).map((j) => j.name))
      })
      .catch((err) => !cancelled && setError(String(err)))
    return () => {
      cancelled = true
    }
  }, [source.id, source.has_skel, targetId])

  // Effective map = automatic map with the person's overrides on top ("" = unmapped).
  const effective = useMemo(() => {
    const m: Record<string, string> = { ...(report?.map ?? {}) }
    for (const [k, v] of Object.entries(overrides)) {
      if (v === '') delete m[k]
      else m[k] = v
    }
    return m
  }, [report, overrides])

  const srcJoints = useMemo(() => {
    if (!report) return []
    return [...new Set([...Object.keys(report.map), ...(report.unmapped_source ?? [])])].sort()
  }, [report])

  const run = async (kind: 'remap' | 'retarget') => {
    if (targetId === '' || !target) return
    const suggested = kind === 'remap' ? `${source.name}_on_${target.name}` : `${source.name}_for_${target.name}`
    const name = window.prompt('Name for the new row:', suggested.slice(0, 64))
    if (!name) return
    setBusy(true)
    setError(null)
    try {
      if (kind === 'remap') {
        const res = await animations.remapMesh(source.id, { target_id: targetId, name, mode: source.has_skel ? mode : 'proximity', fit, bone_map: overrides })
        setResult({ kind, r: res.report, name })
      } else {
        const res = await animations.retarget(source.id, { target_id: targetId, name, bone_map: overrides })
        setResult({ kind, r: res.report, name })
      }
      onDone()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const unmappedSrc = srcJoints.filter((s) => !effective[s])
  const listed = showAll ? srcJoints : unmappedSrc

  return (
    <div className="animation-attach-panel hint rig-tools">
      <p>
        Move this {source.has_mesh ? 'mesh' : ''}
        {source.has_mesh && source.has_animation ? ' or ' : ''}
        {source.has_animation ? 'animation' : ''} onto a different rig. The result is saved as a new row; <strong>{source.name}</strong> is left as it is.
      </p>
      <div className="animation-attach-row">
        <span>Target rig:</span>
        <select value={targetId} onChange={(e) => pickTarget(e.target.value ? Number(e.target.value) : '')}>
          <option value="">-- pick a rig --</option>
          {targets.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
              {source.skeleton_hash && t.skeleton_hash === source.skeleton_hash ? ' (same rig)' : ''}
            </option>
          ))}
        </select>
      </div>

      {targetId !== '' && !source.has_skel && (
        <p>This mesh has no rig of its own, so it will be skinned from scratch by proximity to the target's bones.</p>
      )}

      {report && (
        <>
          <p>
            Bone map: <strong>{Object.keys(effective).length}</strong> of {srcJoints.length} joints matched
            {unmappedSrc.length > 0 && <> · {unmappedSrc.length} unmapped (their weights fold into the nearest mapped parent)</>}
            {(report.unmapped_dest?.length ?? 0) > 0 && <> · {report.unmapped_dest!.length} target joints unused</>}.
          </p>
          <label className="animator-check">
            <input type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} /> show every joint (not just unmapped)
          </label>
          {listed.length > 0 && (
            <table className="bone-map-table">
              <thead>
                <tr>
                  <th>{source.name}</th>
                  <th>{target?.name}</th>
                  <th>how</th>
                </tr>
              </thead>
              <tbody>
                {listed.map((s) => (
                  <tr key={s}>
                    <td>{s}</td>
                    <td>
                      <select value={effective[s] ?? ''} onChange={(e) => setOverrides({ ...overrides, [s]: e.target.value })}>
                        <option value="">(unmapped)</option>
                        {dstJoints.map((d) => (
                          <option key={d} value={d}>
                            {d}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td>{s in overrides ? 'override' : (report.how[s] ?? '')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}

      {targetId !== '' && (
        <>
          {source.has_mesh && (
            <div className="animation-attach-row">
              <button disabled={busy} onClick={() => run('remap')}>
                Remap mesh onto {target?.name}
              </button>
              {source.has_skel && (
                <select value={mode} onChange={(e) => setMode(e.target.value as typeof mode)}>
                  <option value="auto">keep weights, by bone map (fallback: proximity)</option>
                  <option value="names">keep weights, by bone map only</option>
                  <option value="proximity">new weights from bone proximity</option>
                </select>
              )}
              <label className="animator-check">
                <input type="checkbox" checked={fit} onChange={(e) => setFit(e.target.checked)} /> fit mesh to rig size
              </label>
            </div>
          )}
          {source.has_animation && source.has_skel && (
            <div className="animation-attach-row">
              <button disabled={busy} onClick={() => run('retarget')}>
                Retarget clip onto {target?.name}
              </button>
            </div>
          )}
        </>
      )}

      {error && <p className="error">{error}</p>}
      {result?.kind === 'remap' && (
        <p>
          Created <strong>{result.name}</strong>: {result.r.vertices_by_name} vertices kept their weights through the bone map, {result.r.vertices_proximity} were weighted by
          proximity{result.r.fit_scale ? `, mesh scaled ×${result.r.fit_scale.toFixed(3)}` : ''}.
          {result.r.warnings?.map((w) => (
            <span key={w} className="error">
              {' '}
              ⚠ {w}
            </span>
          ))}
        </p>
      )}
      {result?.kind === 'retarget' && (
        <p>
          Created <strong>{result.name}</strong>: {result.r.joints_animated} joints animated, root motion scaled ×{result.r.height_ratio.toFixed(2)} for the target's height.
          {result.r.warnings?.map((w) => (
            <span key={w}> {w}.</span>
          ))}
        </p>
      )}
    </div>
  )
}
