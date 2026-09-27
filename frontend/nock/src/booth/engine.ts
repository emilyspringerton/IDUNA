// engine.ts -- the NOCK Booth: four CDJ-style decks into a four-channel DJM-style mixer, plus an
// MPC-style pad sampler. Founder real-time (2026-09-27): "it needs to emulate an MPC and a
// pioneer mixer and 4 CDJS".
//
// Every musical/DSP decision is a PARENA function (../generated/AudioDsp.ts, compiled from
// PARENA stdlib/audio/{deck,mixer4,sampler,dsp}.prn -- the same files SHANKPIT compiles to C).
// This class is the host: it owns buffers and state and runs the per-sample loop. No DOM, no
// Web Audio: booth.worklet.ts wraps it in an AudioWorkletProcessor, and
// scripts/nock_audio_selfcheck.ts drives it directly under Node.
//
// Honest limits (v0): MASTER TEMPO/key lock is tracked but not applied (no time-stretcher yet --
// pitch follows tempo like vinyl); BPM comes from the user (manual/tap), not beat detection.

import * as P from '../generated/AudioDsp.ts'

export const NUM_DECKS = 4
export const NUM_PADS = 16
export const NUM_BANKS = 4
export const MAX_VOICES = 16

interface BiquadState { b0: number; b1: number; b2: number; a1: number; a2: number; s: Float64Array } // s: [L1,L2,R1,R2]

function newBiquad(): BiquadState {
  return { b0: 1, b1: 0, b2: 0, a1: 0, a2: 0, s: new Float64Array(4) }
}
function designBiquad(bq: BiquadState, kind: number, freq: number, q: number, gainDb: number, sr: number) {
  bq.b0 = P.biquadB0(kind, freq, q, gainDb, sr); bq.b1 = P.biquadB1(kind, freq, q, gainDb, sr)
  bq.b2 = P.biquadB2(kind, freq, q, gainDb, sr); bq.a1 = P.biquadA1(kind, freq, q, gainDb, sr)
  bq.a2 = P.biquadA2(kind, freq, q, gainDb, sr)
}
function stepBiquad(bq: BiquadState, x: number, side: number): number {
  const i = side * 2
  const y = P.biquadTdf2Y(bq.b0, x, bq.s[i])
  const ns1 = P.biquadTdf2S1(bq.b1, bq.a1, x, y, bq.s[i + 1])
  bq.s[i + 1] = P.biquadTdf2S2(bq.b2, bq.a2, x, y)
  bq.s[i] = ns1
  return y
}

export interface DeckState {
  buf: Float32Array[] | null // [L, R]
  srcSr: number
  name: string
  pos: number
  playing: boolean
  tempoFader: number // -1..1
  rangeSel: number // 0 6%, 1 10%, 2 16%, 3 WIDE
  bpm: number // track BPM at 0%
  gridOffset: number // source sample of beat 1
  cue: number
  hotCues: (number | null)[]
  loopIn: number
  loopOut: number
  loopOn: boolean
  loopBeats: number
  quantize: boolean
  vinylMode: boolean
  touching: boolean // jog top touched (vinyl mode)
  jogRevPerSec: number
  reverse: boolean
  braking: boolean
  brakeRate: number
  masterTempo: boolean // tracked; not applied in v0 (see header)
  syncOn: boolean
}

export interface ChannelState {
  trim: number; hi: number; mid: number; low: number // knobs 0..1
  color: number // -1..1
  fader: number // 0..1
  assign: number // 0 A, 1 THRU, 2 B
  cue: boolean
  eq: [BiquadState, BiquadState, BiquadState] // low shelf, mid peak, high shelf
  filt: BiquadState
  filtKind: number
  dirty: boolean
  peakDb: number
}

export interface Pad {
  buf: Float32Array[] | null
  srcSr: number
  name: string
  tune: number // semitones
  level: number // 0..1
  choke: number // 0 = none, 1..32
  oneShot: boolean
  attackMs: number; decayMs: number; sustain: number; releaseMs: number
}

