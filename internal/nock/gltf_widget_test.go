package nock

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// buildCubeSceneGLTF builds a plain .gltf (embedded base64 buffer) holding one unit cube mesh
// (corners at +/-0.5) instanced by three nodes: "Floor" (scaled 10x0.2x10, red material via the
// mesh), a "Room" parent translated (0,5,0) with a child "Door.001" scaled 1x2x0.2 and rotated
// 90deg about Y (so its x/z extents swap), and a "Plane" node scaled flat to zero Y thickness.
func buildCubeSceneGLTF(t *testing.T) []byte {
	t.Helper()
	var buf []byte
	for _, c := range [][3]float32{{-.5, -.5, -.5}, {.5, -.5, -.5}, {-.5, .5, -.5}, {.5, .5, -.5}, {-.5, -.5, .5}, {.5, -.5, .5}, {-.5, .5, .5}, {.5, .5, .5}} {
		for _, v := range c {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
	}
	s := math.Sqrt(0.5)
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []int{0, 1, 3}}},
		"buffers":     []any{map[string]any{"byteLength": len(buf), "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(buf)}},
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": len(buf)}},
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 8, "type": "VEC3"}},
		"materials":   []any{map[string]any{"pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{1, 0, 0, 1}}}},
		"meshes":      []any{map[string]any{"name": "Cube", "primitives": []any{map[string]any{"attributes": map[string]int{"POSITION": 0}, "material": 0}}}},
		"nodes": []any{
			map[string]any{"name": "Floor", "mesh": 0, "scale": []float64{10, 0.2, 10}},
			map[string]any{"name": "Room", "translation": []float64{0, 5, 0}, "children": []int{2}},
			map[string]any{"name": "Door.001", "mesh": 0, "translation": []float64{3, 0, 0}, "rotation": []float64{0, s, 0, s}, "scale": []float64{1, 2, 0.2}},
			map[string]any{"name": "Plane", "mesh": 0, "scale": []float64{4, 0, 4}},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func gltfNear(a, b float64) bool { return math.Abs(a-b) < 1e-5 }

func TestGLTFToWidgetBoxes_HierarchyRotationColorDoor(t *testing.T) {
	boxes, err := GLTFToWidgetBoxes(buildCubeSceneGLTF(t), 1)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(boxes) != 3 {
		t.Fatalf("expected 3 boxes (one per mesh node), got %d: %+v", len(boxes), boxes)
	}
	floor, door, plane := boxes[0], boxes[1], boxes[2]
	if floor.Name != "Floor" || !gltfNear(floor.SX, 10) || !gltfNear(floor.SY, 0.2) || !gltfNear(floor.SZ, 10) || !gltfNear(floor.CY, 0) {
		t.Errorf("floor box wrong: %+v", floor)
	}
	if floor.R != 1 || floor.G != 0 || floor.B != 0 {
		t.Errorf("floor should carry the red baseColorFactor, got %v/%v/%v", floor.R, floor.G, floor.B)
	}
	if floor.IsDoor {
		t.Error("floor must not be a door")
	}
	// Parent translation (0,5,0) + child translation (3,0,0); 90deg Y rotation swaps x/z extents.
	if !door.IsDoor || !gltfNear(door.CX, 3) || !gltfNear(door.CY, 5) || !gltfNear(door.CZ, 0) ||
		!gltfNear(door.SX, 0.2) || !gltfNear(door.SY, 2) || !gltfNear(door.SZ, 1) {
		t.Errorf("door box wrong: %+v", door)
	}
	if !gltfNear(plane.SY, MinBoxExtent) || !gltfNear(plane.SX, 4) {
		t.Errorf("flat plane should be widened to MinBoxExtent thickness: %+v", plane)
	}
}

func TestGLTFToWidgetBoxes_Scale(t *testing.T) {
	boxes, err := GLTFToWidgetBoxes(buildCubeSceneGLTF(t), 2)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if d := boxes[1]; !gltfNear(d.CX, 6) || !gltfNear(d.CY, 10) || !gltfNear(d.SY, 4) {
		t.Errorf("scale 2 not applied to door: %+v", d)
	}
}

func TestGLTFToWidgetBoxes_AcceptsExistingSkinnedFixture(t *testing.T) {
	// The Animations tab's own synthetic character .glb (a skinned triangle) must also convert.
	boxes, err := GLTFToWidgetBoxes(buildSyntheticGLBBytes(t), 1)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(boxes) == 0 {
		t.Fatal("expected at least one box")
	}
}

func TestGLTFToWidgetBoxes_NoMeshIsError(t *testing.T) {
	raw := []byte(`{"asset":{"version":"2.0"},"nodes":[{"name":"Empty"}]}`)
	if _, err := GLTFToWidgetBoxes(raw, 1); err == nil {
		t.Fatal("expected an error for a mesh-less scene")
	}
}

// ---- Blender export shapes (#463: "all gltf importers should work with blender files") ----

// cubeBuffer returns the 8 corners of a unit cube (+/-0.5) as little-endian float32 VEC3.
func cubeBuffer() []byte {
	var buf []byte
	for _, c := range [][3]float32{{-.5, -.5, -.5}, {.5, -.5, -.5}, {-.5, .5, -.5}, {.5, .5, -.5}, {-.5, -.5, .5}, {.5, -.5, .5}, {-.5, .5, .5}, {.5, .5, .5}} {
		for _, v := range c {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
	}
	return buf
}

func gltfDocWith(buffers []any, extra map[string]any) []byte {
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []int{0}}},
		"buffers":     buffers,
		"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 96}},
		"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5126, "count": 8, "type": "VEC3"}},
		"meshes":      []any{map[string]any{"name": "Cube", "primitives": []any{map[string]any{"attributes": map[string]int{"POSITION": 0}}}}},
		"nodes":       []any{map[string]any{"name": "Cube", "mesh": 0, "translation": []float64{4, 1, -2}, "scale": []float64{2, 2, 2}}},
	}
	for k, v := range extra {
		doc[k] = v
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// Blender's "glTF Separate" export: a .gltf pointing at a percent-encoded sibling .bin.
func TestGLTFToWidgetBoxes_SeparateBinResource(t *testing.T) {
	raw := gltfDocWith([]any{map[string]any{"byteLength": 96, "uri": "Level%20One.bin"}}, nil)

	if _, err := GLTFToWidgetBoxes(raw, 1); err == nil || !strings.Contains(err.Error(), "Level One.bin") {
		t.Fatalf("without the sibling file the error must name it, got: %v", err)
	}
	boxes, err := GLTFToWidgetBoxesWithResources(raw, 1, map[string][]byte{"Level One.bin": cubeBuffer()})
	if err != nil {
		t.Fatalf("convert with resources: %v", err)
	}
	b := boxes[0]
	if !gltfNear(b.CX, 4) || !gltfNear(b.CY, 1) || !gltfNear(b.CZ, -2) || !gltfNear(b.SX, 2) || !gltfNear(b.SY, 2) || !gltfNear(b.SZ, 2) {
		t.Errorf("separate-bin cube wrong: %+v", b)
	}
	// a subfolder URI ("textures/Level One.bin") resolves by base name too
	raw = gltfDocWith([]any{map[string]any{"byteLength": 96, "uri": "export/Level%20One.bin"}}, nil)
	if _, err := GLTFToWidgetBoxesWithResources(raw, 1, map[string][]byte{"Level One.bin": cubeBuffer()}); err != nil {
		t.Errorf("subfolder uri should resolve by base name: %v", err)
	}
}

// Blender's Draco checkbox: refuse with a re-export hint instead of reading compressed garbage.
func TestGLTFToWidgetBoxes_DracoIsRefusedWithHint(t *testing.T) {
	raw := gltfDocWith([]any{map[string]any{"byteLength": 96, "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(cubeBuffer())}},
		map[string]any{"extensionsRequired": []string{"KHR_draco_mesh_compression"}})
	_, err := GLTFToWidgetBoxes(raw, 1)
	if err == nil || !strings.Contains(err.Error(), "Draco") {
		t.Fatalf("expected a Draco re-export hint, got: %v", err)
	}
}

// KHR_mesh_quantization: SHORT positions with the node scale compensating (non-normalized), and a
// normalized SHORT accessor mapped to [-1,1].
func TestGLTFToWidgetBoxes_QuantizedPositions(t *testing.T) {
	var buf []byte
	for _, c := range [][3]int16{{-100, -100, -100}, {100, 100, 100}} {
		for _, v := range c {
			b := make([]byte, 2)
			binary.LittleEndian.PutUint16(b, uint16(v))
			buf = append(buf, b...)
		}
	}
	buf = append(buf, 0, 0) // pad to 4
	raw := gltfDocWith([]any{map[string]any{"byteLength": len(buf), "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(buf)}},
		map[string]any{
			"bufferViews": []any{map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 12}},
			"accessors":   []any{map[string]any{"bufferView": 0, "componentType": 5122, "count": 2, "type": "VEC3"}},
			"nodes":       []any{map[string]any{"name": "Q", "mesh": 0, "scale": []float64{0.01, 0.01, 0.01}}}, // 200 units * 0.01 = 2
		})
	boxes, err := GLTFToWidgetBoxes(raw, 1)
	if err != nil {
		t.Fatalf("quantized convert: %v", err)
	}
	if !gltfNear(boxes[0].SX, 2) || !gltfNear(boxes[0].SY, 2) || !gltfNear(boxes[0].CX, 0) {
		t.Errorf("SHORT positions wrong: %+v", boxes[0])
	}
	// normalized SHORT: 32767 -> 1.0, -32767 -> -1.0 => a 2-wide box
	g := &loadedGLTF{
		doc: gltfDoc{
			BufferViews: []gltfBufferView{{Buffer: 0, ByteLength: 4}},
			Accessors:   []gltfAccessor{{BufferView: ptrInt(0), ComponentType: 5122, Count: 2, Type: "SCALAR", Normalized: true}},
		},
		buffers: [][]byte{{0xFF, 0x7F, 0x01, 0x80}}, // 32767, -32767
	}
	rows, err := g.accessorFloats(0)
	if err != nil || !gltfNear(rows[0][0], 1) || !gltfNear(rows[1][0], -1) {
		t.Errorf("normalized SHORT wrong: %v %v", rows, err)
	}
}

func ptrInt(i int) *int { return &i }

// A malformed file must be an error, never a panic (the importer is reachable from an upload).
func TestGLTFToWidgetBoxes_MalformedIndicesDontPanic(t *testing.T) {
	raw := gltfDocWith([]any{map[string]any{"byteLength": 96, "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(cubeBuffer())}},
		map[string]any{"accessors": []any{map[string]any{"bufferView": 7, "componentType": 5126, "count": 8, "type": "VEC3"}}})
	if _, err := GLTFToWidgetBoxes(raw, 1); err == nil {
		t.Fatal("expected an error for an out-of-range bufferView")
	}
	raw = gltfDocWith([]any{map[string]any{"byteLength": 96, "uri": "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(cubeBuffer())}},
		map[string]any{"bufferViews": []any{map[string]any{"buffer": 3, "byteOffset": 0, "byteLength": 96}}})
	if _, err := GLTFToWidgetBoxes(raw, 1); err == nil {
		t.Fatal("expected an error for an out-of-range buffer")
	}
}
