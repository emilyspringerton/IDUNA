package nock

// gltf_convert.go — server-side glTF -> GOLDENBAND asset conversion, so NOCK's animation
// repository can accept a raw, drag-and-dropped .glb file directly (founder real-time,
// 2026-09-16: "ok I need to import quaternion assets nock tools drag and drop") instead of
// requiring the user to run `gbtool import --gltf` on their own machine first. This is a real,
// deliberate PORT of GOLDENBAND/tools/gbtool's own gltf.go/gskel.go/gmesh.go/gband.go/
// import_gltf.go logic -- not a reimplementation from scratch, and not a shared Go module either
// (GOLDENBAND is a standalone repo outside this monorepo's go.work, same as every other
// standalone repo here; IDUNA has no dependency on it). Same real algorithms, same real
// binary-layout constants (must stay byte-for-byte identical to GOLDENBAND/format/
// GBAND_FORMAT.md, GSKEL_FORMAT.md, GMESH_FORMAT.md or a downloaded asset silently stops being a
// valid .gband/.gskel/.gmesh file), condensed into byte-buffer writers instead of gbtool's own
// file-writing ones since a server-side conversion has no real filesystem path to write to.
//
// v0 scope, same real limits gbtool's own import_gltf.go documents, carried over verbatim:
// first skin/mesh/animation only, LINEAR sampler interpolation only, nlerp (not slerp) for
// quaternion resampling. One REAL, additional, server-specific limit: only a single-file .glb
// (binary container, embedded buffer) is accepted here -- a plain .gltf JSON file with an
// EXTERNAL .bin sidecar can't be converted from one dragged-and-dropped file, since there is no
// second file to resolve it against. Blender's default "glTF Binary (.glb)" export is exactly
// this shape, so this covers the real common case; a .gltf+.bin pair still works via gbtool's
// own CLI locally, uploaded as pre-converted output through NockAnimationsHandler.create.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// ── glTF reader (ported from GOLDENBAND/tools/gbtool/gltf.go) ──────────────────────────────

type gltfDoc struct {
	Buffers     []gltfBuffer     `json:"buffers"`
	BufferViews []gltfBufferView `json:"bufferViews"`
	Accessors   []gltfAccessor   `json:"accessors"`
	Nodes       []gltfNode       `json:"nodes"`
	Meshes      []gltfMesh       `json:"meshes"`
	Skins       []gltfSkin       `json:"skins"`
	Animations  []gltfAnimation  `json:"animations"`
}

type gltfBuffer struct {
	URI        string `json:"uri"`
	ByteLength int    `json:"byteLength"`
}
type gltfBufferView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	ByteStride int `json:"byteStride"`
}
type gltfAccessor struct {
	BufferView    *int   `json:"bufferView"`
	ByteOffset    int    `json:"byteOffset"`
	ComponentType int    `json:"componentType"`
	Count         int    `json:"count"`
	Type          string `json:"type"`
}
type gltfNode struct {
	Name        string    `json:"name"`
	Children    []int     `json:"children"`
	Translation []float64 `json:"translation"`
	Rotation    []float64 `json:"rotation"`
}
type gltfMesh struct {
	Primitives []gltfPrimitive `json:"primitives"`
}
type gltfPrimitive struct {
	Attributes map[string]int `json:"attributes"`
	Indices    *int           `json:"indices"`
}
type gltfSkin struct {
	InverseBindMatrices *int  `json:"inverseBindMatrices"`
	Joints              []int `json:"joints"`
}
type gltfAnimation struct {
	Name     string            `json:"name"`
	Channels []gltfAnimChannel `json:"channels"`
	Samplers []gltfAnimSampler `json:"samplers"`
}
type gltfAnimChannel struct {
	Sampler int            `json:"sampler"`
	Target  gltfAnimTarget `json:"target"`
}
type gltfAnimTarget struct {
	Node *int   `json:"node"`
	Path string `json:"path"`
}
type gltfAnimSampler struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

const (
	gltfUnsignedByte  = 5121
	gltfUnsignedShort = 5123
	gltfUnsignedInt   = 5125
	gltfFloat         = 5126
)

var gltfTypeComponents = map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT4": 16}

const (
	glbMagic       = 0x46546C67
	glbChunkJSON   = 0x4E4F534A
	glbChunkBinary = 0x004E4942
)

type loadedGLTF struct {
	doc     gltfDoc
	buffers [][]byte
}

