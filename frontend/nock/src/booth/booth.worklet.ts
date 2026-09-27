// booth.worklet.ts -- runs BoothEngine (PARENA-driven decks/mixer/sampler) on the audio thread.
// Loaded by Booth.tsx via `?worker&url` + audioWorklet.addModule. Output 0 = MASTER, output 1 =
// headphone CUE mix (see Booth.tsx for how each is routed).

import { BoothEngine } from './engine.ts'

// Minimal AudioWorkletGlobalScope typings (not in lib.dom).
declare const sampleRate: number
declare function registerProcessor(name: string, ctor: unknown): void
declare class AudioWorkletProcessor {
  readonly port: MessagePort
}

export type BoothMsg =
  | { t: 'load'; d: number; buf: Float32Array[]; srcSr: number; name: string; bpm?: number }
  | { t: 'loadPad'; bank: number; pad: number; buf: Float32Array[]; srcSr: number; name: string }
  | { t: 'call'; m: string; a: unknown[] }
  | { t: 'deck'; d: number; k: string; v: unknown }
  | { t: 'ch'; c: number; k: string; v: unknown }
  | { t: 'pad'; bank: number; pad: number; k: string; v: unknown }
  | { t: 'eng'; k: string; v: unknown }

class BoothProcessor extends AudioWorkletProcessor {
  private e = new BoothEngine(sampleRate)
  private frames = 0

  constructor() {
    super()
    this.port.onmessage = (ev: MessageEvent<BoothMsg>) => {
      const m = ev.data
      const e = this.e as unknown as Record<string, unknown>
      switch (m.t) {
        case 'load': this.e.load(m.d, m.buf, m.srcSr, m.name, m.bpm); break
        case 'loadPad': Object.assign(this.e.pads[m.bank][m.pad], { buf: m.buf, srcSr: m.srcSr, name: m.name }); break
        case 'call': (e[m.m] as (...a: unknown[]) => void).apply(this.e, m.a); break
        case 'deck': (this.e.decks[m.d] as unknown as Record<string, unknown>)[m.k] = m.v; break
        case 'ch': Object.assign(this.e.ch[m.c], { [m.k]: m.v, dirty: true }); break
        case 'pad': (this.e.pads[m.bank][m.pad] as unknown as Record<string, unknown>)[m.k] = m.v; break
        case 'eng': e[m.k] = m.v; break
      }
    }
  }

  process(_in: Float32Array[][], outputs: Float32Array[][]): boolean {
    const [master, cue] = outputs
    this.e.process(master[0], master[1] ?? master[0], cue?.[0], cue?.[1])
    if ((this.frames += master[0].length) >= sampleRate / 30) {
      this.frames = 0
      this.port.postMessage(this.e.snapshot())
    }
    return true
  }
}

registerProcessor('nock-booth', BoothProcessor)
