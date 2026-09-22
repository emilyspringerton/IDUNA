// goldenband.ts — shared GOLDENBAND .gskel/.gmesh/.gband binary parsing + encoding, factored out
// of AnimationViewer.tsx so AnimationEditor.tsx (2026-09-17, founder real-time: "lets build the
// animations editor - clone then edit workflow") can reuse the exact same read path, plus a real
// encodeGBand (the inverse of parseGBand) so an edited clip can be written back out. Byte layouts
// mirror GOLDENBAND/tools/gbtool's own Go writer exactly -- see that repo's own format/*_FORMAT.md
// for the authoritative spec.
import * as THREE from 'three'

export interface ParsedJoint {
  name: string
  parentIndex: number
  restTranslation: THREE.Vector3
  restRotation: THREE.Quaternion
  inverseBind: THREE.Matrix4
}

export function parseGSkel(buf: ArrayBuffer): ParsedJoint[] {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GSKL') throw new Error(`not a real .gskel file (bad magic ${magic})`)
  const jointCount = dv.getUint32(8, true)
  const HEADER = 12
  const REC = 128
  const decoder = new TextDecoder()
  const joints: ParsedJoint[] = []
  for (let i = 0; i < jointCount; i++) {
    const off = HEADER + i * REC
    const nameBytes = new Uint8Array(buf, off, 32)
    const nul = nameBytes.indexOf(0)
    const name = decoder.decode(nameBytes.subarray(0, nul === -1 ? 32 : nul))
    const parentIndex = dv.getInt32(off + 32, true)
    const restTranslation = new THREE.Vector3(dv.getFloat32(off + 36, true), dv.getFloat32(off + 40, true), dv.getFloat32(off + 44, true))
    const restRotation = new THREE.Quaternion(dv.getFloat32(off + 48, true), dv.getFloat32(off + 52, true), dv.getFloat32(off + 56, true), dv.getFloat32(off + 60, true))
    const ibArr = new Float32Array(16)
    for (let k = 0; k < 16; k++) ibArr[k] = dv.getFloat32(off + 64 + k * 4, true)
    const inverseBind = new THREE.Matrix4().fromArray(ibArr) // column-major, matches THREE's own Matrix4 convention directly
    joints.push({ name, parentIndex, restTranslation, restRotation, inverseBind })
  }
  return joints
}

export interface ParsedMesh {
  positions: Float32Array
  normals: Float32Array
  skinIndices: Uint16Array
  skinWeights: Float32Array
  indices: Uint32Array
}

export function parseGMesh(buf: ArrayBuffer): ParsedMesh {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GMSH') throw new Error(`not a real .gmesh file (bad magic ${magic})`)
  const vertexCount = dv.getUint32(8, true)
  const indexCount = dv.getUint32(12, true)
  const HEADER = 16
  const REC = 52
  const positions = new Float32Array(vertexCount * 3)
  const normals = new Float32Array(vertexCount * 3)
  const skinIndices = new Uint16Array(vertexCount * 4)
  const skinWeights = new Float32Array(vertexCount * 4)
  for (let i = 0; i < vertexCount; i++) {
    const off = HEADER + i * REC
    for (let k = 0; k < 3; k++) positions[i * 3 + k] = dv.getFloat32(off + k * 4, true)
    for (let k = 0; k < 3; k++) normals[i * 3 + k] = dv.getFloat32(off + 12 + k * 4, true)
    for (let k = 0; k < 4; k++) skinIndices[i * 4 + k] = dv.getUint8(off + 32 + k)
    for (let k = 0; k < 4; k++) skinWeights[i * 4 + k] = dv.getFloat32(off + 36 + k * 4, true)
  }
  const idxOff = HEADER + vertexCount * REC
  const indices = new Uint32Array(indexCount)
  for (let i = 0; i < indexCount; i++) indices[i] = dv.getUint32(idxOff + i * 4, true)
  return { positions, normals, skinIndices, skinWeights, indices }
}

export interface ParsedGBand {
  tickRate: number
  durationTicks: number
  numChannels: number
  data: Float32Array // durationTicks * numChannels, row-major by tick -- mutable, an editor edits this in place
}

export function parseGBand(buf: ArrayBuffer): ParsedGBand {
  const dv = new DataView(buf)
  const magic = String.fromCharCode(dv.getUint8(0), dv.getUint8(1), dv.getUint8(2), dv.getUint8(3))
  if (magic !== 'GBND') throw new Error(`not a real .gband file (bad magic ${magic})`)
  const tickRate = dv.getUint32(8, true)
  const durationTicks = dv.getUint32(12, true)
  const numChannels = dv.getUint32(16, true)
  const HEADER = 84
  const count = durationTicks * numChannels
  const data = new Float32Array(count)
  for (let i = 0; i < count; i++) data[i] = dv.getFloat32(HEADER + i * 4, true)
  return { tickRate, durationTicks, numChannels, data }
}

function hexToBytes(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2)
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16)
  return out
}

