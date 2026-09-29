package handlers_test

// S513: MIXFORGE's client-side mix recorder -- save/list/download recordings scoped to the
// caller's own IDUNA player token. Real coverage: happy path (save -> appears in list -> bytes
// round-trip byte-for-byte on download), content-type allowlist rejection, cross-player ownership
// refusal (404, not 403 -- never confirms another player's recording ID exists), and the
// RecordingsEnabled-per-game gate (404 for a game that hasn't opted in, mirroring SteamAppID's own
// "not configured" honesty elsewhere in this file).

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func (e *gameEnv) postRecording(t *testing.T, game, token string, contentType string, body []byte, name string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/games/"+game+"/recordings", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if name != "" {
		req.Header.Set("X-Recording-Name", name)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	m := map[string]any{}
	if b := bytes.TrimSpace(rec.Body.Bytes()); len(b) > 0 {
		_ = json.Unmarshal(b, &m)
	}
	return rec.Code, m
}

func TestMixforgeRecordings_SaveListDownloadRoundTrip(t *testing.T) {
	e := newGameEnv(t)
	_, _, token := e.register(t, "mixforge", "DJ-One")

	fakeMix := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF, 0x01}, 1000) // 4000 bytes, stands in for real Opus bytes
	code, created := e.postRecording(t, "mixforge", token, "audio/webm", fakeMix, "friday-set")
	if code != 200 {
		t.Fatalf("create: want 200 got %d body=%v", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create: missing id in response %v", created)
	}
	if sz, _ := created["size_bytes"].(float64); int(sz) != len(fakeMix) {
		t.Fatalf("create: size_bytes = %v, want %d", created["size_bytes"], len(fakeMix))
	}

	code, listBody, _ := e.do("GET", "/api/v1/games/mixforge/recordings", token, nil)
	if code != 200 {
		t.Fatalf("list: want 200 got %d", code)
	}
	recs, _ := listBody["recordings"].([]any)
	if len(recs) != 1 {
		t.Fatalf("list: want 1 recording, got %d (%v)", len(recs), listBody)
	}
	row := recs[0].(map[string]any)
	if row["id"] != id || row["name"] != "friday-set.webm" {
		t.Fatalf("list: unexpected row %v", row)
	}

	req := httptest.NewRequest("GET", "/api/v1/games/mixforge/recordings/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("download: want 200 got %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), fakeMix) {
		t.Fatalf("download: bytes did not round-trip (got %d bytes, want %d)", rec.Body.Len(), len(fakeMix))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/webm" {
		t.Fatalf("download: Content-Type = %q, want audio/webm", ct)
	}
}

// TestMixforgeRecordings_AcceptsRealBrowserContentTypeWithCodecParam is a real, live-found
// regression: a browser MediaRecorder's mimeType (and therefore its Blob's .type, sent verbatim
// as Content-Type) is "audio/webm;codecs=opus", not bare "audio/webm" -- confirmed against
// production via a real Playwright run before this fix landed, every real recording was rejected
// with "unsupported Content-Type". The original header (codec param included) must still be what
// gets stored as mime_type, since that's what a later <audio> tag needs for accurate playback.
func TestMixforgeRecordings_AcceptsRealBrowserContentTypeWithCodecParam(t *testing.T) {
	e := newGameEnv(t)
	_, _, token := e.register(t, "mixforge", "DJ")
	code, created := e.postRecording(t, "mixforge", token, "audio/webm;codecs=opus", []byte("real opus bytes"), "set")
	if code != 200 {
		t.Fatalf("want 200 got %d body=%v", code, created)
	}
	if created["mime_type"] != "audio/webm;codecs=opus" {
		t.Fatalf("mime_type should preserve the original header incl. codec param, got %v", created["mime_type"])
	}
	if created["name"] != "set.webm" {
		t.Fatalf("name should still get a .webm extension from the base type, got %v", created["name"])
	}
}

func TestMixforgeRecordings_RejectsUnsupportedContentType(t *testing.T) {
	e := newGameEnv(t)
	_, _, token := e.register(t, "mixforge", "DJ")
	code, body := e.postRecording(t, "mixforge", token, "application/octet-stream", []byte("not audio"), "")
	if code != 400 {
		t.Fatalf("want 400 got %d body=%v", code, body)
	}
}

func TestMixforgeRecordings_CrossPlayerDownload404s(t *testing.T) {
	e := newGameEnv(t)
	_, _, tokenA := e.register(t, "mixforge", "Alice")
	_, _, tokenB := e.register(t, "mixforge", "Bob")

	code, created := e.postRecording(t, "mixforge", tokenA, "audio/ogg", []byte("alices mix"), "")
	if code != 200 {
		t.Fatalf("create: want 200 got %d", code)
	}
	id := created["id"].(string)

	req := httptest.NewRequest("GET", "/api/v1/games/mixforge/recordings/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("Bob downloading Alice's recording: want 404 got %d", rec.Code)
	}

	code, listBody, _ := e.do("GET", "/api/v1/games/mixforge/recordings", tokenB, nil)
	if code != 200 {
		t.Fatalf("list: want 200 got %d", code)
	}
	if recs, _ := listBody["recordings"].([]any); len(recs) != 0 {
		t.Fatalf("Bob's own list should be empty, got %v", recs)
	}
}

func TestMixforgeRecordings_404sForGameWithoutRecordingsEnabled(t *testing.T) {
	e := newGameEnv(t)
	// "othergame" (testGames()) has no RecordingsEnabled set -- matches SteamAppID's own
	// "not configured" 404 honesty for a game that hasn't opted in.
	_, _, token := e.register(t, "othergame", "P")
	code, body := e.postRecording(t, "othergame", token, "audio/webm", []byte("x"), "")
	if code != 404 {
		t.Fatalf("want 404 got %d body=%v", code, body)
	}
}
