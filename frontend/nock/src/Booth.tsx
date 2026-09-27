import { useCallback, useEffect, useRef, useState, type PointerEvent as RPointerEvent } from 'react'
import { sounds, type Sound } from './api'
import workletUrl from './booth/booth.worklet.ts?worker&url'
import type { BoothMsg } from './booth/booth.worklet.ts'
import type { BoothEngine } from './booth/engine.ts'

// Booth.tsx -- founder real-time (2026-09-27): "it needs to emulate an MPC and a pioneer mixer and
// 4 CDJS". Four CDJ-style decks around a four-channel DJM-style mixer (club layout 3·1 | mixer |
// 2·4), with an MPC-style 4x4 pad sampler below. Audio runs in an AudioWorklet (booth/engine.ts);
// every curve, filter, resample, loop and swing decision is PARENA stdlib/audio compiled to TS.
// Tracks and pad samples come from the Sounds library.
//
// Honest v0 limits: key lock (MASTER TEMPO) is not applied yet (no time-stretcher); BPM is typed
// or tapped, not detected; the headphone CUE mix is a second output you can monitor instead of
// master (browsers can't split one context across two sound cards without setSinkId juggling).

type Snap = ReturnType<BoothEngine['snapshot']>

const PAD_KEYS = ['z', 'x', 'c', 'v', 'a', 's', 'd', 'f', 'q', 'w', 'e', 'r', '1', '2', '3', '4'] // pad 0 = bottom-left
const DECK_ORDER_LEFT = [2, 0] // 3, 1
const DECK_ORDER_RIGHT = [1, 3] // 2, 4

function Knob({ label, value, min = 0, max = 1, center, onChange, size = 38 }: {
  label: string; value: number; min?: number; max?: number; center?: number; onChange: (v: number) => void; size?: number
}) {
  const drag = useRef<{ y: number; v: number } | null>(null)
  const t = (value - min) / (max - min)
  const ang = -135 + t * 270
  const down = (e: RPointerEvent) => { (e.target as Element).setPointerCapture(e.pointerId); drag.current = { y: e.clientY, v: value } }
  const move = (e: RPointerEvent) => {
    if (!drag.current) return
    const nv = drag.current.v + ((drag.current.y - e.clientY) / 150) * (max - min)
    onChange(Math.min(max, Math.max(min, nv)))
  }
  return (
    <div className="knob" title={`${label}: ${value.toFixed(2)} (double-click resets)`}
      onDoubleClick={() => onChange(center ?? (min + max) / 2)}>
      <svg width={size} height={size} viewBox="-20 -20 40 40" onPointerDown={down} onPointerMove={move}
        onPointerUp={() => { drag.current = null }}>
        <circle r="16" className="knob-body" />
        <line x1="0" y1="0" x2="0" y2="-14" className="knob-mark" transform={`rotate(${ang})`} />
      </svg>
      <span>{label}</span>
    </div>
  )
}

function Meter({ lit, total = 15 }: { lit: number; total?: number }) {
  return (
    <div className="meter">
      {Array.from({ length: total }, (_, i) => total - 1 - i).map((i) => (
        <div key={i} className={`led ${i < lit ? (i >= 13 ? 'red' : i >= 11 ? 'amber' : 'green') : ''}`} />
      ))}
    </div>
  )
}

function Jog({ onJog, onTouch }: { onJog: (revPerSec: number) => void; onTouch: (t: boolean) => void }) {
  const last = useRef<{ a: number; t: number } | null>(null)
  const el = useRef<HTMLDivElement>(null)
  const angle = (e: RPointerEvent) => {
    const r = el.current!.getBoundingClientRect()
    return Math.atan2(e.clientY - (r.top + r.height / 2), e.clientX - (r.left + r.width / 2))
  }
  return (
    <div ref={el} className="jog" title="Jog: drag around the platter. Vinyl mode = scratch, otherwise pitch bend."
      onPointerDown={(e) => { (e.target as Element).setPointerCapture(e.pointerId); last.current = { a: angle(e), t: performance.now() }; onTouch(true) }}
      onPointerMove={(e) => {
        if (!last.current) return
        const a = angle(e), now = performance.now()
        let da = a - last.current.a
        if (da > Math.PI) da -= 2 * Math.PI
        if (da < -Math.PI) da += 2 * Math.PI
        const dt = Math.max(1, now - last.current.t) / 1000
        onJog(da / (2 * Math.PI) / dt)
        last.current = { a, t: now }
      }}
      onPointerUp={() => { last.current = null; onJog(0); onTouch(false) }}
    />
  )
}

