package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postUpload sends one multipart request with a "file" part to h.upload.
func postUpload(t *testing.T, h *DriveSlurpHandler, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/drive-slurp/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.upload(rec, req)
	return rec
}

func TestDriveSlurpUpload_SavesAndDedupes(t *testing.T) {
	root := t.TempDir()
	h := &DriveSlurpHandler{IdunaRoot: root}
	content := []byte("hearthstone dbf bytes\x00\x01\x02")

	rec := postUpload(t, h, "dbf.unity3d", content)
	if rec.Code != http.StatusOK {
		t.Fatalf("first upload: status %d body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "saved" {
		t.Fatalf("first upload status = %v, want saved", resp["status"])
	}
	saved := resp["saved_path"].(string)
	got, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("saved file missing: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("saved content differs from upload")
	}
	if !strings.HasPrefix(filepath.Base(saved), resp["job_id"].(string)+"-") {
		t.Fatalf("saved name %q not prefixed by job id", saved)
	}
	if _, err := os.Stat(h.jobsLogPath()); err != nil {
		t.Fatalf("job log not written: %v", err)
	}

	// Same bytes again, different name: must not write a second copy.
	rec2 := postUpload(t, h, "renamed.unity3d", content)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"duplicate"`) {
		t.Fatalf("re-upload: status %d body %s, want duplicate", rec2.Code, rec2.Body.String())
	}
	entries, _ := os.ReadDir(h.slurpDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upload-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if n := len(entries); n != 1 {
		t.Fatalf("slurp dir has %d entries after duplicate upload, want 1", n)
	}
}

func TestDriveSlurpUpload_RejectsNonMultipart(t *testing.T) {
	h := &DriveSlurpHandler{IdunaRoot: t.TempDir()}
	req := httptest.NewRequest(http.MethodPost, "/admin/drive-slurp/upload", strings.NewReader("raw"))
	rec := httptest.NewRecorder()
	h.upload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

// The upload drop zone and its script must render while Drive is NOT
// connected: the script was once nested inside the connected-only branch,
// so the zone rendered as inert HTML and clicks/drops did nothing.
func TestDriveSlurpPage_UploadScriptRendersWhenDisconnected(t *testing.T) {
	h := &DriveSlurpHandler{IdunaRoot: t.TempDir()}
	rec := httptest.NewRecorder()
	h.page(rec, httptest.NewRequest(http.MethodGet, "/admin/drive-slurp", nil))
	body := rec.Body.String()
	for _, want := range []string{`id="dropzone"`, `id="upload-input"`, "<script>", "/admin/drive-slurp/upload"} {
		if !strings.Contains(body, want) {
			t.Fatalf("disconnected page missing %q", want)
		}
	}
}
