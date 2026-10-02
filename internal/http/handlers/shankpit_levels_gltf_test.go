package handlers_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/shankpit"
)

// separateGLTF is the Blender "glTF Separate" shape: the geometry lives in a sibling .bin whose
// name is percent-encoded in the .gltf's own buffer uri.
const separateGLTF = `{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0,1]}],
"buffers":[{"byteLength":96,"uri":"My%20Level.bin"}],
"bufferViews":[{"buffer":0,"byteLength":96}],
"accessors":[{"bufferView":0,"componentType":5126,"count":8,"type":"VEC3"}],
"meshes":[{"name":"Cube","primitives":[{"attributes":{"POSITION":0}}]}],
"nodes":[{"name":"Wall","mesh":0,"scale":[4,3,0.5]},{"name":"door_front","mesh":0,"translation":[2,0,0]}]}`

func unitCubeBin() []byte {
	var buf []byte
	for _, c := range [][3]float32{{-.5, -.5, -.5}, {.5, -.5, -.5}, {-.5, .5, -.5}, {.5, .5, -.5}, {-.5, -.5, .5}, {.5, -.5, .5}, {-.5, .5, .5}, {.5, .5, .5}} {
		for _, v := range c {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, math.Float32bits(v))
			buf = append(buf, b...)
		}
	}
	return buf
}

func postLevelGLTF(t *testing.T, fields map[string]string, gltf string, resources map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		mw.WriteField(k, v) //nolint:errcheck
	}
	fw, _ := mw.CreateFormFile("file", "level.gltf")
	fw.Write([]byte(gltf)) //nolint:errcheck
	for name, data := range resources {
		rw, _ := mw.CreateFormFile("resource", name)
		rw.Write(data) //nolint:errcheck
	}
	mw.Close() //nolint:errcheck
	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/shankpit-levels/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	// Import is stateless (the editor merges the result and saves through the normal path), so no store is needed.
	(&handlers.ShankpitLevelsHandler{}).ServeHTTP(rec, req)
	return rec
}

func TestShankpitLevelsHandler_ImportGLTFSeparateBin(t *testing.T) {
	rec := postLevelGLTF(t, map[string]string{"scale": "2"}, separateGLTF, map[string][]byte{"My Level.bin": unitCubeBin()})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Walls []shankpit.Wall `json:"walls"`
		Doors []shankpit.Door `json:"doors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Walls) != 2 || out.Walls[0].SX != 8 || out.Walls[0].SY != 6 || out.Walls[1].X != 4 {
		t.Fatalf("unexpected scaled walls: %+v", out.Walls)
	}
	if len(out.Doors) != 1 || out.Doors[0].WallID != out.Walls[1].ID {
		t.Errorf("door_front should become a door on its wall: %+v", out.Doors)
	}
}

func TestShankpitLevelsHandler_ImportGLTFMissingBinNamesIt(t *testing.T) {
	rec := postLevelGLTF(t, nil, separateGLTF, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "My Level.bin") {
		t.Fatalf("expected 422 naming the missing .bin, got %d: %s", rec.Code, rec.Body.String())
	}
}
