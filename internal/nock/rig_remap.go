package nock

// rig_remap.go — move a skinned mesh onto a different rig (founder real-time, 2026-09-27: "we
// need the primatives for remapping a mesh onto a new rig"). Blender's equivalent is re-parenting
// a mesh to a new armature with vertex groups renamed, or "With Automatic Weights".
//
// RemapMesh produces a new .gmesh whose bone indices/weights point at the destination rig's
// joints. The geometry (positions/normals/UVs/triangles) is untouched unless Fit is set. The
// three modes:
//
//   - RemapNames: keep the mesh's existing skin weights and re-point them through a BoneMap.
//     An influence on a source joint with no mapping moves to its nearest mapped ANCESTOR (a
//     twist or end bone the new rig lacks folds into its parent). Weights that land on the same
//     destination joint are summed, then the strongest four are kept and renormalised.
//   - RemapProximity: throw the old weights away and compute new ones from how close each vertex
//     is to the destination rig's bones in bind space (envelope-style automatic weights). This is
//     the mode for a mesh that was never skinned, or whose rig has nothing in common with the new
//     one.
//   - RemapAuto (default): RemapNames, with RemapProximity used for any vertex whose influences
//     couldn't be mapped at all.
//
// Known limits, stated so nobody mistakes this for more than it is:
//   - Proximity weights are distance-based envelopes, not Blender's heat-diffusion "automatic
//     weights". Joints close together (inner thighs, armpits) can bleed into each other. A weight
//     painting tool is the eventual fix; until then the report says how many vertices it touched.
//   - RemapNames assumes the new rig's bind pose lines up with the mesh (same T/A-pose, same
//     scale). RemapReport.BindMismatch measures how far apart the mapped joints are in bind space
//     so a caller can warn when that isn't true. Nothing here re-poses a mesh.
//   - Fit is uniform scale + translation only (no rotation), matching bounding boxes.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// RemapMode selects how RemapMesh assigns weights.
type RemapMode string

const (
	RemapAuto      RemapMode = "auto"
	RemapNames     RemapMode = "names"
	RemapProximity RemapMode = "proximity"
)

// RemapOptions configures RemapMesh.
type RemapOptions struct {
	Mode RemapMode
	// BoneMap overrides for AutoBoneMap (source joint → destination joint, "" to unmap).
	Overrides BoneMap
	// Fit uniformly scales and translates the mesh so its bounding box lines up with the
	// destination rig's joint bounding box (bottoms aligned, centres aligned in X/Z). For a mesh
	// authored at a different scale from the rig it's being bound to.
	Fit bool
}

// RemapReport describes what RemapMesh did.
type RemapReport struct {
	Mode              RemapMode     `json:"mode"`
	BoneMap           BoneMapReport `json:"bone_map"`
	VerticesByName    int           `json:"vertices_by_name"`
	VerticesProximity int           `json:"vertices_proximity"`
	// BindMismatch is the mean bind-space distance between each mapped source joint and its
	// destination joint, as a fraction of the destination rig's height. Near 0 means the rigs
	// share a bind pose; above ~0.05 the name remap will visibly distort the mesh.
	BindMismatch float64    `json:"bind_mismatch"`
	FitScale     float64    `json:"fit_scale,omitempty"`
	FitOffset    [3]float64 `json:"fit_offset,omitempty"`
	Warnings     []string   `json:"warnings,omitempty"`
}

