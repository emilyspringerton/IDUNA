package nock

// keyframes.go — keyframe animation authoring for NOCK (founder real-time, 2026-09-27: "we need
// a way to animate in NOCK"). The Blender "Dope Sheet": a clip is a set of per-joint rotation and
// translation keys, and the tool bakes them to a uniformly sampled .gband.
//
// A KeyframeDoc is the editable source. The .gband is derived from it, the same source → artifact
// split NOCK's procedural textures use (PARENA source kept next to the rendered PNG). NOCK stores
// both, so reopening a clip in the animator gets its keys back rather than 60 ticks of baked
// floats.
//
// Interpolation is set per key and applies to the segment that starts at that key:
//   - "linear" (default): slerp for rotation, lerp for translation;
//   - "smooth": the same, with smoothstep easing (zero velocity at both keys; Blender's default
//     Bezier "auto clamped" is close to this for a two-key segment);
//   - "step": hold the key's value until the next key (blocking/stepped preview).
// Before the first key a channel holds the first key's value, after the last it holds the last.
//
// frontend/nock/src/keyframes.ts is the browser twin of Bake (live preview while posing); the Go
// version here is the one that produces the stored .gband, and the two are tested against the
// same fixed cases.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// KeyframeDoc is an editable keyframe clip.
type KeyframeDoc struct {
	Version       int        `json:"version"`
	TickRate      int        `json:"tick_rate"`
	DurationTicks int        `json:"duration_ticks"`
	Tracks        []KeyTrack `json:"tracks"`
}

// KeyTrack holds one joint's keys.
type KeyTrack struct {
	Joint       string   `json:"joint"`
	Rotation    []RotKey `json:"rotation,omitempty"`
	Translation []PosKey `json:"translation,omitempty"`
}

// RotKey is a local-rotation key, quaternion (x, y, z, w).
type RotKey struct {
	Tick   int        `json:"tick"`
	Q      [4]float64 `json:"q"`
	Interp string     `json:"interp,omitempty"`
}

// PosKey is a local-translation key.
type PosKey struct {
	Tick   int        `json:"tick"`
	T      [3]float64 `json:"t"`
	Interp string     `json:"interp,omitempty"`
}

// MaxKeyframeTicks bounds a doc so a typo can't allocate gigabytes (10 minutes at 60 Hz).
const MaxKeyframeTicks = 36000

// ParseKeyframeDoc decodes and validates a doc against the rig it animates.
func ParseKeyframeDoc(raw []byte, skel *Skeleton) (*KeyframeDoc, error) {
	var d KeyframeDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("keyframes: invalid JSON: %w", err)
	}
	return &d, d.Validate(skel)
}

// Validate checks the doc against the rig and normalises it: keys sorted by tick, duplicate
// ticks collapsed (the later one wins), quaternions normalised, interpolation names checked.
func (d *KeyframeDoc) Validate(skel *Skeleton) error {
	if d.Version == 0 {
		d.Version = 1
	}
	if d.TickRate <= 0 || d.TickRate > 1000 {
		return fmt.Errorf("keyframes: tick_rate must be 1..1000, got %d", d.TickRate)
	}
	if d.DurationTicks <= 0 || d.DurationTicks > MaxKeyframeTicks {
		return fmt.Errorf("keyframes: duration_ticks must be 1..%d, got %d", MaxKeyframeTicks, d.DurationTicks)
	}
	seen := map[string]bool{}
	for ti := range d.Tracks {
		tr := &d.Tracks[ti]
		if skel != nil && skel.JointIndex(tr.Joint) < 0 {
			return fmt.Errorf("keyframes: joint %q is not in this rig", tr.Joint)
		}
		if seen[tr.Joint] {
			return fmt.Errorf("keyframes: joint %q has two tracks", tr.Joint)
		}
		seen[tr.Joint] = true
		for _, k := range tr.Rotation {
			if err := checkKey(tr.Joint, k.Tick, k.Interp, d.DurationTicks); err != nil {
				return err
			}
		}
		for _, k := range tr.Translation {
			if err := checkKey(tr.Joint, k.Tick, k.Interp, d.DurationTicks); err != nil {
				return err
			}
		}
		sort.SliceStable(tr.Rotation, func(a, b int) bool { return tr.Rotation[a].Tick < tr.Rotation[b].Tick })
		sort.SliceStable(tr.Translation, func(a, b int) bool { return tr.Translation[a].Tick < tr.Translation[b].Tick })
		tr.Rotation = dedupeRot(tr.Rotation)
		tr.Translation = dedupePos(tr.Translation)
		for i := range tr.Rotation {
			q := Quat(tr.Rotation[i].Q).Normalize()
			tr.Rotation[i].Q = [4]float64(q)
		}
	}
	return nil
}

