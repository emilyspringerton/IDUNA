package nock

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
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

func near(a, b float64) bool { return math.Abs(a-b) < 1e-5 }

func TestGLTFToWidgetBoxes_HierarchyRotationColorDoor(t *testing.T) {
	boxes, err := GLTFToWidgetBoxes(buildCubeSceneGLTF(t), 1)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(boxes) != 3 {
		t.Fatalf("expected 3 boxes (one per mesh node), got %d: %+v", len(boxes), boxes)
	}
	floor, door, plane := boxes[0], boxes[1], boxes[2]
	if floor.Name != "Floor" || !near(floor.SX, 10) || !near(floor.SY, 0.2) || !near(floor.SZ, 10) || !near(floor.CY, 0) {
		t.Errorf("floor box wrong: %+v", floor)
	}
	if floor.R != 1 || floor.G != 0 || floor.B != 0 {
		t.Errorf("floor should carry the red baseColorFactor, got %v/%v/%v", floor.R, floor.G, floor.B)
	}
	if floor.IsDoor {
		t.Error("floor must not be a door")
	}
	// Parent translation (0,5,0) + child translation (3,0,0); 90deg Y rotation swaps x/z extents.
	if !door.IsDoor || !near(door.CX, 3) || !near(door.CY, 5) || !near(door.CZ, 0) ||
		!near(door.SX, 0.2) || !near(door.SY, 2) || !near(door.SZ, 1) {
		t.Errorf("door box wrong: %+v", door)
	}
	if !near(plane.SY, MinBoxExtent) || !near(plane.SX, 4) {
		t.Errorf("flat plane should be widened to MinBoxExtent thickness: %+v", plane)
	}
}

func TestGLTFToWidgetBoxes_Scale(t *testing.T) {
	boxes, err := GLTFToWidgetBoxes(buildCubeSceneGLTF(t), 2)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if d := boxes[1]; !near(d.CX, 6) || !near(d.CY, 10) || !near(d.SY, 4) {
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