// RemapMesh re-skins mesh (currently bound to src) onto dst. src may be nil in RemapProximity
// mode (an unskinned mesh has no source rig).
func RemapMesh(mesh *Mesh, src, dst *Skeleton, opts RemapOptions) (*Mesh, RemapReport, error) {
	mode := opts.Mode
	if mode == "" {
		mode = RemapAuto
	}
	rep := RemapReport{Mode: mode}
	if mesh == nil || len(mesh.Vertices) == 0 {
		return nil, rep, fmt.Errorf("remap: mesh has no vertices")
	}
	if dst == nil || len(dst.Joints) == 0 {
		return nil, rep, fmt.Errorf("remap: destination rig has no joints")
	}
	if mode != RemapProximity && src == nil {
		return nil, rep, fmt.Errorf("remap: mode %q needs the mesh's current rig (use proximity for an unskinned mesh)", mode)
	}
	switch mode {
	case RemapAuto, RemapNames, RemapProximity:
	default:
		return nil, rep, fmt.Errorf("remap: unknown mode %q (auto, names, proximity)", mode)
	}

	out := &Mesh{Vertices: append([]Vertex(nil), mesh.Vertices...), Indices: append([]uint32(nil), mesh.Indices...)}
	dstBind := dst.BindPositions()
	dstHeight := extentY(dstBind)

	if opts.Fit {
		scale, offset := fitTransform(out.Vertices, dstBind)
		for i := range out.Vertices {
			out.Vertices[i].Position = out.Vertices[i].Position.Scale(scale).Add(offset)
		}
		rep.FitScale, rep.FitOffset = scale, offset
	}

	var srcToDst []int // source joint index → destination joint index, or -1
	if mode != RemapProximity {
		rep.BoneMap = AutoBoneMap(src, dst, opts.Overrides)
		srcToDst = make([]int, len(src.Joints))
		for i, j := range src.Joints {
			srcToDst[i] = -1
			if d := rep.BoneMap.Map[j.Name]; d != "" {
				srcToDst[i] = dst.JointIndex(d)
			}
		}
		// Unmapped joints inherit their nearest mapped ancestor.
		for i, j := range src.Joints {
			if srcToDst[i] >= 0 {
				continue
			}
			for p := j.Parent; p >= 0; p = src.Joints[p].Parent {
				if srcToDst[p] >= 0 {
					srcToDst[i] = srcToDst[p]
					break
				}
			}
		}
		rep.BindMismatch = bindMismatch(src, dst, rep.BoneMap.Map, dstHeight)
		if rep.BindMismatch > 0.05 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("bind poses differ (mean mapped-joint offset %.0f%% of rig height): a name remap will distort the mesh; try mode=proximity with fit", rep.BindMismatch*100))
		}
		if len(rep.BoneMap.Map) == 0 {
			rep.Warnings = append(rep.Warnings, "no joints could be mapped by name; every vertex fell back to proximity weights")
		}
	}

	env := newEnvelope(dst, dstBind)
	for i := range out.Vertices {
		v := &out.Vertices[i]
		if mode != RemapProximity {
			acc := map[int]float64{}
			for k := 0; k < 4; k++ {
				w := float64(v.BoneWeights[k])
				si := int(v.BoneIndices[k])
				if w <= 0 || si >= len(srcToDst) || srcToDst[si] < 0 {
					continue
				}
				acc[srcToDst[si]] += w
			}
			if len(acc) > 0 {
				v.BoneIndices, v.BoneWeights = topFour(acc)
				rep.VerticesByName++
				continue
			}
			if mode == RemapNames {
				// Nothing mappable and no fallback allowed: bind rigidly to the root.
				v.BoneIndices, v.BoneWeights = [4]uint8{0, 0, 0, 0}, [4]float32{1, 0, 0, 0}
				continue
			}
		}
		v.BoneIndices, v.BoneWeights = topFour(env.weights(v.Position))
		rep.VerticesProximity++
	}
	if mode == RemapNames {
		if dropped := len(out.Vertices) - rep.VerticesByName; dropped > 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d vertices had no mappable influence and were bound rigidly to %q", dropped, dst.Joints[0].Name))
		}
	}
	return out, rep, nil
}

// topFour keeps the four strongest influences and normalises them to sum to 1. Ties break on
// joint index so output is deterministic.
func topFour(acc map[int]float64) ([4]uint8, [4]float32) {
	type iw struct {
		j int
		w float64
	}
	list := make([]iw, 0, len(acc))
	for j, w := range acc {
		if w > 0 {
			list = append(list, iw{j, w})
		}
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].w != list[b].w {
			return list[a].w > list[b].w
		}
		return list[a].j < list[b].j
	})
	if len(list) > 4 {
		list = list[:4]
	}
	total := 0.0
	for _, e := range list {
		total += e.w
	}
	var idx [4]uint8
	var wts [4]float32
	if total <= 0 {
		wts[0] = 1
		return idx, wts
	}
	for k, e := range list {
		idx[k] = uint8(e.j)
		wts[k] = float32(e.w / total)
	}
	return idx, wts
}

// envelope computes distance-based weights against a rig's bones in bind space. Each joint owns
// the segments from itself to each of its children; a leaf owns just its own point. Joints named
// like end sites ("HeadTop_End", "index_04_leaf_l", "*_nub") carry no geometry in practice and are skipped so they
// don't steal fingertip/head weights.
type envelope struct {
	segs  [][][2]Vec3 // per joint: its segments
	scale float64     // rig height, to make the falloff scale-independent
}

