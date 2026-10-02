package nock

// gltf_widget.go — glTF -> SHANKPIT Widget geometry (founder real-time, 2026-09-27: "we need a
// way to go from nock tools gltf importer into the shankpit widgets"). NOCK's glTF importer
// (gltf_convert.go) turns a Blender export into GOLDENBAND character assets; a SHANKPIT Widget
// (internal/shankpit/widget_store.go) is walls+doors only, and every wall is an axis-aligned box
// (center x/y/z + full extents sx/sy/sz -- SHANKPIT's native GameMap has no other primitive).
// This file is the bridge: it reuses gltf_convert.go's own glTF reader (loadGLTFBytes /
// accessorFloats: .glb, embedded-buffer .gltf, and .gltf + sibling .bin files) and reduces a scene to boxes.
//
// Mapping, v0:
//   - One box per mesh-bearing NODE in the default scene (a mesh instanced by three nodes gives
//     three boxes; a mesh no node references gets one untransformed box), sized to the world-space AABB of that node's POSITION data, all primitives
//     merged. This is exact for the Blender "blockout" workflow (a level piece modeled as scaled/
//     moved cubes) and lossy for anything else: a rotated cube becomes its enclosing AABB, and a
//     detailed mesh becomes one bounding box, not a collision hull.
//   - Full TRS / matrix node transforms are composed down the hierarchy. Skinned meshes use
//     their bind-pose positions under the node transform (no skinning is applied).
//   - Color = the primitive's material pbrMetallicRoughness.baseColorFactor (first primitive
//     with one), else a neutral grey. Textures are ignored.
//   - A node whose name starts with "door" (case-insensitive: "Door", "door_front", "Door.001")
//     is flagged IsDoor -- the caller makes it a real Widget door on that wall, with no script.
//   - glTF is Y-up meters and so is SHANKPIT; Scale multiplies everything uniformly.
//   - A zero-thickness extent (a Blender plane) is widened to MinBoxExtent, since SHANKPIT
//     rejects a non-positive wall size.

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// MinBoxExtent is the thickness a flat (zero-extent) axis is widened to, in output units.
const MinBoxExtent = 0.1

// WidgetBox is one axis-aligned box extracted from a glTF scene.
type WidgetBox struct {
	Name       string
	CX, CY, CZ float64
	SX, SY, SZ float64
	R, G, B    float64
	IsDoor     bool
}

type gltfSceneDoc struct {
	Scene  *int `json:"scene"`
	Scenes []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Nodes []struct {
		Name        string    `json:"name"`
		Children    []int     `json:"children"`
		Mesh        *int      `json:"mesh"`
		Matrix      []float64 `json:"matrix"`
		Translation []float64 `json:"translation"`
		Rotation    []float64 `json:"rotation"`
		Scale       []float64 `json:"scale"`
	} `json:"nodes"`
	Meshes []struct {
		Name       string `json:"name"`
		Primitives []struct {
			Attributes map[string]int `json:"attributes"`
			Material   *int           `json:"material"`
		} `json:"primitives"`
	} `json:"meshes"`
	Materials []struct {
		PBR struct {
			BaseColorFactor []float64 `json:"baseColorFactor"`
		} `json:"pbrMetallicRoughness"`
	} `json:"materials"`
}

// mat4 is column-major, glTF's own convention.
type mat4 [16]float64

func mat4Identity() mat4 { return mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1} }

func (a mat4) mul(b mat4) mat4 {
	var out mat4
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += a[k*4+r] * b[c*4+k]
			}
			out[c*4+r] = s
		}
	}
	return out
}

func (a mat4) apply(x, y, z float64) (float64, float64, float64) {
	return a[0]*x + a[4]*y + a[8]*z + a[12],
		a[1]*x + a[5]*y + a[9]*z + a[13],
		a[2]*x + a[6]*y + a[10]*z + a[14]
}

