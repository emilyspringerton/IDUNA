package nock

// rig.go — NOCK's Go-side read/write model for GOLDENBAND's three asset formats (.gskel rig,
// .gmesh skinned mesh, .gband clip), plus the small quaternion/matrix kit the rig tools need.
// Founder real-time, 2026-09-27: "continue to evolve NOCK tools into a total blender replacement
// we need a way to animate in NOCK also we need the primatives for remapping a mesh onto a new
// rig".
//
// gltf_convert.go already had the ENCODERS (encodeGSkel/encodeGMesh/encodeGBand). This file adds
// the matching DECODERS and exported value types, so everything built on them (rig_bonemap.go,
// rig_remap.go, rig_retarget.go, keyframes.go) works on real decoded data, never raw offsets. The
// byte layouts are the same ones GOLDENBAND/format/*_FORMAT.md specifies and
// frontend/nock/src/goldenband.ts parses.
//
// Math conventions match GOLDENBAND/src/gpose.c: quaternions are (x,y,z,w), matrices are
// column-major float[16], and a joint's local transform is T(translation)*R(rotation), with no
// scale.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

// ── Value types ───────────────────────────────────────────────────────────────────────────

// Joint is one decoded .gskel joint.
type Joint struct {
	Name            string
	Parent          int // -1 for a root; always < this joint's own index (GSKEL_FORMAT.md)
	RestTranslation Vec3
	RestRotation    Quat
	InverseBind     Mat4
}

// Skeleton is a decoded .gskel rig.
type Skeleton struct {
	Joints []Joint
}

// Vertex is one decoded .gmesh vertex. BoneIndices index into the paired Skeleton's Joints.
type Vertex struct {
	Position    Vec3
	Normal      Vec3
	UV          [2]float32
	BoneIndices [4]uint8
	BoneWeights [4]float32
}

// Mesh is a decoded .gmesh.
type Mesh struct {
	Vertices []Vertex
	Indices  []uint32
}

// Clip is a decoded .gband clip plus the channel names from its manifest. The .gband binary
// alone has no channel names, so a Clip is only meaningful with its manifest.
type Clip struct {
	TickRate      int
	DurationTicks int
	Channels      []string
	Data          []float32 // DurationTicks*len(Channels), row-major by tick
}

// ── Decoders ──────────────────────────────────────────────────────────────────────────────

// DecodeGSkel parses .gskel bytes (12-byte header, then 128-byte joint records).
func DecodeGSkel(b []byte) (*Skeleton, error) {
	if len(b) < 12 || string(b[0:4]) != "GSKL" {
		return nil, fmt.Errorf("not a .gskel file (bad magic)")
	}
	n := int(binary.LittleEndian.Uint32(b[8:12]))
	if n <= 0 || n > gskelMaxJoints {
		return nil, fmt.Errorf(".gskel joint count %d out of range (1..%d)", n, gskelMaxJoints)
	}
	if len(b) < 12+n*128 {
		return nil, fmt.Errorf(".gskel truncated: %d joints need %d bytes, have %d", n, 12+n*128, len(b))
	}
	s := &Skeleton{Joints: make([]Joint, n)}
	for i := 0; i < n; i++ {
		rec := b[12+i*128 : 12+(i+1)*128]
		name := rec[0:32]
		end := 0
		for end < 32 && name[end] != 0 {
			end++
		}
		j := Joint{Name: string(name[:end]), Parent: int(int32(binary.LittleEndian.Uint32(rec[32:36])))}
		for k := 0; k < 3; k++ {
			j.RestTranslation[k] = f32at(rec, 36+k*4)
		}
		for k := 0; k < 4; k++ {
			j.RestRotation[k] = f32at(rec, 48+k*4)
		}
		for k := 0; k < 16; k++ {
			j.InverseBind[k] = f32at(rec, 64+k*4)
		}
		if j.Parent >= i {
			return nil, fmt.Errorf(".gskel joint %d (%q) has parent %d, must be < %d", i, j.Name, j.Parent, i)
		}
		s.Joints[i] = j
	}
	return s, nil
}

