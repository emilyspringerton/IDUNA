package nock

// rig_bonemap.go — bone mapping between two rigs, the base every rig-remapping primitive uses
// (rig_remap.go moves a mesh onto a new rig, rig_retarget.go moves a clip onto a new rig).
//
// A BoneMap says which destination joint each source joint becomes. AutoBoneMap builds one in
// three passes, stopping at the first pass that matches a joint:
//
//  1. exact name ("LeftArm" == "LeftArm"), then the same name once prefixes like "mixamorig:" /
//     "Bip01 " / "DEF-", case and punctuation are ignored ("root" == "mixamorig:Root");
//  2. canonical body part: side pulled out ("LeftArm", "arm.L", "Arm_L", "upperarm_l" all mean
//     left upper arm) and synonyms folded ("UpLeg"/"thigh"/"upperleg", "ForeArm"/"lowerarm",
//     "Leg"/"calf"/"shin", ...). Used only for parts that are a single joint on both rigs;
//  3. chain order, for parts that are a chain of several joints (spine, neck, finger segments,
//     a toe plus its end site): the Nth joint of that part in skeleton order matches the Nth on
//     the other rig. The number in the name is deliberately ignored, because conventions
//     disagree on where counting starts (Mixamo's Spine/Spine1/Spine2 is Unreal's
//     spine_01/02/03, so matching "1" to "01" would be off by one). Found on a real 65-joint
//     Unreal mannequin vs. a Mixamo-named copy of it.
//
// Explicit overrides always win, and a joint can be explicitly unmapped by mapping it to "".
// Known limit: pass 2's synonym table covers the common humanoid conventions (Mixamo, Unreal
// Mannequin, Blender Rigify DEF bones, 3ds Max Biped). Anything else falls through to exact
// names only, and the UI/CLI shows the unmapped joints so a person can fill them in by hand.

import (
	"regexp"
	"sort"
	"strings"
)

// BoneMap maps source joint name → destination joint name. A missing key or "" means unmapped.
type BoneMap map[string]string

// BoneMapReport describes how a bone map was built, for a person to review.
type BoneMapReport struct {
	Map            BoneMap           `json:"map"`
	How            map[string]string `json:"how"` // source joint → "exact" | "canonical" | "chain" | "override"
	UnmappedSource []string          `json:"unmapped_source"`
	UnmappedDest   []string          `json:"unmapped_dest"`
}

var (
	bonePrefixRe = regexp.MustCompile(`^(mixamorig\d*[:_]|bip\d*[ _]|def[-_]|org[-_]|mch[-_]|armature[|_:]|cc_base_|b_)`)
	boneSplitRe  = regexp.MustCompile(`[^a-z0-9]+`)
	camelRe      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	digitsRe     = regexp.MustCompile(`^([a-z]+?)0*(\d+)$`)
)

// boneSynonyms folds body-part words onto one canonical word. Two-word parts are joined
// before lookup ("up"+"leg" → "upleg").
var boneSynonyms = map[string]string{
	"hips": "hips", "pelvis": "hips", "hip": "hips",
	"spine": "spine", "chest": "spine", "upperchest": "spine",
	"neck": "neck", "head": "head",
	"shoulder": "shoulder", "clavicle": "shoulder", "collar": "shoulder",
	"arm": "upperarm", "upperarm": "upperarm", "uparm": "upperarm",
	"forearm": "lowerarm", "lowerarm": "lowerarm", "elbow": "lowerarm",
	"hand": "hand", "wrist": "hand",
	"upleg": "upperleg", "upperleg": "upperleg", "thigh": "upperleg", "femur": "upperleg",
	"leg": "lowerleg", "lowerleg": "lowerleg", "calf": "lowerleg", "shin": "lowerleg", "knee": "lowerleg",
	"foot": "foot", "ankle": "foot",
	"toe": "toe", "toes": "toe", "toebase": "toe", "ball": "toe",
	"thumb": "thumb", "index": "index", "indexfinger": "index", "middle": "middle",
	"ring": "ring", "pinky": "pinky", "little": "pinky",
}

// canonicalBone splits a joint name into (side, base, number). side is "l", "r" or "". number
// is "" when the name carries none. base is "" when no body-part word was recognised.
func canonicalBone(name string) (side, base, num string) {
	n := camelRe.ReplaceAllString(name, "${1}_${2}")
	n = strings.ToLower(n)
	for {
		stripped := bonePrefixRe.ReplaceAllString(n, "")
		if stripped == n {
			break
		}
		n = stripped
	}
	var words []string
	for _, w := range boneSplitRe.Split(n, -1) {
		if w == "" {
			continue
		}
		switch w {
		case "left", "l":
			side = "l"
			continue
		case "right", "r":
			side = "r"
			continue
		}
		if m := digitsRe.FindStringSubmatch(w); m != nil {
			words = append(words, m[1])
			num = m[2]
			continue
		}
		if isAllDigits(w) {
			num = strings.TrimLeft(w, "0")
			if num == "" {
				num = "0"
			}
			continue
		}
		words = append(words, w)
	}
	// Longest-first join so "up"+"leg" and "fore"+"arm" beat "leg"/"arm" alone; right-to-left so
	// the most specific word wins ("LeftHandThumb1" is a thumb, not a hand).
	for size := len(words); size >= 1; size-- {
		for i := len(words) - size; i >= 0; i-- {
			if b, ok := boneSynonyms[strings.Join(words[i:i+size], "")]; ok {
				return side, b, num
			}
		}
	}
	return side, "", num
}