export default function Booth() {
  const ctxRef = useRef<AudioContext | null>(null)
  const nodeRef = useRef<AudioWorkletNode | null>(null)
  const monitorRef = useRef<'master' | 'cue'>('master')
  const [started, setStarted] = useState(false)
  const [snap, setSnap] = useState<Snap | null>(null)
  const [library, setLibrary] = useState<Sound[]>([])
  const [error, setError] = useState<string | null>(null)
  // mirrored control state (engine is the source of truth for audio; this drives the UI)
  const [ch, setCh] = useState(() => Array.from({ length: 4 }, (_, i) => ({ trim: 0.5, hi: 0.5, mid: 0.5, low: 0.5, color: 0, fader: 0, assign: i < 2 ? 0 : 2, cue: false })))
  const [decks, setDecks] = useState(() => Array.from({ length: 4 }, () => ({ tempoFader: 0, rangeSel: 1, bpm: 120, vinylMode: true, syncOn: false, reverse: false, quantize: true })))
  const [eng, setEng] = useState({ xfader: 0.5, xfCurve: 0, faderCurve: 1, masterLevel: 0.8, hpMix: 0.5, hpLevel: 0.8, masterDeck: 0, bank: 0, samplerLevel: 0.8, bpm: 120, swing: 50, repeatGrid: 2 })
  const [monitor, setMonitor] = useState<'master' | 'cue'>('master')
  const [padNames, setPadNames] = useState<string[][]>(() => Array.from({ length: 4 }, () => Array(16).fill('')))
  const [repeat, setRepeat] = useState(false)
  const [shift, setShift] = useState(false)
  const taps = useRef<number[][]>([[], [], [], []])

  const send = useCallback((m: BoothMsg, transfer: Transferable[] = []) => nodeRef.current?.port.postMessage(m, transfer), [])

  useEffect(() => { sounds.list().then(setLibrary).catch((e) => setError(String(e))) }, [])

  const start = async () => {
    try {
      const ctx = new AudioContext({ latencyHint: 'interactive' })
      await ctx.audioWorklet.addModule(workletUrl)
      const node = new AudioWorkletNode(ctx, 'nock-booth', { numberOfInputs: 0, numberOfOutputs: 2, outputChannelCount: [2, 2] })
      node.port.onmessage = (e) => setSnap(e.data as Snap)
      node.connect(ctx.destination, monitorRef.current === 'master' ? 0 : 1)
      ctxRef.current = ctx; nodeRef.current = node
      setStarted(true)
    } catch (e) { setError(`Audio start: ${e}`) }
  }

  const route = (m: 'master' | 'cue') => {
    const node = nodeRef.current, ctx = ctxRef.current
    setMonitor(m); monitorRef.current = m
    if (!node || !ctx) return
    node.disconnect()
    node.connect(ctx.destination, m === 'master' ? 0 : 1)
  }

  const decode = async (id: number) => {
    const bytes = await (await fetch(sounds.audioUrl(id), { credentials: 'include' })).arrayBuffer()
    const b = await ctxRef.current!.decodeAudioData(bytes)
    return { b, buf: Array.from({ length: b.numberOfChannels }, (_, i) => new Float32Array(b.getChannelData(i))) }
  }

  const loadDeck = async (d: number, id: number) => {
    const s = library.find((x) => x.id === id)
    if (!s) return
    try {
      const { b, buf } = await decode(id)
      send({ t: 'load', d, buf, srcSr: b.sampleRate, name: s.name, bpm: decks[d].bpm }, buf.map((x) => x.buffer))
    } catch (e) { setError(`Load ${s.name}: ${e}`) }
  }
  const loadPad = async (pad: number, id: number) => {
    const s = library.find((x) => x.id === id)
    if (!s) return
    try {
      const { b, buf } = await decode(id)
      send({ t: 'loadPad', bank: eng.bank, pad, buf, srcSr: b.sampleRate, name: s.name }, buf.map((x) => x.buffer))
      setPadNames((p) => p.map((row, i) => (i === eng.bank ? row.map((n, j) => (j === pad ? s.name : n)) : row)))
    } catch (e) { setError(`Load pad: ${e}`) }
  }

  const setChan = (c: number, k: keyof (typeof ch)[number], v: number | boolean) => {
    setCh((all) => all.map((x, i) => (i === c ? { ...x, [k]: v } : x)))
    send({ t: 'ch', c, k, v })
  }
  const setDeck = (d: number, k: keyof (typeof decks)[number], v: number | boolean) => {
    setDecks((all) => all.map((x, i) => (i === d ? { ...x, [k]: v } : x)))
    send({ t: 'deck', d, k, v })
  }
  const setE = (k: keyof typeof eng, v: number) => { setEng((e) => ({ ...e, [k]: v })); send({ t: 'eng', k, v }) }
  const call = useCallback((m: string, ...a: unknown[]) => send({ t: 'call', m, a }), [send])

  const tap = (d: number) => {
    const now = performance.now(), t = taps.current[d].filter((x) => now - x < 3000)
    t.push(now); taps.current[d] = t
    if (t.length >= 4) {
      const bpm = 60000 / ((t[t.length - 1] - t[0]) / (t.length - 1))
      setDeck(d, 'bpm', Math.round(bpm * 10) / 10)
    }
  }

  // keyboard pads (MPC 4x4 on z..v / a..f / q..r / 1..4)
  useEffect(() => {
    if (!started) return
    const held = new Set<string>()
    const kd = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).tagName === 'INPUT' || e.repeat) return
      const p = PAD_KEYS.indexOf(e.key.toLowerCase())
      if (p < 0) return
      held.add(e.key)
      if (repeat) send({ t: 'eng', k: 'repeatHeld', v: [...new Set([...held].map((k) => PAD_KEYS.indexOf(k.toLowerCase())))] })
      else call('padDown', p, 100)
    }
    const ku = (e: KeyboardEvent) => {
      const p = PAD_KEYS.indexOf(e.key.toLowerCase())
      if (p < 0) return
      held.delete(e.key)
      if (repeat) send({ t: 'eng', k: 'repeatHeld', v: [...held].map((k) => PAD_KEYS.indexOf(k.toLowerCase())) })
      call('padUp', p)
    }
    window.addEventListener('keydown', kd); window.addEventListener('keyup', ku)
    return () => { window.removeEventListener('keydown', kd); window.removeEventListener('keyup', ku) }
  }, [started, repeat, send, call])

  useEffect(() => () => { ctxRef.current?.close() }, [])

  const fmtTime = (samples: number, sr: number) => {
    const s = samples / sr
    return `${Math.floor(s / 60)}:${(s % 60).toFixed(1).padStart(4, '0')}`
  }

  const deckPanel = (d: number) => {
    const s = snap?.decks[d], k = decks[d]
    return (
      <div key={d} className={`deck ${s?.playing ? 'playing' : ''}`}>
        <div className="deck-head">
          <strong>DECK {d + 1}</strong>
          {eng.masterDeck === d ? <span className="badge">MASTER</span> : <button type="button" className="small" onClick={() => setE('masterDeck', d)}>make master</button>}
        </div>
        <select value="" onChange={(e) => loadDeck(d, Number(e.target.value))}>
          <option value="">{s?.loaded ? s.name : '— load track —'}</option>
          {library.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
        </select>
        <div className="deck-display">
          <div className="deck-progress"><div style={{ width: `${s && s.len ? (100 * s.pos) / s.len : 0}%` }} /></div>
          <div className="deck-readout">
            <span>{s ? fmtTime(s.pos, s.srcSr) : '0:00.0'}</span>
            <span className="bpm">{s ? s.bpm.toFixed(1) : '---'} BPM</span>
            <span>{s ? `${s.pitchPct >= 0 ? '+' : ''}${s.pitchPct.toFixed(2)}%` : ''}</span>
          </div>
          <div className="phase"><div style={{ left: `${(s?.phase ?? 0) * 100}%` }} /></div>
        </div>
        <div className="deck-body">
          <Jog onJog={(v) => send({ t: 'deck', d, k: 'jogRevPerSec', v })} onTouch={(t) => send({ t: 'deck', d, k: 'touching', v: t })} />
          <div className="tempo">
            <input type="range" className="vertical" min={-1} max={1} step={0.001} value={k.tempoFader}
              onChange={(e) => setDeck(d, 'tempoFader', Number(e.target.value))} onDoubleClick={() => setDeck(d, 'tempoFader', 0)} />
            <select value={k.rangeSel} onChange={(e) => setDeck(d, 'rangeSel', Number(e.target.value))}>
              <option value={0}>±6</option><option value={1}>±10</option><option value={2}>±16</option><option value={3}>WIDE</option>
            </select>
          </div>
        </div>
        <div className="row">
          <button type="button" className="cue-btn" onPointerDown={() => call('cuePress', d)} onPointerUp={() => call('cueRelease', d)}>CUE</button>
          <button type="button" className="play-btn" onClick={() => call('playPause', d)}>{s?.playing ? '❚❚' : '▶'}</button>
          <button type="button" onClick={() => call('brake', d)} title="vinyl stop">STOP</button>
        </div>
        <div className="row hotcues">
          {Array.from({ length: 8 }, (_, i) => (
            <button type="button" key={i} className={s?.hotCues[i] != null ? 'set' : ''}
              onClick={() => call('hotCue', d, i, shift)}>{'ABCDEFGH'[i]}</button>
          ))}
        </div>
        <div className="row">
          <button type="button" className={s?.loopOn ? 'active' : ''} onClick={() => call('beatLoop', d)}>LOOP {s ? (s.loopBeats < 1 ? `1/${1 / s.loopBeats}` : s.loopBeats) : 4}</button>
          <button type="button" onClick={() => call('loopHalve', d)}>½</button>
          <button type="button" onClick={() => call('loopDouble', d)}>2×</button>
          <button type="button" onClick={() => call('loopExit', d)}>EXIT</button>
        </div>
        <div className="row">
          <label>BPM <input type="number" step={0.1} value={k.bpm} onChange={(e) => setDeck(d, 'bpm', Number(e.target.value))} /></label>
          <button type="button" onClick={() => tap(d)}>TAP</button>
          <button type="button" onClick={() => call('setGridHere', d)} title="beat 1 is here">GRID</button>
        </div>
        <div className="row toggles">
          <label><input type="checkbox" checked={k.syncOn} onChange={(e) => setDeck(d, 'syncOn', e.target.checked)} /> SYNC</label>
          <label><input type="checkbox" checked={k.vinylMode} onChange={(e) => setDeck(d, 'vinylMode', e.target.checked)} /> VINYL</label>
          <label><input type="checkbox" checked={k.quantize} onChange={(e) => setDeck(d, 'quantize', e.target.checked)} /> Q</label>
          <label><input type="checkbox" checked={k.reverse} onChange={(e) => setDeck(d, 'reverse', e.target.checked)} /> REV</label>
        </div>
      </div>
    )
  }

  const strip = (c: number) => {
    const x = ch[c]
    return (
      <div key={c} className="strip">
        <div className="strip-num">{c + 1}</div>
        <Knob label="TRIM" value={x.trim} onChange={(v) => setChan(c, 'trim', v)} />
        <Knob label="HI" value={x.hi} onChange={(v) => setChan(c, 'hi', v)} />
        <Knob label="MID" value={x.mid} onChange={(v) => setChan(c, 'mid', v)} />
        <Knob label="LOW" value={x.low} onChange={(v) => setChan(c, 'low', v)} />
        <Knob label="COLOR" value={x.color} min={-1} max={1} center={0} onChange={(v) => setChan(c, 'color', v)} />
        <button type="button" className={`cue ${x.cue ? 'active' : ''}`} onClick={() => setChan(c, 'cue', !x.cue)}>CUE</button>
        <div className="fader-row">
          <Meter lit={snap?.meters[c] ?? 0} />
          <input type="range" className="vertical" min={0} max={1} step={0.001} value={x.fader} onChange={(e) => setChan(c, 'fader', Number(e.target.value))} />
        </div>
        <select value={x.assign} onChange={(e) => setChan(c, 'assign', Number(e.target.value))}>
          <option value={0}>A</option><option value={1}>THRU</option><option value={2}>B</option>
        </select>
      </div>
    )
  }

  if (!started) {
    return (
      <div className="layout-single">
        <h2>Booth</h2>
        <p className="hint">Four CDJ-style decks, a four-channel DJM-style mixer and an MPC-style pad sampler. Load tracks and pad samples from the Sounds tab.</p>
        {error && <p className="error">{error}</p>}
        <button type="button" onClick={start}>Power on (start audio)</button>
      </div>
    )
  }

  return (
    <div className="booth">
      {error && <p className="error">{error}</p>}
      <div className="booth-top">
        <div className="deck-col">{DECK_ORDER_LEFT.map(deckPanel)}</div>
        <div className="mixer">
          <div className="strips">{[0, 1, 2, 3].map(strip)}</div>
          <div className="master-section">
            <Meter lit={snap?.master ?? 0} />
            <Knob label="MASTER" value={eng.masterLevel} onChange={(v) => setE('masterLevel', v)} />
            <Knob label="HP MIX" value={eng.hpMix} onChange={(v) => setE('hpMix', v)} />
            <Knob label="HP LVL" value={eng.hpLevel} onChange={(v) => setE('hpLevel', v)} />
            <label>Monitor <select value={monitor} onChange={(e) => route(e.target.value as 'master' | 'cue')}>
              <option value="master">MASTER</option><option value="cue">HEADPHONES (cue mix)</option>
            </select></label>
            <label>Ch fader curve <select value={eng.faderCurve} onChange={(e) => setE('faderCurve', Number(e.target.value))}>
              <option value={0}>slow</option><option value={1}>standard</option><option value={2}>fast</option>
            </select></label>
            <label>X-fader curve <select value={eng.xfCurve} onChange={(e) => setE('xfCurve', Number(e.target.value))}>
              <option value={0}>smooth</option><option value={1}>mid</option><option value={2}>sharp (scratch)</option>
            </select></label>
          </div>
          <div className="xfader">
            <span>A</span>
            <input type="range" min={0} max={1} step={0.001} value={eng.xfader} onChange={(e) => setE('xfader', Number(e.target.value))} onDoubleClick={() => setE('xfader', 0.5)} />
            <span>B</span>
          </div>
        </div>
        <div className="deck-col">{DECK_ORDER_RIGHT.map(deckPanel)}</div>
      </div>

      <div className="mpc">
        <div className="mpc-controls">
          <strong>MPC</strong>
          {['A', 'B', 'C', 'D'].map((b, i) => (
            <button type="button" key={b} className={eng.bank === i ? 'active' : ''} onClick={() => setE('bank', i)}>BANK {b}</button>
          ))}
          <label>BPM <input type="number" value={eng.bpm} onChange={(e) => setE('bpm', Number(e.target.value))} /></label>
          <label>Swing <input type="range" min={50} max={75} value={eng.swing} onChange={(e) => setE('swing', Number(e.target.value))} /> {eng.swing}%</label>
          <label>Grid <select value={eng.repeatGrid} onChange={(e) => setE('repeatGrid', Number(e.target.value))}>
            {['1/8', '1/8T', '1/16', '1/16T', '1/32', '1/32T'].map((g, i) => <option key={g} value={i}>{g}</option>)}
          </select></label>
          <label><input type="checkbox" checked={repeat} onChange={(e) => { setRepeat(e.target.checked); send({ t: 'eng', k: 'repeatHeld', v: [] }) }} /> NOTE REPEAT (hold keys)</label>
          <label><input type="checkbox" checked={shift} onChange={(e) => setShift(e.target.checked)} /> SHIFT (hot cue clears)</label>
          <Knob label="LEVEL" value={eng.samplerLevel} onChange={(v) => setE('samplerLevel', v)} />
        </div>
        <div className="pads">
          {[12, 13, 14, 15, 8, 9, 10, 11, 4, 5, 6, 7, 0, 1, 2, 3].map((p) => (
            <div key={p} className={`pad ${snap?.voices.includes(p) ? 'hit' : ''}`}>
              <button type="button" className="pad-hit"
                onPointerDown={(e) => call('padDown', p, Math.round(40 + 87 * Math.min(1, (e.pressure || 0.8))))}
                onPointerUp={() => call('padUp', p)}>
                <span className="pad-num">{`${'ABCD'[eng.bank]}${String(p + 1).padStart(2, '0')}`}</span>
                <span className="pad-name">{padNames[eng.bank][p] || '—'}</span>
                <kbd>{PAD_KEYS[p].toUpperCase()}</kbd>
              </button>
              <select value="" onChange={(e) => loadPad(p, Number(e.target.value))}>
                <option value="">load…</option>
                {library.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
