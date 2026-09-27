package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/http/handlers"
	"iduna/internal/nock"
	"iduna/internal/store"
)

// Uses the real migrations (not an inline CREATE TABLE) so the schema under test is the one
// production runs.
func newSoundsTest(t *testing.T) (*handlers.NockSoundsHandler, *handlers.NockSoundFiltersPublicHandler) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := store.RunSQLiteMigrations(db, "../../../migrations/truestore"); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	st := &nock.SoundStore{DB: db}
	return &handlers.NockSoundsHandler{Store: st}, &handlers.NockSoundFiltersPublicHandler{Store: st}
}

func do(t *testing.T, h http.Handler, method, url, ctype string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNockSounds_UploadRecordAndStream(t *testing.T) {
	h, _ := newSoundsTest(t)
	wav := append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 64)...)

	rec := do(t, h, "POST", "/admin/nock/api/sounds?name=kick_01&source=upload&duration_ms=250&sample_rate=48000&channels=1", "audio/wav", wav)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var s nock.Sound
	json.Unmarshal(rec.Body.Bytes(), &s) //nolint:errcheck
	if s.SizeBytes != int64(len(wav)) || s.SampleRate != 48000 || len(s.ContentHash) != 64 {
		t.Fatalf("metadata: %+v", s)
	}

	// A MediaRecorder recording (codecs parameter on the MIME type) is accepted as-is.
	rec = do(t, h, "POST", "/admin/nock/api/sounds?name=mic_take_1&source=record", "audio/webm;codecs=opus", []byte("webm-bytes"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("record: %d %s", rec.Code, rec.Body)
	}

	rec = do(t, h, "GET", "/admin/nock/api/sounds/1/audio", "", nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), wav) || rec.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("stream: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = do(t, h, "GET", "/admin/nock/api/sounds", "", nil)
	if !strings.Contains(rec.Body.String(), "mic_take_1") || strings.Contains(rec.Body.String(), "audio_data") {
		t.Fatalf("list should carry metadata only: %s", rec.Body)
	}

	for _, bad := range []struct{ url, ctype string }{
		{"/admin/nock/api/sounds?name=x1", "image/png"},               // not audio
		{"/admin/nock/api/sounds?name=kick_01", "audio/wav"},          // duplicate name
		{"/admin/nock/api/sounds?name=../etc", "audio/wav"},           // invalid name
		{"/admin/nock/api/sounds?name=x2&source=stolen", "audio/wav"}, // bad source
	} {
		if rec := do(t, h, "POST", bad.url, bad.ctype, []byte("x")); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: want 400, got %d", bad.url, bad.ctype, rec.Code)
		}
	}

	if rec := do(t, h, "PATCH", "/admin/nock/api/sounds/2", "application/json", []byte(`{"name":"vocal_take"}`)); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "DELETE", "/admin/nock/api/sounds/2", "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestNockSoundFilters_PassAround(t *testing.T) {
	h, pub := newSoundsTest(t)
	chain := `{"version":1,"stages":[
		{"type":"biquad","kind":1,"freq":80,"q":0.707},
		{"type":"expander","threshold_db":-45,"ratio":2,"range_db":-27},
		{"type":"compressor","threshold_db":-20,"ratio":3,"knee_db":4,"attack_ms":10,"release_ms":200},
		{"type":"deesser","freq":6000,"intensity":0.5},
		{"type":"limiter","ceiling_db":-1},
		{"type":"normalize","target_lufs":-18}]}`
	body := `{"name":"podcast_voice","description":"jivetalking-style voice chain","chain":` + chain + `}`
	rec := do(t, h, "POST", "/admin/nock/api/sound-filters", "application/json", []byte(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// Pass it around: clone, then an engine fetches it by name with no login.
	if rec := do(t, h, "POST", "/admin/nock/api/sound-filters/1/clone", "application/json", []byte(`{"name":"podcast_voice_dark"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("clone: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, pub, "GET", "/api/v1/nock-sound-filters/podcast_voice_dark", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deesser"`) {
		t.Fatalf("public fetch: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, pub, "DELETE", "/api/v1/nock-sound-filters/podcast_voice_dark", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("public route must be read-only, got %d", rec.Code)
	}

	// Chains every consumer can't run are rejected at the door.
	for _, bad := range []string{
		`{"version":1,"stages":[{"type":"biquad","kind":9,"freq":80}]}`,                 // unknown biquad kind
		`{"version":1,"stages":[{"type":"compressor","threshold_db":-20,"ratio":0.5}]}`, // ratio < 1
		`{"version":1,"stages":[{"type":"reverb"}]}`,                                    // unknown stage
		`{"version":2,"stages":[]}`,                                                     // unknown version
		`{"version":1,"stages":[{"type":"gain","gain_db":3,"volume":11}]}`,              // unknown field
	} {
		rec := do(t, h, "PATCH", "/admin/nock/api/sound-filters/1", "application/json", []byte(`{"chain":`+bad+`}`))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("chain %s: want 400, got %d %s", bad, rec.Code, rec.Body)
		}
	}
	if rec := do(t, h, "PATCH", "/admin/nock/api/sound-filters/1", "application/json", []byte(`{"description":"tweaked"}`)); rec.Code != 200 {
		t.Fatalf("patch desc: %d %s", rec.Code, rec.Body)
	}
}