func checkKey(joint string, tick int, interp string, dur int) error {
	if tick < 0 || tick >= dur {
		return fmt.Errorf("keyframes: %s key at tick %d is outside 0..%d", joint, tick, dur-1)
	}
	switch interp {
	case "", "linear", "smooth", "step":
		return nil
	}
	return fmt.Errorf("keyframes: %s key at tick %d has unknown interp %q (linear, smooth, step)", joint, tick, interp)
}

func dedupeRot(ks []RotKey) []RotKey {
	out := ks[:0]
	for _, k := range ks {
		if len(out) > 0 && out[len(out)-1].Tick == k.Tick {
			out[len(out)-1] = k
			continue
		}
		out = append(out, k)
	}
	return out
}

func dedupePos(ks []PosKey) []PosKey {
	out := ks[:0]
	for _, k := range ks {
		if len(out) > 0 && out[len(out)-1].Tick == k.Tick {
			out[len(out)-1] = k
			continue
		}
		out = append(out, k)
	}
	return out
}

// segment finds the key segment containing tick: indices a, b and the eased blend factor.
func segment(ticks []int, interps []string, tick int) (int, int, float64) {
	n := len(ticks)
	if tick <= ticks[0] {
		return 0, 0, 0
	}
	if tick >= ticks[n-1] {
		return n - 1, n - 1, 0
	}
	i := sort.Search(n, func(i int) bool { return ticks[i] > tick }) - 1
	u := float64(tick-ticks[i]) / float64(ticks[i+1]-ticks[i])
	switch interps[i] {
	case "step":
		u = 0
	case "smooth":
		u = u * u * (3 - 2*u)
	}
	return i, i + 1, u
}

// Bake samples the doc into a Clip, one sample per tick. Channel order follows the rig's joint
// order (translation before rotation within a joint) so a baked clip reads like an imported one.
func (d *KeyframeDoc) Bake(skel *Skeleton) (*Clip, error) {
	if err := d.Validate(skel); err != nil {
		return nil, err
	}
	byJoint := map[string]*KeyTrack{}
	for i := range d.Tracks {
		byJoint[d.Tracks[i].Joint] = &d.Tracks[i]
	}
	type col struct {
		track *KeyTrack
		isRot bool
	}
	var channels []string
	var cols []col
	for _, j := range skel.Joints {
		tr := byJoint[j.Name]
		if tr == nil {
			continue
		}
		if len(tr.Translation) > 0 {
			channels = append(channels, j.Name+".tx", j.Name+".ty", j.Name+".tz")
			cols = append(cols, col{tr, false})
		}
		if len(tr.Rotation) > 0 {
			channels = append(channels, j.Name+".qx", j.Name+".qy", j.Name+".qz", j.Name+".qw")
			cols = append(cols, col{tr, true})
		}
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("keyframes: no keys to bake -- key at least one joint")
	}
	nc := len(channels)
	c := &Clip{TickRate: d.TickRate, DurationTicks: d.DurationTicks, Channels: channels, Data: make([]float32, d.DurationTicks*nc)}
	off := 0
	for _, cl := range cols {
		if cl.isRot {
			ks := cl.track.Rotation
			ticks, interps := make([]int, len(ks)), make([]string, len(ks))
			for i, k := range ks {
				ticks[i], interps[i] = k.Tick, k.Interp
			}
			var prev Quat
			for t := 0; t < d.DurationTicks; t++ {
				a, b, u := segment(ticks, interps, t)
				q := Slerp(Quat(ks[a].Q), Quat(ks[b].Q), u)
				if t > 0 && q[0]*prev[0]+q[1]*prev[1]+q[2]*prev[2]+q[3]*prev[3] < 0 {
					q = Quat{-q[0], -q[1], -q[2], -q[3]}
				}
				prev = q
				for k := 0; k < 4; k++ {
					c.Data[t*nc+off+k] = float32(q[k])
				}
			}
			off += 4
			continue
		}
		ks := cl.track.Translation
		ticks, interps := make([]int, len(ks)), make([]string, len(ks))
		for i, k := range ks {
			ticks[i], interps[i] = k.Tick, k.Interp
		}
		for t := 0; t < d.DurationTicks; t++ {
			a, b, u := segment(ticks, interps, t)
			for k := 0; k < 3; k++ {
				c.Data[t*nc+off+k] = float32(ks[a].T[k] + (ks[b].T[k]-ks[a].T[k])*u)
			}
		}
		off += 3
	}
	return c, nil
}

