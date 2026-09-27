// audio_selfcheck.ts -- `npm run test:audio`. Drives NOCK's real audio code (the PARENA-generated
// TS, the filter-chain runner and the Booth engine) under Node, no browser: known signals in,
// measured signals out. Also re-checks a few PARENA values against the C test's expectations so
// a TS-emitter regression shows up here, not as "the booth sounds off".
import * as P from '../src/generated/AudioDsp.ts'
import { autoChain, integratedLufs, measure, renderChain, type Chain } from '../src/sound/chain.ts'
import { decodeWav16, encodeWav } from '../src/sound/wav.ts'
import { BoothEngine } from '../src/booth/engine.ts'

let failures = 0
function check(what: string, got: number, want: number, tol: number) {
  const ok = Math.abs(got - want) <= tol
  if (!ok) failures++
  console.log(`${ok ? 'PASS' : 'FAIL'}: ${what} (got ${got.toFixed(6)}, want ${want} ±${tol})`)
}
const sr = 48000
const sine = (f: number, secs: number, amp = 0.5) =>
  Float32Array.from({ length: Math.round(sr * secs) }, (_, i) => amp * Math.sin((2 * Math.PI * f * i) / sr))
const rmsDb = (x: Float32Array, from = 0) => {
  let s = 0
  for (let i = from; i < x.length; i++) s += x[i] * x[i]
  return 20 * Math.log10(Math.sqrt(s / (x.length - from)))
}

// PARENA TS target agrees with the C target's verified values
check('TS K-weighting b0 @48k == BS.1770', P.kweightShelfB0(48000), 1.53512485958697, 1e-9)
check('TS comp soft knee', P.compGainDb(-20, -20, 4, 6), -0.5625, 1e-12)
check('TS I32 content-type (speech)', P.jiveContentType(8, 0.3, 0.001, 35), 0, 0)

// BS.1770 calibration: a 997 Hz sine of peak amplitude 0.1 is -23.01 dB RMS per channel; two
// channels sum +3.01 dB, K-weighting at 997 Hz ~ +0.69 dB cancels the -0.691 offset -> -20.0 LUFS.
{
  const x = sine(997, 5, 0.1)
  check('integrated LUFS of 997 Hz @ -20 dBFS peak', integratedLufs([x, x], sr), -20, 0.1)
}

// Filter chain: HPF kills rumble, keeps voice band
{
  const chain: Chain = { version: 1, stages: [{ type: 'biquad', kind: 1, freq: 80, q: 0.707 }] }
  const lo = renderChain([sine(20, 1)], sr, chain)[0], hi = renderChain([sine(1000, 1)], sr, chain)[0]
  check('chain HPF 80 Hz: 20 Hz down ~24 dB', rmsDb(lo, sr / 2) - rmsDb(sine(20, 1), sr / 2), -24.1, 0.6)
  check('chain HPF 80 Hz: 1 kHz untouched', rmsDb(hi, sr / 2) - rmsDb(sine(1000, 1), sr / 2), 0, 0.05)
}
// Limiter never lets a sample through above the ceiling
{
  const out = renderChain([sine(440, 1, 1.0)], sr, { version: 1, stages: [{ type: 'limiter', ceiling_db: -6 }] })[0]
  let pk = 0
  for (const v of out) pk = Math.max(pk, Math.abs(v))
  check('limiter ceiling -6 dBFS holds', 20 * Math.log10(pk), -6, 0.01)
}
// Normalize hits the loudness target (jivetalking pass 3/4)
{
  const x = sine(1000, 4, 0.02)
  const out = renderChain([x, x], sr, { version: 1, stages: [{ type: 'normalize', target_lufs: -18, ceiling_db: -1 }] })
  check('normalize to -18 LUFS', integratedLufs(out, sr), -18, 0.05)
}
// Auto chain from measurements (jivetalking rules) produces a runnable, sane chain
{
  const voice = sine(220, 3, 0.3)
  for (let i = 0; i < sr / 2; i++) voice[i] = (Math.random() - 0.5) * 0.002 // quiet room tone lead-in
  const m = measure([voice], sr)
  const c = autoChain(m)
  const exp = c.stages.find((s) => s.type === 'expander')!
  check('auto chain: expander threshold = noise floor + 5 (jive-nr-threshold, clamped)', exp.threshold_db!, Math.min(-40, Math.max(-70, m.noiseFloorDb + 5)), 1e-9)
  const out = renderChain([voice], sr, c)
  check('auto chain output lands at -18 LUFS', integratedLufs(out, sr), -18, 0.5)
}
// WAV roundtrip through PARENA's PCM scaling
{
  const x = sine(1000, 0.1, 0.5)
  const back = decodeWav16(encodeWav([x], sr)).channels[0]
  let err = 0
  for (let i = 0; i < x.length; i++) err = Math.max(err, Math.abs(back[i] - x[i]))
  check("wav16 roundtrip error <= 1 LSB (+ asymmetric 32767/32768 scale)", err, 0, 1.5 / 32768)
}

