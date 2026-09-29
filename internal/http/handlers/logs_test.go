package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/userlog"
)

func newLogsMux(t *testing.T, keys *jwt.Keys, hecToken string) (*http.ServeMux, userlog.EventLog) {
	t.Helper()
	store, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mux := http.NewServeMux()
	handlers.RegisterLogsRoutes(mux, &handlers.LogsHandler{Store: store, HECToken: hecToken}, keys)
	return mux, store
}

// TestCollectorAppendsRealEvent -- a real HEC-shaped POST, correctly authenticated, actually
// lands in the event store and comes back with Splunk's own real success shape.
func TestCollectorAppendsRealEvent(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, store := newLogsMux(t, keys, "secret-hec-token")

	body := `{"event":{"user":"alice","action":"login"},"sourcetype":"iduna:auth","source":"iduna-main"}`
	req := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Splunk secret-hec-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["text"] != "Success" || resp["code"] != float64(0) {
		t.Errorf("expected Splunk's own real success shape, got %v", resp)
	}

	recs, err := store.ReadFrom(req.Context(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(recs))
	}
	if recs[0].Event.Type != "iduna:auth" || recs[0].Event.Source != "iduna-main" {
		t.Errorf("stored event = %+v, want Type=iduna:auth Source=iduna-main", recs[0].Event)
	}
}

// TestCollectorRejectsBadToken -- a real, wrong HEC token is a real 401, not silently accepted.
func TestCollectorRejectsBadToken(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	body := `{"event":{"x":1},"sourcetype":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Splunk wrong-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rr.Code)
	}
}

// TestCollectorRejectsMissingSourcetype -- Splunk's own real HEC validation rule, matched here.
func TestCollectorRejectsMissingSourcetype(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	req := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(`{"event":{"x":1}}`))
	req.Header.Set("Authorization", "Splunk secret-hec-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rr.Code)
	}
}

// TestSearchRequiresAuth -- the search endpoint is a real, protected endpoint, not a public one.
func TestSearchRequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	req := httptest.NewRequest(http.MethodGet, "/services/search/jobs?search=type=x", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("want 401 with no token, got %d", rr.Code)
	}
}

// TestSearchRequiresPermission -- a valid JWT without logs.read is a real 403, not a silent pass.
func TestSearchRequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	token := makeAgentToken(t, keys, "rogue", []string{"read.only"})
	req := httptest.NewRequest(http.MethodGet, "/services/search/jobs?search=type=x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

// TestSearchFiltersCorrectly -- a real, end-to-end search: two events ingested via the real HEC
// endpoint, a real search query correctly returns only the matching one.
func TestSearchFiltersCorrectly(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	post := func(sourcetype, source, event string) {
		body := `{"event":` + event + `,"sourcetype":"` + sourcetype + `","source":"` + source + `"}`
		req := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Splunk secret-hec-token")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("seeding event failed: %d %s", rr.Code, rr.Body.String())
		}
	}
	post("iduna:auth", "iduna-main", `{"user":"alice","action":"login"}`)
	post("iduna:apple", "iduna-main", `{"title":"did a thing"}`)

	token := makeAgentToken(t, keys, "operator", []string{"logs.read"})
	req := httptest.NewRequest(http.MethodGet, "/services/search/jobs?search=type=iduna:auth", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Results []userlog.Record `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 matching result, got %d: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Event.Type != "iduna:auth" {
		t.Errorf("got Type=%q, want iduna:auth", resp.Results[0].Event.Type)
	}
}

// TestSearchRejectsBadSyntax -- a real, honest 400 for an unrecognized search term.
func TestSearchRejectsBadSyntax(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	token := makeAgentToken(t, keys, "operator", []string{"logs.read"})
	req := httptest.NewRequest(http.MethodGet, "/services/search/jobs?search=bogus_term", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rr.Code)
	}
}