// DecodeGMesh parses .gmesh bytes (16-byte header, 52-byte vertex records, then u32 indices).
func DecodeGMesh(b []byte) (*Mesh, error) {
	if len(b) < 16 || string(b[0:4]) != "GMSH" {
		return nil, fmt.Errorf("not a .gmesh file (bad magic)")
	}
	nv := int(binary.LittleEndian.Uint32(b[8:12]))
	ni := int(binary.LittleEndian.Uint32(b[12:16]))
	if len(b) < 16+nv*52+ni*4 {
		return nil, fmt.Errorf(".gmesh truncated")
	}
	m := &Mesh{Vertices: make([]Vertex, nv), Indices: make([]uint32, ni)}
	for i := 0; i < nv; i++ {
		rec := b[16+i*52 : 16+(i+1)*52]
		var v Vertex
		for k := 0; k < 3; k++ {
			v.Position[k] = f32at(rec, k*4)
			v.Normal[k] = f32at(rec, 12+k*4)
		}
		v.UV[0], v.UV[1] = float32(f32at(rec, 24)), float32(f32at(rec, 28))
		copy(v.BoneIndices[:], rec[32:36])
		for k := 0; k < 4; k++ {
			v.BoneWeights[k] = float32(f32at(rec, 36+k*4))
		}
		m.Vertices[i] = v
	}
	off := 16 + nv*52
	for i := 0; i < ni; i++ {
		m.Indices[i] = binary.LittleEndian.Uint32(b[off+i*4:])
	}
	return m, nil
}

// DecodeGBand parses .gband bytes (84-byte header, then float32 data) and attaches the channel
// names, which must come from the clip's manifest.
func DecodeGBand(b []byte, channels []string) (*Clip, error) {
	if len(b) < 84 || string(b[0:4]) != "GBND" {
		return nil, fmt.Errorf("not a .gband file (bad magic)")
	}
	tr := int(binary.LittleEndian.Uint32(b[8:12]))
	dt := int(binary.LittleEndian.Uint32(b[12:16]))
	nc := int(binary.LittleEndian.Uint32(b[16:20]))
	if nc != len(channels) {
		return nil, fmt.Errorf(".gband has %d channels but the manifest names %d", nc, len(channels))
	}
	if len(b) < 84+dt*nc*4 {
		return nil, fmt.Errorf(".gband truncated")
	}
	c := &Clip{TickRate: tr, DurationTicks: dt, Channels: append([]string(nil), channels...), Data: make([]float32, dt*nc)}
	for i := range c.Data {
		c.Data[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[84+i*4:]))
	}
	return c, nil
}

func f32at(b []byte, off int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b[off : off+4])))
}

// ── Encoders (thin exported wrappers over gltf_convert.go's existing ones) ──────────────────

// Encode writes the skeleton back to .gskel bytes.
func (s *Skeleton) Encode() ([]byte, error) {
	out := make([]gskelJoint, len(s.Joints))
	for i, j := range s.Joints {
		gj := gskelJoint{name: j.Name, parentIndex: int32(j.Parent)}
		for k := 0; k < 3; k++ {
			gj.restTranslation[k] = float32(j.RestTranslation[k])
		}
		for k := 0; k < 4; k++ {
			gj.restRotation[k] = float32(j.RestRotation[k])
		}
		for k := 0; k < 16; k++ {
			gj.inverseBind[k] = float32(j.InverseBind[k])
		}
		out[i] = gj
	}
	return encodeGSkel(out)
}

// TopologyHashHex is the same compatibility hash glTF import stores as skeleton_hash (joint
// names and parents, in order).
func (s *Skeleton) TopologyHashHex() string {
	js := make([]gskelJoint, len(s.Joints))
	for i, j := range s.Joints {
		js[i] = gskelJoint{name: j.Name, parentIndex: int32(j.Parent)}
	}
	h := topologyHash(js)
	return hex.EncodeToString(h[:])
}

