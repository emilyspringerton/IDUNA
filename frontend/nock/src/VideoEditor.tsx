import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { videos, type NockVideo, type VideoEDL, type VideoSegment, type VideoTimeline, type VideoUploadLink } from './api'
import TimelinePreview from './video/TimelinePreview'
import { loadNleDsp, type NleDsp } from './video/nleEngine'

// VideoEditor.tsx -- the MIXFORGE EDITOR: NOCK's non-linear video+audio editing booth. Founder
// real-time, 2026-09-27: "blue ocean we need a nock video editor that can take uploads from any
// phone via nock", then 2026-09-28: "ensure NOCK tools video editor is PARENA wasm powered and
// shares components with MIXFORGE's shared components... ensure we have full non linear video
// editing for the video and audio clips via nock tools we need a full documentary editing booth",
// "keep the mixforge branding in nock call it the MIXFORGE EDITOR", and "the mpc should work off
// of video streams to pull clips in addition to the traditional import and manual snip workflow."
//
// Four panes, one tab:
//   1. Phone upload: mint a short-lived link, show it as a QR code, any phone camera scans it and
//      uploads from its camera roll (/nock/upload/<token> -- no app, no login on the phone).
//   2. Clip library: everything uploaded (phone or desktop), with a 720p browser-safe proxy so
//      iPhone HEVC plays everywhere. Each clip's viewer has BOTH acquisition paths onto the
//      timeline: the original manual mark-in/mark-out, and an MPC-style pad row that captures a
//      clip live off the playing video stream (quick-grab "last N seconds" pads, plus a
//      press-and-hold pad that marks in on press / out on release, PARENA-computed via
//      video/nleEngine.ts's pad_capture_in/pad_capture_out).
//   3. Timeline: non-linear multi-clip editing -- ordered segments with in/out points PLUS a
//      crossfade transition into each segment and a per-segment fade in/out envelope, both
//      computed by PARENA/stdlib/video/nle.prn (compiled to video/nle.wasm). A live,
//      PARENA-wasm-powered preview (video/TimelinePreview.tsx) composites the whole assembled
//      timeline client-side -- seek anywhere, hear/see the real crossfades -- without waiting on
//      a server render.
//   4. Render: the same live preview's math is re-applied server-side via ffmpeg's xfade/
//      acrossfade/fade filters to produce one distributable MP4 -- see internal/nock/video_store.go.
//
// "Shares components with MIXFORGE": video/nleEngine.ts's instantiateDsp and video/waveform.ts are
// direct ports of MIXFORGE/web/engine.mjs's instantiateDsp and MIXFORGE/web/waveform.mjs (same
// wasm-boot + peak-waveform-canvas pattern) -- not a cross-repo import (MIXFORGE is a build-step-
// free static site in a separate repo; NOCK is a separate Vite/React/TS app), the same real
// pattern, ported. dj.html/multiplayer.html themselves are untouched.

const OUTPUT_PRESETS: { label: string; width: number; height: number }[] = [
  { label: '1080p landscape (16:9)', width: 1920, height: 1080 },
  { label: '1080p vertical (9:16)', width: 1080, height: 1920 },
  { label: 'Square (1:1)', width: 1080, height: 1080 },
  { label: '720p landscape', width: 1280, height: 720 },
]

function fmtTime(ms: number): string {
  const total = Math.max(0, ms) / 1000
  const m = Math.floor(total / 60)
  const s = total - m * 60
  return `${m}:${s.toFixed(1).padStart(4, '0')}`
}

function fmtBytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`
  return `${(n / (1 << 20)).toFixed(1)} MB`
}

function minutesLeft(iso: string): number {
  return Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 60000))
}

// ---------------------------------------------------------------------------------------------

function PhoneUploadPanel({ onUploaded }: { onUploaded: () => void }) {
  const [links, setLinks] = useState<VideoUploadLink[]>([])
  const [label, setLabel] = useState('')
  const [ttl, setTtl] = useState(60)
  const [shown, setShown] = useState<VideoUploadLink | null>(null)
  const [error, setError] = useState<string | null>(null)
  const lastCounts = useRef<string>('')

  const refresh = useCallback(() => {
    videos
      .links()
      .then((l) => {
        setLinks(l)
        // A phone upload landing bumps a link's upload_count -- use that to refresh the library
        // without the phone having to tell us anything.
        const sig = l.map((x) => `${x.id}:${x.upload_count}`).join(',')
        if (lastCounts.current && sig !== lastCounts.current) onUploaded()
        lastCounts.current = sig
      })
      .catch(() => setLinks([]))
  }, [onUploaded])

  useEffect(() => {
    refresh()
    const t = window.setInterval(refresh, 4000)
    return () => window.clearInterval(t)
  }, [refresh])

  const create = async () => {
    setError(null)
    try {
      const l = await videos.createLink(label.trim(), ttl, 0)
      setShown(l)
      setLabel('')
      refresh()
    } catch (e) {
      setError(String((e as Error).message ?? e))
    }
  }

  const active = links.filter((l) => l.active)

  return (
    <section className="video-panel">
      <h3>Upload from a phone</h3>
      <p className="hint">Scan the QR code with any phone camera. No app or login on the phone.</p>
      <div className="video-form-row">
        <input placeholder="Label (e.g. Emily's iPhone)" value={label} onChange={(e) => setLabel(e.target.value)} />
        <select value={ttl} onChange={(e) => setTtl(Number(e.target.value))} aria-label="Link lifetime">
          <option value={15}>15 min</option>
          <option value={60}>1 hour</option>
          <option value={480}>8 hours</option>
          <option value={1440}>1 day</option>
        </select>
        <button type="button" onClick={create}>
          New phone link
        </button>
      </div>
      {error && <p className="error">{error}</p>}
      {shown && shown.active && (
        <div className="video-qr">
          <img src={videos.linkQrUrl(shown.id)} alt="QR code for the phone upload link" width={192} height={192} />
          <div className="video-qr-meta">
            <div className="video-qr-url">{shown.upload_url}</div>
            <div className="hint">Expires in {minutesLeft(shown.expires_at)} min</div>
            <div className="video-form-row">
              <button type="button" onClick={() => navigator.clipboard?.writeText(shown.upload_url)}>
                Copy link
              </button>
              <button type="button" onClick={() => setShown(null)}>
                Hide
              </button>
            </div>
          </div>
        </div>
      )}
      {active.length > 0 && (
        <ul className="video-link-list">
          {active.map((l) => (
            <li key={l.id}>
              <span className="video-link-label">{l.label || `Link #${l.id}`}</span>
              <span className="hint">
                {l.upload_count} uploaded · {minutesLeft(l.expires_at)} min left
              </span>
              <button type="button" onClick={() => setShown(l)}>
                QR
              </button>
              <button
                type="button"
                className="danger"
                onClick={async () => {
                  await videos.revokeLink(l.id).catch(() => undefined)
                  if (shown?.id === l.id) setShown(null)
                  refresh()
                }}
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------------------------

function DesktopUpload({ onUploaded }: { onUploaded: () => void }) {
  const [progress, setProgress] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const onFiles = async (files: FileList | null) => {
    if (!files) return
    setError(null)
    for (const f of Array.from(files)) {
      try {
        setProgress(`${f.name}: 0%`)
        await videos.upload(f, (p) => setProgress(p < 100 ? `${f.name}: ${p}%` : `${f.name}: processing…`))
        onUploaded()
      } catch (e) {
        setError(`${f.name}: ${(e as Error).message}`)
      }
    }
    setProgress(null)
  }

  return (
    <div className="video-form-row">
      <label className="video-file-btn">
        Upload from this computer
        <input type="file" accept="video/*" multiple onChange={(e) => { onFiles(e.target.files); e.target.value = '' }} />
      </label>
      {progress && <span className="hint">{progress}</span>}
      {error && <span className="error">{error}</span>}
    </div>
  )
}

// ---------------------------------------------------------------------------------------------