export async function sha256Hex(bytes: Uint8Array): Promise<string> {
  // TS's stricter lib.dom types now distinguish a plain ArrayBuffer-backed Uint8Array from one
  // that could be SharedArrayBuffer-backed; crypto.subtle.digest only accepts the former. A
  // fresh copy is always plain-ArrayBuffer-backed regardless of what `bytes` itself was backed
  // by, same real fix TS itself suggests for this exact narrowing gap.
  const digest = await crypto.subtle.digest('SHA-256', new Uint8Array(bytes))
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

// encodeGBand is parseGBand's own real inverse -- mirrors GOLDENBAND/tools/gbtool/gband.go's
// WriteFile byte-for-byte (84-byte header: magic+version+tick_rate+duration_ticks+num_channels+
// skeleton_hash[32]+content_hash[32], then duration_ticks*num_channels row-major-by-tick floats).
// contentHashHex is the real sha256 over just the data floats (little-endian bytes), matching
// GBandFile.ComputeContentHash's own real definition exactly -- callers should compute it via
// gbandDataBytes + sha256Hex below rather than trusting a stale value.
export function encodeGBand(tickRate: number, durationTicks: number, numChannels: number, skeletonHashHex: string, contentHashHex: string, data: Float32Array): ArrayBuffer {
  const HEADER = 84
  const buf = new ArrayBuffer(HEADER + data.length * 4)
  const dv = new DataView(buf)
  dv.setUint8(0, 'G'.charCodeAt(0)); dv.setUint8(1, 'B'.charCodeAt(0)); dv.setUint8(2, 'N'.charCodeAt(0)); dv.setUint8(3, 'D'.charCodeAt(0))
  dv.setUint32(4, 1, true) // version
  dv.setUint32(8, tickRate, true)
  dv.setUint32(12, durationTicks, true)
  dv.setUint32(16, numChannels, true)
  const skelBytes = hexToBytes(skeletonHashHex.padEnd(64, '0').slice(0, 64))
  const contentBytes = hexToBytes(contentHashHex.padEnd(64, '0').slice(0, 64))
  new Uint8Array(buf, 20, 32).set(skelBytes)
  new Uint8Array(buf, 52, 32).set(contentBytes)
  for (let i = 0; i < data.length; i++) dv.setFloat32(HEADER + i * 4, data[i], true)
  return buf
}

// gbandDataBytes returns just the little-endian float bytes (no header) -- the exact real input
// GBandFile.ComputeContentHash hashes, so a caller can compute a real, correct content_hash
// before calling encodeGBand.
export function gbandDataBytes(data: Float32Array): Uint8Array {
  const buf = new ArrayBuffer(data.length * 4)
  const dv = new DataView(buf)
  for (let i = 0; i < data.length; i++) dv.setFloat32(i * 4, data[i], true)
  return new Uint8Array(buf)
}

// buildAnimationClip turns a parsed .gband + its manifest's own channel names into a real
// THREE.AnimationClip -- one position + quaternion track per joint that has at least one
// animated channel, falling back to that joint's own rest value for any tick/component the clip
// doesn't cover (GSKEL_FORMAT.md/gseq.c's own documented convention, applied here too).
export function buildAnimationClip(gband: ParsedGBand, channelNames: string[], joints: ParsedJoint[]): THREE.AnimationClip {
  const { tickRate, durationTicks, numChannels, data } = gband
  const times = new Float32Array(durationTicks)
  for (let t = 0; t < durationTicks; t++) times[t] = t / tickRate

  const channelColumn = (name: string): Float32Array | null => {
    const idx = channelNames.indexOf(name)
    if (idx === -1) return null
    const out = new Float32Array(durationTicks)
    for (let t = 0; t < durationTicks; t++) out[t] = data[t * numChannels + idx]
    return out
  }

  const tracks: THREE.KeyframeTrack[] = []
  for (const j of joints) {
    const tx = channelColumn(`${j.name}.tx`)
    const ty = channelColumn(`${j.name}.ty`)
    const tz = channelColumn(`${j.name}.tz`)
    const qx = channelColumn(`${j.name}.qx`)
    const qy = channelColumn(`${j.name}.qy`)
    const qz = channelColumn(`${j.name}.qz`)
    const qw = channelColumn(`${j.name}.qw`)

    if (tx || ty || tz) {
      const values = new Float32Array(durationTicks * 3)
      for (let t = 0; t < durationTicks; t++) {
        values[t * 3 + 0] = tx ? tx[t] : j.restTranslation.x
        values[t * 3 + 1] = ty ? ty[t] : j.restTranslation.y
        values[t * 3 + 2] = tz ? tz[t] : j.restTranslation.z
      }
      tracks.push(new THREE.VectorKeyframeTrack(`${j.name}.position`, Array.from(times), Array.from(values)))
    }
    if (qx || qy || qz || qw) {
      const values = new Float32Array(durationTicks * 4)
      for (let t = 0; t < durationTicks; t++) {
        values[t * 4 + 0] = qx ? qx[t] : j.restRotation.x
        values[t * 4 + 1] = qy ? qy[t] : j.restRotation.y
        values[t * 4 + 2] = qz ? qz[t] : j.restRotation.z
        values[t * 4 + 3] = qw ? qw[t] : j.restRotation.w
      }
      tracks.push(new THREE.QuaternionKeyframeTrack(`${j.name}.quaternion`, Array.from(times), Array.from(values)))
    }
  }
  return new THREE.AnimationClip('clip', durationTicks / tickRate, tracks)
}

export function buildBones(joints: ParsedJoint[]): THREE.Bone[] {
  const bones = joints.map((j) => {
    const b = new THREE.Bone()
    b.name = j.name
    b.position.copy(j.restTranslation)
    b.quaternion.copy(j.restRotation)
    return b
  })
  joints.forEach((j, i) => {
    if (j.parentIndex >= 0) bones[j.parentIndex].add(bones[i])
  })
  return bones
}