// KeyframesFromClip turns a baked clip (an imported mocap clip, say) back into an editable doc by
// keeping every `every`-th tick plus the last one, so a person can open any clip in the animator
// and adjust it. every=1 keeps every tick. Only joints the clip animates get tracks.
func KeyframesFromClip(c *Clip, skel *Skeleton, every int) (*KeyframeDoc, error) {
	if every < 1 {
		return nil, fmt.Errorf("keyframes: every must be >= 1")
	}
	d := &KeyframeDoc{Version: 1, TickRate: c.TickRate, DurationTicks: c.DurationTicks}
	var ticks []int
	for t := 0; t < c.DurationTicks; t += every {
		ticks = append(ticks, t)
	}
	if last := c.DurationTicks - 1; ticks[len(ticks)-1] != last {
		ticks = append(ticks, last)
	}
	for _, j := range skel.Joints {
		hasT := c.channelIndex(j.Name+".tx") >= 0 || c.channelIndex(j.Name+".ty") >= 0 || c.channelIndex(j.Name+".tz") >= 0
		hasR := c.channelIndex(j.Name+".qx") >= 0 || c.channelIndex(j.Name+".qw") >= 0
		if !hasT && !hasR {
			continue
		}
		tr := KeyTrack{Joint: j.Name}
		// A channel that never moves (Unreal-style exports key a constant translation on every
		// joint) collapses to one key; it bakes to the same values at a fraction of the size.
		constT, constR := true, true
		t0, r0 := c.JointPoseAt(0, j.Name, j.RestTranslation, j.RestRotation)
		for t := 1; t < c.DurationTicks; t++ {
			pt, pr := c.JointPoseAt(t, j.Name, j.RestTranslation, j.RestRotation)
			if pt.Sub(t0).Len() > 1e-6 {
				constT = false
			}
			if math.Abs(pr[0]*r0[0]+pr[1]*r0[1]+pr[2]*r0[2]+pr[3]*r0[3]) < 1-1e-9 {
				constR = false
			}
		}
		for _, t := range ticks {
			pt, pr := c.JointPoseAt(t, j.Name, j.RestTranslation, j.RestRotation)
			if hasT && (!constT || t == 0) {
				tr.Translation = append(tr.Translation, PosKey{Tick: t, T: round6v(pt)})
			}
			if hasR && (!constR || t == 0) {
				tr.Rotation = append(tr.Rotation, RotKey{Tick: t, Q: round6q(pr)})
			}
		}
		d.Tracks = append(d.Tracks, tr)
	}
	if len(d.Tracks) == 0 {
		return nil, fmt.Errorf("keyframes: clip animates none of this rig's joints")
	}
	return d, nil
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }
func round6v(v Vec3) [3]float64 {
	return [3]float64{round6(v[0]), round6(v[1]), round6(v[2])}
}
func round6q(q Quat) [4]float64 {
	return [4]float64{round6(q[0]), round6(q[1]), round6(q[2]), round6(q[3])}
}