// JointIndex returns the index of the named joint, or -1.
func (s *Skeleton) JointIndex(name string) int {
	for i, j := range s.Joints {
		if j.Name == name {
			return i
		}
	}
	return -1
}

// Encode writes the mesh back to .gmesh bytes.
func (m *Mesh) Encode() ([]byte, error) {
	out := make([]gmeshVertex, len(m.Vertices))
	for i, v := range m.Vertices {
		gv := gmeshVertex{uv: v.UV, boneIndices: v.BoneIndices, boneWeights: v.BoneWeights}
		for k := 0; k < 3; k++ {
			gv.position[k] = float32(v.Position[k])
			gv.normal[k] = float32(v.Normal[k])
		}
		out[i] = gv
	}
	return encodeGMesh(out, m.Indices)
}

// Encode writes the clip to .gband bytes plus a GBAND_FORMAT.md manifest. skeletonHashHex may be
// empty (encoded as zeros, the format's own "unresolved" sentinel).
func (c *Clip) Encode(skeletonHashHex, authorshipKind, authorshipWho string) (gband []byte, manifestJSON, contentHashHex string, err error) {
	if c.TickRate <= 0 || c.DurationTicks <= 0 {
		return nil, "", "", fmt.Errorf("clip needs tick_rate > 0 and duration_ticks > 0")
	}
	if len(c.Channels) == 0 {
		return nil, "", "", fmt.Errorf("clip has no channels")
	}
	if len(c.Data) != c.DurationTicks*len(c.Channels) {
		return nil, "", "", fmt.Errorf("clip data has %d floats, want %d", len(c.Data), c.DurationTicks*len(c.Channels))
	}
	var skel [32]byte
	if skeletonHashHex != "" {
		raw, derr := hex.DecodeString(skeletonHashHex)
		if derr != nil || len(raw) != 32 {
			return nil, "", "", fmt.Errorf("skeleton hash must be 64 hex chars")
		}
		copy(skel[:], raw)
	}
	gband, content := encodeGBand(uint32(c.TickRate), uint32(c.DurationTicks), uint32(len(c.Channels)), skel, c.Data)
	contentHashHex = hex.EncodeToString(content[:])
	manifestJSON, err = buildGBandManifest(skeletonHashHex, contentHashHex, c.TickRate, c.DurationTicks, c.Channels, authorshipKind, authorshipWho)
	return gband, manifestJSON, contentHashHex, err
}

// channelIndex returns the index of the named channel, or -1.
func (c *Clip) channelIndex(name string) int {
	for i, n := range c.Channels {
		if n == name {
			return i
		}
	}
	return -1
}

// JointPoseAt returns the joint's local rotation/translation at a tick, falling back to the
// given rest values for any component the clip doesn't animate (GSKEL_FORMAT.md's convention).
func (c *Clip) JointPoseAt(tick int, joint string, restT Vec3, restR Quat) (Vec3, Quat) {
	t, r := restT, restR
	row := tick * len(c.Channels)
	for k, s := range []string{"tx", "ty", "tz"} {
		if i := c.channelIndex(joint + "." + s); i >= 0 {
			t[k] = float64(c.Data[row+i])
		}
	}
	for k, s := range []string{"qx", "qy", "qz", "qw"} {
		if i := c.channelIndex(joint + "." + s); i >= 0 {
			r[k] = float64(c.Data[row+i])
		}
	}
	return t, r.Normalize()
}

// ── Math kit ──────────────────────────────────────────────────────────────────────────────

// Vec3 is a 3-vector.
type Vec3 [3]float64

// Quat is a unit quaternion, (x, y, z, w).
type Quat [4]float64

