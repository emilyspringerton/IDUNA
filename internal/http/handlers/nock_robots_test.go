package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"

	_ "modernc.org/sqlite"
)

func TestNockRobotsUploadGetDownload(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	mig, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "truestore", "202609270001_nock_robots.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(mig)); err != nil {
		t.Fatal(err)
	}
	h := &handlers.NockRobotsHandler{Store: &nock.RobotStore{DB: db}}
	td := filepath.Join("..", "..", "nock", "testdata")
	read := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join(td, n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	spec, grobot := read("ur5e.grobot.json"), read("ur5e.grobot")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for field, data := range map[string][]byte{"spec": spec, "grobot": grobot, "gskel": read("ur5e.gskel")} {
		fw, _ := mw.CreateFormFile(field, field)
		fw.Write(data)
	}
	mw.WriteField("source_location", "test")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/nock/api/robots", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID  int64 `json:"id"`
		Rig struct {
			Joints []struct {
				Name   string  `json:"name"`
				Effort float64 `json:"effort"`
			} `json:"joints"`
		} `json:"rig"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == 0 || len(created.Rig.Joints) != 6 || created.Rig.Joints[0].Effort != 150 {
		t.Fatalf("created: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/robots/1/grobot", nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), grobot) {
		t.Fatalf("grobot download: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/robots/1/spec", nil))
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), spec) {
		t.Fatalf("spec download: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/nock/api/robots", nil))
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(`"UR5e"`)) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	// Missing compiled rig -> 400, not a half-stored row.
	body.Reset()
	mw = multipart.NewWriter(&body)
	mw.WriteField("spec", string(spec))
	mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/admin/nock/api/robots", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upload without grobot: %d", rec.Code)
	}
}