// TestSearchRegexFiltersCorrectly -- S227-01: real regex-based search, a real, separate
// query parameter from the `search` mini-language.
func TestSearchRegexFiltersCorrectly(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	post := func(sourcetype, event string) {
		body := `{"event":` + event + `,"sourcetype":"` + sourcetype + `"}`
		req := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Splunk secret-hec-token")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("seeding event failed: %d %s", rr.Code, rr.Body.String())
		}
	}
	post("iduna:auth", `{"user":"alice","status_code":200}`)
	post("iduna:auth", `{"user":"bob","status_code":404}`)

	token := makeAgentToken(t, keys, "operator", []string{"logs.read"})
	req := httptest.NewRequest(http.MethodGet, `/services/search/jobs?regex=%22status_code%22%3A4%5Cd%5Cd`, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Results []userlog.Record `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 matching result (the 4xx one), got %d: %+v", len(resp.Results), resp.Results)
	}
	if !bytes.Contains(resp.Results[0].Event.Data, []byte("bob")) {
		t.Errorf("expected the bob/404 event to match, got: %s", resp.Results[0].Event.Data)
	}
}

// TestSearchRejectsBadRegex -- an invalid regex pattern is a real, honest 400, not a panic or a
// silently-ignored filter.
func TestSearchRejectsBadRegex(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	token := makeAgentToken(t, keys, "operator", []string{"logs.read"})
	req := httptest.NewRequest(http.MethodGet, "/services/search/jobs?regex=%28%5B", nil) // "(["
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rr.Code)
	}
}

// TestStreamRequiresPermission -- the new GET /services/search/stream (founder real-time,
// 2026-09-29: "make sure we are using log streaming ... if we arent already") is gated by the
// exact same logs.read permission as the synchronous search endpoint, not left open.
func TestStreamRequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	token := makeAgentToken(t, keys, "rogue", []string{"read.only"})
	req := httptest.NewRequest(http.MethodGet, "/services/search/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

// TestStreamDeliversAlreadyIngestedEvent -- a real, live SSE tail of the SAME unified log
// /services/collector writes into and /services/search/jobs reads from: an event ingested before
// the stream connects still arrives over the stream (UserEventStreamHandler's own poll-a-cursor
// loop starts from from_seq, default 1, i.e. full replay), proving this is the same store, not a
// second, disconnected one. Takes ~2s (the handler's own fixed poll interval) -- inherent to the
// existing UserEventStreamHandler implementation this reuses verbatim, not something this test
// controls.
func TestStreamDeliversAlreadyIngestedEvent(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	mux, _ := newLogsMux(t, keys, "secret-hec-token")

	body := `{"event":{"player_id":"p1","old_display_name":"Runner-A7B2","new_display_name":"Gary"},"sourcetype":"iduna:games.display_name_change","source":"iduna-games"}`
	req2 := httptest.NewRequest(http.MethodPost, "/services/collector", bytes.NewBufferString(body))
	req2.Header.Set("Authorization", "Splunk secret-hec-token")
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("seeding event failed: %d %s", rr2.Code, rr2.Body.String())
	}

	streamToken := makeAgentToken(t, keys, "operator", []string{"logs.read"})
	ctx, cancel := context.WithCancel(context.Background())
	streamReq := httptest.NewRequest(http.MethodGet, "/services/search/stream", nil).WithContext(ctx)
	streamReq.Header.Set("Authorization", "Bearer "+streamToken)
	streamRR := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(streamRR, streamReq)
		close(done)
	}()
	time.Sleep(2500 * time.Millisecond) // past the handler's own 2s poll tick
	cancel()
	<-done

	if streamRR.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", streamRR.Code, streamRR.Body.String())
	}
	streamed := streamRR.Body.String()
	if !strings.Contains(streamed, "event: user_event") {
		t.Errorf("expected at least one user_event SSE frame, got: %s", streamed)
	}
	if !strings.Contains(streamed, "iduna:games.display_name_change") || !strings.Contains(streamed, "Gary") {
		t.Errorf("expected the seeded display-name-change event to be streamed, got: %s", streamed)
	}
}
