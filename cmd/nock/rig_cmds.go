package main

// rig_cmds.go — CLI affordances for NOCK's animator and rig-remapping primitives (founder
// real-time, 2026-09-27: "we need a way to animate in NOCK also we need the primatives for
// remapping a mesh onto a new rig"). File-in, file-out over GOLDENBAND assets (.gskel/.gmesh/
// .gband + .gband.json), no database or server needed, so an agent or a shell script can drive
// the same internal/nock functions the web animator calls.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"iduna/internal/nock"
)

var rigCommands = map[string]func([]string) error{
	"rig-map":        cmdRigMap,
	"rig-remap-mesh": cmdRigRemapMesh,
	"rig-retarget":   cmdRigRetarget,
	"anim-bake":      cmdAnimBake,
	"anim-keys":      cmdAnimKeys,
}

func readSkel(path string) (*nock.Skeleton, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return nock.DecodeGSkel(b)
}

// readClip loads NAME.gband + NAME.gband.json (NAME may be given with or without .gband).
func readClip(name string) (*nock.Clip, error) {
	name = strings.TrimSuffix(name, ".gband")
	b, err := os.ReadFile(name + ".gband")
	if err != nil {
		return nil, err
	}
	mb, err := os.ReadFile(name + ".gband.json")
	if err != nil {
		return nil, fmt.Errorf("the clip's manifest (channel names) is required: %w", err)
	}
	var mf struct {
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(mb, &mf); err != nil {
		return nil, fmt.Errorf("%s.gband.json: %w", name, err)
	}
	return nock.DecodeGBand(b, mf.Channels)
}

func writeClip(name string, c *nock.Clip, skel *nock.Skeleton, kind, who string) error {
	name = strings.TrimSuffix(name, ".gband")
	gb, manifest, _, err := c.Encode(skel.TopologyHashHex(), kind, who)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name+".gband", gb, 0o644); err != nil {
		return err
	}
	return os.WriteFile(name+".gband.json", []byte(manifest+"\n"), 0o644)
}

