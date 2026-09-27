package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/nock"
	"iduna/internal/store"
)

func newVideoHandlers(t *testing.T) (*NockVideosHandler, *NockPhoneUploadHandler) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../../migrations/truestore"); err != nil {
		t.Fatal(err)
	}
	s := &nock.VideoStore{DB: db, Dir: t.TempDir()}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Wait)
	return &NockVideosHandler{Store: s, BaseURL: "https://okemily.com", MaxBytes: 50 << 20},
		&NockPhoneUploadHandler{Store: s, MaxBytes: 50 << 20}
}

func testClip(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "IMG_4242.MOV")
	b, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", out).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func multipartBody(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// End to end over HTTP: mint a phone link, open the phone page, upload from "the phone", see
// it in the admin library, cut it on a timeline, render, download.
func TestNockVideoPhoneToRenderFlow(t *testing.T) {
	admin, phone := newVideoHandlers(t)
	clip := testClip(t)

	rec := doJSON(t, admin, http.MethodPost, "/admin/nock/api/video-upload-links", map[string]any{"label": "field cam", "ttl_minutes": 30, "max_uploads": 2})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: %d %s", rec.Code, rec.Body)
	}
	var link struct {
		ID        int64  `json:"id"`
		Token     string `json:"token"`
		UploadURL string `json:"upload_url"`
		Active    bool   `json:"active"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &link)
	if !link.Active || link.UploadURL != "https://okemily.com/nock/upload/"+link.Token {
		t.Fatalf("link: %+v", link)
	}

	rec = doJSON(t, admin, http.MethodGet, "/admin/nock/api/video-upload-links/"+strconv.FormatInt(link.ID, 10)+"/qr", nil)
	if rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("qr: %d", rec.Code)
	}

	// Phone opens the page.
	rec = doJSON(t, phone, http.MethodGet, "/nock/upload/"+link.Token, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Choose videos") || !strings.Contains(rec.Body.String(), "field cam") {
		t.Fatalf("phone page: %d", rec.Code)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "nonce-") {
		t.Fatal("phone page missing token-protecting headers")
	}

	// Phone uploads.
	body, ct := multipartBody(t, "IMG_4242.MOV", clip)
	req := httptest.NewRequest(http.MethodPost, "/nock/upload/"+link.Token, body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	phone.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("phone upload: %d %s", rec.Code, rec.Body)
	}

	// Non-video from the phone is refused with 415.
	body, ct = multipartBody(t, "x.mp4", []byte("not a video"))
	req = httptest.NewRequest(http.MethodPost, "/nock/upload/"+link.Token, body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	phone.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", rec.Code)
	}

	// Bad token -> 410 page, and 410 on POST.
	rec = doJSON(t, phone, http.MethodGet, "/nock/upload/bogus", nil)
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("bogus page: %d", rec.Code)
	}

	// Admin sees it.
	admin.Store.Wait()
	rec = doJSON(t, admin, http.MethodGet, "/admin/nock/api/videos", nil)
	var vids []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Source      string `json:"source"`
		ProxyStatus string `json:"proxy_status"`
		HasThumb    bool   `json:"has_thumb"`
		DurationMS  int64  `json:"duration_ms"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &vids)
	if len(vids) != 1 || vids[0].Name != "IMG_4242" || vids[0].Source != "phone" || vids[0].ProxyStatus != "ready" || !vids[0].HasThumb {
		t.Fatalf("library: %s", rec.Body)
	}
	vid := vids[0].ID

	// Range request against the proxy (what <video> seeking sends).
	req = httptest.NewRequest(http.MethodGet, "/admin/nock/api/videos/"+strconv.FormatInt(vid, 10)+"/proxy", nil)
	req.Header.Set("Range", "bytes=0-99")
	rec = httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 || rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("range: %d len=%d", rec.Code, rec.Body.Len())
	}

	// Timeline: create, cut, render, download.
	rec = doJSON(t, admin, http.MethodPost, "/admin/nock/api/video-timelines", map[string]any{"name": "phone_cut"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body)
	}
	var tl struct {
		ID           int64  `json:"id"`
		RenderStatus string `json:"render_status"`
		RenderError  string `json:"render_error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tl)
	rec = doJSON(t, admin, http.MethodPut, "/admin/nock/api/video-timelines/"+strconv.FormatInt(tl.ID, 10), map[string]any{
		"edl": map[string]any{"width": 320, "height": 240, "fps": 30, "segments": []map[string]any{
			{"clip_id": vid, "in_ms": 0, "out_ms": 800}, {"clip_id": vid, "in_ms": 1200, "out_ms": 2000},
		}},
	})
	if rec.Code != 200 {
		t.Fatalf("save edl: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, admin, http.MethodPost, "/admin/nock/api/video-timelines/"+strconv.FormatInt(tl.ID, 10)+"/render", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("render: %d %s", rec.Code, rec.Body)
	}
	admin.Store.Wait()
	rec = doJSON(t, admin, http.MethodGet, "/admin/nock/api/video-timelines/"+strconv.FormatInt(tl.ID, 10), nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &tl)
	if tl.RenderStatus != "ready" {
		t.Fatalf("render status %s: %s", tl.RenderStatus, tl.RenderError)
	}
	rec = doJSON(t, admin, http.MethodGet, "/admin/nock/api/video-timelines/"+strconv.FormatInt(tl.ID, 10)+"/output?download=1", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), `phone_cut.mp4`) || rec.Body.Len() < 1000 {
		t.Fatalf("output: %d %q %d", rec.Code, rec.Header().Get("Content-Disposition"), rec.Body.Len())
	}

	// Revoke -> phone page goes dead.
	rec = doJSON(t, admin, http.MethodDelete, "/admin/nock/api/video-upload-links/"+strconv.FormatInt(link.ID, 10), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	rec = doJSON(t, phone, http.MethodGet, "/nock/upload/"+link.Token, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("revoked page: %d", rec.Code)
	}
}

func TestNockVideoUploadTooLarge(t *testing.T) {
	admin, _ := newVideoHandlers(t)
	admin.MaxBytes = 1000
	body, ct := multipartBody(t, "big.mp4", make([]byte, 5<<20))
	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/videos", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d %s", rec.Code, rec.Body)
	}
}
