import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { videos, type NockVideo, type VideoEDL } from '../api'
import { loadNleDsp, type NleDsp } from './nleEngine'

// TimelinePreview.tsx -- the MIXFORGE EDITOR's live, non-linear preview: seek anywhere across the
// WHOLE assembled timeline and see/hear the real composited result immediately, including
// crossfade transitions and fade envelopes -- no waiting on a server ffmpeg render. Founder
// real-time: "ensure we have full non linear video editing for the video and audio clips via nock
// tools." Every crossfade/fade number comes from src/video/nle.wasm (nleEngine.ts) -- this
// component owns two <video> elements, a canvas, and the Web Audio graph that plays them, same
// "host owns buffers/loop, wasm owns the per-instant decision" split MIXFORGE/web/engine.mjs
// established.
//
// Honest v0 limits: exactly one video track (the timeline's own segment order) -- no picture-in-
// picture/overlay tracks. Drift correction reseeks a video element if it's off by more than 150ms
// from where the timeline says it should be, same "roughly synchronized, not sample-accurate"
// honesty MIXFORGE's own room playback already carries. The final "Render MP4" button (server-side
// ffmpeg) is the distributable output; this is the editing-time preview.

interface Geometry {
  durs: number[] // trimmed length of each segment, seconds
  transitions: number[] // crossfade INTO segment i from i-1, seconds (0 for i===0)
  starts: number[] // segment i's start time in the merged timeline, seconds
  total: number
}

function computeGeometry(edl: VideoEDL): Geometry {
  const n = edl.segments.length
  const durs = edl.segments.map((s) => Math.max(0, (s.out_ms - s.in_ms) / 1000))
  const transitions = edl.segments.map((s, i) => {
    if (i === 0) return 0
    const raw = Math.max(0, (s.transition_ms ?? 0) / 1000)
    return Math.min(raw, durs[i], durs[i - 1])
  })
  const starts: number[] = []
  let acc = 0
  for (let i = 0; i < n; i++) {
    starts.push(i === 0 ? 0 : acc - transitions[i])
    acc = starts[i] + durs[i]
  }
  return { durs, transitions, starts, total: n === 0 ? 0 : starts[n - 1] + durs[n - 1] }
}

function activeIndex(geo: Geometry, g: number): number {
  let idx = 0
  for (let i = 0; i < geo.starts.length; i++) {
    if (geo.starts[i] <= g) idx = i
    else break
  }
  return idx
}

function fmt(s: number): string {
  const m = Math.floor(s / 60)
  const r = s - m * 60
  return `${m}:${r.toFixed(1).padStart(4, '0')}`
}