interface Voice {
  pad: Pad; padIndex: number; pos: number; rate: number; gain: number
  tOnMs: number; released: boolean; tOffMs: number; levelAtOff: number; active: boolean
}

export class BoothEngine {
  readonly sr: number
  decks: DeckState[]
  ch: ChannelState[]
  xfader = 0.5
  xfCurve = 0 // 0 smooth, 1 mid, 2 sharp
  faderCurve = 1 // 0 slow, 1 standard, 2 fast
  masterLevel = 0.8
  masterPeakDb = -200
  hpMix = 0.5 // headphones: 0 cue .. 1 master
  hpLevel = 0.8
  masterDeck = 0 // tempo master for SYNC
  // sampler
  pads: Pad[][] // [bank][pad]
  bank = 0
  samplerLevel = 0.8
  bpm = 120 // sampler clock for NOTE REPEAT / swing
  swing = 50
  repeatGrid = 2 // 1/16
  repeatHeld: number[] = [] // pad indices held with NOTE REPEAT
  private tick = 0
  private nextStep = 0
  private voices: Voice[] = []
  private ms = 0

  constructor(sampleRate: number) {
    this.sr = sampleRate
    this.decks = Array.from({ length: NUM_DECKS }, () => ({
      buf: null, srcSr: sampleRate, name: '', pos: 0, playing: false, tempoFader: 0, rangeSel: 1,
      bpm: 120, gridOffset: 0, cue: 0, hotCues: Array(8).fill(null), loopIn: 0, loopOut: 0,
      loopOn: false, loopBeats: 4, quantize: true, vinylMode: true, touching: false, jogRevPerSec: 0,
      reverse: false, braking: false, brakeRate: 0, masterTempo: false, syncOn: false,
    }))
    this.ch = Array.from({ length: NUM_DECKS }, (_, i) => ({
      trim: 0.5, hi: 0.5, mid: 0.5, low: 0.5, color: 0, fader: 0, assign: i < 2 ? 0 : 2, cue: false,
      eq: [newBiquad(), newBiquad(), newBiquad()], filt: newBiquad(), filtKind: -1, dirty: true, peakDb: -200,
    }))
    this.pads = Array.from({ length: NUM_BANKS }, () => Array.from({ length: NUM_PADS }, () => ({
      buf: null, srcSr: sampleRate, name: '', tune: 0, level: 1, choke: 0, oneShot: true,
      attackMs: 0.5, decayMs: 200, sustain: 1, releaseMs: 30,
    })))
  }

