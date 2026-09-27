// chain.ts -- NOCK's filter-chain runner. Every number that matters comes from TypeScript
// compiled out of PARENA stdlib/audio (../generated/AudioDsp.ts, see scripts/gen_nock_audio.sh);
// this file is only the host loop: it owns the sample buffers and per-channel filter state and
// threads them through PARENA's step functions -- the same split SHANKPIT's C runner uses, so a
// chain authored here sounds the same in the engine.
//
// Chain JSON (version 1) mirrors IDUNA internal/nock.FilterChain, which validates it server-side.

import * as P from '../generated/AudioDsp.ts'

export type StageType = 'biquad' | 'compressor' | 'expander' | 'deesser' | 'limiter' | 'gain' | 'normalize'

export interface Stage {
  type: StageType
  bypass?: boolean
  kind?: number
  freq?: number
  q?: number
  gain_db?: number
  threshold_db?: number
  ratio?: number
  knee_db?: number
  range_db?: number
  attack_ms?: number
  release_ms?: number
  makeup_db?: number
  intensity?: number
  ceiling_db?: number
  target_lufs?: number
  mix?: number
}

export interface Chain {
  version: 1
  stages: Stage[]
}

export const BIQUAD_KINDS = ['Lowpass', 'Highpass', 'Bandpass', 'Notch', 'Peak (bell)', 'Low shelf', 'High shelf']

/** Defaults a new stage of each type starts with in the editor. */
export function defaultStage(type: StageType): Stage {
  switch (type) {
    case 'biquad':
      return { type, kind: 1, freq: 80, q: 0.707, gain_db: 0, mix: 1 }
    case 'compressor':
      return { type, threshold_db: -20, ratio: 3, knee_db: 4, attack_ms: 10, release_ms: 200, makeup_db: 0, mix: 1 }
    case 'expander':
      return { type, threshold_db: -50, ratio: 2, knee_db: 3, range_db: -27, attack_ms: 10, release_ms: 250 }
    case 'deesser':
      return { type, freq: 6000, intensity: 0.4, attack_ms: 1, release_ms: 60 }
    case 'limiter':
      return { type, ceiling_db: -1, release_ms: 50 }
    case 'gain':
      return { type, gain_db: 0 }
    case 'normalize':
      return { type, target_lufs: -18, ceiling_db: -1.5 }
  }
}

// ---------------------------------------------------------------------------------------------
// Measurement (BS.1770 / EBU R128 via PARENA's K-weighting + gating)
// ---------------------------------------------------------------------------------------------

export interface Measurement {
  lufs: number // integrated, gated
  peakDb: number // sample peak
  rmsDb: number
  noiseFloorDb: number // 10th percentile of 50 ms RMS windows
  dynamicRangeDb: number // peak - noise floor
}

function kWeighted(ch: Float32Array, sr: number): Float64Array {
  const sb0 = P.kweightShelfB0(sr), sb1 = P.kweightShelfB1(sr), sb2 = P.kweightShelfB2(sr)
  const sa1 = P.kweightShelfA1(sr), sa2 = P.kweightShelfA2(sr)
  const ha1 = P.kweightHpA1(sr), ha2 = P.kweightHpA2(sr)
  const out = new Float64Array(ch.length)
  let s1 = 0, s2 = 0, h1 = 0, h2 = 0
  for (let i = 0; i < ch.length; i++) {
    const x = ch[i]
    const y = P.biquadTdf2Y(sb0, x, s1)
    const ns1 = P.biquadTdf2S1(sb1, sa1, x, y, s2)
    s2 = P.biquadTdf2S2(sb2, sa2, x, y)
    s1 = ns1
    const z = P.biquadTdf2Y(1, y, h1) // stage 2: b = [1, -2, 1]
    const nh1 = P.biquadTdf2S1(-2, ha1, y, z, h2)
    h2 = P.biquadTdf2S2(1, ha2, y, z)
    h1 = nh1
    out[i] = z
  }
  return out
}

/** Integrated loudness (LUFS), 400 ms blocks with 75% overlap, absolute + relative gating. */
export function integratedLufs(channels: Float32Array[], sr: number): number {
  const kw = channels.map((c) => kWeighted(c, sr))
  const n = channels[0]?.length ?? 0
  const block = Math.round(0.4 * sr), hop = Math.round(0.1 * sr)
  const blocks: number[] = []
  for (let start = 0; start + block <= n; start += hop) {
    let ms = 0
    for (const c of kw) {
      let sum = 0
      for (let i = start; i < start + block; i++) sum += c[i] * c[i]
      ms += sum / block
    }
    blocks.push(ms)
  }
  const abs = blocks.filter((ms) => P.lufsAbsoluteGate(P.lufsFromMeanSquare(ms)) === 1)
  if (abs.length === 0) return -70
  const ungated = P.lufsFromMeanSquare(abs.reduce((a, b) => a + b, 0) / abs.length)
  const rel = abs.filter((ms) => P.lufsRelativeGate(P.lufsFromMeanSquare(ms), ungated) === 1)
  if (rel.length === 0) return ungated
  return P.lufsFromMeanSquare(rel.reduce((a, b) => a + b, 0) / rel.length)
}