// Mat4 is a column-major 4x4 matrix (element [col*4+row]), the same layout as GSkelJoint's
// inverse_bind.
type Mat4 [16]float64

// IdentityQuat is the no-rotation quaternion.
var IdentityQuat = Quat{0, 0, 0, 1}

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a[0] * s, a[1] * s, a[2] * s} }
func (a Vec3) Dot(b Vec3) float64   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a Vec3) Len() float64         { return math.Sqrt(a.Dot(a)) }

// Mul returns the Hamilton product q*r (apply r first, then q).
func (q Quat) Mul(r Quat) Quat {
	return Quat{
		q[3]*r[0] + q[0]*r[3] + q[1]*r[2] - q[2]*r[1],
		q[3]*r[1] - q[0]*r[2] + q[1]*r[3] + q[2]*r[0],
		q[3]*r[2] + q[0]*r[1] - q[1]*r[0] + q[2]*r[3],
		q[3]*r[3] - q[0]*r[0] - q[1]*r[1] - q[2]*r[2],
	}
}

// Conj is the inverse of a unit quaternion.
func (q Quat) Conj() Quat { return Quat{-q[0], -q[1], -q[2], q[3]} }

// Normalize returns q scaled to unit length (identity for a zero quaternion).
func (q Quat) Normalize() Quat {
	m := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	if m < 1e-12 {
		return IdentityQuat
	}
	return Quat{q[0] / m, q[1] / m, q[2] / m, q[3] / m}
}

// Rotate applies q to v.
func (q Quat) Rotate(v Vec3) Vec3 {
	p := q.Mul(Quat{v[0], v[1], v[2], 0}).Mul(q.Conj())
	return Vec3{p[0], p[1], p[2]}
}

// Slerp interpolates from a to b by t in [0,1] along the shortest arc.
func Slerp(a, b Quat, t float64) Quat {
	d := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	if d < 0 {
		b = Quat{-b[0], -b[1], -b[2], -b[3]}
		d = -d
	}
	if d > 0.9995 {
		return Quat{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t, a[3] + (b[3]-a[3])*t}.Normalize()
	}
	th := math.Acos(d)
	s := math.Sin(th)
	wa, wb := math.Sin((1-t)*th)/s, math.Sin(t*th)/s
	return Quat{a[0]*wa + b[0]*wb, a[1]*wa + b[1]*wb, a[2]*wa + b[2]*wb, a[3]*wa + b[3]*wb}
}

// QuatFromMat4 extracts the rotation from m's upper 3x3, removing any uniform or per-axis scale
// first (armature exports often carry a 0.01 scale in their inverse-bind matrices).
func QuatFromMat4(m Mat4) Quat {
	var c [3]Vec3
	for col := 0; col < 3; col++ {
		v := Vec3{m[col*4], m[col*4+1], m[col*4+2]}
		if l := v.Len(); l > 1e-12 {
			v = v.Scale(1 / l)
		}
		c[col] = v
	}
	// r[row][col] = c[col][row]
	m00, m01, m02 := c[0][0], c[1][0], c[2][0]
	m10, m11, m12 := c[0][1], c[1][1], c[2][1]
	m20, m21, m22 := c[0][2], c[1][2], c[2][2]
	tr := m00 + m11 + m22
	var q Quat
	switch {
	case tr > 0:
		s := math.Sqrt(tr+1) * 2
		q = Quat{(m21 - m12) / s, (m02 - m20) / s, (m10 - m01) / s, 0.25 * s}
	case m00 > m11 && m00 > m22:
		s := math.Sqrt(1+m00-m11-m22) * 2
		q = Quat{0.25 * s, (m01 + m10) / s, (m02 + m20) / s, (m21 - m12) / s}
	case m11 > m22:
		s := math.Sqrt(1+m11-m00-m22) * 2
		q = Quat{(m01 + m10) / s, 0.25 * s, (m12 + m21) / s, (m02 - m20) / s}
	default:
		s := math.Sqrt(1+m22-m00-m11) * 2
		q = Quat{(m02 + m20) / s, (m12 + m21) / s, 0.25 * s, (m10 - m01) / s}
	}
	return q.Normalize()
}