// trsMatrix builds T * R * S, the glTF node local transform when no explicit matrix is given.
func trsMatrix(t, q, s []float64) mat4 {
	tx, ty, tz := 0.0, 0.0, 0.0
	if len(t) == 3 {
		tx, ty, tz = t[0], t[1], t[2]
	}
	qx, qy, qz, qw := 0.0, 0.0, 0.0, 1.0
	if len(q) == 4 {
		qx, qy, qz, qw = q[0], q[1], q[2], q[3]
		if n := math.Sqrt(qx*qx + qy*qy + qz*qz + qw*qw); n > 0 {
			qx, qy, qz, qw = qx/n, qy/n, qz/n, qw/n
		}
	}
	sx, sy, sz := 1.0, 1.0, 1.0
	if len(s) == 3 {
		sx, sy, sz = s[0], s[1], s[2]
	}
	return mat4{
		(1 - 2*(qy*qy+qz*qz)) * sx, (2 * (qx*qy + qz*qw)) * sx, (2 * (qx*qz - qy*qw)) * sx, 0,
		(2 * (qx*qy - qz*qw)) * sy, (1 - 2*(qx*qx+qz*qz)) * sy, (2 * (qy*qz + qx*qw)) * sy, 0,
		(2 * (qx*qz + qy*qw)) * sz, (2 * (qy*qz - qx*qw)) * sz, (1 - 2*(qx*qx+qy*qy)) * sz, 0,
		tx, ty, tz, 1,
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// GLTFToWidgetBoxes converts a .glb (or embedded-buffer .gltf) into axis-aligned boxes, in
// scene-traversal order. scale <= 0 means 1. Returns an error if the scene has no meshes.
func GLTFToWidgetBoxes(raw []byte, scale float64) ([]WidgetBox, error) {
	return GLTFToWidgetBoxesWithResources(raw, scale, nil)
}

// GLTFToWidgetBoxesWithResources is GLTFToWidgetBoxes plus the sibling files of a .gltf with
// external buffers (Blender's "glTF Separate" export: .gltf + .bin), keyed by file name.
func GLTFToWidgetBoxesWithResources(raw []byte, scale float64, resources map[string][]byte) ([]WidgetBox, error) {
	if scale <= 0 {
		scale = 1
	}
	g, err := loadGLTFBytesWithResources(raw, resources)
	if err != nil {
		return nil, err
	}
	var doc gltfSceneDoc
	if err := json.Unmarshal(g.rawJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing glTF JSON: %w", err)
	}

	var roots []int
	switch {
	case doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes):
		roots = doc.Scenes[*doc.Scene].Nodes
	case len(doc.Scenes) > 0:
		roots = doc.Scenes[0].Nodes
	default:
		isChild := make([]bool, len(doc.Nodes))
		for _, n := range doc.Nodes {
			for _, c := range n.Children {
				if c >= 0 && c < len(isChild) {
					isChild[c] = true
				}
			}
		}
		for i := range doc.Nodes {
			if !isChild[i] {
				roots = append(roots, i)
			}
		}
	}

	var boxes []WidgetBox
	meshUsed := make([]bool, len(doc.Meshes))
	visited := make([]bool, len(doc.Nodes))
	var walk func(idx int, parent mat4) error
	walk = func(idx int, parent mat4) error {
		if idx < 0 || idx >= len(doc.Nodes) {
			return fmt.Errorf("node index %d out of range", idx)
		}
		if visited[idx] {
			return fmt.Errorf("node %d appears twice in the scene graph (cycle or shared child)", idx)
		}
		visited[idx] = true
		n := doc.Nodes[idx]
		local := trsMatrix(n.Translation, n.Rotation, n.Scale)
		if len(n.Matrix) == 16 {
			copy(local[:], n.Matrix)
		}
		world := parent.mul(local)

		if n.Mesh != nil {
			if *n.Mesh < 0 || *n.Mesh >= len(doc.Meshes) {
				return fmt.Errorf("node %d references mesh %d, out of range", idx, *n.Mesh)
			}
			meshUsed[*n.Mesh] = true
			name := n.Name
			if name == "" {
				name = doc.Meshes[*n.Mesh].Name
			}
			box, ok, err := meshBox(g, &doc, *n.Mesh, world, scale)
			if err != nil {
				return fmt.Errorf("node %d: %w", idx, err)
			}
			if ok {
				box.Name = name
				box.IsDoor = strings.HasPrefix(strings.ToLower(n.Name), "door")
				boxes = append(boxes, box)
			}
		}
		for _, c := range n.Children {
			if err := walk(c, world); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root, mat4Identity()); err != nil {
			return nil, err
		}
	}
	// A mesh no scene node references (valid glTF, and the shape gltf_convert.go itself accepts,
	// since it reads meshes[0] directly) still becomes a box, untransformed.
	for mi := range doc.Meshes {
		if meshUsed[mi] {
			continue
		}
		box, ok, err := meshBox(g, &doc, mi, mat4Identity(), scale)
		if err != nil {
			return nil, fmt.Errorf("mesh %d: %w", mi, err)
		}
		if ok {
			box.Name = doc.Meshes[mi].Name
			boxes = append(boxes, box)
		}
	}
	if len(boxes) == 0 {
		return nil, fmt.Errorf("no mesh geometry found in this file's scene -- a widget needs at least one mesh node")
	}
	return boxes, nil
}

// meshBox computes the world-space AABB (all primitives merged) and color of one mesh under a
// given transform. ok is false when the mesh has no POSITION data at all.
func meshBox(g *loadedGLTF, doc *gltfSceneDoc, meshIdx int, world mat4, scale float64) (WidgetBox, bool, error) {
	mesh := doc.Meshes[meshIdx]
	minV := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	maxV := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	box := WidgetBox{R: 0.6, G: 0.6, B: 0.65}
	colored := false
	for _, prim := range mesh.Primitives {
		posIdx, ok := prim.Attributes["POSITION"]
		if !ok {
			continue
		}
		pts, err := g.accessorFloats(posIdx)
		if err != nil {
			return box, false, err
		}
		for _, p := range pts {
			if len(p) < 3 {
				continue
			}
			x, y, z := world.apply(p[0], p[1], p[2])
			for i, v := range [3]float64{x, y, z} {
				minV[i] = math.Min(minV[i], v)
				maxV[i] = math.Max(maxV[i], v)
			}
		}
		if !colored && prim.Material != nil && *prim.Material >= 0 && *prim.Material < len(doc.Materials) {
			if f := doc.Materials[*prim.Material].PBR.BaseColorFactor; len(f) >= 3 {
				box.R, box.G, box.B = clamp01(f[0]), clamp01(f[1]), clamp01(f[2])
				colored = true
			}
		}
	}
	if math.IsInf(minV[0], 1) {
		return box, false, nil
	}
	box.CX = (minV[0] + maxV[0]) / 2 * scale
	box.CY = (minV[1] + maxV[1]) / 2 * scale
	box.CZ = (minV[2] + maxV[2]) / 2 * scale
	box.SX = math.Max((maxV[0]-minV[0])*scale, MinBoxExtent)
	box.SY = math.Max((maxV[1]-minV[1])*scale, MinBoxExtent)
	box.SZ = math.Max((maxV[2]-minV[2])*scale, MinBoxExtent)
	return box, true, nil
}