// normBoneName lowercases, strips rig prefixes and drops punctuation: "mixamorig:Left_Arm" and
// "leftarm" compare equal.
func normBoneName(name string) string {
	n := strings.ToLower(name)
	for {
		stripped := bonePrefixRe.ReplaceAllString(n, "")
		if stripped == n {
			break
		}
		n = stripped
	}
	return boneSplitRe.ReplaceAllString(n, "")
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// AutoBoneMap builds a bone map from src to dst. overrides (may be nil) are applied last and
// always win; an override to "" explicitly unmaps a joint. Each destination joint is used at
// most once by the automatic passes.
func AutoBoneMap(src, dst *Skeleton, overrides BoneMap) BoneMapReport {
	rep := BoneMapReport{Map: BoneMap{}, How: map[string]string{}}
	usedDst := map[string]bool{}
	assign := func(s, d, how string) {
		rep.Map[s] = d
		rep.How[s] = how
		usedDst[d] = true
	}

	// Pass 1: exact names, then normalised names (unique on the destination side only).
	for _, sj := range src.Joints {
		if dst.JointIndex(sj.Name) >= 0 {
			assign(sj.Name, sj.Name, "exact")
		}
	}
	dstByNorm := map[string][]int{}
	for i, dj := range dst.Joints {
		dstByNorm[normBoneName(dj.Name)] = append(dstByNorm[normBoneName(dj.Name)], i)
	}
	for _, sj := range src.Joints {
		if _, done := rep.Map[sj.Name]; done {
			continue
		}
		if c := dstByNorm[normBoneName(sj.Name)]; len(c) == 1 && !usedDst[dst.Joints[c[0]].Name] {
			assign(sj.Name, dst.Joints[c[0]].Name, "exact")
		}
	}

	type canon struct{ key, ordKey string }
	canonOf := func(s *Skeleton) ([]canon, map[string]int) {
		out := make([]canon, len(s.Joints))
		counts := map[string]int{}
		for i, j := range s.Joints {
			side, base, _ := canonicalBone(j.Name)
			if base == "" {
				continue
			}
			key := side + "|" + base
			out[i] = canon{key: key, ordKey: key + "#" + itoa(counts[key])}
			counts[key]++
		}
		return out, counts
	}
	sc, srcCount := canonOf(src)
	dc, dstCount := canonOf(dst)

	// Pass 2: single-joint parts, matched by canonical part alone.
	dstByKey := map[string]int{}
	for i, c := range dc {
		if c.key != "" {
			dstByKey[c.key] = i
		}
	}
	for i, sj := range src.Joints {
		k := sc[i].key
		if _, done := rep.Map[sj.Name]; done || k == "" || srcCount[k] != 1 || dstCount[k] != 1 {
			continue
		}
		if di := dstByKey[k]; !usedDst[dst.Joints[di].Name] {
			assign(sj.Name, dst.Joints[di].Name, "canonical")
		}
	}

	// Pass 3: chains, by position in the chain.
	dstByOrd := map[string]int{}
	for i, c := range dc {
		if c.ordKey != "" {
			dstByOrd[c.ordKey] = i
		}
	}
	for i, sj := range src.Joints {
		if _, done := rep.Map[sj.Name]; done || sc[i].ordKey == "" {
			continue
		}
		if di, ok := dstByOrd[sc[i].ordKey]; ok && !usedDst[dst.Joints[di].Name] {
			assign(sj.Name, dst.Joints[di].Name, "chain")
		}
	}

	for s, d := range overrides {
		if src.JointIndex(s) < 0 {
			continue
		}
		if d != "" && dst.JointIndex(d) < 0 {
			continue
		}
		if d == "" {
			delete(rep.Map, s)
			rep.How[s] = "override"
			continue
		}
		rep.Map[s] = d
		rep.How[s] = "override"
	}

	mappedDst := map[string]bool{}
	for _, d := range rep.Map {
		mappedDst[d] = true
	}
	for _, sj := range src.Joints {
		if rep.Map[sj.Name] == "" {
			rep.UnmappedSource = append(rep.UnmappedSource, sj.Name)
		}
	}
	for _, dj := range dst.Joints {
		if !mappedDst[dj.Name] {
			rep.UnmappedDest = append(rep.UnmappedDest, dj.Name)
		}
	}
	sort.Strings(rep.UnmappedSource)
	sort.Strings(rep.UnmappedDest)
	return rep
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