// parseBoneMapFlag accepts "src=dst,src2=dst2" (dst empty to unmap) or @file.json.
func parseBoneMapFlag(v string) (nock.BoneMap, error) {
	m := nock.BoneMap{}
	if v == "" {
		return m, nil
	}
	if strings.HasPrefix(v, "@") {
		b, err := os.ReadFile(v[1:])
		if err != nil {
			return nil, err
		}
		return m, json.Unmarshal(b, &m)
	}
	for _, pair := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("-map entry %q is not src=dst", pair)
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	return m, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdRigMap(args []string) error {
	fs := flag.NewFlagSet("rig-map", flag.ExitOnError)
	src := fs.String("src", "", "source rig .gskel")
	dst := fs.String("dst", "", "destination rig .gskel")
	mapFlag := fs.String("map", "", "overrides: src=dst,... or @file.json")
	fs.Parse(args)
	s, err := readSkel(*src)
	if err != nil {
		return err
	}
	d, err := readSkel(*dst)
	if err != nil {
		return err
	}
	ov, err := parseBoneMapFlag(*mapFlag)
	if err != nil {
		return err
	}
	return printJSON(nock.AutoBoneMap(s, d, ov))
}

func cmdRigRemapMesh(args []string) error {
	fs := flag.NewFlagSet("rig-remap-mesh", flag.ExitOnError)
	meshPath := fs.String("mesh", "", "input .gmesh")
	src := fs.String("src", "", "the mesh's current rig .gskel (omit for an unskinned mesh; forces -mode proximity)")
	dst := fs.String("dst", "", "destination rig .gskel")
	out := fs.String("out", "", "output .gmesh")
	mode := fs.String("mode", "auto", "auto | names | proximity")
	fit := fs.Bool("fit", false, "scale/translate the mesh to the destination rig's size")
	mapFlag := fs.String("map", "", "bone map overrides: src=dst,... or @file.json")
	fs.Parse(args)
	if *meshPath == "" || *dst == "" || *out == "" {
		return fmt.Errorf("rig-remap-mesh needs -mesh, -dst and -out")
	}
	mb, err := os.ReadFile(*meshPath)
	if err != nil {
		return err
	}
	mesh, err := nock.DecodeGMesh(mb)
	if err != nil {
		return err
	}
	var s *nock.Skeleton
	if *src != "" {
		if s, err = readSkel(*src); err != nil {
			return err
		}
	} else {
		*mode = string(nock.RemapProximity)
	}
	d, err := readSkel(*dst)
	if err != nil {
		return err
	}
	ov, err := parseBoneMapFlag(*mapFlag)
	if err != nil {
		return err
	}
	res, rep, err := nock.RemapMesh(mesh, s, d, nock.RemapOptions{Mode: nock.RemapMode(*mode), Fit: *fit, Overrides: ov})
	if err != nil {
		return err
	}
	b, err := res.Encode()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	return printJSON(rep)
}

func cmdRigRetarget(args []string) error {
	fs := flag.NewFlagSet("rig-retarget", flag.ExitOnError)
	clipName := fs.String("clip", "", "input clip NAME (reads NAME.gband + NAME.gband.json)")
	src := fs.String("src", "", "the clip's rig .gskel")
	dst := fs.String("dst", "", "destination rig .gskel")
	out := fs.String("out", "", "output clip NAME (writes NAME.gband + NAME.gband.json)")
	mapFlag := fs.String("map", "", "bone map overrides: src=dst,... or @file.json")
	fs.Parse(args)
	if *clipName == "" || *src == "" || *dst == "" || *out == "" {
		return fmt.Errorf("rig-retarget needs -clip, -src, -dst and -out")
	}
	c, err := readClip(*clipName)
	if err != nil {
		return err
	}
	s, err := readSkel(*src)
	if err != nil {
		return err
	}
	d, err := readSkel(*dst)
	if err != nil {
		return err
	}
	ov, err := parseBoneMapFlag(*mapFlag)
	if err != nil {
		return err
	}
	res, rep, err := nock.RetargetClip(c, s, d, ov)
	if err != nil {
		return err
	}
	if err := writeClip(*out, res, d, "retargeted", "nock rig-retarget"); err != nil {
		return err
	}
	return printJSON(rep)
}

func cmdAnimBake(args []string) error {
	fs := flag.NewFlagSet("anim-bake", flag.ExitOnError)
	keys := fs.String("keys", "", "keyframe doc .json (see internal/nock/keyframes.go)")
	skelPath := fs.String("skel", "", "the rig .gskel the keys animate")
	out := fs.String("out", "", "output clip NAME (writes NAME.gband + NAME.gband.json)")
	fs.Parse(args)
	if *keys == "" || *skelPath == "" || *out == "" {
		return fmt.Errorf("anim-bake needs -keys, -skel and -out")
	}
	s, err := readSkel(*skelPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*keys)
	if err != nil {
		return err
	}
	doc, err := nock.ParseKeyframeDoc(raw, s)
	if err != nil {
		return err
	}
	c, err := doc.Bake(s)
	if err != nil {
		return err
	}
	if err := writeClip(*out, c, s, "human", "nock anim-bake"); err != nil {
		return err
	}
	fmt.Printf("baked %d ticks x %d channels -> %s.gband\n", c.DurationTicks, len(c.Channels), strings.TrimSuffix(*out, ".gband"))
	return nil
}

func cmdAnimKeys(args []string) error {
	fs := flag.NewFlagSet("anim-keys", flag.ExitOnError)
	clipName := fs.String("clip", "", "input clip NAME (reads NAME.gband + NAME.gband.json)")
	skelPath := fs.String("skel", "", "the clip's rig .gskel")
	every := fs.Int("every", 5, "keep one key every N ticks (the last tick is always kept)")
	fs.Parse(args)
	c, err := readClip(*clipName)
	if err != nil {
		return err
	}
	s, err := readSkel(*skelPath)
	if err != nil {
		return err
	}
	doc, err := nock.KeyframesFromClip(c, s, *every)
	if err != nil {
		return err
	}
	return printJSON(doc)
}
