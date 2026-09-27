import { useCallback, useEffect, useRef, useState } from 'react'
import { soundFilters, sounds, type Sound, type SoundFilter } from './api'
import { autoChain, BIQUAD_KINDS, defaultStage, measure, renderChain, type Chain, type Measurement, type Stage, type StageType } from './sound/chain.ts'
import { encodeWav } from './sound/wav.ts'

// Sounds.tsx -- NOCK's sound library (founder real-time, 2026-09-27: "nock and shankpit engine need
// sound engineering primatives we need a way to upload and record in nock as well as pass filters
// around"). Upload files, record from the mic, build a filter chain, hear it, save the render back
// into the library, and save/clone/export chains so they can be passed around -- including to
// SHANKPIT, which fetches a chain by name from the public /api/v1/nock-sound-filters/<name> route.
//
// All processing runs in this tab with TypeScript compiled from PARENA stdlib/audio (see
// sound/chain.ts); nothing is rendered server-side.

const STAGE_TYPES: StageType[] = ['biquad', 'compressor', 'expander', 'deesser', 'limiter', 'gain', 'normalize']

// Which numeric fields each stage type exposes in the editor: [key, label, min, max, step]
const FIELDS: Record<StageType, [keyof Stage, string, number, number, number][]> = {
  biquad: [['freq', 'Freq Hz', 10, 24000, 1], ['q', 'Q', 0.1, 40, 0.01], ['gain_db', 'Gain dB', -48, 24, 0.5], ['mix', 'Mix', 0, 1, 0.01]],
  compressor: [['threshold_db', 'Thresh dB', -80, 0, 0.5], ['ratio', 'Ratio', 1, 40, 0.1], ['knee_db', 'Knee dB', 0, 24, 0.5],
    ['attack_ms', 'Attack ms', 0, 500, 0.5], ['release_ms', 'Release ms', 1, 5000, 1], ['makeup_db', 'Makeup dB', -24, 24, 0.5], ['mix', 'Mix', 0, 1, 0.01]],
  expander: [['threshold_db', 'Thresh dB', -100, 0, 0.5], ['ratio', 'Ratio', 1, 40, 0.1], ['knee_db', 'Knee dB', 0, 24, 0.5],
    ['range_db', 'Range dB', -120, 0, 1], ['attack_ms', 'Attack ms', 0, 500, 0.5], ['release_ms', 'Release ms', 1, 5000, 1]],
  deesser: [['freq', 'Freq Hz', 2000, 16000, 100], ['intensity', 'Intensity', 0, 1, 0.01], ['attack_ms', 'Attack ms', 0, 100, 0.5], ['release_ms', 'Release ms', 1, 1000, 1]],
  limiter: [['ceiling_db', 'Ceiling dB', -24, 0, 0.1], ['release_ms', 'Release ms', 1, 2000, 1]],
  gain: [['gain_db', 'Gain dB', -60, 24, 0.5]],
  normalize: [['target_lufs', 'Target LUFS', -40, -5, 0.5], ['ceiling_db', 'Ceiling dB', -12, 0, 0.1]],
}

let sharedCtx: AudioContext | null = null
function audioCtx(): AudioContext {
  if (!sharedCtx) sharedCtx = new AudioContext()
  return sharedCtx
}

async function decodeSound(id: number): Promise<AudioBuffer> {
  const bytes = await (await fetch(sounds.audioUrl(id), { credentials: 'include' })).arrayBuffer()
  return audioCtx().decodeAudioData(bytes)
}
const channelsOf = (b: AudioBuffer) => Array.from({ length: b.numberOfChannels }, (_, i) => b.getChannelData(i))

function fmtDb(v: number) {
  return v <= -199 ? '-inf' : v.toFixed(1)
}

