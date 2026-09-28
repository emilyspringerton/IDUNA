// nle_test.mjs -- verifies src/video/nle.wasm (PARENA/stdlib/video/nle.prn compiled to
// WebAssembly): the crossfade/fade/pad-capture kernels the MIXFORGE EDITOR's live preview and
// pad-capture panel call into. Same instantiation shape MIXFORGE/web/dsp_test.mjs already uses
// (a plain, synchronous WebAssembly.Instance -- see ../src/video/nleEngine.ts's own
// instantiateDsp, ported from MIXFORGE/web/engine.mjs).
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const bytes = readFileSync(path.join(here, "..", "src", "video", "nle.wasm"));
const mod = new WebAssembly.Module(bytes);
const x = new WebAssembly.Instance(mod, {}).exports;

let passed = 0, failed = 0;
function check(name, ok, detail = "") {
  if (ok) { passed++; console.log(`  ok   ${name}`); }
  else { failed++; console.error(`  FAIL ${name} ${detail}`); }
}
const near = (a, b, eps) => Math.abs(a - b) <= eps;

console.log("kernels (nle.prn)");

let cosErr = 0;
for (let i = 0; i <= 1000; i++) {
  const t = i / 1000;
  cosErr = Math.max(cosErr, Math.abs(x.quarter_cos(t) - Math.cos((Math.PI * t) / 2)));
}
check("quarter_cos within 3e-5 of cos(pi x/2) on [0,1]", cosErr < 3e-5, `max err ${cosErr}`);

check("xfade_progress: dur<=0 is fully progressed", x.xfade_progress(0, 0) === 1 && x.xfade_progress(5, -1) === 1);
check("xfade_progress: 0/half/end/clamped-past-end", x.xfade_progress(0, 2) === 0 && near(x.xfade_progress(1, 2), 0.5, 1e-12) && x.xfade_progress(2, 2) === 1 && x.xfade_progress(3, 2) === 1);

check("xfade_out_gain: 1 -> 0 across the transition", near(x.xfade_out_gain(0, 2), 1, 1e-9) && near(x.xfade_out_gain(1, 2), Math.SQRT1_2, 3e-5) && near(x.xfade_out_gain(2, 2), 0, 3e-5));
check("xfade_in_gain: 0 -> 1 across the transition", near(x.xfade_in_gain(0, 2), 0, 3e-5) && near(x.xfade_in_gain(1, 2), Math.SQRT1_2, 3e-5) && near(x.xfade_in_gain(2, 2), 1, 1e-9));
let powErr = 0;
for (let i = 0; i <= 20; i++) {
  const t = (i / 20) * 2;
  powErr = Math.max(powErr, Math.abs(x.xfade_out_gain(t, 2) ** 2 + x.xfade_in_gain(t, 2) ** 2 - 1));
}
check("crossfade preserves power across the sweep", powErr < 1e-4, `err ${powErr}`);

check("fade_envelope: fade-in ramps 0 -> 1", near(x.fade_envelope(0, 5, 1, 1), 0, 3e-5) && near(x.fade_envelope(0.5, 5, 1, 1), Math.SQRT1_2, 3e-5) && near(x.fade_envelope(1, 5, 1, 1), 1, 1e-9));
check("fade_envelope: holds at 1 in the middle", x.fade_envelope(2.5, 5, 1, 1) === 1);
check("fade_envelope: fade-out ramps 1 -> 0", near(x.fade_envelope(4, 5, 1, 1), 1, 1e-9) && near(x.fade_envelope(4.5, 5, 1, 1), Math.SQRT1_2, 3e-5) && near(x.fade_envelope(5, 5, 1, 1), 0, 3e-5));
check("fade_envelope: disabled (<=0) fades hold at 1 throughout", x.fade_envelope(0, 5, 0, 0) === 1 && x.fade_envelope(5, 5, -1, -1) === 1);

check("pad_capture_in: preroll before the press, clamped to 0", x.pad_capture_in(5, 1) === 4 && x.pad_capture_in(0.5, 1) === 0);
check("pad_capture_out: short tap extends to the minimum length", x.pad_capture_out(10, 10.3, 1) === 11);
check("pad_capture_out: a real hold just uses the release time", x.pad_capture_out(10, 13, 1) === 13);

check("default constants", x.default_preroll_seconds() === 1 && x.default_min_clip_seconds() === 1);

console.log(`nle_test: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