  // ---------------------------------------------------------------- deck controls
  load(d: number, buf: Float32Array[], srcSr: number, name: string, bpm?: number) {
    const k = this.decks[d]
    k.buf = buf.length === 1 ? [buf[0], buf[0]] : buf
    k.srcSr = srcSr; k.name = name; k.pos = 0; k.cue = 0; k.playing = false; k.loopOn = false
    k.hotCues = Array(8).fill(null)
    if (bpm) k.bpm = bpm
  }
  rate(d: number): number {
    const k = this.decks[d]
    if (k.syncOn && d !== this.masterDeck) {
      const m = this.decks[this.masterDeck]
      return P.deckSyncRate(P.deckBpm(m.bpm, P.deckRate(m.tempoFader, m.rangeSel)), k.bpm, k.rangeSel)
    }
    return P.deckRate(k.tempoFader, k.rangeSel)
  }
  currentBpm(d: number): number { return P.deckBpm(this.decks[d].bpm, this.rate(d)) }
  beatLen(d: number): number { return P.deckBeatLen(this.decks[d].bpm, this.decks[d].srcSr) }
  playPause(d: number) {
    const k = this.decks[d]
    if (!k.buf) return
    k.playing = !k.playing
    k.braking = false
  }
  cuePress(d: number) {
    const k = this.decks[d]
    const q = (p: number) => P.deckQuantizePos(p, k.gridOffset, this.beatLen(d), k.quantize ? 1 : 0)
    switch (P.deckCuePress(k.playing ? 1 : 0, k.pos, k.cue)) {
      case 1: k.cue = q(k.pos); k.pos = k.cue; break // SET cue
      case 2: k.playing = false; k.pos = k.cue; break // JUMP back and pause
      case 3: k.playing = true; break // PREVIEW while held
    }
  }
  cueRelease(d: number) {
    const k = this.decks[d]
    // releasing a PREVIEW returns to the cue point (CDJ behaviour)
    if (k.playing && Math.abs(k.pos - k.cue) < k.srcSr * 30) { k.playing = false; k.pos = k.cue }
  }
  hotCue(d: number, i: number, clear = false) {
    const k = this.decks[d]
    if (clear) { k.hotCues[i] = null; return }
    const hc = k.hotCues[i]
    if (hc === null) k.hotCues[i] = P.deckQuantizePos(k.pos, k.gridOffset, this.beatLen(d), k.quantize ? 1 : 0)
    else { k.pos = hc; if (!k.playing && k.buf) k.playing = true }
  }
  beatLoop(d: number, beats?: number) {
    const k = this.decks[d]
    if (beats) k.loopBeats = beats
    k.loopIn = P.deckQuantizePos(k.pos, k.gridOffset, this.beatLen(d), k.quantize ? 1 : 0)
    k.loopOut = P.deckLoopOut(k.loopIn, k.loopBeats, this.beatLen(d))
    k.loopOn = true
  }
  loopHalve(d: number) { const k = this.decks[d]; k.loopBeats = P.deckLoopHalve(k.loopBeats); if (k.loopOn) k.loopOut = P.deckLoopOut(k.loopIn, k.loopBeats, this.beatLen(d)) }
  loopDouble(d: number) { const k = this.decks[d]; k.loopBeats = P.deckLoopDouble(k.loopBeats); if (k.loopOn) k.loopOut = P.deckLoopOut(k.loopIn, k.loopBeats, this.beatLen(d)) }
  loopExit(d: number) { this.decks[d].loopOn = false }
  setGridHere(d: number) { this.decks[d].gridOffset = this.decks[d].pos }
  phase(d: number): number { const k = this.decks[d]; return P.deckBeatPhase(k.pos, k.gridOffset, this.beatLen(d)) }

  // ---------------------------------------------------------------- sampler controls
  padDown(pad: number, velocity = 127, full = false) {
    const p = this.pads[this.bank][pad]
    if (!p.buf) return
    for (const v of this.voices) if (v.active && P.mpcChoke(p.choke, v.pad.choke) === 1) v.active = false
    let slot = this.voices.find((v) => !v.active)
    if (!slot && this.voices.length < MAX_VOICES) { slot = {} as Voice; this.voices.push(slot) }
    if (!slot) {
      // steal the lowest-scoring voice (released first, then quietest, then oldest)
      let best = this.voices[0], bestScore = Infinity
      for (const v of this.voices) {
        const s = P.mpcVoiceStealScore(v.released ? 1 : 0, v.gain, this.ms - v.tOnMs)
        if (s < bestScore) { bestScore = s; best = v }
      }
      slot = best
    }
    Object.assign(slot, {
      pad: p, padIndex: pad, pos: 0, rate: P.mpcTuneRatio(p.tune) * (p.srcSr / this.sr),
      gain: P.mpcVelocityGain(velocity, full ? 1 : 0) * p.level, tOnMs: this.ms, released: false,
      tOffMs: 0, levelAtOff: 0, active: true,
    })
  }
  padUp(pad: number) {
    for (const v of this.voices) {
      if (v.active && !v.released && v.padIndex === pad && !v.pad.oneShot) {
        v.released = true; v.tOffMs = this.ms
        v.levelAtOff = P.mpcAdsrLevel(this.ms - v.tOnMs, v.pad.attackMs, v.pad.decayMs, v.pad.sustain)
      }
    }
  }

