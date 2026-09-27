package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/nock"
)

// animatorFixture stores two rigs: "mixa" (Mixamo names, with a skinned mesh) and "unreal"
// (Unreal names, no mesh). Inverse binds are left at identity so bind space == rest FK space.
func animatorFixture(t *testing.T, store *nock.AnimStore) (mixaID, unrealID int64) {
	t.Helper()
	ident := nock.Mat4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	j := func(name string, parent int, x, y, z float64) nock.Joint {
		return nock.Joint{Name: name, Parent: parent, RestTranslation: nock.Vec3{x, y, z}, RestRotation: nock.IdentityQuat, InverseBind: ident}
	}
	mixa := &nock.Skeleton{Joints: []nock.Joint{
		j("mixamorig:Hips", -1, 0, 1, 0), j("mixamorig:Spine", 0, 0, 0.2, 0),
		j("mixamorig:LeftArm", 1, 0.2, 0.3, 0), j("mixamorig:LeftForeArm", 2, 0.3, 0, 0), j("mixamorig:LeftHand", 3, 0.25, 0, 0),
	}}
	unreal := &nock.Skeleton{Joints: []nock.Joint{
		j("pelvis", -1, 0, 1, 0), j("spine_01", 0, 0, 0.2, 0),
		j("upperarm_l", 1, 0.2, 0.3, 0), j("lowerarm_l", 2, 0.3, 0, 0), j("hand_l", 3, 0.25, 0, 0),
	}}
	mesh := &nock.Mesh{Vertices: []nock.Vertex{
		{Position: nock.Vec3{0.65, 1.5, 0}, BoneIndices: [4]uint8{3, 4}, BoneWeights: [4]float32{0.5, 0.5}},
		{Position: nock.Vec3{0, 1.1, 0}, BoneIndices: [4]uint8{0}, BoneWeights: [4]float32{1}},
		{Position: nock.Vec3{0.3, 1.5, 0}, BoneIndices: [4]uint8{2}, BoneWeights: [4]float32{1}},
	}, Indices: []uint32{0, 1, 2}}
	mb, err := mesh.Encode()
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := mixa.Encode()
	ub, _ := unreal.Encode()
	ctx := context.Background()
	a, err := store.CreateAnimation(ctx, "mixa", 0, 0, 0, "", nil, "", sb, mb, mixa.TopologyHashHex(), "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateAnimation(ctx, "unreal", 0, 0, 0, "", nil, "", ub, nil, unreal.TopologyHashHex(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return a.ID, b.ID
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestNockAnimator_AuthorRemapRetarget(t *testing.T) {
	h := newAnimationsTestHandler(t)
	mixaID, unrealID := animatorFixture(t, h.Store)
	base := "/admin/nock/api/animations/"

	// A rig with no animation opens in the animator as an empty doc.
	code, got := doJSON(t, h, http.MethodGet, fmt.Sprintf("%s%d/keyframes", base, mixaID), nil)
	if code != http.StatusOK || got["source"] != "empty" {
		t.Fatalf("GET keyframes on bare rig: %d %v", code, got)
	}
	if tr, ok := got["keyframes"].(map[string]any)["tracks"].([]any); !ok || len(tr) != 0 {
		t.Fatalf("empty doc must carry tracks: [] (not null), got %#v", got["keyframes"])
	}

	// Author a wave: shoulder up 90° over 10 ticks.
	doc := map[string]any{"tick_rate": 30, "duration_ticks": 11, "tracks": []any{
		map[string]any{"joint": "mixamorig:LeftArm", "rotation": []any{
			map[string]any{"tick": 0, "q": []float64{0, 0, 0, 1}},
			map[string]any{"tick": 10, "q": []float64{0, 0, 0.7071068, 0.7071068}, "interp": "smooth"},
		}},
	}}
	code, got = doJSON(t, h, http.MethodPost, fmt.Sprintf("%s%d/keyframes", base, mixaID), map[string]any{"name": "mixa_wave", "keyframes": doc})
	if code != http.StatusCreated {
		t.Fatalf("POST keyframes: %d %v", code, got)
	}
	waveID := int64(got["id"].(float64))
	if got["num_channels"].(float64) != 4 || got["duration_ticks"].(float64) != 11 {
		t.Fatalf("authored clip shape: %v", got)
	}
	code, got = doJSON(t, h, http.MethodGet, fmt.Sprintf("%s%d/keyframes", base, waveID), nil)
	if code != http.StatusOK || got["source"] != "stored" {
		t.Fatalf("authored clip should reopen with its stored keys: %d %v", code, got)
	}
	// Bad joint → 400, nothing stored.
	bad := map[string]any{"tick_rate": 30, "duration_ticks": 5, "tracks": []any{map[string]any{"joint": "nope", "rotation": []any{map[string]any{"tick": 0, "q": []float64{0, 0, 0, 1}}}}}}
	if code, _ = doJSON(t, h, http.MethodPost, fmt.Sprintf("%s%d/keyframes", base, mixaID), map[string]any{"name": "bad", "keyframes": bad}); code != http.StatusBadRequest {
		t.Fatalf("bad joint should 400, got %d", code)
	}

	// Bone map preview.
	code, got = doJSON(t, h, http.MethodGet, fmt.Sprintf("%s%d/bone-map?target=%d", base, mixaID, unrealID), nil)
	if code != http.StatusOK || got["map"].(map[string]any)["mixamorig:LeftForeArm"] != "lowerarm_l" {
		t.Fatalf("bone map: %d %v", code, got)
	}

	// Remap the Mixamo mesh onto the Unreal rig.
	code, got = doJSON(t, h, http.MethodPost, fmt.Sprintf("%s%d/remap-mesh", base, mixaID), map[string]any{"target_id": unrealID, "name": "mixa_on_unreal"})
	if code != http.StatusCreated {
		t.Fatalf("remap-mesh: %d %v", code, got)
	}
	remapped := got["animation"].(map[string]any)
	row, err := h.Store.GetAnimation(context.Background(), int64(remapped["id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	skel, mesh, _, err := nock.DecodeAsset(row)
	if err != nil || skel == nil || mesh == nil {
		t.Fatalf("remapped row should carry rig + mesh: %v", err)
	}
	if skel.Joints[3].Name != "lowerarm_l" || mesh.Vertices[0].BoneIndices[0] != 3 || mesh.Vertices[0].BoneIndices[1] != 4 {
		t.Fatalf("remapped weights: %v %v", mesh.Vertices[0].BoneIndices, mesh.Vertices[0].BoneWeights)
	}
	if got["report"].(map[string]any)["vertices_by_name"].(float64) != 3 {
		t.Fatalf("report: %v", got["report"])
	}

	// Retarget the authored wave onto the remapped character, then check it lands as a clip on
	// the Unreal rig.
	code, got = doJSON(t, h, http.MethodPost, fmt.Sprintf("%s%d/retarget", base, waveID), map[string]any{"target_id": row.ID, "name": "unreal_wave"})
	if code != http.StatusCreated {
		t.Fatalf("retarget: %d %v", code, got)
	}
	rt := got["animation"].(map[string]any)
	rrow, _ := h.Store.GetAnimation(context.Background(), int64(rt["id"].(float64)))
	_, rmesh, rclip, err := nock.DecodeAsset(rrow)
	if err != nil || rclip == nil || rmesh == nil {
		t.Fatalf("retargeted row should carry mesh + clip: %v", err)
	}
	if rclip.Channels[0] != "upperarm_l.qx" {
		t.Fatalf("retargeted channels: %v", rclip.Channels)
	}
	if rrow.SkeletonHash != row.SkeletonHash {
		t.Fatal("retargeted clip must carry the destination rig's skeleton hash")
	}
}
