package nock

import (
	"math"
	"testing"
)

// ── fixtures ──────────────────────────────────────────────────────────────────────────────

type jspec struct {
	name   string
	parent int
	t      Vec3
	r      Quat
}

// mkSkel builds a Skeleton whose inverse binds are the true inverse of each joint's rest world
// matrix (what a real glTF exporter writes), optionally with an armature frame (rotation+scale)
// applied on top, the way a Blender export carries its armature object's transform.
func mkSkel(specs []jspec, frame Quat, scale float64) *Skeleton {
	s := &Skeleton{}
	for _, sp := range specs {
		r := sp.r
		if r == (Quat{}) {
			r = IdentityQuat
		}
		s.Joints = append(s.Joints, Joint{Name: sp.name, Parent: sp.parent, RestTranslation: sp.t, RestRotation: r})
	}
	rot, pos := s.RestWorld()
	for i := range s.Joints {
		wr := frame.Mul(rot[i])
		wp := frame.Rotate(pos[i]).Scale(scale)
		m := trsMat(wp, wr, scale)
		inv, ok := m.Invert()
		if !ok {
			panic("singular")
		}
		s.Joints[i].InverseBind = inv
	}
	return s
}

func trsMat(t Vec3, q Quat, s float64) Mat4 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	var m Mat4
	m[0], m[1], m[2] = (1-2*(y*y+z*z))*s, 2*(x*y+z*w)*s, 2*(x*z-y*w)*s
	m[4], m[5], m[6] = 2*(x*y-z*w)*s, (1-2*(x*x+z*z))*s, 2*(y*z+x*w)*s
	m[8], m[9], m[10] = 2*(x*z+y*w)*s, 2*(y*z-x*w)*s, (1-2*(x*x+y*y))*s
	m[12], m[13], m[14], m[15] = t[0], t[1], t[2], 1
	return m
}

func axisAngle(ax Vec3, deg float64) Quat {
	h := deg * math.Pi / 360
	l := ax.Len()
	return Quat{ax[0] / l * math.Sin(h), ax[1] / l * math.Sin(h), ax[2] / l * math.Sin(h), math.Cos(h)}
}

// mixamoRig: a small Mixamo-named humanoid slice, T-pose, arm along +X.
func mixamoRig() *Skeleton {
	return mkSkel([]jspec{
		{"mixamorig:Hips", -1, Vec3{0, 1, 0}, Quat{}},
		{"mixamorig:Spine", 0, Vec3{0, 0.2, 0}, Quat{}},
		{"mixamorig:LeftArm", 1, Vec3{0.2, 0.3, 0}, Quat{}},
		{"mixamorig:LeftForeArm", 2, Vec3{0.3, 0, 0}, Quat{}},
		{"mixamorig:LeftForeArmTwist", 3, Vec3{0.1, 0, 0}, Quat{}},
		{"mixamorig:LeftHand", 3, Vec3{0.25, 0, 0}, Quat{}},
		{"mixamorig:LeftUpLeg", 0, Vec3{0.1, -0.05, 0}, Quat{}},
		{"mixamorig:LeftLeg", 6, Vec3{0, -0.45, 0}, Quat{}},
	}, IdentityQuat, 1)
}

// unrealRig: the same body, Unreal names, and a different bone-axis convention (the arm chain's
// local frame is rotated 90° about Z, with child offsets expressed in that rotated frame, so the
// joints land in the same world places).
func unrealRig(size float64) *Skeleton {
	rz := axisAngle(Vec3{0, 0, 1}, 90)
	inv := rz.Conj()
	return mkSkel([]jspec{
		{"pelvis", -1, Vec3{0, 1, 0}.Scale(size), Quat{}},
		{"spine_01", 0, Vec3{0, 0.2, 0}.Scale(size), Quat{}},
		{"upperarm_l", 1, Vec3{0.2, 0.3, 0}.Scale(size), rz},
		{"lowerarm_l", 2, inv.Rotate(Vec3{0.3, 0, 0}).Scale(size), Quat{}},
		{"hand_l", 3, inv.Rotate(Vec3{0.25, 0, 0}).Scale(size), Quat{}},
		{"thigh_l", 0, Vec3{0.1, -0.05, 0}.Scale(size), Quat{}},
		{"calf_l", 5, Vec3{0, -0.45, 0}.Scale(size), Quat{}},
	}, IdentityQuat, 1)
}

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func nearV(a, b Vec3, eps float64) bool {
	return near(a[0], b[0], eps) && near(a[1], b[1], eps) && near(a[2], b[2], eps)
}