// ---- Booth ----
{
  const e = new BoothEngine(sr)
  const tone = sine(1000, 10, 0.5)
  e.load(0, [tone, tone], sr, 'tone', 120)
  e.ch[0].fader = 1; e.ch[0].assign = 1 // THRU
  e.masterLevel = 1
  e.playPause(0)
  const L = new Float32Array(sr), R = new Float32Array(sr)
  for (let o = 0; o < sr; o += 128) e.process(L.subarray(o, o + 128), R.subarray(o, o + 128))
  check('deck 1 THRU, fader up: plays at unity', rmsDb(L, sr / 4), rmsDb(tone, sr / 4), 0.1)
  check('deck advanced one second of source', e.decks[0].pos, 1 + sr, 2)

  e.ch[0].low = 0; e.ch[0].mid = 0; e.ch[0].hi = 0; e.ch[0].dirty = true // full EQ kill
  for (let o = 0; o < sr; o += 128) e.process(L.subarray(o, o + 128), R.subarray(o, o + 128))
  check('3-band kill silences deck (> 30 dB down)', rmsDb(L, sr / 2) - rmsDb(tone, sr / 2) < -30 ? 1 : 0, 1, 0)
  e.ch[0].low = e.ch[0].mid = e.ch[0].hi = 0.5; e.ch[0].dirty = true

  e.ch[0].assign = 0; e.xfader = 1 // channel on A, crossfader fully on B
  for (let o = 0; o < sr; o += 128) e.process(L.subarray(o, o + 128), R.subarray(o, o + 128))
  check('crossfader to B cuts an A-assigned channel', rmsDb(L, sr / 2) < -100 ? 1 : 0, 1, 0)
  e.xfader = 0

  e.decks[0].tempoFader = 0.5; e.decks[0].rangeSel = 2 // +8%
  const before = e.decks[0].pos
  for (let o = 0; o < sr; o += 128) e.process(L.subarray(o, o + 128), R.subarray(o, o + 128))
  check('+8% pitch consumes 1.08 s of source per second', (e.decks[0].pos - before) / sr, 1.08, 0.001)

  // SYNC: deck 2 (100 bpm track) syncs to deck 1 (120 bpm at +8% = 129.6) within WIDE range
  e.load(1, [tone, tone], sr, 'tone2', 100)
  e.decks[1].rangeSel = 3; e.decks[1].syncOn = true; e.masterDeck = 0
  check('SYNC matches master BPM', e.currentBpm(1), 129.6, 1e-9)

  // Loop: 1-beat loop at 120 bpm keeps the playhead inside [in, out)
  e.decks[0].tempoFader = 0
  e.beatLoop(0, 1)
  for (let o = 0; o < sr * 2; o += 128) e.process(new Float32Array(128), new Float32Array(128))
  const k = e.decks[0]
  check('beat loop holds playhead inside the loop', k.pos >= k.loopIn && k.pos < k.loopOut ? 1 : 0, 1, 0)

  // Master limiter: four hot decks summed never clip past -0.3 dBFS
  for (let d = 0; d < 4; d++) { e.load(d, [sine(200 + d, 2, 1), sine(200 + d, 2, 1)], sr, 'hot', 120); e.ch[d].fader = 1; e.ch[d].assign = 1; e.ch[d].trim = 1; e.ch[d].dirty = true; e.playPause(d) }
  for (let o = 0; o < sr; o += 128) e.process(L.subarray(o, o + 128), R.subarray(o, o + 128))
  let pk = 0
  for (const v of L) pk = Math.max(pk, Math.abs(v))
  check('master limiter holds -0.3 dBFS with 4 hot decks', 20 * Math.log10(pk) <= -0.3 + 1e-6 ? 1 : 0, 1, 0)

  // MPC: pad hit plays its sample; choke group cuts the open hat
  const s = new BoothEngine(sr)
  s.pads[0][0] = { ...s.pads[0][0], buf: [sine(3000, 1, 0.5)], name: 'open_hat', choke: 1 }
  s.pads[0][1] = { ...s.pads[0][1], buf: [sine(5000, 0.05, 0.5)], name: 'closed_hat', choke: 1 }
  s.samplerLevel = 1; s.masterLevel = 1
  s.padDown(0)
  const A = new Float32Array(4800), B = new Float32Array(4800)
  s.process(A, B)
  check('pad plays (open hat audible)', rmsDb(A) > -12 ? 1 : 0, 1, 0)
  s.padDown(1)
  s.process(A, B); s.process(A, B)
  check('choke group: open hat cut by closed hat', s.snapshot().voices.includes(0) ? 0 : 1, 1, 0)

  // NOTE REPEAT at 120 bpm 1/16 with swing 50: 8 hits per second
  const r = new BoothEngine(sr)
  r.pads[0][2] = { ...r.pads[0][2], buf: [sine(100, 0.01, 0.5)], name: 'tick' }
  let hits = 0
  const orig = r.padDown.bind(r)
  r.padDown = (p: number, v?: number, f?: boolean) => { hits++; orig(p, v, f) }
  r.repeatHeld = [2]
  for (let o = 0; o < sr; o += 128) r.process(new Float32Array(128), new Float32Array(128))
  check('note repeat 1/16 @120 bpm = 8 hits/s', hits, 8, 1)
}

console.log(failures ? `${failures} FAILURE(S)` : 'ALL NOCK AUDIO CHECKS PASSED')
process.exit(failures ? 1 : 0)
