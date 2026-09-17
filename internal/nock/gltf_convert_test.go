package nock

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

// buildSyntheticGLBBytes mirrors GOLDENBAND/tools/gbtool's own test fixture builder
// (import_gltf_test.go's buildSyntheticGLB) -- a real, spec-conformant single-file .glb: two
// nodes (root -> child), a skin over both with joints DELIBERATELY reversed vs. topological
// order (exercises the bone-index remap for real), a 3-vertex triangle mesh skinned to the
// child joint, and one rotation animation (identity -> 90deg about Z).
func buildSyntheticGLBBytes(t *testing.T) []byte {
	t.Helper()
	var buf []byte
	appendF32 := func(vals ...float32) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
		return off
	}
	appendU8 := func(vals ...uint8) int {
		off := len(buf)
		buf = append(buf, vals...)
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		return off
	}
	appendU16 := func(vals ...uint16) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 2)
			binary.LittleEndian.PutUint16(b, v)
			buf = append(buf, b...)
		}
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		return off
	}

	posOff := appendF32(0, 0, 0, 1, 0, 0, 0, 1, 0)
	posLen := 3 * 3 * 4
	jointsOff := appendU8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	jointsLen := 3 * 4
	weightsOff := appendF32(1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0)
	weightsLen := 3 * 4 * 4
	idxOff := appendU16(0, 1, 2)
	idxLen := 3 * 2
	identity := func() []float32 {
		m := make([]float32, 16)
		m[0], m[5], m[10], m[15] = 1, 1, 1, 1
		return m
	}
	ibmOff := appendF32(append(identity(), identity()...)...)
	ibmLen := 2 * 16 * 4
	timesOff := appendF32(0, 1)
	timesLen := 2 * 4
	half := float32(math.Sqrt(0.5))
	rotOff := appendF32(0, 0, 0, 1, 0, 0, half, half)
	rotLen := 2 * 4 * 4

	type bufferView struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type accessor struct {
		BufferView    int    `json:"bufferView"`
		ComponentType int    `json:"componentType"`
		Count         int    `json:"count"`
		Type          string `json:"type"`
	}
	doc := map[string]any{
		"asset":   map[string]any{"version": "2.0"},
		"buffers": []map[string]any{{"byteLength": len(buf)}},
		"bufferViews": []bufferView{
			{0, posOff, posLen}, {0, jointsOff, jointsLen}, {0, weightsOff, weightsLen},
			{0, idxOff, idxLen}, {0, ibmOff, ibmLen}, {0, timesOff, timesLen}, {0, rotOff, rotLen},
		},
		"accessors": []accessor{
			{0, gltfFloat, 3, "VEC3"}, {1, gltfUnsignedByte, 3, "VEC4"}, {2, gltfFloat, 3, "VEC4"},
			{3, gltfUnsignedShort, 3, "SCALAR"}, {4, gltfFloat, 2, "MAT4"}, {5, gltfFloat, 2, "SCALAR"}, {6, gltfFloat, 2, "VEC4"},
		},
		"nodes": []map[string]any{
			{"name": "root", "children": []int{1}},
			{"name": "child"},
		},
		"meshes": []map[string]any{{
			"primitives": []map[string]any{{
				"attributes": map[string]int{"POSITION": 0, "JOINTS_0": 1, "WEIGHTS_0": 2},
				"indices":    3,
			}},
		}},
		"skins": []map[string]any{{"joints": []int{1, 0}, "inverseBindMatrices": 4}},
		"animations": []map[string]any{{
			"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 1, "path": "rotation"}}},
			"samplers": []map[string]any{{"input": 5, "output": 6}},
		}},
	}
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal glTF JSON: %v", err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}

	var glb []byte
	appendChunk := func(chunkType uint32, data []byte) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(hdr[4:8], chunkType)
		glb = append(glb, hdr...)
		glb = append(glb, data...)
	}
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], glbMagic)
	binary.LittleEndian.PutUint32(header[4:8], 2)
	glb = append(glb, header...)
	appendChunk(glbChunkJSON, jsonBytes)
	appendChunk(glbChunkBinary, buf)
	binary.LittleEndian.PutUint32(glb[8:12], uint32(len(glb)))
	return glb
}