export function measure(channels: Float32Array[], sr: number): Measurement {
  let peak = 0, sumSq = 0
  const n = channels[0]?.length ?? 0
  for (const c of channels) for (let i = 0; i < n; i++) {
    const a = Math.abs(c[i])
    if (a > peak) peak = a
    sumSq += c[i] * c[i]
  }
  const win = Math.max(1, Math.round(0.05 * sr))
  const windows: number[] = []
  for (let s = 0; s + win <= n; s += win) {
    let ss = 0
    for (const c of channels) for (let i = s; i < s + win; i++) ss += c[i] * c[i]
    windows.push(P.linearToDb(Math.sqrt(ss / (win * channels.length))))
  }
  windows.sort((a, b) => a - b)
  const noiseFloorDb = windows.length ? windows[Math.floor(windows.length * 0.1)] : -200
  const peakDb = P.linearToDb(peak)
  return {
    lufs: integratedLufs(channels, sr),
    peakDb,
    rmsDb: P.linearToDb(Math.sqrt(sumSq / Math.max(1, n * channels.length))),
    noiseFloorDb,
    dynamicRangeDb: peakDb - noiseFloorDb,
  }
}

// ---------------------------------------------------------------------------------------------
// Auto chain: jivetalking's adaptive rules (PARENA stdlib/audio/jive_rules.prn) in the browser
// ---------------------------------------------------------------------------------------------

/**
 * Builds a voice-mastering chain from a recording's measurements using the SAME rules the
 * jivetalking fork runs (PARENA jive_rules.prn). Honest limits: the browser measures level
 * statistics only (no spectral centroid/rolloff/flux yet), so the spectrum-driven decisions
 * (highpass cutoff, de-esser intensity, lowpass) fall back to jivetalking's own defaults.
 */
export function autoChain(m: Measurement): Chain {
  const nf = m.noiseFloorDb
  return {
    version: 1,
    stages: [
      { type: 'biquad', kind: 1, freq: 80, q: 0.707, gain_db: 0, mix: 1 },
      {
        type: 'expander',
        threshold_db: P.jiveNrThreshold(1, nf),
        ratio: P.jiveGateRatio(10),
        knee_db: P.jiveGateKnee(0),
        range_db: P.jiveGateRangeDb(0.65, nf),
        attack_ms: P.jiveGateAttack(0, 0, 0),
        release_ms: P.jiveGateRelease(0.02, 0.1, 0.65, 10),
      },
      {
        type: 'compressor',
        threshold_db: P.jiveLa2aThreshold(m.peakDb, m.dynamicRangeDb),
        ratio: P.jiveLa2aRatio(0, 0, 0, m.dynamicRangeDb),
        knee_db: P.jiveLa2aKnee(0, 0),
        attack_ms: P.jiveLa2aAttack(0),
        release_ms: P.jiveLa2aRelease(10, 0, 0, 0, 0, m.lufs),
        makeup_db: 0,
        mix: P.jiveLa2aMix(nf),
      },
      { type: 'deesser', freq: 6000, intensity: 0.4, attack_ms: 1, release_ms: 60 },
      { type: 'limiter', ceiling_db: -1.5, release_ms: 50 },
      { type: 'normalize', target_lufs: -18, ceiling_db: -1.5 },
    ],
  }
}

// ---------------------------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------------------------

function runBiquad(chs: Float32Array[], s: Stage, sr: number) {
  const k = s.kind ?? 1, f = Math.min(s.freq ?? 1000, sr * 0.49), q = s.q ?? 0.707, g = s.gain_db ?? 0
  const b0 = P.biquadB0(k, f, q, g, sr), b1 = P.biquadB1(k, f, q, g, sr), b2 = P.biquadB2(k, f, q, g, sr)
  const a1 = P.biquadA1(k, f, q, g, sr), a2 = P.biquadA2(k, f, q, g, sr)
  const mix = s.mix ?? 1
  for (const c of chs) {
    let s1 = 0, s2 = 0
    for (let i = 0; i < c.length; i++) {
      const x = c[i]
      const y = P.biquadTdf2Y(b0, x, s1)
      const ns1 = P.biquadTdf2S1(b1, a1, x, y, s2)
      s2 = P.biquadTdf2S2(b2, a2, x, y)
      s1 = ns1
      c[i] = P.mixDryWet(x, y, mix)
    }
  }
}