// posedWorld runs FK for a skeleton posed by clip at tick.
func posedWorld(s *Skeleton, c *Clip, tick int) []Vec3 {
	rot := make([]Quat, len(s.Joints))
	pos := make([]Vec3, len(s.Joints))
	for i, j := range s.Joints {
		lt, lr := c.JointPoseAt(tick, j.Name, j.RestTranslation, j.RestRotation)
		if j.Parent < 0 {
			rot[i], pos[i] = lr, lt
			continue
		}
		rot[i] = rot[j.Parent].Mul(lr)
		pos[i] = pos[j.Parent].Add(rot[j.Parent].Rotate(lt))
	}
	return pos
}

// ── codecs ────────────────────────────────────────────────────────────────────────────────

func TestSkeletonMeshClipRoundTrip(t *testing.T) {
	s := mixamoRig()
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := DecodeGSkel(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Joints) != len(s.Joints) || s2.Joints[3].Name != "mixamorig:LeftForeArm" || s2.Joints[3].Parent != 2 {
		t.Fatalf("gskel round trip lost joints: %+v", s2.Joints[3])
	}
	if s2.TopologyHashHex() != s.TopologyHashHex() {
		t.Fatal("topology hash changed across round trip")
	}

	m := &Mesh{Vertices: []Vertex{{Position: Vec3{1, 2, 3}, Normal: Vec3{0, 1, 0}, UV: [2]float32{0.5, 0.25}, BoneIndices: [4]uint8{3, 5}, BoneWeights: [4]float32{0.75, 0.25}}}, Indices: []uint32{0, 0, 0}}
	mb, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	m2, err := DecodeGMesh(mb)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Vertices[0].BoneIndices != m.Vertices[0].BoneIndices || m2.Vertices[0].UV != m.Vertices[0].UV || m2.Vertices[0].Position != m.Vertices[0].Position {
		t.Fatalf("gmesh round trip mismatch: %+v", m2.Vertices[0])
	}

	c := &Clip{TickRate: 30, DurationTicks: 2, Channels: []string{"a.tx", "a.ty"}, Data: []float32{1, 2, 3, 4}}
	gb, manifest, _, err := c.Encode(s.TopologyHashHex(), "human", "test")
	if err != nil {
		t.Fatal(err)
	}
	if manifest == "" {
		t.Fatal("empty manifest")
	}
	c2, err := DecodeGBand(gb, c.Channels)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Data[3] != 4 || c2.TickRate != 30 {
		t.Fatalf("gband round trip mismatch: %+v", c2)
	}
	if _, err := DecodeGBand(gb, []string{"only-one"}); err == nil {
		t.Fatal("expected channel-count mismatch error")
	}
}

func TestBindPositionsHonourArmatureFrame(t *testing.T) {
	frame := axisAngle(Vec3{1, 0, 0}, -90)
	s := mkSkel([]jspec{{"root", -1, Vec3{0, 0, 1}, Quat{}}, {"child", 0, Vec3{0, 0, 1}, Quat{}}}, frame, 0.01)
	bp := s.BindPositions()
	// Skeleton-space (0,0,2) under -90° about X is (0,2,0), then scaled 0.01.
	if !nearV(bp[1], Vec3{0, 0.02, 0}, 1e-6) {
		t.Fatalf("bind position = %v, want (0,0.02,0)", bp[1])
	}
	f, sc := s.bindFrame()
	if !near(sc, 0.01, 1e-6) || !nearV(f.Rotate(Vec3{0, 0, 1}), Vec3{0, 1, 0}, 1e-6) {
		t.Fatalf("bindFrame = %v, %v", f, sc)
	}
}

// ── bone map ──────────────────────────────────────────────────────────────────────────────

