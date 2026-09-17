package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
)

func newAnimationsTestHandler(t *testing.T) *handlers.NockAnimationsHandler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE nock_animations (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			tick_rate       INTEGER,
			duration_ticks  INTEGER,
			num_channels    INTEGER,
			content_hash    TEXT,
			gband_data      BLOB,
			manifest_json   TEXT,
			gskel_data      BLOB,
			gmesh_data      BLOB,
			skeleton_hash   TEXT,
			source_location TEXT,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create nock_animations: %v", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_nock_animations_name ON nock_animations(name)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	return &handlers.NockAnimationsHandler{Store: &nock.AnimStore{DB: db}}
}

// buildTinyGLB is a minimal, real, single-rotation-channel .glb -- enough to exercise the real
// importGLTF HTTP route end to end (multipart parse -> nock.ImportGLTFBytes -> AnimStore.Create).
func buildTinyGLB(t *testing.T) []byte {
	t.Helper()
	var buf []byte
	appendF32 := func(vals ...float32) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
		return off
	}
	timesOff := appendF32(0, 1)
	timesLen := 2 * 4
	half := float32(math.Sqrt(0.5))
	rotOff := appendF32(0, 0, 0, 1, 0, 0, half, half)
	rotLen := 2 * 4 * 4

	type bufferView struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type accessor struct {
		BufferView    int    `json:"bufferView"`
		ComponentType int    `json:"componentType"`
		Count         int    `json:"count"`
		Type          string `json:"type"`
	}
	const gltfFloat = 5126
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []map[string]any{{"byteLength": len(buf)}},
		"bufferViews": []bufferView{{0, timesOff, timesLen}, {0, rotOff, rotLen}},
		"accessors":   []accessor{{0, gltfFloat, 2, "SCALAR"}, {1, gltfFloat, 2, "VEC4"}},
		"nodes":       []map[string]any{{"name": "root"}},
		"animations": []map[string]any{{
			"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 0, "path": "rotation"}}},
			"samplers": []map[string]any{{"input": 0, "output": 1}},
		}},
	}
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	var glb []byte
	appendChunk := func(chunkType uint32, data []byte) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(hdr[4:8], chunkType)
		glb = append(glb, hdr...)
		glb = append(glb, data...)
	}
	header := make([]byte, 12)
	const glbMagic = 0x46546C67
	binary.LittleEndian.PutUint32(header[0:4], glbMagic)
	binary.LittleEndian.PutUint32(header[4:8], 2)
	glb = append(glb, header...)
	appendChunk(0x4E4F534A, jsonBytes) // "JSON"
	appendChunk(0x004E4942, buf)       // "BIN\0"
	binary.LittleEndian.PutUint32(glb[8:12], uint32(len(glb)))
	return glb
}

// buildTwoClipGLB is buildTinyGLB's own real multi-clip sibling -- same bare-node rotation
// fixture, but with TWO real, separately-named animations reusing the same accessors (glTF's own
// spec permits this). Mirrors the real shape a Quaternius-style "Universal Animation Library"
// file has (S459-109, founder confirmed a real multi-clip upload).
func buildTwoClipGLB(t *testing.T) []byte {
	t.Helper()
	var buf []byte
	appendF32 := func(vals ...float32) int {
		off := len(buf)
		for _, v := range vals {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
		return off
	}
	timesOff := appendF32(0, 1)
	timesLen := 2 * 4
	half := float32(math.Sqrt(0.5))
	rotOff := appendF32(0, 0, 0, 1, 0, 0, half, half)
	rotLen := 2 * 4 * 4

	type bufferView struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type accessor struct {
		BufferView    int    `json:"bufferView"`
		ComponentType int    `json:"componentType"`
		Count         int    `json:"count"`
		Type          string `json:"type"`
	}
	const gltfFloat = 5126
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0"},
		"buffers":     []map[string]any{{"byteLength": len(buf)}},
		"bufferViews": []bufferView{{0, timesOff, timesLen}, {0, rotOff, rotLen}},
		"accessors":   []accessor{{0, gltfFloat, 2, "SCALAR"}, {1, gltfFloat, 2, "VEC4"}},
		"nodes":       []map[string]any{{"name": "root"}},
		"animations": []map[string]any{
			{
				"name":     "Idle",
				"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 0, "path": "rotation"}}},
				"samplers": []map[string]any{{"input": 0, "output": 1}},
			},
			{
				"name":     "Jump Start", // deliberately has a space -- real glTF clip names aren't
				"channels": []map[string]any{{"sampler": 0, "target": map[string]any{"node": 0, "path": "rotation"}}}, // constrained to NOCK's own stricter row-name pattern
				"samplers": []map[string]any{{"input": 0, "output": 1}},
			},
		},
	}
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	var glb []byte
	appendChunk := func(chunkType uint32, data []byte) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(hdr[4:8], chunkType)
		glb = append(glb, hdr...)
		glb = append(glb, data...)
	}
	header := make([]byte, 12)
	const glbMagic = 0x46546C67
	binary.LittleEndian.PutUint32(header[0:4], glbMagic)
	binary.LittleEndian.PutUint32(header[4:8], 2)
	glb = append(glb, header...)
	appendChunk(0x4E4F534A, jsonBytes) // "JSON"
	appendChunk(0x004E4942, buf)       // "BIN\0"
	binary.LittleEndian.PutUint32(glb[8:12], uint32(len(glb)))
	return glb
}