/** Stereo-linked dynamics: one detector over all channels, one gain applied to all. */
function runDynamics(chs: Float32Array[], sr: number, gainDbFor: (levelDb: number) => number,
  attackMs: number, releaseMs: number, mix: number) {
  const n = chs[0]?.length ?? 0
  const ac = P.timeCoef(attackMs, sr), rc = P.timeCoef(releaseMs, sr)
  let env = 0
  for (let i = 0; i < n; i++) {
    let x = 0
    for (const c of chs) x = Math.max(x, Math.abs(c[i]))
    env = P.envFollow(env, x, ac, rc)
    const g = P.dbToLinear(gainDbFor(P.linearToDb(env)))
    for (const c of chs) c[i] = P.mixDryWet(c[i], c[i] * g, mix)
  }
}

function runDeesser(chs: Float32Array[], s: Stage, sr: number) {
  const f = Math.min(s.freq ?? 6000, sr * 0.45)
  const b0 = P.biquadB0(1, f, 0.707, 0, sr), b1 = P.biquadB1(1, f, 0.707, 0, sr), b2 = P.biquadB2(1, f, 0.707, 0, sr)
  const a1 = P.biquadA1(1, f, 0.707, 0, sr), a2 = P.biquadA2(1, f, 0.707, 0, sr)
  const ac = P.timeCoef(s.attack_ms ?? 1, sr), rc = P.timeCoef(s.release_ms ?? 60, sr)
  const n = chs[0]?.length ?? 0
  const st = chs.map(() => ({ s1: 0, s2: 0 }))
  let envS = 0, envF = 0, g = 0
  for (let i = 0; i < n; i++) {
    let sib = 0, full = 0
    chs.forEach((c, ci) => {
      const x = c[i], z = st[ci]
      const y = P.biquadTdf2Y(b0, x, z.s1)
      const ns1 = P.biquadTdf2S1(b1, a1, x, y, z.s2)
      z.s2 = P.biquadTdf2S2(b2, a2, x, y)
      z.s1 = ns1
      sib = Math.max(sib, Math.abs(y))
      full = Math.max(full, Math.abs(x))
    })
    envS = P.envFollow(envS, sib, ac, rc)
    envF = P.envFollow(envF, full, ac, rc)
    g = P.gainSmooth(g, P.deessGainDb(P.linearToDb(envS), P.linearToDb(envF), s.intensity ?? 0), ac, rc)
    const lin = P.dbToLinear(g)
    for (const c of chs) c[i] *= lin
  }
}

function runLimiter(chs: Float32Array[], s: Stage, sr: number) {
  const ceiling = s.ceiling_db ?? -1, rc = P.timeCoef(s.release_ms ?? 50, sr)
  const n = chs[0]?.length ?? 0
  let g = 0
  for (let i = 0; i < n; i++) {
    let x = 0
    for (const c of chs) x = Math.max(x, Math.abs(c[i]))
    // attack coef 1.0 = instant: the gain for this sample is computed from this sample, so the
    // output can never exceed the ceiling (no lookahead needed for correctness, only for sound).
    g = P.gainSmooth(g, P.limiterGainDb(P.linearToDb(x), ceiling), 1.0, rc)
    const lin = P.dbToLinear(Math.min(g, P.limiterGainDb(P.linearToDb(x), ceiling)))
    for (const c of chs) c[i] *= lin
  }
}

function scale(chs: Float32Array[], gainDb: number) {
  const g = P.dbToLinear(gainDb)
  for (const c of chs) for (let i = 0; i < c.length; i++) c[i] *= g
}

/** Renders `chain` over copies of `input`; the input buffers are not modified. */
export function renderChain(input: Float32Array[], sr: number, chain: Chain): Float32Array[] {
  const chs = input.map((c) => new Float32Array(c))
  for (const s of chain.stages) {
    if (s.bypass) continue
    switch (s.type) {
      case 'biquad':
        runBiquad(chs, s, sr)
        break
      case 'compressor': {
        const t = s.threshold_db ?? -20, r = s.ratio ?? 3, k = s.knee_db ?? 0, mk = s.makeup_db ?? 0
        runDynamics(chs, sr, (l) => P.compGainDb(l, t, r, k) + mk, s.attack_ms ?? 10, s.release_ms ?? 200, s.mix ?? 1)
        break
      }
      case 'expander': {
        const t = s.threshold_db ?? -50, r = s.ratio ?? 2, k = s.knee_db ?? 0, rg = s.range_db ?? -40
        runDynamics(chs, sr, (l) => P.expanderGainDb(l, t, r, k, rg), s.attack_ms ?? 10, s.release_ms ?? 250, 1)
        break
      }
      case 'deesser':
        runDeesser(chs, s, sr)
        break
      case 'limiter':
        runLimiter(chs, s, sr)
        break
      case 'gain':
        scale(chs, s.gain_db ?? 0)
        break
      case 'normalize': {
        // jivetalking Pass 3/4: measure, then one linear gain (reduced if it would pass the ceiling)
        const m = measure(chs, sr)
        scale(chs, P.loudnormGainDb(s.target_lufs ?? -18, m.lufs, m.peakDb, s.ceiling_db ?? -1.5))
        break
      }
    }
  }
  return chs
}
