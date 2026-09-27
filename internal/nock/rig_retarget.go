package nock

// rig_retarget.go — move an animation clip from one rig onto another (the other half of
// "remapping onto a new rig": rig_remap.go moves the mesh, this moves the motion). Blender does
// this with add-ons such as Rokoko or Auto-Rig Pro's remap.
//
// Method: world-space rest-pose deltas. At each tick the source rig is posed by forward
// kinematics; for every mapped joint, the rotation that carries it from its rest orientation to
// its animated orientation (in mesh space) is applied to the destination joint's own rest
// orientation, and converted back to a local rotation under the destination parent. Working in
// world space rather than copying local quaternions means the two rigs may use different bone
// axis conventions (Mixamo's Y-along-bone vs Unreal's X-along-bone) and it still lines up.
// Translation channels (root motion, usually the hips) transfer as rest-relative offsets scaled
// by the ratio of the two rigs' heights.
//
// Known limits:
//   - Both rigs need the same kind of rest pose (both T-pose, or both A-pose). A T-pose clip
//     retargeted onto an A-pose rig keeps its arms 45° off. No automatic pose matching yet.
//   - Motion on a source joint with no mapping is not transferred on its own, though it still
//     moves mapped children because it is part of their world transform. In particular, root
//     motion on an unmapped "Root" joint above the hips is lost; map it or bake it into the hips.
//   - No IK: feet may slide or float if the leg proportions differ a lot between rigs.

import "fmt"

// RetargetReport describes what RetargetClip did.
type RetargetReport struct {
	BoneMap        BoneMapReport `json:"bone_map"`
	JointsAnimated int           `json:"joints_animated"`
	HeightRatio    float64       `json:"height_ratio"`
	Warnings       []string      `json:"warnings,omitempty"`
}