// TestNockAnimationsHandler_ImportGLTFMultiClip is the real regression test for the 2026-09-17
// fix (founder: confirmed a real multi-clip upload -- Quaternius's "Universal Animation Library"
// -- had every clip past the first silently discarded on import).
func TestNockAnimationsHandler_ImportGLTFMultiClip(t *testing.T) {
	h := newAnimationsTestHandler(t)
	glb := buildTwoClipGLB(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "trick-pack") //nolint:errcheck
	fw, err := mw.CreateFormFile("file", "pack.glb")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(glb) //nolint:errcheck
	mw.Close()    //nolint:errcheck

	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/animations/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import-gltf (multi-clip): expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID              int64 `json:"id"`
		AdditionalClips []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"additional_clips"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.AdditionalClips) != 1 {
		t.Fatalf("expected exactly 1 additional clip in the response, got %d: %+v", len(resp.AdditionalClips), resp.AdditionalClips)
	}
	if resp.AdditionalClips[0].Name != "trick-pack_Jump_Start" {
		t.Errorf(`expected the sanitized name "trick-pack_Jump_Start", got %q`, resp.AdditionalClips[0].Name)
	}

	// The additional clip must be a real, independently listable row -- not just present in the
	// import response.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/admin/nock/api/animations", nil))
	var list []nock.AnimationSummary
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 real rows (primary + 1 additional clip), got %d: %+v", len(list), list)
	}
}

func TestNockAnimationsHandler_ImportGLTFDragAndDrop(t *testing.T) {
	h := newAnimationsTestHandler(t)
	glb := buildTinyGLB(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "test-clip") //nolint:errcheck
	mw.WriteField("tick_rate", "2")    //nolint:errcheck
	fw, err := mw.CreateFormFile("file", "test.glb")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(glb) //nolint:errcheck
	mw.Close()    //nolint:errcheck

	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/animations/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import-gltf: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var created nock.Animation
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.Name != "test-clip" || created.TickRate == nil || *created.TickRate != 2 || created.DurationTicks == nil || *created.DurationTicks != 3 {
		t.Errorf("unexpected created animation: %+v", created)
	}

	// GET list should now show it.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/admin/nock/api/animations", nil))
	var list []nock.AnimationSummary
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "test-clip" {
		t.Errorf("unexpected list: %+v", list)
	}

	// Download the real .gband bytes back out and confirm the magic survived the round trip.
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/admin/nock/api/animations/1/gband", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("download gband: expected 200, got %d", rec3.Code)
	}
	if got := rec3.Body.Bytes(); len(got) < 4 || string(got[0:4]) != "GBND" {
		t.Errorf("downloaded gband doesn't start with GBND magic: %q", got)
	}
}

// buildMeshOnlyGLB builds a minimal, real .glb with a single node and no "animations" block at
// all -- the shape of a real rigged-mesh-with-no-baked-animation-yet upload (the founder's own
// real Mannequin_F.glb, 2026-09-17), which used to be hard-rejected with a 422.
func buildMeshOnlyGLB(t *testing.T) []byte {
	t.Helper()
	doc := map[string]any{
		"asset": map[string]any{"version": "2.0"},
		"nodes": []map[string]any{{"name": "root"}},
	}
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	var glb []byte
	appendChunk := func(chunkType uint32, data []byte) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(data)))
		binary.LittleEndian.PutUint32(hdr[4:8], chunkType)
		glb = append(glb, hdr...)
		glb = append(glb, data...)
	}
	header := make([]byte, 12)
	const glbMagic = 0x46546C67
	binary.LittleEndian.PutUint32(header[0:4], glbMagic)
	binary.LittleEndian.PutUint32(header[4:8], 2)
	glb = append(glb, header...)
	appendChunk(0x4E4F534A, jsonBytes) // "JSON"
	binary.LittleEndian.PutUint32(glb[8:12], uint32(len(glb)))
	return glb
}