function ClipLibrary({
  clips,
  selected,
  onSelect,
  onChanged,
}: {
  clips: NockVideo[]
  selected: number | null
  onSelect: (id: number) => void
  onChanged: () => void
}) {
  if (clips.length === 0) {
    return <p className="hint">No clips yet. Upload one from a phone or this computer.</p>
  }
  return (
    <div className="video-grid">
      {clips.map((c) => (
        <div key={c.id} className={`video-card ${selected === c.id ? 'active' : ''}`} onClick={() => onSelect(c.id)}>
          <div className="video-thumb">
            {c.has_thumb ? <img src={videos.thumbUrl(c.id)} alt="" loading="lazy" /> : <span className="hint">{c.proxy_status === 'pending' ? 'processing…' : 'no preview'}</span>}
            <span className="video-duration">{fmtTime(c.duration_ms)}</span>
          </div>
          <div className="video-card-name" title={c.name}>
            {c.name}
          </div>
          <div className="hint">
            {c.width}×{c.height} · {fmtBytes(c.size_bytes)} {c.source === 'phone' && <span className="video-badge">phone</span>}
          </div>
          {c.proxy_status === 'failed' && (
            <div className="error" title={c.proxy_error}>
              preview failed{' '}
              <button type="button" onClick={(e) => { e.stopPropagation(); videos.reproxy(c.id).then(onChanged) }}>
                retry
              </button>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------------------------

function ClipViewer({
  clip,
  onAdd,
  onChanged,
}: {
  clip: NockVideo
  onAdd: (seg: VideoSegment) => void
  onChanged: () => void
}) {
  const ref = useRef<HTMLVideoElement>(null)
  const [inMs, setIn] = useState(0)
  const [outMs, setOut] = useState(clip.duration_ms)
  const [name, setName] = useState(clip.name)
  const [error, setError] = useState<string | null>(null)
  const [dsp, setDsp] = useState<NleDsp | null>(null)
  const [holding, setHolding] = useState(false)
  const holdPressSecRef = useRef(0)
  const [justCaptured, setJustCaptured] = useState<string | null>(null)

  useEffect(() => {
    loadNleDsp().then(setDsp)
  }, [])

  const now = () => Math.round((ref.current?.currentTime ?? 0) * 1000)
  const nowSec = () => ref.current?.currentTime ?? 0
  const src = clip.proxy_status === 'ready' ? videos.proxyUrl(clip.id) : videos.originalUrl(clip.id)
  const valid = outMs > inMs

  // MPC-style pad capture off the playing stream (founder real-time: "the mpc should work off of
  // video streams to pull clips in addition to the traditional import and manual snip workflow").
  // Quick-grab pads add "the last N seconds ending now" straight to the timeline in one tap; the
  // HOLD pad marks in on press / out on release, like a sampler gate. Both go through the exact
  // same onAdd callback the manual mark-in/mark-out flow above already uses -- a third acquisition
  // path onto the timeline, not a separate one.
  const quickGrab = (prerollSec: number) => {
    if (!dsp) return
    const end = nowSec()
    const start = dsp.pad_capture_in(end, prerollSec)
    if (end - start < 0.1) return
    onAdd({ clip_id: clip.id, in_ms: Math.round(start * 1000), out_ms: Math.round(end * 1000) })
    setJustCaptured(`Captured ${fmtTime((end - start) * 1000)} → timeline`)
    window.setTimeout(() => setJustCaptured(null), 2000)
  }
  const holdDown = () => {
    if (!dsp) return
    holdPressSecRef.current = nowSec()
    setHolding(true)
  }
  const holdUp = () => {
    if (!dsp) return
    setHolding(false)
    const press = holdPressSecRef.current
    const release = nowSec()
    const start = dsp.pad_capture_in(press, 0)
    const end = dsp.pad_capture_out(press, release, dsp.default_min_clip_seconds())
    onAdd({ clip_id: clip.id, in_ms: Math.round(start * 1000), out_ms: Math.round(Math.min(end, clip.duration_ms / 1000) * 1000) })
    setJustCaptured(`Captured ${fmtTime((end - start) * 1000)} → timeline`)
    window.setTimeout(() => setJustCaptured(null), 2000)
  }

  return (
    <section className="video-panel">
      <div className="video-form-row">
        <input value={name} onChange={(e) => setName(e.target.value)} aria-label="Clip name" />
        {name !== clip.name && (
          <button type="button" onClick={() => videos.rename(clip.id, name).then(onChanged).catch((e) => setError(e.message))}>
            Rename
          </button>
        )}
        <a href={videos.originalUrl(clip.id, true)}>Download original</a>
        <button
          type="button"
          className="danger"
          onClick={async () => {
            if (!confirm(`Delete clip "${clip.name}"?`)) return
            try {
              await videos.delete(clip.id)
              onChanged()
            } catch (e) {
              setError((e as Error).message)
            }
          }}
        >
          Delete
        </button>
      </div>
      <video ref={ref} className="video-player" src={src} controls playsInline preload="metadata" />
      {clip.proxy_status !== 'ready' && <p className="hint">Preview is still processing — showing the original (may not play in every browser).</p>}
      <div className="video-form-row">
        <button type="button" onClick={() => setIn(now())}>
          Mark in [{fmtTime(inMs)}]
        </button>
        <button type="button" onClick={() => setOut(now())}>
          Mark out [{fmtTime(outMs)}]
        </button>
        <button type="button" onClick={() => { setIn(0); setOut(clip.duration_ms) }}>
          Whole clip
        </button>
        <button type="button" className="primary" disabled={!valid} onClick={() => onAdd({ clip_id: clip.id, in_ms: inMs, out_ms: outMs })}>
          Add {fmtTime(outMs - inMs)} to timeline
        </button>
      </div>
      <div className="video-form-row nle-pad-row">
        <span className="hint">MPC capture (off the playing stream):</span>
        {[2, 5, 10, 30].map((s) => (
          <button key={s} type="button" className="nle-pad" disabled={!dsp} onClick={() => quickGrab(s)} title={`Grab the last ${s}s ending at the current playhead`}>
            −{s}s
          </button>
        ))}
        <button
          type="button"
          className={`nle-pad nle-pad-hold ${holding ? 'active' : ''}`}
          disabled={!dsp}
          onPointerDown={holdDown}
          onPointerUp={holdUp}
          onPointerLeave={() => holding && holdUp()}
          title="Press and hold while playing: press = in point, release = out point"
        >
          HOLD TO MARK
        </button>
        {justCaptured && <span className="hint">{justCaptured}</span>}
      </div>
      {!valid && <p className="error">Out must be after in.</p>}
      {error && <p className="error">{error}</p>}
    </section>
  )
}

// ---------------------------------------------------------------------------------------------

function TimelinePanel({
  clips,
  timeline,
  edl,
  setEdl,
  dirty,
  onSave,
  onRender,
  onDelete,
}: {
  clips: NockVideo[]
  timeline: VideoTimeline
  edl: VideoEDL
  setEdl: (e: VideoEDL) => void
  dirty: boolean
  onSave: () => void
  onRender: () => void
  onDelete: () => void
}) {
  const byId = useMemo(() => new Map(clips.map((c) => [c.id, c])), [clips])
  // Total accounts for crossfade overlap (a transition makes two clips share time, not add to
  // it) -- clamped the same way the live preview/server render clamp it, so this number matches
  // what actually plays.
  const total = edl.segments.reduce((a, s, i) => {
    const dur = s.out_ms - s.in_ms
    if (i === 0) return a + dur
    const prevDur = edl.segments[i - 1].out_ms - edl.segments[i - 1].in_ms
    const tr = Math.min(Math.max(0, s.transition_ms ?? 0), dur, prevDur)
    return a + dur - tr
  }, 0)
  const presetIdx = OUTPUT_PRESETS.findIndex((p) => p.width === edl.width && p.height === edl.height)

  const move = (i: number, d: number) => {
    const segs = [...edl.segments]
    const j = i + d
    if (j < 0 || j >= segs.length) return
    ;[segs[i], segs[j]] = [segs[j], segs[i]]
    setEdl({ ...edl, segments: segs })
  }
  const patch = (i: number, p: Partial<VideoSegment>) => {
    const segs = edl.segments.map((s, k) => (k === i ? { ...s, ...p } : s))
    setEdl({ ...edl, segments: segs })
  }

  return (
    <section className="video-panel">
      <div className="video-form-row">
        <h3 className="video-tl-title">{timeline.name}</h3>
        <select
          value={presetIdx}
          onChange={(e) => {
            const p = OUTPUT_PRESETS[Number(e.target.value)]
            if (p) setEdl({ ...edl, width: p.width, height: p.height })
          }}
          aria-label="Output size"
        >
          {presetIdx < 0 && <option value={-1}>{edl.width}×{edl.height}</option>}
          {OUTPUT_PRESETS.map((p, i) => (
            <option key={p.label} value={i}>
              {p.label}
            </option>
          ))}
        </select>
        <select value={edl.fps} onChange={(e) => setEdl({ ...edl, fps: Number(e.target.value) })} aria-label="Frame rate">
          {[24, 25, 30, 60].map((f) => (
            <option key={f} value={f}>
              {f} fps
            </option>
          ))}
        </select>
        <span className="hint">Total {fmtTime(total)}</span>
      </div>

      {edl.segments.length === 0 ? (
        <p className="hint">Empty. Pick a clip, mark in/out, then "Add to timeline".</p>
      ) : (
        <ol className="video-segments">
          {edl.segments.map((s, i) => {
            const c = byId.get(s.clip_id)
            return (
              <li key={i}>
                <span className="video-seg-idx">{i + 1}</span>
                {c?.has_thumb ? <img src={videos.thumbUrl(s.clip_id)} alt="" /> : <span className="video-seg-noimg" />}
                <span className="video-seg-name">{c?.name ?? `clip ${s.clip_id}`}</span>
                <label>
                  in
                  <input type="number" step={0.1} min={0} value={s.in_ms / 1000} onChange={(e) => patch(i, { in_ms: Math.round(Number(e.target.value) * 1000) })} />
                </label>
                <label>
                  out
                  <input type="number" step={0.1} min={0} value={s.out_ms / 1000} onChange={(e) => patch(i, { out_ms: Math.round(Number(e.target.value) * 1000) })} />
                </label>
                <span className="hint">{fmtTime(s.out_ms - s.in_ms)}</span>
                {i > 0 && (
                  <label title="Crossfade blending in from the previous segment (PARENA-computed, equal-power)">
                    xfade
                    <input
                      type="number"
                      step={0.1}
                      min={0}
                      value={(s.transition_ms ?? 0) / 1000}
                      onChange={(e) => patch(i, { transition_ms: Math.max(0, Math.round(Number(e.target.value) * 1000)) })}
                    />
                  </label>
                )}
                <label title="This clip's own fade-in from black + silence">
                  fade in
                  <input
                    type="number"
                    step={0.1}
                    min={0}
                    value={(s.fade_in_ms ?? 0) / 1000}
                    onChange={(e) => patch(i, { fade_in_ms: Math.max(0, Math.round(Number(e.target.value) * 1000)) })}
                  />
                </label>
                <label title="This clip's own fade-out to black + silence">
                  fade out
                  <input
                    type="number"
                    step={0.1}
                    min={0}
                    value={(s.fade_out_ms ?? 0) / 1000}
                    onChange={(e) => patch(i, { fade_out_ms: Math.max(0, Math.round(Number(e.target.value) * 1000)) })}
                  />
                </label>
                <button type="button" onClick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">
                  ↑
                </button>
                <button type="button" onClick={() => move(i, 1)} disabled={i === edl.segments.length - 1} aria-label="Move down">
                  ↓
                </button>
                <button type="button" className="danger" onClick={() => setEdl({ ...edl, segments: edl.segments.filter((_, k) => k !== i) })} aria-label="Remove">
                  ✕
                </button>
              </li>
            )
          })}
        </ol>
      )}

      <div className="video-form-row">
        <button type="button" onClick={onSave} disabled={!dirty}>
          {dirty ? 'Save' : 'Saved'}
        </button>
        <button type="button" className="primary" onClick={onRender} disabled={edl.segments.length === 0 || timeline.render_status === 'rendering'}>
          {timeline.render_status === 'rendering' ? 'Rendering…' : 'Render MP4'}
        </button>
        <button type="button" className="danger" onClick={onDelete}>
          Delete timeline
        </button>
        <span className={`video-status video-status-${timeline.render_status}`}>{timeline.render_status}</span>
      </div>
      {timeline.render_status === 'failed' && timeline.render_error && <pre className="video-error">{timeline.render_error}</pre>}
      {(timeline.render_status === 'ready' || timeline.render_status === 'stale') && (
        <div>
          {timeline.render_status === 'stale' && <p className="hint">Edited since this render — render again to update it.</p>}
          <video className="video-player" src={videos.outputUrl(timeline.id, false, timeline.rendered_at)} controls playsInline preload="metadata" />
          <a href={videos.outputUrl(timeline.id, true)}>Download {timeline.name}.mp4</a>
        </div>
      )}
    </section>
  )
}

// ---------------------------------------------------------------------------------------------

export default function VideoEditor() {
  const [clips, setClips] = useState<NockVideo[]>([])
  const [selected, setSelected] = useState<number | null>(null)
  const [timelines, setTimelines] = useState<VideoTimeline[]>([])
  const [tlId, setTlId] = useState<number | null>(null)
  const [edl, setEdlState] = useState<VideoEDL | null>(null)
  const [dirty, setDirty] = useState(false)
  const [newName, setNewName] = useState('')
  const [error, setError] = useState<string | null>(null)

  const refreshClips = useCallback(() => {
    videos.list().then(setClips).catch(() => setClips([]))
  }, [])
  const refreshTimelines = useCallback(() => {
    return videos
      .timelines()
      .then((t) => {
        setTimelines(t)
        return t
      })
      .catch(() => [] as VideoTimeline[])
  }, [])

  useEffect(() => {
    refreshClips()
    refreshTimelines().then((t) => {
      if (t.length > 0) setTlId((cur) => cur ?? t[0].id)
    })
  }, [refreshClips, refreshTimelines])

  const timeline = timelines.find((t) => t.id === tlId) ?? null

  // Load the server's EDL whenever the selected timeline changes (not on every poll, or local
  // unsaved edits would be clobbered).
  useEffect(() => {
    if (timeline && !dirty) setEdlState(timeline.edl)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tlId, timeline?.updated_at])

  // Poll while background work is in flight: clip proxies or a render.
  const busy = clips.some((c) => c.proxy_status === 'pending') || timelines.some((t) => t.render_status === 'rendering')
  useEffect(() => {
    if (!busy) return
    const t = window.setInterval(() => {
      refreshClips()
      refreshTimelines()
    }, 2000)
    return () => window.clearInterval(t)
  }, [busy, refreshClips, refreshTimelines])

  const setEdl = (e: VideoEDL) => {
    setEdlState(e)
    setDirty(true)
  }

  const save = async (): Promise<boolean> => {
    if (!timeline || !edl) return false
    setError(null)
    try {
      await videos.saveTimeline(timeline.id, edl)
      setDirty(false)
      await refreshTimelines()
      return true
    } catch (e) {
      setError((e as Error).message)
      return false
    }
  }

  const render = async () => {
    if (!timeline) return
    if (dirty && !(await save())) return
    setError(null)
    try {
      await videos.render(timeline.id)
      await refreshTimelines()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const createTimeline = async () => {
    setError(null)
    try {
      const t = await videos.createTimeline(newName.trim())
      setNewName('')
      await refreshTimelines()
      setDirty(false)
      setTlId(t.id)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const addSegment = async (seg: VideoSegment) => {
    if (!timeline || !edl) {
      setError('Create a timeline first.')
      return
    }
    setEdl({ ...edl, segments: [...edl.segments, seg] })
  }

  const selectedClip = clips.find((c) => c.id === selected) ?? null

  return (
    <div className="video-editor">
      <h2 className="nle-brand">MIXFORGE EDITOR</h2>
      <div className="video-col">
        <PhoneUploadPanel onUploaded={refreshClips} />
        <section className="video-panel">
          <h3>Clips</h3>
          <DesktopUpload onUploaded={refreshClips} />
          <ClipLibrary clips={clips} selected={selected} onSelect={setSelected} onChanged={() => { refreshClips(); setSelected(null) }} />
        </section>
      </div>
      <div className="video-col">
        {selectedClip ? (
          <ClipViewer key={selectedClip.id} clip={selectedClip} onAdd={addSegment} onChanged={refreshClips} />
        ) : (
          <section className="video-panel">
            <p className="hint">Select a clip to preview and cut it.</p>
          </section>
        )}
        <section className="video-panel">
          <div className="video-form-row">
            <h3>Timeline</h3>
            <select
              value={tlId ?? ''}
              onChange={(e) => {
                if (dirty && !confirm('Discard unsaved timeline changes?')) return
                setDirty(false)
                setTlId(Number(e.target.value))
              }}
              aria-label="Timeline"
            >
              {timelines.length === 0 && <option value="">(none yet)</option>}
              {timelines.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
            <input placeholder="new_timeline_name" value={newName} onChange={(e) => setNewName(e.target.value)} />
            <button type="button" onClick={createTimeline} disabled={!newName.trim()}>
              New
            </button>
          </div>
          {error && <p className="error">{error}</p>}
        </section>
        {timeline && edl && <TimelinePreview clips={clips} edl={edl} />}
        {timeline && edl && (
          <TimelinePanel
            clips={clips}
            timeline={timeline}
            edl={edl}
            setEdl={setEdl}
            dirty={dirty}
            onSave={save}
            onRender={render}
            onDelete={async () => {
              if (!confirm(`Delete timeline "${timeline.name}"? Clips are kept.`)) return
              await videos.deleteTimeline(timeline.id).catch((e) => setError(e.message))
              setDirty(false)
              setTlId(null)
              const t = await refreshTimelines()
              if (t.length > 0) setTlId(t[0].id)
            }}
          />
        )}
      </div>
    </div>
  )
}
