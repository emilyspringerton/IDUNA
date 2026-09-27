// wav.ts -- 16-bit PCM WAV encode/decode for NOCK renders. Sample scaling goes through PARENA
// (sampleToPcm16Scale / pcm16ToSample) so clip behaviour matches the engine side exactly.

import { pcm16ToSample, sampleToPcm16Scale } from '../generated/AudioDsp.ts'

export function encodeWav(channels: Float32Array[], sampleRate: number): Uint8Array {
  const nch = channels.length, n = channels[0]?.length ?? 0
  const dataBytes = n * nch * 2
  const buf = new ArrayBuffer(44 + dataBytes)
  const v = new DataView(buf)
  const str = (o: number, s: string) => { for (let i = 0; i < s.length; i++) v.setUint8(o + i, s.charCodeAt(i)) }
  str(0, 'RIFF'); v.setUint32(4, 36 + dataBytes, true); str(8, 'WAVE')
  str(12, 'fmt '); v.setUint32(16, 16, true); v.setUint16(20, 1, true); v.setUint16(22, nch, true)
  v.setUint32(24, sampleRate, true); v.setUint32(28, sampleRate * nch * 2, true)
  v.setUint16(32, nch * 2, true); v.setUint16(34, 16, true)
  str(36, 'data'); v.setUint32(40, dataBytes, true)
  let o = 44
  for (let i = 0; i < n; i++) for (let c = 0; c < nch; c++) {
    v.setInt16(o, Math.round(sampleToPcm16Scale(channels[c][i])), true)
    o += 2
  }
  return new Uint8Array(buf)
}

/** Minimal PCM16 WAV decoder (for tests / environments without decodeAudioData). */
export function decodeWav16(bytes: Uint8Array): { sampleRate: number; channels: Float32Array[] } {
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const nch = v.getUint16(22, true), sr = v.getUint32(24, true)
  const n = v.getUint32(40, true) / (2 * nch)
  const chs = Array.from({ length: nch }, () => new Float32Array(n))
  let o = 44
  for (let i = 0; i < n; i++) for (let c = 0; c < nch; c++) { chs[c][i] = pcm16ToSample(v.getInt16(o, true)); o += 2 }
  return { sampleRate: sr, channels: chs }
}