func TestCanonicalBone(t *testing.T) {
	cases := []struct{ in, side, base, num string }{
		{"mixamorig:LeftArm", "l", "upperarm", ""},
		{"upperarm_l", "l", "upperarm", ""},
		{"arm.L", "l", "upperarm", ""},
		{"DEF-upper_arm.R", "r", "upperarm", ""},
		{"Bip01 R UpperArm", "r", "upperarm", ""},
		{"mixamorig:LeftForeArm", "l", "lowerarm", ""},
		{"mixamorig:LeftUpLeg", "l", "upperleg", ""},
		{"thigh_l", "l", "upperleg", ""},
		{"mixamorig:LeftLeg", "l", "lowerleg", ""},
		{"calf_r", "r", "lowerleg", ""},
		{"mixamorig:LeftHandThumb1", "l", "thumb", "1"},
		{"thumb_01_l", "l", "thumb", "1"},
		{"spine_03", "", "spine", "3"},
		{"mixamorig:Spine2", "", "spine", "2"},
		{"pelvis", "", "hips", ""},
		{"mixamorig:LeftToeBase", "l", "toe", ""},
		{"root", "", "", ""},
	}
	for _, c := range cases {
		side, base, num := canonicalBone(c.in)
		if side != c.side || base != c.base || num != c.num {
			t.Errorf("canonicalBone(%q) = (%q,%q,%q), want (%q,%q,%q)", c.in, side, base, num, c.side, c.base, c.num)
		}
	}
}

func TestAutoBoneMapMixamoToUnreal(t *testing.T) {
	rep := AutoBoneMap(mixamoRig(), unrealRig(1), nil)
	want := map[string]string{
		"mixamorig:Hips":        "pelvis",
		"mixamorig:Spine":       "spine_01",
		"mixamorig:LeftArm":     "upperarm_l",
		"mixamorig:LeftForeArm": "lowerarm_l",
		"mixamorig:LeftHand":    "hand_l",
		"mixamorig:LeftUpLeg":   "thigh_l",
		"mixamorig:LeftLeg":     "calf_l",
	}
	for s, d := range want {
		if rep.Map[s] != d {
			t.Errorf("map[%q] = %q, want %q (how=%s)", s, rep.Map[s], d, rep.How[s])
		}
	}
	if len(rep.UnmappedSource) != 1 || rep.UnmappedSource[0] != "mixamorig:LeftForeArmTwist" {
		t.Errorf("unmapped source = %v, want just the twist bone", rep.UnmappedSource)
	}

	// Overrides win, and "" unmaps.
	rep = AutoBoneMap(mixamoRig(), unrealRig(1), BoneMap{"mixamorig:LeftForeArmTwist": "lowerarm_l", "mixamorig:LeftLeg": ""})
	if rep.Map["mixamorig:LeftForeArmTwist"] != "lowerarm_l" || rep.How["mixamorig:LeftForeArmTwist"] != "override" {
		t.Errorf("override not applied: %v", rep.Map)
	}
	if _, ok := rep.Map["mixamorig:LeftLeg"]; ok {
		t.Errorf("explicit unmap not applied")
	}
}

// Chains match by position, not by the number in the name: Mixamo counts its spine from an
// unnumbered "Spine", Unreal from "spine_01". Also "root" matches "mixamorig:Root" by normalised
// name. (Both found on a real 65-joint Unreal mannequin vs. a Mixamo-named copy.)
func TestAutoBoneMapChainsAndNormalisedNames(t *testing.T) {
	src := mkSkel([]jspec{
		{"root", -1, Vec3{}, Quat{}}, {"pelvis", 0, Vec3{0, 1, 0}, Quat{}},
		{"spine_01", 1, Vec3{0, 0.1, 0}, Quat{}}, {"spine_02", 2, Vec3{0, 0.1, 0}, Quat{}}, {"spine_03", 3, Vec3{0, 0.1, 0}, Quat{}},
	}, IdentityQuat, 1)
	dst := mkSkel([]jspec{
		{"mixamorig:Root", -1, Vec3{}, Quat{}}, {"mixamorig:Hips", 0, Vec3{0, 1, 0}, Quat{}},
		{"mixamorig:Spine", 1, Vec3{0, 0.1, 0}, Quat{}}, {"mixamorig:Spine1", 2, Vec3{0, 0.1, 0}, Quat{}}, {"mixamorig:Spine2", 3, Vec3{0, 0.1, 0}, Quat{}},
	}, IdentityQuat, 1)
	rep := AutoBoneMap(src, dst, nil)
	want := map[string]string{"root": "mixamorig:Root", "pelvis": "mixamorig:Hips", "spine_01": "mixamorig:Spine", "spine_02": "mixamorig:Spine1", "spine_03": "mixamorig:Spine2"}
	for s, d := range want {
		if rep.Map[s] != d {
			t.Errorf("map[%q] = %q, want %q (how=%s)", s, rep.Map[s], d, rep.How[s])
		}
	}
	if len(rep.UnmappedSource) != 0 || len(rep.UnmappedDest) != 0 {
		t.Errorf("unmapped: %v / %v", rep.UnmappedSource, rep.UnmappedDest)
	}
}