// TestNockAnimationsHandler_ImportGLTFMeshOnlySucceeds is the real regression test for the
// 2026-09-17 fix ("Error: 422: ... no animation found in this file"). A glTF with no animated
// channel must now import successfully instead of hard-erroring.
func TestNockAnimationsHandler_ImportGLTFMeshOnlySucceeds(t *testing.T) {
	h := newAnimationsTestHandler(t)
	glb := buildMeshOnlyGLB(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "mannequin") //nolint:errcheck
	fw, err := mw.CreateFormFile("file", "mannequin.glb")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw.Write(glb) //nolint:errcheck
	mw.Close()    //nolint:errcheck

	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/animations/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import-gltf (mesh/skeleton only): expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var created nock.Animation
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.TickRate != nil || created.DurationTicks != nil {
		t.Errorf("expected nil animation fields for a mesh-only import, got %+v", created)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/admin/nock/api/animations", nil))
	var list []nock.AnimationSummary
	if err := json.Unmarshal(rec2.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].HasAnimation {
		t.Errorf("unexpected list: %+v", list)
	}
}

// TestNockAnimationsHandler_AttachAnimation is the real regression test for the 2026-09-17 fix
// ("build fill in the gaps... you can add animations to it later, either by uploading a separate
// file with the same rig"): a mesh-only row can have a matching-rig animation clip merged onto
// it in place, via either the "pick an existing library row" (JSON) or "upload a fresh file"
// (multipart) path.
func TestNockAnimationsHandler_AttachAnimation(t *testing.T) {
	h := newAnimationsTestHandler(t)

	importGLTF := func(name string, glb []byte) nock.Animation {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		mw.WriteField("name", name) //nolint:errcheck
		fw, err := mw.CreateFormFile("file", name+".glb")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		fw.Write(glb) //nolint:errcheck
		mw.Close()    //nolint:errcheck
		req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/animations/import-gltf", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("import-gltf %q: expected 201, got %d: %s", name, rec.Code, rec.Body.String())
		}
		var a nock.Animation
		if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
			t.Fatalf("unmarshal %q: %v", name, err)
		}
		return a
	}

	// Both fixtures use a bare "root" node with no real skin -- convertGLTF's own synthetic
	// single-root-joint fallback means they share the exact same real skeleton_hash, making this
	// a real, deterministic "same rig" match, not a coincidence-prone one.
	meshOnly := importGLTF("mesh-only", buildMeshOnlyGLB(t))
	animated := importGLTF("animated-source", buildTinyGLB(t))
	if meshOnly.TickRate != nil {
		t.Fatalf("expected the mesh-only import to have no animation yet, got %+v", meshOnly)
	}

	// Path 1: attach via JSON {"source_id": ...}, reusing an existing library row.
	jsonBody, _ := json.Marshal(map[string]int64{"source_id": animated.ID})
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/nock/api/animations/%d/attach-animation", meshOnly.ID), bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("attach-animation (json source_id): expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var attached nock.Animation
	if err := json.Unmarshal(rec.Body.Bytes(), &attached); err != nil {
		t.Fatalf("unmarshal attach response: %v", err)
	}
	if attached.ID != meshOnly.ID {
		t.Errorf("expected attach-animation to update the SAME row (id %d), got id %d", meshOnly.ID, attached.ID)
	}
	if attached.TickRate == nil || *attached.TickRate != 30 {
		t.Errorf("expected the attached animation's real tick_rate (30, this import's default) to carry over, got %+v", attached)
	}

	// Path 2: attach via a fresh multipart glTF upload directly.
	meshOnly2 := importGLTF("mesh-only-2", buildMeshOnlyGLB(t))
	var body2 bytes.Buffer
	mw2 := multipart.NewWriter(&body2)
	fw2, err := mw2.CreateFormFile("file", "clip.glb")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	fw2.Write(buildTinyGLB(t)) //nolint:errcheck
	mw2.Close()                //nolint:errcheck
	req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/nock/api/animations/%d/attach-animation", meshOnly2.ID), &body2)
	req2.Header.Set("Content-Type", mw2.FormDataContentType())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("attach-animation (multipart upload): expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestNockAnimationsHandler_ImportGLTFRejectsGarbage(t *testing.T) {
	h := newAnimationsTestHandler(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "bad") //nolint:errcheck
	fw, _ := mw.CreateFormFile("file", "bad.glb")
	fw.Write([]byte("not a real glb")) //nolint:errcheck
	mw.Close()                         //nolint:errcheck

	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/animations/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for garbage glTF, got %d: %s", rec.Code, rec.Body.String())
	}
}