export default function TimelinePreview({ clips, edl }: { clips: NockVideo[]; edl: VideoEDL }) {
  const byId = useMemo(() => new Map(clips.map((c) => [c.id, c])), [clips])
  const geo = useMemo(() => computeGeometry(edl), [edl])
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const videoARef = useRef<HTMLVideoElement>(null)
  const videoBRef = useRef<HTMLVideoElement>(null)
  const dspRef = useRef<NleDsp | null>(null)
  const audioRef = useRef<{ ctx: AudioContext; gainA: GainNode; gainB: GainNode } | null>(null)
  const clipInARef = useRef<number | null>(null)
  const clipInBRef = useRef<number | null>(null)
  const rafRef = useRef<number | null>(null)
  const clockRef = useRef<{ startPerfMs: number; startOffsetSec: number } | null>(null)

  const [ready, setReady] = useState(false)
  const [playing, setPlaying] = useState(false)
  const [t, setT] = useState(0)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    loadNleDsp().then((d) => (dspRef.current = d))
  }, [])

  const stopLoop = useCallback(() => {
    if (rafRef.current != null) cancelAnimationFrame(rafRef.current)
    rafRef.current = null
  }, [])

  useEffect(() => stopLoop, [stopLoop])

  // renderAt -- the one real per-frame decision function: given global timeline time g, pick the
  // active segment (and, inside a transition window, its predecessor), point the two <video>
  // elements at the right clip/time, and draw the composited canvas frame + audio gains.
  const renderAt = useCallback(
    (g: number) => {
      const dsp = dspRef.current
      const canvas = canvasRef.current
      const vA = videoARef.current
      const vB = videoBRef.current
      if (!dsp || !canvas || !vA || !vB || geo.starts.length === 0) return
      const i = activeIndex(geo, g)
      const seg = edl.segments[i]
      const clipCur = byId.get(seg.clip_id)
      const localCur = g - geo.starts[i]
      const inTransition = i > 0 && localCur < geo.transitions[i]
      const prevIdx = inTransition ? i - 1 : null

      const fadeEnv = (segIdx: number, local: number) => {
        const s = edl.segments[segIdx]
        return dsp.fade_envelope(local, geo.durs[segIdx], Math.max(0, (s.fade_in_ms ?? 0) / 1000), Math.max(0, (s.fade_out_ms ?? 0) / 1000))
      }

      const gainCur = inTransition ? dsp.xfade_in_gain(localCur, geo.transitions[i]) : 1
      const envCur = fadeEnv(i, localCur)
      const alphaCur = gainCur * envCur

      let alphaPrev = 0
      let localPrev = 0
      if (prevIdx != null) {
        localPrev = localCur + geo.durs[prevIdx] - geo.transitions[i]
        alphaPrev = dsp.xfade_out_gain(localCur, geo.transitions[i]) * fadeEnv(prevIdx, localPrev)
      }

      const point = (video: HTMLVideoElement, clipIdRef: React.MutableRefObject<number | null>, segIdx: number, local: number) => {
        const s = edl.segments[segIdx]
        const c = byId.get(s.clip_id)
        if (!c) return
        const target = s.in_ms / 1000 + local
        if (clipIdRef.current !== c.id) {
          video.src = c.proxy_status === 'ready' ? videos.proxyUrl(c.id) : videos.originalUrl(c.id)
          clipIdRef.current = c.id
          video.currentTime = target
        } else if (Math.abs(video.currentTime - target) > 0.15) {
          video.currentTime = target
        }
      }
      point(vA, clipInARef, i, localCur)
      if (prevIdx != null) point(vB, clipInBRef, prevIdx, localPrev)

      const audio = audioRef.current
      if (audio) {
        audio.gainA.gain.value = alphaCur
        audio.gainB.gain.value = alphaPrev
      }

      const ctx2d = canvas.getContext('2d')!
      ctx2d.clearRect(0, 0, canvas.width, canvas.height)
      ctx2d.fillStyle = '#000'
      ctx2d.fillRect(0, 0, canvas.width, canvas.height)
      if (prevIdx != null && vB.readyState >= 2) {
        ctx2d.globalAlpha = alphaPrev
        ctx2d.drawImage(vB, 0, 0, canvas.width, canvas.height)
      }
      if (vA.readyState >= 2) {
        ctx2d.globalAlpha = alphaCur
        ctx2d.drawImage(vA, 0, 0, canvas.width, canvas.height)
      }
      ctx2d.globalAlpha = 1
      if (!clipCur) return
    },
    [byId, edl.segments, geo],
  )

  const ensureAudio = useCallback(() => {
    if (audioRef.current) return audioRef.current
    const vA = videoARef.current!, vB = videoBRef.current!
    const ctx = new AudioContext()
    const gainA = ctx.createGain(), gainB = ctx.createGain()
    ctx.createMediaElementSource(vA).connect(gainA).connect(ctx.destination)
    ctx.createMediaElementSource(vB).connect(gainB).connect(ctx.destination)
    const a = { ctx, gainA, gainB }
    audioRef.current = a
    return a
  }, [])

  const seek = useCallback(
    (g: number) => {
      const clamped = Math.max(0, Math.min(geo.total, g))
      setT(clamped)
      renderAt(clamped)
    },
    [geo.total, renderAt],
  )

  const play = useCallback(() => {
    if (geo.starts.length === 0) {
      setError('Add at least one segment to the timeline first.')
      return
    }
    setError(null)
    ensureAudio().ctx.resume()
    setReady(true)
    const startT = t >= geo.total ? 0 : t
    clockRef.current = { startPerfMs: performance.now(), startOffsetSec: startT }
    videoARef.current?.play().catch(() => undefined)
    videoBRef.current?.play().catch(() => undefined)
    setPlaying(true)
    const tick = () => {
      const c = clockRef.current
      if (!c) return
      const g = c.startOffsetSec + (performance.now() - c.startPerfMs) / 1000
      if (g >= geo.total) {
        renderAt(geo.total)
        setT(geo.total)
        pause()
        return
      }
      setT(g)
      renderAt(g)
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [geo.total, t, ensureAudio, renderAt])

  const pause = useCallback(() => {
    stopLoop()
    setPlaying(false)
    videoARef.current?.pause()
    videoBRef.current?.pause()
  }, [stopLoop])

  // Re-render the current frame whenever the EDL changes (a segment/fade/transition edit) so the
  // preview reflects it immediately, even while paused.
  useEffect(() => {
    if (!playing) renderAt(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [edl])

  const aspect = edl.height > 0 ? edl.width / edl.height : 16 / 9

  return (
    <section className="video-panel">
      <h3>Live preview (PARENA-wasm, non-linear -- seek anywhere)</h3>
      {error && <p className="error">{error}</p>}
      <div className="nle-preview-canvas-wrap" style={{ aspectRatio: `${aspect}` }}>
        <canvas ref={canvasRef} width={edl.width || 1920} height={edl.height || 1080} className="nle-preview-canvas" />
      </div>
      <video ref={videoARef} className="nle-hidden-video" playsInline preload="auto" />
      <video ref={videoBRef} className="nle-hidden-video" playsInline preload="auto" />
      <div className="video-form-row">
        <button type="button" onClick={playing ? pause : play} disabled={geo.starts.length === 0}>
          {playing ? 'Pause' : 'Play'}
        </button>
        <input
          type="range"
          min={0}
          max={geo.total || 0.001}
          step={0.01}
          value={t}
          onChange={(e) => {
            if (playing) pause()
            seek(Number(e.target.value))
          }}
          style={{ flex: 1 }}
          aria-label="Timeline scrub"
        />
        <span className="hint">
          {fmt(t)} / {fmt(geo.total)}
        </span>
      </div>
      {!ready && geo.starts.some((_, i) => geo.transitions[i] > 0) && <p className="hint">Crossfades are computed by nle.wasm and shown live once you press Play or scrub.</p>}
    </section>
  )
}