// MulPoint transforms a point by m.
func (m Mat4) MulPoint(p Vec3) Vec3 {
	return Vec3{
		m[0]*p[0] + m[4]*p[1] + m[8]*p[2] + m[12],
		m[1]*p[0] + m[5]*p[1] + m[9]*p[2] + m[13],
		m[2]*p[0] + m[6]*p[1] + m[10]*p[2] + m[14],
	}
}

// Invert returns m's inverse (general 4x4, cofactor expansion) and false if m is singular.
func (m Mat4) Invert() (Mat4, bool) {
	var inv Mat4
	inv[0] = m[5]*m[10]*m[15] - m[5]*m[11]*m[14] - m[9]*m[6]*m[15] + m[9]*m[7]*m[14] + m[13]*m[6]*m[11] - m[13]*m[7]*m[10]
	inv[4] = -m[4]*m[10]*m[15] + m[4]*m[11]*m[14] + m[8]*m[6]*m[15] - m[8]*m[7]*m[14] - m[12]*m[6]*m[11] + m[12]*m[7]*m[10]
	inv[8] = m[4]*m[9]*m[15] - m[4]*m[11]*m[13] - m[8]*m[5]*m[15] + m[8]*m[7]*m[13] + m[12]*m[5]*m[11] - m[12]*m[7]*m[9]
	inv[12] = -m[4]*m[9]*m[14] + m[4]*m[10]*m[13] + m[8]*m[5]*m[14] - m[8]*m[6]*m[13] - m[12]*m[5]*m[10] + m[12]*m[6]*m[9]
	inv[1] = -m[1]*m[10]*m[15] + m[1]*m[11]*m[14] + m[9]*m[2]*m[15] - m[9]*m[3]*m[14] - m[13]*m[2]*m[11] + m[13]*m[3]*m[10]
	inv[5] = m[0]*m[10]*m[15] - m[0]*m[11]*m[14] - m[8]*m[2]*m[15] + m[8]*m[3]*m[14] + m[12]*m[2]*m[11] - m[12]*m[3]*m[10]
	inv[9] = -m[0]*m[9]*m[15] + m[0]*m[11]*m[13] + m[8]*m[1]*m[15] - m[8]*m[3]*m[13] - m[12]*m[1]*m[11] + m[12]*m[3]*m[9]
	inv[13] = m[0]*m[9]*m[14] - m[0]*m[10]*m[13] - m[8]*m[1]*m[14] + m[8]*m[2]*m[13] + m[12]*m[1]*m[10] - m[12]*m[2]*m[9]
	inv[2] = m[1]*m[6]*m[15] - m[1]*m[7]*m[14] - m[5]*m[2]*m[15] + m[5]*m[3]*m[14] + m[13]*m[2]*m[7] - m[13]*m[3]*m[6]
	inv[6] = -m[0]*m[6]*m[15] + m[0]*m[7]*m[14] + m[4]*m[2]*m[15] - m[4]*m[3]*m[14] - m[12]*m[2]*m[7] + m[12]*m[3]*m[6]
	inv[10] = m[0]*m[5]*m[15] - m[0]*m[7]*m[13] - m[4]*m[1]*m[15] + m[4]*m[3]*m[13] + m[12]*m[1]*m[7] - m[12]*m[3]*m[5]
	inv[14] = -m[0]*m[5]*m[14] + m[0]*m[6]*m[13] + m[4]*m[1]*m[14] - m[4]*m[2]*m[13] - m[12]*m[1]*m[6] + m[12]*m[2]*m[5]
	inv[3] = -m[1]*m[6]*m[11] + m[1]*m[7]*m[10] + m[5]*m[2]*m[11] - m[5]*m[3]*m[10] - m[9]*m[2]*m[7] + m[9]*m[3]*m[6]
	inv[7] = m[0]*m[6]*m[11] - m[0]*m[7]*m[10] - m[4]*m[2]*m[11] + m[4]*m[3]*m[10] + m[8]*m[2]*m[7] - m[8]*m[3]*m[6]
	inv[11] = -m[0]*m[5]*m[11] + m[0]*m[7]*m[9] + m[4]*m[1]*m[11] - m[4]*m[3]*m[9] - m[8]*m[1]*m[7] + m[8]*m[3]*m[5]
	inv[15] = m[0]*m[5]*m[10] - m[0]*m[6]*m[9] - m[4]*m[1]*m[10] + m[4]*m[2]*m[9] + m[8]*m[1]*m[6] - m[8]*m[2]*m[5]
	det := m[0]*inv[0] + m[1]*inv[4] + m[2]*inv[8] + m[3]*inv[12]
	if math.Abs(det) < 1e-18 {
		return Mat4{}, false
	}
	for i := range inv {
		inv[i] /= det
	}
	return inv, true
}