// loadGLTFBytes accepts EITHER a real .glb (binary container, magic-detected) or a plain .gltf
// JSON text with base64 data-URI buffers -- see this file's own header note on why an external
// .bin sidecar isn't supported from a single uploaded file.
func loadGLTFBytes(raw []byte) (*loadedGLTF, error) {
	var jsonBytes, glbBin []byte
	if len(raw) >= 12 && binary.LittleEndian.Uint32(raw[0:4]) == glbMagic {
		var err error
		jsonBytes, glbBin, err = parseGLBContainer(raw)
		if err != nil {
			return nil, err
		}
	} else {
		jsonBytes = raw
	}

	var doc gltfDoc
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return nil, fmt.Errorf("parsing glTF JSON: %w", err)
	}

	buffers := make([][]byte, len(doc.Buffers))
	for i, b := range doc.Buffers {
		switch {
		case b.URI == "" && glbBin != nil:
			buffers[i] = glbBin
		case len(b.URI) > 5 && b.URI[:5] == "data:":
			comma := indexByte(b.URI, ',')
			if comma < 0 {
				return nil, fmt.Errorf("buffer %d: malformed data URI", i)
			}
			decoded, err := base64.StdEncoding.DecodeString(b.URI[comma+1:])
			if err != nil {
				return nil, fmt.Errorf("buffer %d: %w", i, err)
			}
			buffers[i] = decoded
		default:
			return nil, fmt.Errorf("buffer %d references an external file (%q) -- only a single self-contained .glb or a .gltf with embedded base64 buffers can be converted from one dragged-and-dropped file", i, b.URI)
		}
		if len(buffers[i]) < b.ByteLength {
			return nil, fmt.Errorf("buffer %d is %d bytes, expected at least %d", i, len(buffers[i]), b.ByteLength)
		}
	}
	return &loadedGLTF{doc: doc, buffers: buffers}, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func parseGLBContainer(raw []byte) (jsonChunk, binChunk []byte, err error) {
	version := binary.LittleEndian.Uint32(raw[4:8])
	if version != 2 {
		return nil, nil, fmt.Errorf("unsupported GLB version %d", version)
	}
	totalLen := binary.LittleEndian.Uint32(raw[8:12])
	if int(totalLen) > len(raw) {
		return nil, nil, fmt.Errorf("GLB declares length %d, file is only %d bytes", totalLen, len(raw))
	}
	off := 12
	for off+8 <= len(raw) {
		chunkLen := int(binary.LittleEndian.Uint32(raw[off : off+4]))
		chunkType := binary.LittleEndian.Uint32(raw[off+4 : off+8])
		dataStart := off + 8
		dataEnd := dataStart + chunkLen
		if dataEnd > len(raw) {
			return nil, nil, fmt.Errorf("GLB chunk at offset %d overruns file", off)
		}
		data := raw[dataStart:dataEnd]
		switch chunkType {
		case glbChunkJSON:
			jsonChunk = data
		case glbChunkBinary:
			binChunk = data
		}
		off = dataEnd
	}
	if jsonChunk == nil {
		return nil, nil, fmt.Errorf("GLB file has no JSON chunk")
	}
	return jsonChunk, binChunk, nil
}

func (g *loadedGLTF) accessorFloats(idx int) ([][]float64, error) {
	if idx < 0 || idx >= len(g.doc.Accessors) {
		return nil, fmt.Errorf("accessor index %d out of range", idx)
	}
	acc := g.doc.Accessors[idx]
	numComp, ok := gltfTypeComponents[acc.Type]
	if !ok {
		return nil, fmt.Errorf("accessor %d: unsupported type %q", idx, acc.Type)
	}
	if acc.BufferView == nil {
		return nil, fmt.Errorf("accessor %d: sparse accessors are not supported", idx)
	}
	bv := g.doc.BufferViews[*acc.BufferView]
	buf := g.buffers[bv.Buffer]

	compSize, err := gltfComponentSize(acc.ComponentType)
	if err != nil {
		return nil, fmt.Errorf("accessor %d: %w", idx, err)
	}
	elemSize := compSize * numComp
	stride := bv.ByteStride
	if stride == 0 {
		stride = elemSize
	}
	base := bv.ByteOffset + acc.ByteOffset
	rows := make([][]float64, acc.Count)
	for i := 0; i < acc.Count; i++ {
		off := base + i*stride
		row := make([]float64, numComp)
		for c := 0; c < numComp; c++ {
			row[c], err = gltfReadComponent(buf, off+c*compSize, acc.ComponentType)
			if err != nil {
				return nil, fmt.Errorf("accessor %d, element %d: %w", idx, i, err)
			}
		}
		rows[i] = row
	}
	return rows, nil
}

func gltfComponentSize(componentType int) (int, error) {
	switch componentType {
	case gltfUnsignedByte:
		return 1, nil
	case gltfUnsignedShort:
		return 2, nil
	case gltfUnsignedInt, gltfFloat:
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported componentType %d", componentType)
	}
}

