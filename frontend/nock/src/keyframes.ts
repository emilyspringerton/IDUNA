// keyframes.ts — browser twin of IDUNA/internal/nock/keyframes.go (founder real-time, 2026-09-27:
// "we need a way to animate in NOCK"). The Go side bakes the stored .gband; this evaluates the
// same KeyframeDoc per tick for the animator's live preview, so what you pose is what gets baked.
// Keep the interpolation rules in lockstep with keyframes.go's segment()/Bake():
//   - values hold before the first key and after the last;
//   - a key's interp governs the segment that STARTS at it: linear (slerp/lerp), smooth
//     (smoothstep-eased), step (hold until the next key);
//   - sampling is per integer tick.
import * as THREE from 'three'

export type Interp = 'linear' | 'smooth' | 'step'

export interface RotKey {
  tick: number
  q: [number, number, number, number]
  interp?: Interp
}

export interface PosKey {
  tick: number
  t: [number, number, number]
  interp?: Interp
}

export interface KeyTrack {
  joint: string
  rotation?: RotKey[]
  translation?: PosKey[]
}

export interface KeyframeDoc {
  version?: number
  tick_rate: number
  duration_ticks: number
  tracks: KeyTrack[]
}

function segment(ticks: number[], interps: (Interp | undefined)[], tick: number): [number, number, number] {
  const n = ticks.length
  if (tick <= ticks[0]) return [0, 0, 0]
  if (tick >= ticks[n - 1]) return [n - 1, n - 1, 0]
  let i = 0
  while (i < n - 1 && ticks[i + 1] <= tick) i++
  let u = (tick - ticks[i]) / (ticks[i + 1] - ticks[i])
  if (interps[i] === 'step') u = 0
  else if (interps[i] === 'smooth') u = u * u * (3 - 2 * u)
  return [i, i + 1, u]
}

export function evalRotation(keys: RotKey[], tick: number, out: THREE.Quaternion): THREE.Quaternion {
  const [a, b, u] = segment(
    keys.map((k) => k.tick),
    keys.map((k) => k.interp),
    tick,
  )
  const qa = new THREE.Quaternion(...keys[a].q).normalize()
  const qb = new THREE.Quaternion(...keys[b].q).normalize()
  return out.copy(qa).slerp(qb, u) // THREE's slerp takes the shortest arc, like nock.Slerp
}

export function evalTranslation(keys: PosKey[], tick: number, out: THREE.Vector3): THREE.Vector3 {
  const [a, b, u] = segment(
    keys.map((k) => k.tick),
    keys.map((k) => k.interp),
    tick,
  )
  const ta = keys[a].t
  const tb = keys[b].t
  return out.set(ta[0] + (tb[0] - ta[0]) * u, ta[1] + (tb[1] - ta[1]) * u, ta[2] + (tb[2] - ta[2]) * u)
}

function sortedInsert<K extends { tick: number }>(keys: K[], key: K): K[] {
  const rest = keys.filter((k) => k.tick !== key.tick)
  rest.push(key)
  rest.sort((x, y) => x.tick - y.tick)
  return rest
}

// setKey returns a new doc with a rotation and/or translation key set on joint at tick. An
// existing key at that tick keeps its interp.
export function setKey(doc: KeyframeDoc, joint: string, tick: number, rot?: THREE.Quaternion, pos?: THREE.Vector3): KeyframeDoc {
  const tracks = doc.tracks.map((t) => ({ ...t }))
  let tr = tracks.find((t) => t.joint === joint)
  if (!tr) {
    tr = { joint }
    tracks.push(tr)
  }
  if (rot) {
    const prev = tr.rotation?.find((k) => k.tick === tick)
    tr.rotation = sortedInsert(tr.rotation ?? [], { tick, q: [rot.x, rot.y, rot.z, rot.w], interp: prev?.interp })
  }
  if (pos) {
    const prev = tr.translation?.find((k) => k.tick === tick)
    tr.translation = sortedInsert(tr.translation ?? [], { tick, t: [pos.x, pos.y, pos.z], interp: prev?.interp })
  }
  return { ...doc, tracks }
}

export function deleteKeysAt(doc: KeyframeDoc, joint: string, tick: number): KeyframeDoc {
  const tracks = doc.tracks
    .map((t) =>
      t.joint !== joint
        ? t
        : {
            ...t,
            rotation: t.rotation?.filter((k) => k.tick !== tick),
            translation: t.translation?.filter((k) => k.tick !== tick),
          },
    )
    .filter((t) => (t.rotation?.length ?? 0) + (t.translation?.length ?? 0) > 0)
  return { ...doc, tracks }
}

export function setInterpAt(doc: KeyframeDoc, joint: string, tick: number, interp: Interp): KeyframeDoc {
  const tracks = doc.tracks.map((t) =>
    t.joint !== joint
      ? t
      : {
          ...t,
          rotation: t.rotation?.map((k) => (k.tick === tick ? { ...k, interp } : k)),
          translation: t.translation?.map((k) => (k.tick === tick ? { ...k, interp } : k)),
        },
  )
  return { ...doc, tracks }
}

// keyTicks lists every tick that has at least one key on joint (or on any joint when omitted).
export function keyTicks(doc: KeyframeDoc, joint?: string): number[] {
  const s = new Set<number>()
  for (const t of doc.tracks) {
    if (joint && t.joint !== joint) continue
    t.rotation?.forEach((k) => s.add(k.tick))
    t.translation?.forEach((k) => s.add(k.tick))
  }
  return [...s].sort((a, b) => a - b)
}

// clampToDuration drops keys past a shortened clip's end.
export function clampToDuration(doc: KeyframeDoc, duration: number): KeyframeDoc {
  const tracks = doc.tracks
    .map((t) => ({
      ...t,
      rotation: t.rotation?.filter((k) => k.tick < duration),
      translation: t.translation?.filter((k) => k.tick < duration),
    }))
    .filter((t) => (t.rotation?.length ?? 0) + (t.translation?.length ?? 0) > 0)
  return { ...doc, duration_ticks: duration, tracks }
}