// buildMultiClipGLBBytes is buildSyntheticGLBBytes's own real multi-clip sibling -- identical
// mesh/skeleton, but TWO named animations in the glTF's own "animations" array (both reusing the
// same times/rotation accessors -- glTF's own spec permits multiple animations referencing
// shared accessors, and this test only cares that TWO real, independent clips exist, not that
// they differ). Mirrors the real shape a Quaternius-style "Universal Animation Library" file has
// -- many real, separately-named clips bundled into one file -- the case that surfaced
// ImportGLTFBytes's own real "first animation only" v0 gap (2026-09-17).
func buildMultiClipGLBBytes(t *testing.T) []byte {
	t.Helper()
	var buf []byte
	appendF32 := func(vals ...float32) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
		return off
	}
	appendU8 := func(vals ...uint8) int {
		off := len(buf)
		buf = append(buf, vals...)
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		return off
	}
	appendU16 := func(vals ...uint16) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 2)
			binary.LittleEndian.PutUint16(b, v)
			buf = append(buf, b...)
		}
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		return off
	}

	posOff := appendF32(0, 0, 0, 1, 0, 0, 0, 1, 0)
	posLen := 3 * 3 * 4
	jointsOff := appendU8(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	jointsLen := 3 * 4
	weightsOff := appendF32(1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0)
	weightsLen := 3 * 4 * 4
	idxOff := appendU16(0, 1, 2)
	idxLen := 3 * 2
	identity := func() []float32 {
		m := make([]float32, 16)
		m[0], m[5], m[10], m[15] = 1, 1, 1, 1
		return m
	}
	ibmOff := appendF32(append(identity(), identity()...)...)
	ibmLen := 2 * 16 * 4
	timesOff := appendF32(0, 1)
	timesLen := 2 * 4
	half := float32(math.Sqrt(0.5))
	rotOff := appendF32(0, 0, 0, 1, 0, 0, half, half)
	rotLen := 2 * 4 * 4

	type bufferView struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type accessor struct {
		BufferView    int    `json:"bufferView"`
		ComponentType int    `json:"componentType"`
		Count         int    `json:"count"`
		Type          string `json:"type"`
	}
	doc := map[string]any{
		"asset":   map[string]any{"version": "2.0"},
		"buffers": []map[string]any{{"byteLength": len(buf)}},
		"bufferViews": []bufferView{
			{0, posOff, posLen}, {0, jointsOff, jointsLen}, {0, weightsOff, weightsLen},
			{0, idxOff, idxLen}, {0, ibmOff, ibmLen}, {0, timesOff, timesLen}, {0, rotOff, rotLen},
		},
		"accessors": []accessor{
			{0, gltfFloat, 3, "VEC3"}, {1, gltfUnsignedByte, 3, "VEC4"}, {2, gltfFloat, 3, "VEC4"},
			{3, gltfUnsignedShort, 3, "SCALAR"}, {4, gltfFloat, 2, "MAT4"}, {5, gltfFloat, 2, "SCALAR"}, {6, gltfFloat, 2, "VEC4"},
		},
		"nodes": []map[string]any{
			{"name": "root", "children": []int{1}},
			{"name": "child"},
		},
		"meshes": []map[string]any{{
			"primitives": []map[string]any{{
				"attributes": map[string]int{"POSITION": 0, "JOINTS_0": 1, "WEIGHTS_0": 2},
				"indices":    3,
			}},
		}},
		"skins": []map[string]any{{"joints": []int{1, 0}, "inverseBindMatrices": 4}},
		"animations": []map[string]any{
			{
				"name":     "Idle",
				"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 1, "path": "rotation"}}},
				"samplers": []map[string]any{{"input": 5, "output": 6}},
			},
			{
				"name":     "Walk_F",
				"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 1, "path": "rotation"}}},
				"samplers": []map[string]any{{"input": 5, "output": 6}},
			},
		},
	}
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal glTF JSON: %v", err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}

	var glb []byte
	appendChunk := func(chunkType uint32, data []byte) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(hdr[4:8], chunkType)
		glb = append(glb, hdr...)
		glb = append(glb, data...)
	}
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header[0:4], glbMagic)
	binary.LittleEndian.PutUint32(header[4:8], 2)
	glb = append(glb, header...)
	appendChunk(glbChunkJSON, jsonBytes)
	appendChunk(glbChunkBinary, buf)
	binary.LittleEndian.PutUint32(glb[8:12], uint32(len(glb)))
	return glb
}

