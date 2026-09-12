package handlers_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
)

func requireConvertForHandlerTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("convert"); err != nil {
		t.Skip("ImageMagick 'convert' not installed, skipping real image test")
	}
}

func newNockTestHandler(t *testing.T) *handlers.NockHandler {
	t.Helper()
	svc, err := nock.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &handlers.NockHandler{Svc: svc}
}

func TestNockHandler_CreateAndListProjects(t *testing.T) {
	h := newNockTestHandler(t)

	body, _ := json.Marshal(map[string]any{"name": "tex1", "width": 64, "height": 64})
	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/admin/nock/api/projects", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec2.Code)
	}
	var names []string
	if err := json.Unmarshal(rec2.Body.Bytes(), &names); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(names) != 1 || names[0] != "tex1" {
		t.Fatalf("unexpected project list: %+v", names)
	}
}

func TestNockHandler_AddGradientLayerAndExport(t *testing.T) {
	requireConvertForHandlerTest(t)
	h := newNockTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{"name": "tex1", "width": 32, "height": 32})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects", bytes.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	gradBody, _ := json.Marshal(map[string]any{"name": "sky", "from": "#000000", "to": "#ffffff", "direction": "vertical"})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects/tex1/gradient", bytes.NewReader(gradBody)))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("add gradient: %d %s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/admin/nock/api/projects/tex1/export?format=png", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec3.Code, rec3.Body.String())
	}
	if ct := rec3.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected image/png content type, got %q", ct)
	}
	if rec3.Body.Len() == 0 {
		t.Error("expected non-empty exported image body")
	}
}

func TestNockHandler_AddLayerViaMultipartUpload(t *testing.T) {
	requireConvertForHandlerTest(t)
	h := newNockTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{"name": "tex1", "width": 16, "height": 16})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects", bytes.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	// Build a real, tiny PNG via ImageMagick to upload, rather than a fake byte blob -- this
	// exercises the real AddLayer/imFitToCanvas path, not just multipart parsing.
	pngPath := t.TempDir() + "/src.png"
	if err := exec.Command("convert", "-size", "8x8", "xc:green", pngPath).Run(); err != nil {
		t.Fatalf("make test png: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "base")
	fw, err := mw.CreateFormFile("file", "src.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	pngData, err := os.ReadFile(pngPath)
	if err != nil {
		t.Fatalf("read test png: %v", err)
	}
	fw.Write(pngData)
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects/tex1/layers", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("add layer: %d %s", rec2.Code, rec2.Body.String())
	}
	var p nock.Project
	if err := json.Unmarshal(rec2.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Layers) != 1 || p.Layers[0].Name != "base" {
		t.Fatalf("unexpected layers: %+v", p.Layers)
	}
}

const nockTestCheckerSource = `(module gentexture)
(import math)

(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (if (> (math/cos (* (/ x w) 40.0)) 0.0) 0.85 0.15))

(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (if (> (math/cos (* (/ y h) 40.0)) 0.0) 0.85 0.15))

(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64
  (math/sqrt (/ x w)))
`

func requireProcGenForHandlerTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("parena"); err != nil {
		if _, statErr := exec.LookPath("/home/fatbaby/PARENA/parena"); statErr != nil {
			t.Skip("parena compiler not available, skipping real procedural-texture handler test")
		}
	}
}

func TestNockHandler_AddProceduralLayer(t *testing.T) {
	requireProcGenForHandlerTest(t)
	h := newNockTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{"name": "tex1", "width": 32, "height": 32})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects", bytes.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	procBody, _ := json.Marshal(map[string]any{"name": "checker", "source": nockTestCheckerSource})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects/tex1/procedural", bytes.NewReader(procBody)))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("add procedural layer: %d %s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/admin/nock/api/projects/tex1/layers/checker/procedural", nil))
	if rec3.Code != http.StatusOK {
		t.Fatalf("get procedural source: %d %s", rec3.Code, rec3.Body.String())
	}
	var got map[string]string
	json.Unmarshal(rec3.Body.Bytes(), &got)
	if !bytes.Contains([]byte(got["source"]), []byte("pixel-r")) {
		t.Errorf("expected source to round-trip, got: %s", got["source"])
	}
}

func TestNockHandler_AddProceduralLayer_RejectsTargetEscape(t *testing.T) {
	h := newNockTestHandler(t)

	createBody, _ := json.Marshal(map[string]any{"name": "tex1", "width": 16, "height": 16})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects", bytes.NewReader(createBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	evilSrc := `(module gentexture) #target {:c (inline-c "system(\"echo pwned\")")}`
	procBody, _ := json.Marshal(map[string]any{"name": "evil", "source": evilSrc})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/admin/nock/api/projects/tex1/procedural", bytes.NewReader(procBody)))
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting #target, got %d %s", rec2.Code, rec2.Body.String())
	}
}
