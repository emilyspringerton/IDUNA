// waveform.ts -- peak-overview waveform rendering, ported directly from MIXFORGE/web/waveform.mjs
// (same offscreen-canvas-cache pattern: render a one-time min/max peak overview, then repaint just
// a playhead overlay on top each UI tick without re-walking sample data). Real, literal component
// sharing with MIXFORGE (founder real-time, see nleEngine.ts's own header comment for why this is
// a port rather than a cross-repo import). Used by the MIXFORGE EDITOR to show a clip's audio
// under the timeline ruler -- useful for cutting to dialogue/narration in a documentary edit.

export function renderWaveform(canvas: HTMLCanvasElement, L: Float32Array): HTMLCanvasElement {
  const w = canvas.width, h = canvas.height;
  const off = document.createElement('canvas');
  off.width = w;
  off.height = h;
  const octx = off.getContext('2d')!;
  octx.fillStyle = '#161622';
  octx.fillRect(0, 0, w, h);
  const step = Math.max(1, Math.floor(L.length / w));
  octx.strokeStyle = '#5fd3ff';
  octx.beginPath();
  for (let x = 0; x < w; x++) {
    const start = x * step;
    let min = 1, max = -1;
    for (let k = 0; k < step && start + k < L.length; k++) {
      const v = L[start + k];
      if (v < min) min = v;
      if (v > max) max = v;
    }
    const y0 = h / 2 - max * (h / 2 - 1);
    const y1 = h / 2 - min * (h / 2 - 1);
    octx.moveTo(x + 0.5, y0);
    octx.lineTo(x + 0.5, y1);
  }
  octx.stroke();
  return off;
}

export function paintWaveform(canvas: HTMLCanvasElement, waveImage: HTMLCanvasElement | null, frac: number): void {
  const cctx = canvas.getContext('2d')!;
  const w = canvas.width, h = canvas.height;
  cctx.clearRect(0, 0, w, h);
  if (waveImage) cctx.drawImage(waveImage, 0, 0);
  else {
    cctx.fillStyle = '#161622';
    cctx.fillRect(0, 0, w, h);
  }
  const x = Math.max(0, Math.min(w, frac * w));
  cctx.fillStyle = 'rgba(255,95,162,0.18)';
  cctx.fillRect(0, 0, x, h);
  cctx.strokeStyle = '#ff5fa2';
  cctx.beginPath();
  cctx.moveTo(x, 0);
  cctx.lineTo(x, h);
  cctx.stroke();
}

/** Decodes a clip's audio (if any) into a mono Float32Array peak source, off an existing
 * AudioContext. Returns null for a clip with no audio track (nothing to draw). */
export async function decodeWaveformSource(ctx: AudioContext, url: string): Promise<Float32Array | null> {
  const bytes = await (await fetch(url)).arrayBuffer();
  let buf: AudioBuffer;
  try {
    buf = await ctx.decodeAudioData(bytes);
  } catch {
    return null;
  }
  if (buf.numberOfChannels === 0) return null;
  return buf.getChannelData(0);
}