// TestImportGLTFBytesAllClips_MultiClip is the real regression test for the 2026-09-17 fix
// (founder confirmed a real upload -- Quaternius's "Universal Animation Library" -- genuinely
// bundles dozens of separately-named clips in one file, and the old single-clip
// ImportGLTFBytes/[0]-only behavior silently discarded every clip past the first).
func TestImportGLTFBytesAllClips_MultiClip(t *testing.T) {
	glb := buildMultiClipGLBBytes(t)

	primary, additional, err := ImportGLTFBytesAllClips(glb, 2, "mocap", "test")
	if err != nil {
		t.Fatalf("ImportGLTFBytesAllClips: %v", err)
	}
	if string(primary.GSkelData[0:4]) != "GSKL" || string(primary.GMeshData[0:4]) != "GMSH" {
		t.Fatalf("expected the primary result to carry real mesh+skeleton data, got %+v", primary)
	}
	if string(primary.GBandData[0:4]) != "GBND" {
		t.Fatalf("expected the primary result to carry the FIRST clip's animation data")
	}
	if len(additional) != 1 {
		t.Fatalf("expected exactly 1 additional clip (2 animations in the fixture, 1 primary), got %d: %+v", len(additional), additional)
	}
	clip := additional[0]
	if clip.Name != "Walk_F" {
		t.Errorf("expected the additional clip's own real glTF animation name to carry through, got %q", clip.Name)
	}
	if string(clip.GBandData[0:4]) != "GBND" {
		t.Error("expected the additional clip to carry real gband data")
	}
	if clip.GBandData == nil || clip.SkeletonHash == "" {
		t.Errorf("expected the additional clip to carry a real skeleton_hash (for compatibility checks later), got %+v", clip)
	}
	if clip.SkeletonHash != primary.SkeletonHash {
		t.Errorf("expected the additional clip's skeleton_hash to match the primary's own (same source skeleton), got %q vs %q", clip.SkeletonHash, primary.SkeletonHash)
	}
	// The additional clip must NOT carry mesh/skeleton data of its own -- it's meant to be
	// attached (AnimStore.AttachAnimation) against the primary row or any other compatible one.
	var manifest gbandManifest
	if err := json.Unmarshal([]byte(clip.ManifestJSON), &manifest); err != nil {
		t.Fatalf("additional clip manifest is not valid JSON: %v", err)
	}
	if manifest.SkeletonHash != primary.SkeletonHash {
		t.Errorf("additional clip's own manifest skeleton_hash mismatch: %q vs %q", manifest.SkeletonHash, primary.SkeletonHash)
	}
}

