// nleEngine.ts -- the MIXFORGE EDITOR's non-linear-editing DSP host: crossfade curves, per-clip
// fade envelopes, and MPC pad-capture window math, all computed by src/video/nle.wasm (compiled
// from PARENA/stdlib/video/nle.prn -- see scripts/build_nle_wasm.sh).
//
// instantiateDsp below is a direct, deliberate port of MIXFORGE/web/engine.mjs's own function of
// the same name -- the literal "shares components with MIXFORGE" ask (founder real-time: "ensure
// NOCK tools video editor is PARENA wasm powered and shares components with MIXFORGE's shared
// components"). It isn't a cross-repo import (MIXFORGE is a static site with no build step, NOCK
// is a separate Vite/React/TS app in a separate repo -- there's no shared package boundary
// between them), it's the same real pattern, ported: instantiate a PARENA-compiled wasm module
// synchronously with no imports, and hand the host a typed view of its exports. Same division of
// labour as MIXFORGE's own engine.mjs too: this file owns nothing but the wasm boot + typed calls
// -- buffers, video elements and the frame loop live in TimelinePreview.tsx/PadCapture.tsx.

export interface NleDsp {
  clamp01(x: number): number;
  quarter_cos(x: number): number;
  xfade_progress(t: number, dur: number): number;
  xfade_out_gain(t: number, dur: number): number;
  xfade_in_gain(t: number, dur: number): number;
  fade_envelope(t: number, clipDur: number, fadeIn: number, fadeOut: number): number;
  pad_capture_in(pressT: number, preroll: number): number;
  pad_capture_out(pressT: number, releaseT: number, minDur: number): number;
  default_preroll_seconds(): number;
  default_min_clip_seconds(): number;
}

/** Instantiate nle.wasm synchronously (same shape as MIXFORGE/web/engine.mjs's instantiateDsp --
 * works with no top-level await, no imports, safe to call from any context). */
export function instantiateDsp(bytes: ArrayBuffer): NleDsp {
  const mod = new WebAssembly.Module(bytes);
  return new WebAssembly.Instance(mod, {}).exports as unknown as NleDsp;
}

let cached: Promise<NleDsp> | null = null;

/** Fetches + instantiates src/video/nle.wasm once per page load; every caller shares the result. */
export function loadNleDsp(): Promise<NleDsp> {
  if (!cached) {
    cached = import('./nle.wasm?url').then(async (m) => {
      const bytes = await (await fetch(m.default)).arrayBuffer();
      return instantiateDsp(bytes);
    });
  }
  return cached;
}