func newEnvelope(s *Skeleton, bind []Vec3) *envelope {
	e := &envelope{segs: make([][][2]Vec3, len(s.Joints)), scale: math.Max(extentY(bind), 1e-6)}
	for i, j := range s.Joints {
		if isEndSite(j.Name) {
			continue
		}
		if j.Parent >= 0 && !isEndSite(s.Joints[j.Parent].Name) {
			e.segs[j.Parent] = append(e.segs[j.Parent], [2]Vec3{bind[j.Parent], bind[i]})
		}
	}
	for i, j := range s.Joints {
		if len(e.segs[i]) == 0 && !isEndSite(j.Name) {
			e.segs[i] = append(e.segs[i], [2]Vec3{bind[i], bind[i]})
		}
	}
	return e
}

func (e *envelope) weights(p Vec3) map[int]float64 {
	type jd struct {
		j int
		d float64
	}
	var ds []jd
	for j, segs := range e.segs {
		if len(segs) == 0 {
			continue
		}
		best := math.Inf(1)
		for _, s := range segs {
			if d := distToSegment(p, s[0], s[1]); d < best {
				best = d
			}
		}
		ds = append(ds, jd{j, best / e.scale})
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a].d < ds[b].d })
	out := map[int]float64{}
	if len(ds) == 0 {
		out[0] = 1
		return out
	}
	// Inverse-fourth-power falloff over the nearest four, then drop anything under 2% so a vertex
	// on the forearm doesn't pick up a sliver of the hip.
	const eps = 1e-4
	total := 0.0
	for k := 0; k < len(ds) && k < 4; k++ {
		w := 1 / math.Pow(ds[k].d+eps, 4)
		out[ds[k].j] = w
		total += w
	}
	for j, w := range out {
		if w/total < 0.02 {
			delete(out, j)
		}
	}
	return out
}

func isEndSite(name string) bool {
	for _, w := range boneSplitRe.Split(strings.ToLower(name), -1) {
		switch w {
		case "end", "leaf", "nub", "site":
			return true
		}
	}
	return false
}

func distToSegment(p, a, b Vec3) float64 {
	ab := b.Sub(a)
	l2 := ab.Dot(ab)
	if l2 < 1e-18 {
		return p.Sub(a).Len()
	}
	t := p.Sub(a).Dot(ab) / l2
	t = math.Max(0, math.Min(1, t))
	return p.Sub(a.Add(ab.Scale(t))).Len()
}

func extentY(ps []Vec3) float64 {
	if len(ps) == 0 {
		return 0
	}
	lo, hi := ps[0][1], ps[0][1]
	for _, p := range ps {
		lo, hi = math.Min(lo, p[1]), math.Max(hi, p[1])
	}
	return hi - lo
}

func bounds(ps []Vec3) (Vec3, Vec3) {
	lo, hi := ps[0], ps[0]
	for _, p := range ps {
		for k := 0; k < 3; k++ {
			lo[k], hi[k] = math.Min(lo[k], p[k]), math.Max(hi[k], p[k])
		}
	}
	return lo, hi
}

// fitTransform returns the uniform scale + offset that maps the mesh's bounding box onto the
// rig's joint bounding box: heights matched, bottoms aligned, X/Z centres aligned. The rig's
// joints stop short of the mesh surface (the head joint sits below the crown, the foot joint
// above the sole), so matching joint height to mesh height leaves the mesh ~10% too small; that
// is corrected with the typical humanoid head-joint-to-crown ratio.
func fitTransform(verts []Vertex, bind []Vec3) (float64, Vec3) {
	ps := make([]Vec3, len(verts))
	for i, v := range verts {
		ps[i] = v.Position
	}
	mlo, mhi := bounds(ps)
	rlo, rhi := bounds(bind)
	mh, rh := mhi[1]-mlo[1], rhi[1]-rlo[1]
	if mh < 1e-9 || rh < 1e-9 {
		return 1, Vec3{}
	}
	const jointToSurface = 1.1
	s := rh * jointToSurface / mh
	// Place the scaled mesh: bottom at the rig's lowest joint minus the ankle-to-sole gap
	// (half of the extra 10%), X/Z centred on the rig.
	gap := rh * (jointToSurface - 1) / 2
	off := Vec3{
		(rlo[0]+rhi[0])/2 - s*(mlo[0]+mhi[0])/2,
		rlo[1] - gap - s*mlo[1],
		(rlo[2]+rhi[2])/2 - s*(mlo[2]+mhi[2])/2,
	}
	return s, off
}

func bindMismatch(src, dst *Skeleton, m BoneMap, dstHeight float64) float64 {
	sb, db := src.BindPositions(), dst.BindPositions()
	if dstHeight < 1e-9 {
		return 0
	}
	sum, n := 0.0, 0
	for i, j := range src.Joints {
		if d := m[j.Name]; d != "" {
			if di := dst.JointIndex(d); di >= 0 {
				sum += sb[i].Sub(db[di]).Len()
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n) / dstHeight
}