// ── mesh remap ────────────────────────────────────────────────────────────────────────────

func TestRemapMeshByNamesFoldsUnmappedIntoAncestor(t *testing.T) {
	src, dst := mixamoRig(), unrealRig(1)
	m := &Mesh{Vertices: []Vertex{
		// 60% twist (unmapped → its parent LeftForeArm), 40% hand.
		{Position: Vec3{0.55, 1.5, 0}, BoneIndices: [4]uint8{4, 5}, BoneWeights: [4]float32{0.6, 0.4}},
		// Twist + forearm: both land on lowerarm_l and must merge to one full-weight influence.
		{Position: Vec3{0.5, 1.5, 0}, BoneIndices: [4]uint8{4, 3}, BoneWeights: [4]float32{0.5, 0.5}},
	}, Indices: []uint32{0, 1, 0}}
	out, rep, err := RemapMesh(m, src, dst, RemapOptions{Mode: RemapNames})
	if err != nil {
		t.Fatal(err)
	}
	lower, hand := uint8(dst.JointIndex("lowerarm_l")), uint8(dst.JointIndex("hand_l"))
	v0 := out.Vertices[0]
	if v0.BoneIndices[0] != lower || v0.BoneIndices[1] != hand || !near(float64(v0.BoneWeights[0]), 0.6, 1e-6) || !near(float64(v0.BoneWeights[1]), 0.4, 1e-6) {
		t.Fatalf("v0 = %v %v, want lowerarm 0.6 + hand 0.4", v0.BoneIndices, v0.BoneWeights)
	}
	v1 := out.Vertices[1]
	if v1.BoneIndices[0] != lower || v1.BoneWeights[0] != 1 || v1.BoneWeights[1] != 0 {
		t.Fatalf("v1 = %v %v, want lowerarm 1.0", v1.BoneIndices, v1.BoneWeights)
	}
	if rep.VerticesByName != 2 || rep.VerticesProximity != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.BindMismatch > 1e-6 {
		t.Fatalf("same-body rigs should have ~0 bind mismatch, got %v", rep.BindMismatch)
	}
	if out.Vertices[0].Position != m.Vertices[0].Position {
		t.Fatal("geometry must be untouched without Fit")
	}
}