func TestImportGLTFBytes_SyntheticRoundTrip(t *testing.T) {
	glb := buildSyntheticGLBBytes(t)

	result, err := ImportGLTFBytes(glb, 2, "mocap", "test")
	if err != nil {
		t.Fatalf("ImportGLTFBytes: %v", err)
	}

	if string(result.GSkelData[0:4]) != "GSKL" {
		t.Errorf("gskel data doesn't start with GSKL magic: %q", result.GSkelData[0:4])
	}
	if string(result.GMeshData[0:4]) != "GMSH" {
		t.Errorf("gmesh data doesn't start with GMSH magic: %q", result.GMeshData[0:4])
	}
	if string(result.GBandData[0:4]) != "GBND" {
		t.Errorf("gband data doesn't start with GBND magic: %q", result.GBandData[0:4])
	}
	if result.NumChannels != 4 {
		t.Errorf("expected 4 channels (child.qx/qy/qz/qw), got %d", result.NumChannels)
	}
	if result.DurationTicks != 3 {
		t.Errorf("tick_rate=2, 1s clip: expected 3 ticks, got %d", result.DurationTicks)
	}

	var manifest gbandManifest
	if err := json.Unmarshal([]byte(result.ManifestJSON), &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	wantChannels := []string{"child.qx", "child.qy", "child.qz", "child.qw"}
	if len(manifest.Channels) != len(wantChannels) {
		t.Fatalf("manifest channels: got %v, want %v", manifest.Channels, wantChannels)
	}
	for i, c := range wantChannels {
		if manifest.Channels[i] != c {
			t.Errorf("channel %d: got %q, want %q", i, manifest.Channels[i], c)
		}
	}
	if manifest.ContentHash != result.ContentHash {
		t.Errorf("manifest content_hash %q doesn't match result.ContentHash %q", manifest.ContentHash, result.ContentHash)
	}
	if manifest.Authorship.Kind != "mocap" || manifest.Authorship.Who != "test" {
		t.Errorf("unexpected authorship: %+v", manifest.Authorship)
	}
}

func TestImportGLTFBytes_RejectsNonGLB(t *testing.T) {
	_, err := ImportGLTFBytes([]byte("not a glb file"), 30, "mocap", "")
	if err == nil {
		t.Fatal("expected an error for garbage input, got nil")
	}
}

// TestEncodeGSkel_65Joints is the real regression test for a live bug report (2026-09-17):
// "glTF import failed: skin has 65 joints, exceeds GSKEL_MAX_JOINTS (64)" -- a real, common
// full-body-plus-hands rig (e.g. a typical Mixamo-style export with individual finger bones)
// tripped the old 64 cap on its very first real use. GSKEL_MAX_JOINTS was raised to 128
// (GOLDENBAND/src/gskel.h, mirrored here in gskelMaxJoints) -- this proves a real 65-joint
// skeleton now encodes cleanly.
// TestTopologyHash_IgnoresRestPoseDifferences is the real regression test for a bug found live
// (2026-09-17) while backfilling skeleton_hash for two real, already-imported assets confirmed by
// hand to share the same 65-joint rig: hashing the FULL encoded .gskel (rest_translation/
// rest_rotation/inverse_bind included) made them hash different, because those per-joint values
// can legitimately vary between two separate exports of "the same" rig without affecting whether
// an animation clip's channels apply correctly. topologyHash must hash only name+parent_index.
func TestTopologyHash_IgnoresRestPoseDifferences(t *testing.T) {
	base := []gskelJoint{
		{name: "root", parentIndex: -1, restTranslation: [3]float32{0, 0, 0}, restRotation: [4]float32{0, 0, 0, 1}},
		{name: "child", parentIndex: 0, restTranslation: [3]float32{1, 0, 0}, restRotation: [4]float32{0, 0, 0, 1}},
	}
	// Same names/hierarchy, deliberately different rest pose + inverse bind values -- simulating
	// two independent exports of "the same" rig.
	differentRestPose := []gskelJoint{
		{name: "root", parentIndex: -1, restTranslation: [3]float32{0, 0.01, 0}, restRotation: [4]float32{0, 0, 0.1, 0.995}},
		{name: "child", parentIndex: 0, restTranslation: [3]float32{1.02, 0, 0}, restRotation: [4]float32{0, 0, 0, 1}, inverseBind: [16]float32{1, 2, 3, 4}},
	}
	if topologyHash(base) != topologyHash(differentRestPose) {
		t.Error("expected topologyHash to be identical for the same joint names/hierarchy despite different rest pose values")
	}

	differentParent := []gskelJoint{
		{name: "root", parentIndex: -1},
		{name: "child", parentIndex: -1}, // real topology change: child is no longer parented to root
	}
	if topologyHash(base) == topologyHash(differentParent) {
		t.Error("expected topologyHash to differ when the parent hierarchy actually changes")
	}

	differentName := []gskelJoint{
		{name: "root", parentIndex: -1},
		{name: "other_child", parentIndex: 0},
	}
	if topologyHash(base) == topologyHash(differentName) {
		t.Error("expected topologyHash to differ when a joint name actually changes")
	}
}

func TestEncodeGSkel_65Joints(t *testing.T) {
	joints := make([]gskelJoint, 65)
	for i := range joints {
		joints[i] = gskelJoint{
			name:         "joint_" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			parentIndex:  int32(i - 1), // -1 for i==0 (root); a real parent-before-child chain otherwise
			restRotation: [4]float32{0, 0, 0, 1},
			inverseBind:  identityMat4(),
		}
	}
	out, err := encodeGSkel(joints)
	if err != nil {
		t.Fatalf("encodeGSkel with 65 joints: %v (this is the exact real bug report)", err)
	}
	if string(out[0:4]) != "GSKL" {
		t.Fatalf("expected real GSKL magic, got %q", out[0:4])
	}
	wantLen := 12 + 65*128 // header + 65 * 128-byte joint records
	if len(out) != wantLen {
		t.Errorf("expected %d bytes, got %d", wantLen, len(out))
	}
}