  // ---------------------------------------------------------------- audio
  private refreshChannel(c: ChannelState) {
    if (!c.dirty) return
    const kill = (knob: number) => P.mixerEqDb(knob) <= -100
    designBiquad(c.eq[0], 5, P.mixerEqLowHz(), 0.707, kill(c.low) ? -48 : P.mixerEqDb(c.low), this.sr)
    designBiquad(c.eq[1], 4, P.mixerEqMidHz(), P.mixerEqMidQ(), kill(c.mid) ? -48 : P.mixerEqDb(c.mid), this.sr)
    designBiquad(c.eq[2], 6, P.mixerEqHiHz(), 0.707, kill(c.hi) ? -48 : P.mixerEqDb(c.hi), this.sr)
    c.filtKind = P.mixerFilterKind(c.color)
    if (c.filtKind >= 0) designBiquad(c.filt, c.filtKind, Math.min(P.mixerFilterFreq(c.color), this.sr * 0.45), P.mixerFilterQ(c.color), 0, this.sr)
    c.dirty = false
  }

  private deckSample(d: number, side: number): number {
    const k = this.decks[d]
    const b = k.buf![side], i = Math.floor(k.pos)
    if (i < 1 || i + 2 >= b.length) return 0
    return P.deckHermite(b[i - 1], b[i], b[i + 1], b[i + 2], k.pos - i)
  }

  private advanceDeck(d: number) {
    const k = this.decks[d]
    if (!k.buf) return
    let r: number
    if (k.vinylMode && k.touching) r = P.deckScratchRate(k.jogRevPerSec)
    else if (!k.playing) return
    else {
      r = P.deckReverseRate(P.deckJogBend(this.rate(d), k.jogRevPerSec), k.reverse ? 1 : 0)
      if (k.braking) { k.brakeRate = P.deckBrakeRate(k.brakeRate, P.timeCoef(250, this.sr)); r = k.brakeRate; if (r === 0) { k.playing = false; k.braking = false } }
    }
    k.pos = P.deckLoopWrap(P.deckAdvance(k.pos, r, k.srcSr, this.sr), k.loopIn, k.loopOut, k.loopOn ? 1 : 0)
    if (P.deckAtEnd(k.pos, k.buf[0].length, r) === 1) { k.playing = false; k.pos = Math.max(1, Math.min(k.pos, k.buf[0].length - 3)) }
    if (k.pos < 1) k.pos = 1
  }

  brake(d: number) { const k = this.decks[d]; if (k.playing) { k.braking = true; k.brakeRate = this.rate(d) } }

