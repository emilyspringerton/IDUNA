package shankpit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateWalls_MeshSkin(t *testing.T) {
	ok := Wall{SX: 1, SY: 1, SZ: 1, Mesh: "crystal_a", NormalMap: "3f9a0c.tga", LodBias: 1}
	if err := validateWalls([]Wall{ok, {SX: 1, SY: 1, SZ: 1}}); err != nil {
		t.Fatalf("valid skin refused: %v", err)
	}
	bad := []Wall{
		{SX: 1, SY: 1, SZ: 1, Mesh: "../../etc/passwd"},
		{SX: 1, SY: 1, SZ: 1, Mesh: "a b"},
		{SX: 1, SY: 1, SZ: 1, Mesh: ".hidden"},
		{SX: 1, SY: 1, SZ: 1, Mesh: strings.Repeat("a", 41)},
		{SX: 1, SY: 1, SZ: 1, Mesh: "m", NormalMap: "x/y"},
		{SX: 1, SY: 1, SZ: 1, Mesh: "m", LodBias: 4},
		{SX: 1, SY: 1, SZ: 1, NormalMap: "n.tga"}, // a normal map with no mesh
	}
	for i, w := range bad {
		if validateWalls([]Wall{w}) == nil {
			t.Errorf("case %d accepted: %+v", i, w)
		}
	}
}

// The wire form is what level_boxes.h reads: absent when plain, keyed mesh/normal_map/lod_bias when skinned.
func TestWallJSON_MeshSkin(t *testing.T) {
	plain, _ := json.Marshal(Wall{SX: 1, SY: 1, SZ: 1})
	if strings.Contains(string(plain), "mesh") || strings.Contains(string(plain), "lod_bias") || strings.Contains(string(plain), "normal_map") {
		t.Fatalf("a plain box must not carry skin keys: %s", plain)
	}
	skinned, _ := json.Marshal(Wall{SX: 1, SY: 1, SZ: 1, Mesh: "m1", NormalMap: "m1_n.tga", LodBias: -1})
	for _, want := range []string{`"mesh":"m1"`, `"normal_map":"m1_n.tga"`, `"lod_bias":-1`} {
		if !strings.Contains(string(skinned), want) {
			t.Errorf("missing %s in %s", want, skinned)
		}
	}
}

func TestCarryOrientation_KeepsSkin(t *testing.T) {
	var dst Wall
	carryOrientation(Wall{SX: 1, SY: 1, SZ: 1, Mesh: "m", NormalMap: "n.tga", LodBias: 2}, &dst, 90)
	if dst.Mesh != "m" || dst.NormalMap != "n.tga" || dst.LodBias != 2 {
		t.Fatalf("skin lost when flattened: %+v", dst)
	}
}