func gltfReadComponent(buf []byte, off, componentType int) (float64, error) {
	switch componentType {
	case gltfFloat:
		if off+4 > len(buf) {
			return 0, fmt.Errorf("read past end of buffer")
		}
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[off : off+4]))), nil
	case gltfUnsignedInt:
		if off+4 > len(buf) {
			return 0, fmt.Errorf("read past end of buffer")
		}
		return float64(binary.LittleEndian.Uint32(buf[off : off+4])), nil
	case gltfUnsignedShort:
		if off+2 > len(buf) {
			return 0, fmt.Errorf("read past end of buffer")
		}
		return float64(binary.LittleEndian.Uint16(buf[off : off+2])), nil
	case gltfUnsignedByte:
		if off+1 > len(buf) {
			return 0, fmt.Errorf("read past end of buffer")
		}
		return float64(buf[off]), nil
	default:
		return 0, fmt.Errorf("unsupported componentType %d", componentType)
	}
}

// ── GOLDENBAND asset byte-encoders (ported from gband.go/gskel.go/gmesh.go, writing to a
// bytes.Buffer instead of a file -- identical binary layout either way). ───────────────────────

const gskelMaxJoints = 128 // matches GOLDENBAND/src/gskel.h's own GSKEL_MAX_JOINTS exactly (raised from 64 -- a real 65-joint rig hit the old cap)

type gskelJoint struct {
	name            string
	parentIndex     int32
	restTranslation [3]float32
	restRotation    [4]float32
	inverseBind     [16]float32
}

func encodeGSkel(joints []gskelJoint) ([]byte, error) {
	if len(joints) == 0 {
		return nil, fmt.Errorf("at least one joint is required")
	}
	if len(joints) > gskelMaxJoints {
		return nil, fmt.Errorf("%d joints exceeds GSKEL_MAX_JOINTS (%d)", len(joints), gskelMaxJoints)
	}
	for i, j := range joints {
		if j.parentIndex >= int32(i) {
			return nil, fmt.Errorf("joint %d (%q) has parent_index %d, must be < %d", i, j.name, j.parentIndex, i)
		}
		if len(j.name) >= 32 {
			return nil, fmt.Errorf("joint %d name %q is too long (max 31 bytes)", i, j.name)
		}
	}
	buf := new(bytes.Buffer)
	buf.WriteString("GSKL")
	writeU32(buf, 1)
	writeU32(buf, uint32(len(joints)))
	for _, j := range joints {
		rec := make([]byte, 128)
		copy(rec[0:32], j.name)
		binary.LittleEndian.PutUint32(rec[32:36], uint32(j.parentIndex))
		for k := 0; k < 3; k++ {
			binary.LittleEndian.PutUint32(rec[36+k*4:40+k*4], math.Float32bits(j.restTranslation[k]))
		}
		for k := 0; k < 4; k++ {
			binary.LittleEndian.PutUint32(rec[48+k*4:52+k*4], math.Float32bits(j.restRotation[k]))
		}
		for k := 0; k < 16; k++ {
			binary.LittleEndian.PutUint32(rec[64+k*4:68+k*4], math.Float32bits(j.inverseBind[k]))
		}
		buf.Write(rec)
	}
	return buf.Bytes(), nil
}

type gmeshVertex struct {
	position    [3]float32
	normal      [3]float32
	uv          [2]float32
	boneIndices [4]uint8
	boneWeights [4]float32
}

func encodeGMesh(verts []gmeshVertex, indices []uint32) ([]byte, error) {
	if len(verts) == 0 {
		return nil, fmt.Errorf("at least one vertex is required")
	}
	if len(indices) == 0 || len(indices)%3 != 0 {
		return nil, fmt.Errorf("index_count must be a nonzero multiple of 3, got %d", len(indices))
	}
	buf := new(bytes.Buffer)
	buf.WriteString("GMSH")
	writeU32(buf, 1)
	writeU32(buf, uint32(len(verts)))
	writeU32(buf, uint32(len(indices)))
	for _, v := range verts {
		rec := make([]byte, 52)
		for k := 0; k < 3; k++ {
			binary.LittleEndian.PutUint32(rec[0+k*4:4+k*4], math.Float32bits(v.position[k]))
		}
		for k := 0; k < 3; k++ {
			binary.LittleEndian.PutUint32(rec[12+k*4:16+k*4], math.Float32bits(v.normal[k]))
		}
		for k := 0; k < 2; k++ {
			binary.LittleEndian.PutUint32(rec[24+k*4:28+k*4], math.Float32bits(v.uv[k]))
		}
		copy(rec[32:36], v.boneIndices[:])
		for k := 0; k < 4; k++ {
			binary.LittleEndian.PutUint32(rec[36+k*4:40+k*4], math.Float32bits(v.boneWeights[k]))
		}
		buf.Write(rec)
	}
	for _, idx := range indices {
		writeU32(buf, idx)
	}
	return buf.Bytes(), nil
}