func TestRemapMeshProximityAndAutoFallback(t *testing.T) {
	dst := unrealRig(1)
	bp := dst.BindPositions()
	// A vertex just off the middle of the forearm segment (lowerarm_l → hand_l), and one on the
	// thigh segment.
	fore := bp[dst.JointIndex("lowerarm_l")].Add(bp[dst.JointIndex("hand_l")]).Scale(0.5).Add(Vec3{0, 0.02, 0})
	thigh := bp[dst.JointIndex("thigh_l")].Add(bp[dst.JointIndex("calf_l")]).Scale(0.5).Add(Vec3{0.03, 0, 0})
	m := &Mesh{Vertices: []Vertex{{Position: fore}, {Position: thigh}}, Indices: []uint32{0, 1, 0}}
	out, rep, err := RemapMesh(m, nil, dst, RemapOptions{Mode: RemapProximity})
	if err != nil {
		t.Fatal(err)
	}
	if got := dst.Joints[out.Vertices[0].BoneIndices[0]].Name; got != "lowerarm_l" || out.Vertices[0].BoneWeights[0] < 0.9 {
		t.Fatalf("forearm vertex dominant joint = %s (%v)", got, out.Vertices[0].BoneWeights)
	}
	if got := dst.Joints[out.Vertices[1].BoneIndices[0]].Name; got != "thigh_l" || out.Vertices[1].BoneWeights[0] < 0.9 {
		t.Fatalf("thigh vertex dominant joint = %s (%v)", got, out.Vertices[1].BoneWeights)
	}
	for _, v := range out.Vertices {
		sum := 0.0
		for _, w := range v.BoneWeights {
			sum += float64(w)
		}
		if !near(sum, 1, 1e-5) {
			t.Fatalf("weights don't sum to 1: %v", v.BoneWeights)
		}
	}
	if rep.VerticesProximity != 2 {
		t.Fatalf("report = %+v", rep)
	}

	// Auto: a vertex whose only influence is on a joint with no mapping and no mapped ancestor
	// falls back to proximity instead of being dropped.
	src := mkSkel([]jspec{{"tail", -1, Vec3{}, Quat{}}}, IdentityQuat, 1)
	m2 := &Mesh{Vertices: []Vertex{{Position: fore, BoneIndices: [4]uint8{0}, BoneWeights: [4]float32{1}}}, Indices: []uint32{0, 0, 0}}
	out2, rep2, err := RemapMesh(m2, src, dst, RemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.VerticesProximity != 1 || dst.Joints[out2.Vertices[0].BoneIndices[0]].Name != "lowerarm_l" {
		t.Fatalf("auto fallback: %+v, joint %d", rep2, out2.Vertices[0].BoneIndices[0])
	}
	if len(rep2.Warnings) == 0 {
		t.Fatal("expected a no-joints-mapped warning")
	}
}

func TestRemapMeshFit(t *testing.T) {
	dst := unrealRig(1)
	// A "character" authored in centimetres, standing at the origin: 100x too big.
	m := &Mesh{Vertices: []Vertex{{Position: Vec3{-40, 0, -10}}, {Position: Vec3{40, 200, 10}}}, Indices: []uint32{0, 1, 0}}
	out, rep, err := RemapMesh(m, nil, dst, RemapOptions{Mode: RemapProximity, Fit: true})
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := bounds([]Vec3{out.Vertices[0].Position, out.Vertices[1].Position})
	rlo, rhi := bounds(dst.BindPositions())
	rh := rhi[1] - rlo[1]
	if !near(hi[1]-lo[1], rh*1.1, 1e-6) {
		t.Fatalf("fitted height %v, want %v", hi[1]-lo[1], rh*1.1)
	}
	if !near((lo[0]+hi[0])/2, (rlo[0]+rhi[0])/2, 1e-6) {
		t.Fatalf("fitted mesh not centred on the rig")
	}
	if rep.FitScale >= 0.01 {
		t.Fatalf("fit scale %v should shrink a centimetre mesh", rep.FitScale)
	}
}

// ── clip retarget ─────────────────────────────────────────────────────────────────────────

// armClip: bend the Mixamo left arm 90° at the shoulder (about +Z, arm raises) and 45° at the
// elbow, and move the hips up 0.1 over 3 ticks.
func armClip() *Clip {
	q0 := IdentityQuat
	q1 := axisAngle(Vec3{0, 0, 1}, 45)
	q2 := axisAngle(Vec3{0, 0, 1}, 90)
	e := axisAngle(Vec3{0, 0, 1}, 45)
	ch := []string{
		"mixamorig:Hips.tx", "mixamorig:Hips.ty", "mixamorig:Hips.tz",
		"mixamorig:LeftArm.qx", "mixamorig:LeftArm.qy", "mixamorig:LeftArm.qz", "mixamorig:LeftArm.qw",
		"mixamorig:LeftForeArm.qx", "mixamorig:LeftForeArm.qy", "mixamorig:LeftForeArm.qz", "mixamorig:LeftForeArm.qw",
	}
	row := func(hy float64, sh, el Quat) []float32 {
		return []float32{0, float32(hy), 0, float32(sh[0]), float32(sh[1]), float32(sh[2]), float32(sh[3]), float32(el[0]), float32(el[1]), float32(el[2]), float32(el[3])}
	}
	var data []float32
	data = append(data, row(1.0, q0, IdentityQuat)...)
	data = append(data, row(1.05, q1, e)...)
	data = append(data, row(1.1, q2, e)...)
	return &Clip{TickRate: 30, DurationTicks: 3, Channels: ch, Data: data}
}

func TestRetargetClipAcrossAxisConventions(t *testing.T) {
	src, dst := mixamoRig(), unrealRig(1)
	clip := armClip()
	out, rep, err := RetargetClip(clip, src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !near(rep.HeightRatio, 1, 1e-9) {
		t.Fatalf("height ratio %v", rep.HeightRatio)
	}
	// Different local axes on the arm chain, same body: the retargeted hand must land exactly
	// where the source hand does at every tick.
	sh, dh := src.JointIndex("mixamorig:LeftHand"), dst.JointIndex("hand_l")
	for tick := 0; tick < 3; tick++ {
		sp := posedWorld(src, clip, tick)[sh]
		dp := posedWorld(dst, out, tick)[dh]
		if !nearV(sp, dp, 1e-4) {
			t.Fatalf("tick %d: src hand %v, retargeted hand %v", tick, sp, dp)
		}
	}
	// A naive local-quaternion copy would NOT match (the reason world-space retargeting exists):
	naive := &Clip{TickRate: 30, DurationTicks: 3, Channels: []string{"upperarm_l.qx", "upperarm_l.qy", "upperarm_l.qz", "upperarm_l.qw"}}
	for tick := 0; tick < 3; tick++ {
		_, r := clip.JointPoseAt(tick, "mixamorig:LeftArm", Vec3{}, IdentityQuat)
		naive.Data = append(naive.Data, float32(r[0]), float32(r[1]), float32(r[2]), float32(r[3]))
	}
	if nearV(posedWorld(dst, naive, 2)[dh], posedWorld(src, clip, 2)[sh], 1e-3) {
		t.Fatal("fixture is too weak: a naive copy already matches")
	}
}

func TestRetargetClipScalesRootMotion(t *testing.T) {
	src, dst := mixamoRig(), unrealRig(2) // destination is twice the size
	out, rep, err := RetargetClip(armClip(), src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !near(rep.HeightRatio, 2, 1e-9) {
		t.Fatalf("height ratio %v, want 2", rep.HeightRatio)
	}
	ty := -1
	for i, c := range out.Channels {
		if c == "pelvis.ty" {
			ty = i
		}
	}
	if ty < 0 {
		t.Fatalf("no pelvis translation channel in %v", out.Channels)
	}
	// Source hips rise 0.1 from rest 1.0; destination rest is 2.0, so it should reach 2.2.
	if got := out.Data[2*len(out.Channels)+ty]; !near(float64(got), 2.2, 1e-5) {
		t.Fatalf("pelvis.ty at tick 2 = %v, want 2.2", got)
	}
}

// ── keyframes ─────────────────────────────────────────────────────────────────────────────

func TestKeyframeBakeInterpolation(t *testing.T) {
	s := mixamoRig()
	q90 := axisAngle(Vec3{0, 0, 1}, 90)
	doc := &KeyframeDoc{TickRate: 10, DurationTicks: 11, Tracks: []KeyTrack{
		{Joint: "mixamorig:LeftArm", Rotation: []RotKey{
			{Tick: 10, Q: [4]float64(q90)}, // deliberately out of order; Validate sorts
			{Tick: 0, Q: [4]float64(IdentityQuat)},
		}},
		{Joint: "mixamorig:Hips", Translation: []PosKey{
			{Tick: 2, T: [3]float64{0, 1, 0}, Interp: "step"},
			{Tick: 6, T: [3]float64{0, 2, 0}, Interp: "smooth"},
			{Tick: 10, T: [3]float64{0, 3, 0}},
		}},
	}}
	c, err := doc.Bake(s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Channels[0] != "mixamorig:Hips.tx" || c.Channels[3] != "mixamorig:LeftArm.qx" {
		t.Fatalf("channel order follows the rig: %v", c.Channels)
	}
	_, r5 := c.JointPoseAt(5, "mixamorig:LeftArm", Vec3{}, IdentityQuat)
	want := axisAngle(Vec3{0, 0, 1}, 45)
	if !near(math.Abs(r5[0]*want[0]+r5[1]*want[1]+r5[2]*want[2]+r5[3]*want[3]), 1, 1e-6) {
		t.Fatalf("slerp midpoint = %v, want 45° about Z", r5)
	}
	hy := func(tick int) float64 {
		t0, _ := c.JointPoseAt(tick, "mixamorig:Hips", Vec3{}, IdentityQuat)
		return t0[1]
	}
	if hy(0) != 1 || hy(2) != 1 || hy(5) != 1 { // hold before first key; step holds to the next
		t.Fatalf("step/hold: %v %v %v", hy(0), hy(2), hy(5))
	}
	if !near(hy(8), 2.5, 1e-6) { // smoothstep midpoint is still exactly half
		t.Fatalf("smooth midpoint = %v", hy(8))
	}
	if !(hy(7) < 2.25) { // but eased: slower than linear near the key
		t.Fatalf("smooth should ease in, got %v at tick 7", hy(7))
	}
	if hy(10) != 3 {
		t.Fatalf("last key = %v", hy(10))
	}
}

func TestKeyframeValidation(t *testing.T) {
	s := mixamoRig()
	bad := []*KeyframeDoc{
		{TickRate: 0, DurationTicks: 10},
		{TickRate: 30, DurationTicks: 0},
		{TickRate: 30, DurationTicks: 10, Tracks: []KeyTrack{{Joint: "nope", Rotation: []RotKey{{Tick: 0, Q: [4]float64{0, 0, 0, 1}}}}}},
		{TickRate: 30, DurationTicks: 10, Tracks: []KeyTrack{{Joint: "mixamorig:Hips", Rotation: []RotKey{{Tick: 10, Q: [4]float64{0, 0, 0, 1}}}}}},
		{TickRate: 30, DurationTicks: 10, Tracks: []KeyTrack{{Joint: "mixamorig:Hips", Rotation: []RotKey{{Tick: 1, Q: [4]float64{0, 0, 0, 1}, Interp: "bounce"}}}}},
		{TickRate: 30, DurationTicks: 10, Tracks: []KeyTrack{{Joint: "mixamorig:Hips"}, {Joint: "mixamorig:Hips"}}},
	}
	for i, d := range bad {
		if err := d.Validate(s); err == nil {
			t.Errorf("case %d: expected a validation error", i)
		}
	}
	empty := &KeyframeDoc{TickRate: 30, DurationTicks: 10}
	if _, err := empty.Bake(s); err == nil {
		t.Error("baking a doc with no keys should fail")
	}
}

func TestKeyframesFromClipRoundTrip(t *testing.T) {
	s := mixamoRig()
	clip := armClip()
	doc, err := KeyframesFromClip(clip, s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tracks) != 3 {
		t.Fatalf("want tracks for Hips, LeftArm, LeftForeArm; got %d", len(doc.Tracks))
	}
	back, err := doc.Bake(s)
	if err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 3; tick++ {
		a := posedWorld(s, clip, tick)[s.JointIndex("mixamorig:LeftHand")]
		b := posedWorld(s, back, tick)[s.JointIndex("mixamorig:LeftHand")]
		if !nearV(a, b, 1e-5) {
			t.Fatalf("tick %d: %v vs %v", tick, a, b)
		}
	}
	// every=2 on a 3-tick clip keeps ticks 0 and 2 (the last tick is always kept).
	doc2, _ := KeyframesFromClip(clip, s, 2)
	if n := len(doc2.Tracks[0].Translation); n != 2 {
		t.Fatalf("every=2 kept %d keys, want 2", n)
	}
}

// A Blender-style export: the skeleton is Z-up in centimetres and the armature object carries a
// -90° X rotation and 0.01 scale into the Y-up, metre mesh space. Retargeting it onto a plain
// Y-up metre rig must still put the hand in the same mesh-space place.
func TestRetargetClipAcrossArmatureFrames(t *testing.T) {
	toZUp := axisAngle(Vec3{1, 0, 0}, 90) // inverse of the armature frame
	frame := toZUp.Conj()
	var specs []jspec
	for _, j := range mixamoRig().Joints {
		specs = append(specs, jspec{j.Name, j.Parent, toZUp.Rotate(j.RestTranslation).Scale(100), Quat{}})
	}
	src := mkSkel(specs, frame, 0.01)
	dst := unrealRig(1)

	// Raise the shoulder 60° about mesh-space +Z, expressed in the Z-up skeleton's own axes.
	sh := axisAngle(toZUp.Rotate(Vec3{0, 0, 1}), 60)
	clip := &Clip{TickRate: 30, DurationTicks: 1,
		Channels: []string{"mixamorig:LeftArm.qx", "mixamorig:LeftArm.qy", "mixamorig:LeftArm.qz", "mixamorig:LeftArm.qw"},
		Data:     []float32{float32(sh[0]), float32(sh[1]), float32(sh[2]), float32(sh[3])}}
	out, _, err := RetargetClip(clip, src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	sp := frame.Rotate(posedWorld(src, clip, 0)[src.JointIndex("mixamorig:LeftHand")]).Scale(0.01)
	dp := posedWorld(dst, out, 0)[dst.JointIndex("hand_l")]
	if !nearV(sp, dp, 1e-4) {
		t.Fatalf("mesh-space hand: src %v, retargeted %v", sp, dp)
	}
}