func (m Mat4) isIdentity() bool {
	for i := 0; i < 16; i++ {
		want := 0.0
		if i%5 == 0 {
			want = 1
		}
		if math.Abs(m[i]-want) > 1e-6 {
			return false
		}
	}
	return true
}

// ── Skeleton-space helpers ────────────────────────────────────────────────────────────────

// RestWorld runs forward kinematics over the rest pose and returns every joint's world rotation
// and position in skeleton space (the space the joint hierarchy itself defines).
func (s *Skeleton) RestWorld() ([]Quat, []Vec3) {
	rot := make([]Quat, len(s.Joints))
	pos := make([]Vec3, len(s.Joints))
	for i, j := range s.Joints {
		lr := j.RestRotation.Normalize()
		if j.Parent < 0 {
			rot[i], pos[i] = lr, j.RestTranslation
			continue
		}
		rot[i] = rot[j.Parent].Mul(lr)
		pos[i] = pos[j.Parent].Add(rot[j.Parent].Rotate(j.RestTranslation))
	}
	return rot, pos
}

// BindPositions returns every joint's position in MESH (bind) space: the space the paired
// .gmesh's vertices live in. It comes from inverting each inverse-bind matrix. A rig whose
// inverse binds are all identity (glTF with no inverseBindMatrices, or NOCK's skinless "root"
// fallback) has no separate bind space, so the rest-pose FK positions are used instead.
func (s *Skeleton) BindPositions() []Vec3 {
	_, fk := s.RestWorld()
	allIdentity := true
	for _, j := range s.Joints {
		if !j.InverseBind.isIdentity() {
			allIdentity = false
			break
		}
	}
	if allIdentity {
		return fk
	}
	out := make([]Vec3, len(s.Joints))
	for i, j := range s.Joints {
		bind, ok := j.InverseBind.Invert()
		if !ok {
			out[i] = fk[i]
			continue
		}
		out[i] = Vec3{bind[12], bind[13], bind[14]}
	}
	return out
}

// bindFrame returns the rotation and uniform scale that carry skeleton space into mesh space
// (the armature object's own transform, e.g. a Blender export's -90° X / 0.01 scale). Derived
// from the first root joint: bindRoot = frame * fkRoot.
func (s *Skeleton) bindFrame() (Quat, float64) {
	root := 0
	j := s.Joints[root]
	if j.InverseBind.isIdentity() {
		return IdentityQuat, 1
	}
	bind, ok := j.InverseBind.Invert()
	if !ok {
		return IdentityQuat, 1
	}
	frame := QuatFromMat4(bind).Mul(j.RestRotation.Normalize().Conj())
	scale := Vec3{bind[0], bind[1], bind[2]}.Len()
	if scale < 1e-9 {
		scale = 1
	}
	return frame.Normalize(), scale
}