export default function Sounds() {
  const [list, setList] = useState<Sound[]>([])
  const [filters, setFilters] = useState<SoundFilter[]>([])
  const [selected, setSelected] = useState<Sound | null>(null)
  const [chain, setChain] = useState<Chain>({ version: 1, stages: [] })
  const [activeFilter, setActiveFilter] = useState<SoundFilter | null>(null)
  const [stats, setStats] = useState<Measurement | null>(null)
  const [preview, setPreview] = useState<{ url: string; wav: Uint8Array; sr: number; ch: number; ms: number; after: Measurement } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [recording, setRecording] = useState(false)
  const recRef = useRef<MediaRecorder | null>(null)

  const refresh = useCallback(() => {
    sounds.list().then(setList).catch((e) => setError(String(e)))
    soundFilters.list().then(setFilters).catch((e) => setError(String(e)))
  }, [])
  useEffect(() => { refresh() }, [refresh])

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(label); setError(null)
    try { await fn() } catch (e) { setError(String(e)) } finally { setBusy(null) }
  }

  const upload = (files: FileList | null) => run('Uploading', async () => {
    for (const f of Array.from(files ?? [])) {
      const name = f.name.replace(/\.[^.]+$/, '').replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 64) || 'sound'
      let meta = {}
      try {
        const b = await audioCtx().decodeAudioData(await f.arrayBuffer())
        meta = { duration_ms: b.duration * 1000, sample_rate: b.sampleRate, channels: b.numberOfChannels }
      } catch { /* undecodable in this browser: store it anyway, server checks the type */ }
      await sounds.upload(name, f, { source: 'upload', ...meta })
    }
    refresh()
  })

  const toggleRecord = async () => {
    if (recording) { recRef.current?.stop(); return }
    setError(null)
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false } })
      const mr = new MediaRecorder(stream)
      const parts: Blob[] = []
      mr.ondataavailable = (e) => parts.push(e.data)
      mr.onstop = () => {
        stream.getTracks().forEach((t) => t.stop())
        setRecording(false)
        const blob = new Blob(parts, { type: mr.mimeType || 'audio/webm' })
        const name = `take_${new Date().toISOString().replace(/[-:T.Z]/g, '').slice(0, 14)}`
        run('Saving recording', async () => {
          let meta = {}
          try {
            const b = await audioCtx().decodeAudioData(await blob.arrayBuffer())
            meta = { duration_ms: b.duration * 1000, sample_rate: b.sampleRate, channels: b.numberOfChannels }
          } catch { /* keep going without metadata */ }
          await sounds.upload(name, blob, { source: 'record', ...meta })
          refresh()
        })
      }
      recRef.current = mr
      mr.start()
      setRecording(true)
    } catch (e) {
      setError(`Microphone: ${e}`)
    }
  }

  const select = (s: Sound) => {
    setSelected(s); setStats(null); setPreview(null)
    run('Measuring', async () => {
      const b = await decodeSound(s.id)
      setStats(measure(channelsOf(b), b.sampleRate))
    })
  }

  const doRender = () => selected && run('Rendering', async () => {
    const b = await decodeSound(selected.id)
    const out = renderChain(channelsOf(b), b.sampleRate, chain)
    const wav = encodeWav(out, b.sampleRate)
    if (preview) URL.revokeObjectURL(preview.url)
    setPreview({
      url: URL.createObjectURL(new Blob([wav as BlobPart], { type: 'audio/wav' })), wav, sr: b.sampleRate,
      ch: out.length, ms: b.duration * 1000, after: measure(out, b.sampleRate),
    })
  })

  const saveRender = () => selected && preview && run('Saving render', async () => {
    const base = `${selected.name}_${activeFilter?.name ?? 'fx'}`.slice(0, 60)
    let name = base, i = 2
    while (list.some((s) => s.name === name)) name = `${base.slice(0, 58)}_${i++}`
    await sounds.upload(name, new Blob([preview.wav as BlobPart], { type: 'audio/wav' }), {
      source: 'render', duration_ms: preview.ms, sample_rate: preview.sr, channels: preview.ch,
      parent_id: selected.id, filter_id: activeFilter?.id,
    })
    refresh()
  })

  const saveChainAs = () => {
    const name = prompt('Save filter chain as (letters, digits, _ -):', activeFilter?.name ?? 'my_chain')
    if (!name) return
    run('Saving chain', async () => {
      const existing = filters.find((f) => f.name === name)
      const f = existing ? await soundFilters.update(existing.id, { chain }) : await soundFilters.create(name, '', chain)
      setActiveFilter(f)
      refresh()
    })
  }

  const importChain = () => {
    const text = prompt('Paste a filter chain JSON ({"version":1,"stages":[...]}):')
    if (!text) return
    try {
      const parsed = JSON.parse(text)
      const c: Chain = parsed.chain ?? parsed
      if (c.version !== 1 || !Array.isArray(c.stages)) throw new Error('not a version 1 chain')
      setChain(c); setActiveFilter(null)
    } catch (e) { setError(`Import: ${e}`) }
  }

  const setStage = (i: number, patch: Partial<Stage>) =>
    setChain((c) => ({ ...c, stages: c.stages.map((s, j) => (j === i ? { ...s, ...patch } : s)) }))
  const moveStage = (i: number, d: number) => setChain((c) => {
    const st = [...c.stages], j = i + d
    if (j < 0 || j >= st.length) return c;
    [st[i], st[j]] = [st[j], st[i]]
    return { ...c, stages: st }
  })

  return (
    <div className="layout-single sounds">
      <h2>Sounds</h2>
      <p className="hint">
        Upload or record audio, shape it with a filter chain, and save the result. Every filter is PARENA
        (<code>stdlib/audio</code>) compiled to TypeScript and running in this tab; SHANKPIT runs the same chains in C.
        Chains are shareable: save one, then any engine can fetch it by name without a login.
      </p>
      {error && <p className="error">{error}</p>}
      {busy && <p className="hint">{busy}…</p>}

      <section className="sound-actions">
        <label className="btn">
          Upload audio
          <input type="file" accept="audio/*" multiple hidden onChange={(e) => upload(e.target.files)} />
        </label>
        <button type="button" className={recording ? 'danger' : ''} onClick={toggleRecord}>
          {recording ? '■ Stop recording' : '● Record from mic'}
        </button>
      </section>

      <div className="sounds-grid">
        <section>
          <h3>Library</h3>
          <ul className="sound-list">
            {list.map((s) => (
              <li key={s.id} className={selected?.id === s.id ? 'active' : ''}>
                <button type="button" onClick={() => select(s)}>
                  <strong>{s.name}</strong>{' '}
                  <span className="hint">
                    {s.source} · {(s.duration_ms / 1000).toFixed(1)}s · {s.sample_rate ? `${s.sample_rate / 1000}k` : '?'}
                    {s.parent_id ? ` · from #${s.parent_id}` : ''}
                  </span>
                </button>
                <button type="button" className="danger small" onClick={() => {
                  if (confirm(`Delete sound "${s.name}"?`)) run('Deleting', async () => { await sounds.delete(s.id); if (selected?.id === s.id) setSelected(null); refresh() })
                }}>×</button>
              </li>
            ))}
            {list.length === 0 && <li className="hint">No sounds yet — upload or record one.</li>}
          </ul>
        </section>

        <section>
          <h3>Filter chain {activeFilter && <span className="hint">({activeFilter.name})</span>}</h3>
          <div className="chain-toolbar">
            <select value={activeFilter?.id ?? ''} onChange={(e) => {
              const f = filters.find((x) => x.id === Number(e.target.value)) ?? null
              setActiveFilter(f)
              if (f) setChain(f.chain)
            }}>
              <option value="">— saved chains —</option>
              {filters.map((f) => <option key={f.id} value={f.id}>{f.name}</option>)}
            </select>
            <button type="button" disabled={!stats} title="jivetalking's adaptive rules (PARENA jive_rules.prn) from this sound's measurements"
              onClick={() => { if (stats) { setChain(autoChain(stats)); setActiveFilter(null) } }}>Auto (jivetalking)</button>
            <button type="button" onClick={saveChainAs}>Save chain</button>
            <button type="button" disabled={!activeFilter} onClick={() => {
              const name = activeFilter && prompt('Clone as:', `${activeFilter.name}_copy`)
              if (activeFilter && name) run('Cloning', async () => { setActiveFilter(await soundFilters.clone(activeFilter.id, name)); refresh() })
            }}>Clone</button>
            <button type="button" onClick={() => navigator.clipboard.writeText(JSON.stringify(chain, null, 2))}>Copy JSON</button>
            <button type="button" onClick={importChain}>Import JSON</button>
            <button type="button" className="danger" disabled={!activeFilter} onClick={() => {
              if (activeFilter && confirm(`Delete chain "${activeFilter.name}"?`)) run('Deleting chain', async () => { await soundFilters.delete(activeFilter.id); setActiveFilter(null); refresh() })
            }}>Delete chain</button>
          </div>
          {activeFilter && (
            <p className="hint">Engine URL: <code>{soundFilters.publicUrl(activeFilter.name)}</code></p>
          )}

          <ol className="chain-stages">
            {chain.stages.map((s, i) => (
              <li key={i} className={s.bypass ? 'bypassed' : ''}>
                <div className="stage-head">
                  <strong>{s.type}</strong>
                  {s.type === 'biquad' && (
                    <select value={s.kind ?? 1} onChange={(e) => setStage(i, { kind: Number(e.target.value) })}>
                      {BIQUAD_KINDS.map((k, j) => <option key={k} value={j}>{k}</option>)}
                    </select>
                  )}
                  <label><input type="checkbox" checked={!!s.bypass} onChange={(e) => setStage(i, { bypass: e.target.checked })} /> bypass</label>
                  <button type="button" className="small" onClick={() => moveStage(i, -1)}>↑</button>
                  <button type="button" className="small" onClick={() => moveStage(i, 1)}>↓</button>
                  <button type="button" className="small danger" onClick={() => setChain((c) => ({ ...c, stages: c.stages.filter((_, j) => j !== i) }))}>×</button>
                </div>
                <div className="stage-fields">
                  {FIELDS[s.type].map(([key, label, min, max, step]) => (
                    <label key={key}>
                      {label}
                      <input type="number" min={min} max={max} step={step} value={Number(s[key] ?? 0)}
                        onChange={(e) => setStage(i, { [key]: Number(e.target.value) })} />
                    </label>
                  ))}
                </div>
              </li>
            ))}
          </ol>
          <div className="chain-toolbar">
            Add:{' '}
            {STAGE_TYPES.map((t) => (
              <button key={t} type="button" className="small" onClick={() => setChain((c) => ({ ...c, stages: [...c.stages, defaultStage(t)] }))}>{t}</button>
            ))}
          </div>
        </section>

        <section>
          <h3>Listen</h3>
          {selected ? (
            <>
              <p><strong>{selected.name}</strong> (original)</p>
              <audio controls src={sounds.audioUrl(selected.id)} />
              {stats && (
                <p className="hint">
                  {stats.lufs.toFixed(1)} LUFS · peak {fmtDb(stats.peakDb)} dBFS · noise floor {fmtDb(stats.noiseFloorDb)} dBFS
                </p>
              )}
              <button type="button" disabled={chain.stages.length === 0 || !!busy} onClick={doRender}>Render through chain</button>
              {preview && (
                <>
                  <p><strong>Rendered</strong></p>
                  <audio controls src={preview.url} />
                  <p className="hint">
                    {preview.after.lufs.toFixed(1)} LUFS · peak {fmtDb(preview.after.peakDb)} dBFS · noise floor {fmtDb(preview.after.noiseFloorDb)} dBFS
                  </p>
                  <button type="button" onClick={saveRender}>Save render to library</button>
                </>
              )}
            </>
          ) : (
            <p className="hint">Select a sound.</p>
          )}
        </section>
      </div>
    </div>
  )
}