// RetargetClip re-expresses clip (authored for src) on dst. overrides adjust the automatic bone
// map. The result's channels name dst joints.
func RetargetClip(clip *Clip, src, dst *Skeleton, overrides BoneMap) (*Clip, RetargetReport, error) {
	var rep RetargetReport
	if clip == nil || clip.DurationTicks <= 0 || len(clip.Channels) == 0 {
		return nil, rep, fmt.Errorf("retarget: source clip is empty")
	}
	if src == nil || dst == nil {
		return nil, rep, fmt.Errorf("retarget: need both the source and destination rig")
	}
	rep.BoneMap = AutoBoneMap(src, dst, overrides)
	if len(rep.BoneMap.Map) == 0 {
		return nil, rep, fmt.Errorf("retarget: no joints could be mapped between the two rigs; supply a bone map")
	}

	// dst joint → src joint (the first, i.e. highest-in-hierarchy, source joint mapped to it).
	dstFrom := make([]int, len(dst.Joints))
	for i := range dstFrom {
		dstFrom[i] = -1
	}
	for si, sj := range src.Joints {
		if d := rep.BoneMap.Map[sj.Name]; d != "" {
			if di := dst.JointIndex(d); di >= 0 && dstFrom[di] < 0 {
				dstFrom[di] = si
			}
		}
	}

	srcHasT := make([]bool, len(src.Joints))
	srcHasR := make([]bool, len(src.Joints))
	for si, sj := range src.Joints {
		srcHasT[si] = clip.channelIndex(sj.Name+".tx") >= 0 || clip.channelIndex(sj.Name+".ty") >= 0 || clip.channelIndex(sj.Name+".tz") >= 0
		srcHasR[si] = clip.channelIndex(sj.Name+".qx") >= 0
	}

	fs, ss := src.bindFrame()
	fd, sd := dst.bindFrame()
	srcRestW, _ := src.RestWorld()
	dstRestW, _ := dst.RestWorld()
	srcH, dstH := extentY(src.BindPositions()), extentY(dst.BindPositions())
	rep.HeightRatio = 1
	if srcH > 1e-9 && dstH > 1e-9 {
		rep.HeightRatio = dstH / srcH
	}

	// Output channel layout: every mapped dst joint gets rotation; those whose source had
	// translation also get tx/ty/tz. Skeleton order, translation before rotation.
	type outJoint struct {
		di        int
		hasT      bool
		chanStart int
	}
	var outs []outJoint
	var channels []string
	for di, dj := range dst.Joints {
		si := dstFrom[di]
		if si < 0 || !(srcHasR[si] || srcHasT[si] || anyAncestorAnimated(src, si, srcHasR)) {
			continue
		}
		oj := outJoint{di: di, hasT: srcHasT[si], chanStart: len(channels)}
		if oj.hasT {
			channels = append(channels, dj.Name+".tx", dj.Name+".ty", dj.Name+".tz")
		}
		channels = append(channels, dj.Name+".qx", dj.Name+".qy", dj.Name+".qz", dj.Name+".qw")
		outs = append(outs, oj)
	}
	if len(outs) == 0 {
		return nil, rep, fmt.Errorf("retarget: none of the mapped source joints are animated in this clip")
	}
	rep.JointsAnimated = len(outs)

	out := &Clip{TickRate: clip.TickRate, DurationTicks: clip.DurationTicks, Channels: channels, Data: make([]float32, clip.DurationTicks*len(channels))}
	srcAW := make([]Quat, len(src.Joints))
	srcLT := make([]Vec3, len(src.Joints))
	dstAW := make([]Quat, len(dst.Joints))
	prev := make([]Quat, len(outs))

	for t := 0; t < clip.DurationTicks; t++ {
		for si, sj := range src.Joints {
			lt, lr := clip.JointPoseAt(t, sj.Name, sj.RestTranslation, sj.RestRotation)
			srcLT[si] = lt
			if sj.Parent < 0 {
				srcAW[si] = lr
			} else {
				srcAW[si] = srcAW[sj.Parent].Mul(lr)
			}
		}
		for di, dj := range dst.Joints {
			si := dstFrom[di]
			if si < 0 {
				lr := dj.RestRotation.Normalize()
				if dj.Parent < 0 {
					dstAW[di] = lr
				} else {
					dstAW[di] = dstAW[dj.Parent].Mul(lr)
				}
				continue
			}
			// Δ (mesh space) = Fs·AWs·RWs⁻¹·Fs⁻¹ ; AWd = Fd⁻¹·Δ·Fd·RWd
			delta := fs.Mul(srcAW[si]).Mul(srcRestW[si].Conj()).Mul(fs.Conj())
			dstAW[di] = fd.Conj().Mul(delta).Mul(fd).Mul(dstRestW[di]).Normalize()
		}
		row := t * len(channels)
		for k, oj := range outs {
			dj := dst.Joints[oj.di]
			local := dstAW[oj.di]
			if dj.Parent >= 0 {
				local = dstAW[dj.Parent].Conj().Mul(local)
			}
			local = local.Normalize()
			// Keep consecutive ticks on the same hemisphere so the runtime's per-component
			// nlerp between ticks never takes the long way round.
			if t > 0 && local[0]*prev[k][0]+local[1]*prev[k][1]+local[2]*prev[k][2]+local[3]*prev[k][3] < 0 {
				local = Quat{-local[0], -local[1], -local[2], -local[3]}
			}
			prev[k] = local
			c := oj.chanStart
			if oj.hasT {
				si := dstFrom[oj.di]
				sj := src.Joints[si]
				d := srcLT[si].Sub(sj.RestTranslation)
				if sj.Parent >= 0 {
					d = srcAW[sj.Parent].Rotate(d)
				}
				mesh := fs.Rotate(d).Scale(ss * rep.HeightRatio)
				v := fd.Conj().Rotate(mesh).Scale(1 / sd)
				if dj.Parent >= 0 {
					v = dstAW[dj.Parent].Conj().Rotate(v)
				}
				lt := dj.RestTranslation.Add(v)
				out.Data[row+c], out.Data[row+c+1], out.Data[row+c+2] = float32(lt[0]), float32(lt[1]), float32(lt[2])
				c += 3
			}
			for q := 0; q < 4; q++ {
				out.Data[row+c+q] = float32(local[q])
			}
		}
	}
	if n := len(rep.BoneMap.UnmappedDest); n > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d destination joints have no source and stay at their rest pose", n))
	}
	return out, rep, nil
}

func anyAncestorAnimated(s *Skeleton, i int, animated []bool) bool {
	for p := s.Joints[i].Parent; p >= 0; p = s.Joints[p].Parent {
		if animated[p] {
			return true
		}
	}
	return false
}