func encodeGBand(tickRate, durationTicks, numChannels uint32, skeletonHash [32]byte, data []float32) ([]byte, [32]byte) {
	body := new(bytes.Buffer)
	for _, v := range data {
		binary.Write(body, binary.LittleEndian, v) //nolint:errcheck
	}
	contentHash := sha256Sum(body.Bytes())

	buf := new(bytes.Buffer)
	buf.WriteString("GBND")
	writeU32(buf, 1)
	writeU32(buf, tickRate)
	writeU32(buf, durationTicks)
	writeU32(buf, numChannels)
	buf.Write(skeletonHash[:])
	buf.Write(contentHash[:])
	buf.Write(body.Bytes())
	return buf.Bytes(), contentHash
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// topologyHash hashes only what actually determines whether an animation clip's channels apply
// correctly to a skeleton: each joint's name and parent index, in order -- NOT rest_translation/
// rest_rotation/inverse_bind, which can legitimately differ between two exports of "the same"
// rig without affecting compatibility (see this function's own real, live-found call site
// comment in convertGLTF for the exact case that surfaced this).
func topologyHash(joints []gskelJoint) [32]byte {
	var buf bytes.Buffer
	for _, j := range joints {
		buf.WriteString(j.name)
		buf.WriteByte(0) // real, explicit separator -- a name boundary must never be ambiguous
		writeU32(&buf, uint32(j.parentIndex))
	}
	return sha256.Sum256(buf.Bytes())
}

func writeU32(buf *bytes.Buffer, v uint32) {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	buf.Write(b)
}

// ── glTF -> GOLDENBAND conversion (ported from import_gltf.go). ────────────────────────────

type convertedGLTF struct {
	gskelBytes    []byte
	gmeshBytes    []byte
	gbandBytes    []byte
	channels      []string
	tickRate      uint32
	durationTicks uint32
	contentHash   string
	skeletonHash  [32]byte
}

// convertGLTF is import_gltf.go's ConvertGLTF, condensed for server-side use: same real
// topological joint sort, same JOINTS_0 remap, same nlerp quaternion resampling.
func convertGLTF(g *loadedGLTF, tickRate uint32) (*convertedGLTF, error) {
	doc := &g.doc

	nodeParent := make([]int, len(doc.Nodes))
	for i := range nodeParent {
		nodeParent[i] = -1
	}
	for ni, n := range doc.Nodes {
		for _, c := range n.Children {
			if c >= 0 && c < len(nodeParent) {
				nodeParent[c] = ni
			}
		}
	}

	var joints []gskelJoint
	var nodeToJointIdx map[int]int
	var skinJointRemap []int

	if len(doc.Skins) > 0 {
		skin := doc.Skins[0]
		if len(skin.Joints) == 0 {
			return nil, fmt.Errorf("skin has zero joints")
		}
		if len(skin.Joints) > gskelMaxJoints {
			return nil, fmt.Errorf("skin has %d joints, exceeds GSKEL_MAX_JOINTS (%d)", len(skin.Joints), gskelMaxJoints)
		}
		isJointNode := make(map[int]bool, len(skin.Joints))
		for _, ni := range skin.Joints {
			isJointNode[ni] = true
		}
		nearestJointAncestor := func(ni int) int {
			p := nodeParent[ni]
			for p != -1 {
				if isJointNode[p] {
					return p
				}
				p = nodeParent[p]
			}
			return -1
		}
		type info struct {
			name        string
			parentNode  int
			translation [3]float32
			rotation    [4]float32
		}
		infos := make(map[int]info, len(skin.Joints))
		for _, ni := range skin.Joints {
			n := doc.Nodes[ni]
			nm := n.Name
			if nm == "" {
				nm = fmt.Sprintf("joint_%d", ni)
			}
			var tr [3]float32
			copyVec3(&tr, n.Translation, [3]float64{0, 0, 0})
			var rot [4]float32
			copyQuat(&rot, n.Rotation)
			infos[ni] = info{name: nm, parentNode: nearestJointAncestor(ni), translation: tr, rotation: rot}
		}

		var order []int
		placed := make(map[int]bool, len(skin.Joints))
		childrenOf := make(map[int][]int)
		var roots []int
		for _, ni := range skin.Joints {
			p := infos[ni].parentNode
			if p == -1 {
				roots = append(roots, ni)
			} else {
				childrenOf[p] = append(childrenOf[p], ni)
			}
		}
		sort.Ints(roots)
		queue := append([]int{}, roots...)
		for len(queue) > 0 {
			ni := queue[0]
			queue = queue[1:]
			if placed[ni] {
				continue
			}
			placed[ni] = true
			order = append(order, ni)
			kids := append([]int{}, childrenOf[ni]...)
			sort.Ints(kids)
			queue = append(queue, kids...)
		}
		if len(order) != len(skin.Joints) {
			return nil, fmt.Errorf("skin joint hierarchy is not a forest reachable from its own roots")
		}

		nodeToJointIdx = make(map[int]int, len(order))
		for idx, ni := range order {
			nodeToJointIdx[ni] = idx
		}

		var ibms [][]float64
		if skin.InverseBindMatrices != nil {
			var err error
			ibms, err = g.accessorFloats(*skin.InverseBindMatrices)
			if err != nil {
				return nil, fmt.Errorf("skin inverseBindMatrices: %w", err)
			}
		}

		joints = make([]gskelJoint, len(order))
		for idx, ni := range order {
			inf := infos[ni]
			gj := gskelJoint{name: inf.name, restTranslation: inf.translation, restRotation: inf.rotation}
			ident := identityMat4()
			gj.inverseBind = ident
			if inf.parentNode == -1 {
				gj.parentIndex = -1
			} else {
				gj.parentIndex = int32(nodeToJointIdx[inf.parentNode])
			}
			if ibms != nil {
				for origPos, jn := range skin.Joints {
					if jn == ni {
						for k := 0; k < 16 && k < len(ibms[origPos]); k++ {
							gj.inverseBind[k] = float32(ibms[origPos][k])
						}
						break
					}
				}
			}
			joints[idx] = gj
		}

		skinJointRemap = make([]int, len(skin.Joints))
		for raw, nodeIdx := range skin.Joints {
			skinJointRemap[raw] = nodeToJointIdx[nodeIdx]
		}
	} else {
		joints = []gskelJoint{{name: "root", parentIndex: -1, restRotation: [4]float32{0, 0, 0, 1}, inverseBind: identityMat4()}}
	}

	gskelBytes, err := encodeGSkel(joints)
	if err != nil {
		return nil, fmt.Errorf("encoding skeleton: %w", err)
	}
	// Real, deliberate: hash TOPOLOGY only (joint name + parent index, in order), not the full
	// encoded .gskel bytes. Found live (2026-09-17) while backfilling this hash for two already-
	// imported real assets confirmed by hand to share the exact same 65-joint rig: hashing the
	// whole file (rest_translation/rest_rotation/inverse_bind included) made them hash
	// DIFFERENT, because those per-joint float values can legitimately vary slightly between two
	// separate exports of "the same" rig (a different bind pose, minor re-export float noise) --
	// none of which affects whether an animation clip's own per-joint channels apply correctly.
	// gpose.c's own real FK (GOLDENBAND/src/gpose.c) only ever needs matching joint COUNT, ORDER,
	// and PARENT hierarchy to skin correctly; the mesh's own inverse_bind values are independent
	// per-joint corrections that don't need to match anything about the animation's own source
	// rest pose. Topology is the real, correct compatibility signal.
	skeletonHash := topologyHash(joints)

	var gmeshBytes []byte
	if len(doc.Meshes) > 0 {
		gmeshBytes, err = convertMesh(g, doc.Meshes[0], skinJointRemap)
		if err != nil {
			return nil, fmt.Errorf("encoding mesh: %w", err)
		}
	}

	result := &convertedGLTF{gskelBytes: gskelBytes, gmeshBytes: gmeshBytes, skeletonHash: skeletonHash}
	if len(doc.Animations) > 0 {
		gbandBytes, channels, dt, contentHash, err := convertAnimation(doc, g, doc.Animations[0], tickRate, skeletonHash)
		if err != nil {
			return nil, fmt.Errorf("encoding animation: %w", err)
		}
		result.gbandBytes = gbandBytes
		result.channels = channels
		result.tickRate = tickRate
		result.durationTicks = dt
		result.contentHash = hex.EncodeToString(contentHash[:])
	}
	return result, nil
}

func identityMat4() [16]float32 {
	var m [16]float32
	m[0], m[5], m[10], m[15] = 1, 1, 1, 1
	return m
}

func copyVec3(dst *[3]float32, src []float64, def [3]float64) {
	for k := 0; k < 3; k++ {
		v := def[k]
		if k < len(src) {
			v = src[k]
		}
		dst[k] = float32(v)
	}
}

func copyQuat(dst *[4]float32, src []float64) {
	def := [4]float64{0, 0, 0, 1}
	for k := 0; k < 4; k++ {
		v := def[k]
		if k < len(src) {
			v = src[k]
		}
		dst[k] = float32(v)
	}
}

func convertMesh(g *loadedGLTF, mesh gltfMesh, skinJointRemap []int) ([]byte, error) {
	if len(mesh.Primitives) == 0 {
		return nil, fmt.Errorf("mesh has zero primitives")
	}
	prim := mesh.Primitives[0]
	posIdx, ok := prim.Attributes["POSITION"]
	if !ok {
		return nil, fmt.Errorf("mesh primitive has no POSITION attribute")
	}
	positions, err := g.accessorFloats(posIdx)
	if err != nil {
		return nil, fmt.Errorf("POSITION: %w", err)
	}
	vertexCount := len(positions)

	normals := make([][]float64, vertexCount)
	if ni, ok := prim.Attributes["NORMAL"]; ok {
		normals, err = g.accessorFloats(ni)
		if err != nil {
			return nil, fmt.Errorf("NORMAL: %w", err)
		}
	} else {
		for i := range normals {
			normals[i] = []float64{0, 1, 0}
		}
	}
	uvs := make([][]float64, vertexCount)
	if ui, ok := prim.Attributes["TEXCOORD_0"]; ok {
		uvs, err = g.accessorFloats(ui)
		if err != nil {
			return nil, fmt.Errorf("TEXCOORD_0: %w", err)
		}
	} else {
		for i := range uvs {
			uvs[i] = []float64{0, 0}
		}
	}
	joints := make([][]float64, vertexCount)
	weights := make([][]float64, vertexCount)
	if ji, jok := prim.Attributes["JOINTS_0"]; jok {
		wi, wok := prim.Attributes["WEIGHTS_0"]
		if !wok {
			return nil, fmt.Errorf("mesh has JOINTS_0 but no WEIGHTS_0")
		}
		joints, err = g.accessorFloats(ji)
		if err != nil {
			return nil, fmt.Errorf("JOINTS_0: %w", err)
		}
		weights, err = g.accessorFloats(wi)
		if err != nil {
			return nil, fmt.Errorf("WEIGHTS_0: %w", err)
		}
	} else {
		for i := 0; i < vertexCount; i++ {
			joints[i] = []float64{0, 0, 0, 0}
			weights[i] = []float64{1, 0, 0, 0}
		}
	}

	verts := make([]gmeshVertex, vertexCount)
	for i := 0; i < vertexCount; i++ {
		v := gmeshVertex{}
		copyVec3(&v.position, positions[i], [3]float64{0, 0, 0})
		copyVec3(&v.normal, normals[i], [3]float64{0, 1, 0})
		for k := 0; k < 2 && k < len(uvs[i]); k++ {
			v.uv[k] = float32(uvs[i][k])
		}
		for k := 0; k < 4 && k < len(joints[i]); k++ {
			raw := int(joints[i][k])
			final := raw
			if raw >= 0 && raw < len(skinJointRemap) {
				final = skinJointRemap[raw]
			}
			v.boneIndices[k] = uint8(final)
		}
		for k := 0; k < 4 && k < len(weights[i]); k++ {
			v.boneWeights[k] = float32(weights[i][k])
		}
		verts[i] = v
	}

	var indices []uint32
	if prim.Indices != nil {
		rows, err := g.accessorFloats(*prim.Indices)
		if err != nil {
			return nil, fmt.Errorf("primitive indices: %w", err)
		}
		indices = make([]uint32, len(rows))
		for i, r := range rows {
			indices[i] = uint32(r[0])
		}
	} else {
		indices = make([]uint32, vertexCount-(vertexCount%3))
		for i := range indices {
			indices[i] = uint32(i)
		}
	}
	return encodeGMesh(verts, indices)
}

func convertAnimation(doc *gltfDoc, g *loadedGLTF, anim gltfAnimation, tickRate uint32, skeletonHash [32]byte) ([]byte, []string, uint32, [32]byte, error) {
	type groupKey struct {
		node int
		path string
	}
	groups := map[groupKey]gltfAnimSampler{}
	for _, ch := range anim.Channels {
		if ch.Target.Node == nil {
			continue
		}
		if ch.Target.Path != "translation" && ch.Target.Path != "rotation" {
			continue
		}
		if ch.Sampler < 0 || ch.Sampler >= len(anim.Samplers) {
			continue
		}
		groups[groupKey{*ch.Target.Node, ch.Target.Path}] = anim.Samplers[ch.Sampler]
	}
	if len(groups) == 0 {
		return nil, nil, 0, [32]byte{}, fmt.Errorf("animation %q has no supported (translation/rotation) channels", anim.Name)
	}

	maxTime := 0.0
	type decodedGroup struct {
		key    groupKey
		times  []float64
		values [][]float64
	}
	var decoded []decodedGroup
	for key, sampler := range groups {
		timeRows, err := g.accessorFloats(sampler.Input)
		if err != nil {
			return nil, nil, 0, [32]byte{}, fmt.Errorf("animation sampler input: %w", err)
		}
		valueRows, err := g.accessorFloats(sampler.Output)
		if err != nil {
			return nil, nil, 0, [32]byte{}, fmt.Errorf("animation sampler output: %w", err)
		}
		if len(timeRows) != len(valueRows) {
			return nil, nil, 0, [32]byte{}, fmt.Errorf("animation sampler has %d times but %d values", len(timeRows), len(valueRows))
		}
		times := make([]float64, len(timeRows))
		for i, r := range timeRows {
			times[i] = r[0]
			if r[0] > maxTime {
				maxTime = r[0]
			}
		}
		decoded = append(decoded, decodedGroup{key: key, times: times, values: valueRows})
	}
	if tickRate == 0 {
		return nil, nil, 0, [32]byte{}, fmt.Errorf("tick rate must be > 0")
	}
	durationTicks := uint32(math.Floor(maxTime*float64(tickRate))) + 1
	if durationTicks < 1 {
		durationTicks = 1
	}

	var channelNames []string
	var perTick [][]float32
	for _, dg := range decoded {
		n := doc.Nodes[dg.key.node]
		name := n.Name
		if name == "" {
			name = fmt.Sprintf("joint_%d", dg.key.node)
		}
		if dg.key.path == "rotation" {
			samples := resampleQuat(dg.times, dg.values, tickRate, durationTicks)
			for c, suffix := range []string{"qx", "qy", "qz", "qw"} {
				channelNames = append(channelNames, name+"."+suffix)
				col := make([]float32, durationTicks)
				for t := range col {
					col[t] = float32(samples[t][c])
				}
				perTick = append(perTick, col)
			}
		} else {
			samples := resampleLinear(dg.times, dg.values, tickRate, durationTicks, 3)
			for c, suffix := range []string{"tx", "ty", "tz"} {
				channelNames = append(channelNames, name+"."+suffix)
				col := make([]float32, durationTicks)
				for t := range col {
					col[t] = float32(samples[t][c])
				}
				perTick = append(perTick, col)
			}
		}
	}

	numChannels := len(channelNames)
	data := make([]float32, int(durationTicks)*numChannels)
	for t := 0; t < int(durationTicks); t++ {
		for c := 0; c < numChannels; c++ {
			data[t*numChannels+c] = perTick[c][t]
		}
	}
	gbandBytes, contentHash := encodeGBand(tickRate, durationTicks, uint32(numChannels), skeletonHash, data)
	return gbandBytes, channelNames, durationTicks, contentHash, nil
}

func resampleLinear(times []float64, values [][]float64, tickRate uint32, durationTicks uint32, comps int) [][]float64 {
	out := make([][]float64, durationTicks)
	for t := uint32(0); t < durationTicks; t++ {
		out[t] = sampleKeyframesAt(times, values, float64(t)/float64(tickRate), comps)
	}
	return out
}

func resampleQuat(times []float64, values [][]float64, tickRate uint32, durationTicks uint32) [][]float64 {
	lin := resampleLinear(times, values, tickRate, durationTicks, 4)
	for i, q := range lin {
		mag := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
		if mag > 1e-12 {
			lin[i] = []float64{q[0] / mag, q[1] / mag, q[2] / mag, q[3] / mag}
		} else {
			lin[i] = []float64{0, 0, 0, 1}
		}
	}
	return lin
}

func sampleKeyframesAt(times []float64, values [][]float64, time float64, comps int) []float64 {
	n := len(times)
	if n == 0 {
		return make([]float64, comps)
	}
	if time <= times[0] {
		return append([]float64(nil), values[0]...)
	}
	if time >= times[n-1] {
		return append([]float64(nil), values[n-1]...)
	}
	for i := 0; i < n-1; i++ {
		if time >= times[i] && time <= times[i+1] {
			span := times[i+1] - times[i]
			w := 0.0
			if span > 1e-12 {
				w = (time - times[i]) / span
			}
			out := make([]float64, comps)
			for c := 0; c < comps; c++ {
				a, b := 0.0, 0.0
				if c < len(values[i]) {
					a = values[i][c]
				}
				if c < len(values[i+1]) {
					b = values[i+1][c]
				}
				out[c] = a + (b-a)*w
			}
			return out
		}
	}
	return append([]float64(nil), values[n-1]...)
}

// GLTFImportResult is everything ImportGLTFBytes produces, ready to hand straight to
// AnimStore.CreateAnimation.
type GLTFImportResult struct {
	GSkelData     []byte
	GMeshData     []byte
	GBandData     []byte
	ManifestJSON  string
	TickRate      int
	DurationTicks int
	NumChannels   int
	ContentHash   string
	// SkeletonHash (2026-09-17, founder real-time: "build fill in the gaps... you can add
	// animations to it later, either by uploading a separate file with the same rig") -- real,
	// hex-encoded sha256 of the skeleton's own joint data, present whenever the source file had a
	// skin (regardless of whether this specific import also carried animation data). This is the
	// one real, checkable signal that a later-uploaded animation clip's own rig actually matches
	// an already-stored mesh+rig's rig, closing the gap NOCK's own animation-library copy
	// explicitly promises ("uploading a separate file with the same rig") but didn't yet build
	// any affordance for -- see AnimStore.AttachAnimation.
	SkeletonHash string
}

// gbandManifest mirrors GOLDENBAND/format/GBAND_FORMAT.md's own manifest schema exactly (see
// that doc for the full field list) -- kept package-private and separate from
// gbandManifestFields in nock_animations.go's own handler package, since that one only reads
// back the narrow slice this package's own caller needs.
type gbandManifest struct {
	GBandVersion  int              `json:"gband_version"`
	SkeletonHash  string           `json:"skeleton_hash"`
	ContentHash   string           `json:"content_hash"`
	TickRate      int              `json:"tick_rate"`
	DurationTicks int              `json:"duration_ticks"`
	Channels      []string         `json:"channels"`
	Authorship    gbandAuthorship  `json:"authorship"`
	IntentTags    []string         `json:"intent_tags"`
	LoopPoints    gbandLoopPoints  `json:"loop_points"`
	Safety        gbandSafetyNotes `json:"safety"`
}
type gbandAuthorship struct {
	Kind string `json:"kind"`
	Who  string `json:"who"`
}
type gbandLoopPoints struct {
	StartTick int `json:"start_tick"`
	EndTick   int `json:"end_tick"`
}
type gbandSafetyNotes struct {
	MaxJointVelocity *float64 `json:"max_joint_velocity"`
	MaxJointTorque   *float64 `json:"max_joint_torque"`
}

// ImportGLTFBytes is the real, single entry point NockAnimationsHandler's own drag-and-drop
// import route calls: raw bytes of a dragged-and-dropped .glb (or a .gltf with embedded base64
// buffers) in, a fully-formed GOLDENBAND asset set out. authorshipKind/Who are labeled the same
// honest way gbtool's own --kind/--who CLI flags are (GBAND_FORMAT.md's manifest schema).
func ImportGLTFBytes(raw []byte, tickRate uint32, authorshipKind, authorshipWho string) (*GLTFImportResult, error) {
	loaded, err := loadGLTFBytes(raw)
	if err != nil {
		return nil, err
	}
	converted, err := convertGLTF(loaded, tickRate)
	if err != nil {
		return nil, err
	}

	// Real, live-found fix (2026-09-17): a glTF with no animated channels -- a bare mesh, a bare
	// rig, or a rigged mesh with no baked animation yet, all real, legitimate assets on their
	// own -- used to be rejected outright. Now real: store whatever the file actually has.
	// converted.gskelBytes/skeletonHash are always real (a skinless source falls back to a real,
	// synthetic single "root" joint -- see convertGLTF's own doc comment), so SkeletonHash is
	// always populated here too, matching that same convention.
	skeletonHashHex := hex.EncodeToString(converted.skeletonHash[:])
	result := &GLTFImportResult{
		GSkelData:    converted.gskelBytes,
		GMeshData:    converted.gmeshBytes,
		SkeletonHash: skeletonHashHex,
	}
	if converted.gbandBytes != nil {
		manifest := gbandManifest{
			GBandVersion:  1,
			SkeletonHash:  skeletonHashHex,
			ContentHash:   converted.contentHash,
			TickRate:      int(converted.tickRate),
			DurationTicks: int(converted.durationTicks),
			Channels:      converted.channels,
			Authorship:    gbandAuthorship{Kind: authorshipKind, Who: authorshipWho},
			IntentTags:    []string{},
			LoopPoints:    gbandLoopPoints{StartTick: 0, EndTick: int(converted.durationTicks)},
		}
		manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling manifest: %w", err)
		}
		result.GBandData = converted.gbandBytes
		result.ManifestJSON = string(manifestBytes)
		result.TickRate = int(converted.tickRate)
		result.DurationTicks = int(converted.durationTicks)
		result.NumChannels = len(converted.channels)
		result.ContentHash = converted.contentHash
	}
	return result, nil
}
