package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/shankpit"
)

// twoCubeGLTF: one unit cube mesh (base64 embedded buffer) placed by a "Wall" node (scaled
// 4x3x0.5) and a "Door" node translated to x=2 -- the Blender-blockout shape this bridge targets.
const twoCubeGLTF = `{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0,1]}],
"buffers":[{"byteLength":96,"uri":"data:application/octet-stream;base64,AAAAvwAAAL8AAAC/AAAAPwAAAL8AAAC/AAAAvwAAAD8AAAC/AAAAPwAAAD8AAAC/AAAAvwAAAL8AAAA/AAAAPwAAAL8AAAA/AAAAvwAAAD8AAAA/AAAAPwAAAD8AAAA/"}],
"bufferViews":[{"buffer":0,"byteLength":96}],
"accessors":[{"bufferView":0,"componentType":5126,"count":8,"type":"VEC3"}],
"meshes":[{"name":"Cube","primitives":[{"attributes":{"POSITION":0}}]}],
"nodes":[{"name":"Wall","mesh":0,"scale":[4,3,0.5]},{"name":"Door","mesh":0,"translation":[2,0,0]}]}`

func newWidgetsTestHandler(t *testing.T) *handlers.ShankpitWidgetsHandler {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE shankpit_widgets (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
		walls_json TEXT NOT NULL DEFAULT '[]', doors_json TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return &handlers.ShankpitWidgetsHandler{Store: &shankpit.WidgetStore{DB: db}}
}

func postWidgetGLTF(t *testing.T, h http.Handler, fields map[string]string, file string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		mw.WriteField(k, v) //nolint:errcheck
	}
	if file != "" {
		fw, _ := mw.CreateFormFile("file", "blockout.gltf")
		fw.Write([]byte(file)) //nolint:errcheck
	}
	mw.Close() //nolint:errcheck
	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/shankpit-widgets/import-gltf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestShankpitWidgetsHandler_ImportGLTFCreatesWidget(t *testing.T) {
	h := newWidgetsTestHandler(t)
	rec := postWidgetGLTF(t, h, map[string]string{"name": "GLTF_BLOCKOUT"}, twoCubeGLTF)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var wd shankpit.Widget
	if err := json.Unmarshal(rec.Body.Bytes(), &wd); err != nil {
		t.Fatal(err)
	}
	if len(wd.Walls) != 2 || wd.Walls[0].Name != "Wall" || wd.Walls[0].SX != 4 || wd.Walls[0].SY != 3 || wd.Walls[0].SZ != 0.5 {
		t.Fatalf("unexpected walls: %+v", wd.Walls)
	}
	if wd.Walls[1].X != 2 || wd.Walls[1].Friction != 0.3 {
		t.Errorf("unexpected door wall: %+v", wd.Walls[1])
	}
	if len(wd.Doors) != 1 || wd.Doors[0].WallID != wd.Walls[1].ID || wd.Doors[0].ScriptID != 0 {
		t.Errorf("expected one scriptless door on the Door wall, got %+v", wd.Doors)
	}
	// It's a real widget row now -- the list sees it.
	lrec := httptest.NewRecorder()
	h.ServeHTTP(lrec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/shankpit-widgets", nil))
	var list []shankpit.WidgetSummary
	json.Unmarshal(lrec.Body.Bytes(), &list) //nolint:errcheck
	if len(list) != 1 || list[0].WallCount != 2 || list[0].DoorCount != 1 {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestShankpitWidgetsHandler_ImportGLTFPreviewSavesNothing(t *testing.T) {
	h := newWidgetsTestHandler(t)
	rec := postWidgetGLTF(t, h, map[string]string{"preview": "1", "scale": "2"}, twoCubeGLTF)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Walls []shankpit.Wall `json:"walls"`
		Doors []shankpit.Door `json:"doors"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out) //nolint:errcheck
	if len(out.Walls) != 2 || out.Walls[0].SX != 8 || out.Walls[1].X != 4 {
		t.Errorf("unexpected scaled preview: %+v", out.Walls)
	}
	lrec := httptest.NewRecorder()
	h.ServeHTTP(lrec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/shankpit-widgets", nil))
	if lrec.Body.String() != "[]\n" && lrec.Body.String() != "[]" {
		t.Errorf("preview must not save a widget, list = %s", lrec.Body.String())
	}
}

func TestShankpitWidgetsHandler_ImportGLTFErrors(t *testing.T) {
	h := newWidgetsTestHandler(t)
	if rec := postWidgetGLTF(t, h, map[string]string{"name": "X"}, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("missing file: expected 400, got %d", rec.Code)
	}
	if rec := postWidgetGLTF(t, h, map[string]string{"name": "X"}, "not gltf"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("garbage file: expected 422, got %d", rec.Code)
	}
	if rec := postWidgetGLTF(t, h, map[string]string{"name": "X", "scale": "-1"}, twoCubeGLTF); rec.Code != http.StatusBadRequest {
		t.Errorf("bad scale: expected 400, got %d", rec.Code)
	}
}