  /** Renders one block. master/cue are [L, R]. */
  process(masterL: Float32Array, masterR: Float32Array, cueL?: Float32Array, cueR?: Float32Array) {
    const n = masterL.length
    for (const c of this.ch) this.refreshChannel(c)
    const chGain = this.ch.map((c) => P.mixerChannelGain(c.trim, c.fader, this.faderCurve, c.assign, this.xfader, this.xfCurve))
    const trimGain = this.ch.map((c) => P.dbToLinear(P.mixerTrimDb(c.trim)))
    const peaks = [0, 0, 0, 0]
    const tps = P.mpcTicksPerSample(this.bpm, this.sr), grid = P.mpcGridTicks(this.repeatGrid)
    let mPeak = 0
    for (let i = 0; i < n; i++) {
      let mL = 0, mR = 0, cL = 0, cR = 0
      for (let d = 0; d < NUM_DECKS; d++) {
        const k = this.decks[d], c = this.ch[d]
        if (!k.buf) { this.advanceDeck(d); continue }
        let l = this.deckSample(d, 0), r = this.deckSample(d, 1)
        this.advanceDeck(d)
        for (const bq of c.eq) { l = stepBiquad(bq, l, 0); r = stepBiquad(bq, r, 1) }
        if (c.filtKind >= 0) { l = stepBiquad(c.filt, l, 0); r = stepBiquad(c.filt, r, 1) }
        const pk = Math.max(Math.abs(l), Math.abs(r)) * trimGain[d]
        if (pk > peaks[d]) peaks[d] = pk
        if (c.cue) { cL += l * trimGain[d]; cR += r * trimGain[d] } // pre-fader CUE tap
        mL += l * chGain[d]; mR += r * chGain[d]
      }
      // sampler: NOTE REPEAT -- fire each grid step while a pad is held, off-beat steps pushed
      // late by the MPC swing rule
      this.tick += tps
      if (this.repeatHeld.length) {
        while (this.tick >= P.mpcSwingTick(this.nextStep * grid, grid, this.swing)) {
          for (const p of this.repeatHeld) this.padDown(p)
          this.nextStep++
        }
      } else {
        this.nextStep = Math.ceil(this.tick / grid)
      }
      let sL = 0, sR = 0
      for (const v of this.voices) {
        if (!v.active) continue
        const b = v.pad.buf!, idx = Math.floor(v.pos)
        if (idx + 2 >= b[0].length) { v.active = false; continue }
        const t = this.ms - v.tOnMs
        const env = v.released ? P.mpcReleaseLevel(this.ms - v.tOffMs, v.levelAtOff, v.pad.releaseMs)
          : P.mpcAdsrLevel(t, v.pad.attackMs, v.pad.decayMs, v.pad.sustain)
        if (v.released && env <= 0) { v.active = false; continue }
        const g = v.gain * env
        const i0 = Math.max(idx - 1, 0), fr = v.pos - idx
        sL += P.deckHermite(b[0][i0], b[0][idx], b[0][idx + 1], b[0][idx + 2], fr) * g
        const br = b[1] ?? b[0]
        sR += P.deckHermite(br[i0], br[idx], br[idx + 1], br[idx + 2], fr) * g
        v.pos += v.rate
      }
      mL += sL * this.samplerLevel; mR += sR * this.samplerLevel
      // master level, then the master clip limiter (instant: never exceeds -0.3 dBFS)
      mL *= this.masterLevel; mR *= this.masterLevel
      const mp = Math.max(Math.abs(mL), Math.abs(mR))
      const lim = P.dbToLinear(P.mixerMasterLimit(P.linearToDb(mp), -0.3))
      mL *= lim; mR *= lim
      if (mp * lim > mPeak) mPeak = mp * lim
      masterL[i] = mL; masterR[i] = mR
      if (cueL && cueR) {
        cueL[i] = P.mixerHeadphone(cL, mL, this.hpMix, this.hpLevel)
        cueR[i] = P.mixerHeadphone(cR, mR, this.hpMix, this.hpLevel)
      }
      this.ms += 1000 / this.sr
    }
    for (let d = 0; d < NUM_DECKS; d++) this.ch[d].peakDb = P.mixerPeakHold(this.ch[d].peakDb, P.linearToDb(peaks[d]), 0.5)
    this.masterPeakDb = P.mixerPeakHold(this.masterPeakDb, P.linearToDb(mPeak), 0.5)
  }

  /** UI snapshot (posted from the worklet ~30x/s). */
  snapshot() {
    return {
      decks: this.decks.map((k, d) => ({
        name: k.name, loaded: !!k.buf, pos: k.pos, len: k.buf?.[0].length ?? 0, srcSr: k.srcSr,
        playing: k.playing, cue: k.cue, hotCues: k.hotCues, loopOn: k.loopOn, loopIn: k.loopIn,
        loopOut: k.loopOut, loopBeats: k.loopBeats, bpm: this.currentBpm(d),
        pitchPct: (this.rate(d) - 1) * 100, phase: this.phase(d),
      })),
      meters: this.ch.map((c) => P.mixerMeterSegment(c.peakDb)),
      master: P.mixerMeterSegment(this.masterPeakDb),
      voices: this.voices.filter((v) => v.active).map((v) => v.padIndex),
    }
  }
}
