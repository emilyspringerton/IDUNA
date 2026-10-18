package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iduna/internal/deckstats"
)

func TestDeckStatsHandler(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	os.WriteFile(p, []byte(`{"event":"draft","deck_id":1,"t":5,"player":"bot-wall-d","kind":1,"cards":[1,1,2]}`+"\n"+
		`{"event":"match","deck_id":1,"match_id":1,"player":"bot-wall-d","kind":1,"result":"win","rounds":5}`+"\n"), 0o644)
	h := &DeckStatsHandler{Store: &deckstats.Store{Path: p, MinRefresh: time.Nanosecond}}
	get := func(u string) (*httptest.ResponseRecorder, map[string]any) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
		var m map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		return rr, m
	}
	rr, m := get("/api/v1/games/deadweight/decks?min_games=1")
	if rr.Code != 200 || m["total"].(float64) != 1 || len(m["decks"].([]any)) != 1 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	if rr, _ = get("/api/v1/games/deadweight/decks/1"); rr.Code != 200 {
		t.Fatalf("deck: %d", rr.Code)
	}
	if rr, _ = get("/api/v1/games/deadweight/decks/99"); rr.Code != 404 {
		t.Fatalf("missing deck: %d", rr.Code)
	}
	if rr, _ = get("/api/v1/games/deadweight/decks/x"); rr.Code != 400 {
		t.Fatalf("bad id: %d", rr.Code)
	}
	if rr, m = get("/api/v1/games/deadweight/card-stats"); rr.Code != 200 || len(m["cards"].([]any)) != 2 {
		t.Fatalf("card-stats: %d %s", rr.Code, rr.Body)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/games/deadweight/decks", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST must be refused: %d", rr.Code)
	}
}

// A store whose database cannot be opened must answer 503 and keep that out of every cache, never panic or hang.
func TestDeckStatsHandlerUnavailableStore(t *testing.T) {
	h := &DeckStatsHandler{Store: &deckstats.Store{Path: filepath.Join(t.TempDir(), "decks.ndjson"), DBPath: "/proc/definitely/not/writable/deckstats.db"}}
	for _, u := range []string{"/api/v1/games/deadweight/decks", "/api/v1/games/deadweight/card-stats", "/api/v1/games/deadweight/decks/1"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
		if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d cache=%q body=%s", u, rr.Code, rr.Header().Get("Cache-Control"), rr.Body)
		}
	}
}

// The summary tells the page how far through the log the store is (Warming while a big backlog is still being read).
func TestDeckStatsHandlerSummaryReportsProgress(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	os.WriteFile(p, []byte(`{"event":"draft","deck_id":1,"t":5,"player":"bot-wall-d","kind":1,"cards":[1,1,2]}`+"\n"), 0o644)
	h := &DeckStatsHandler{Store: &deckstats.Store{Path: p, MinRefresh: time.Nanosecond}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/games/deadweight/decks?min_games=0", nil))
	var m struct {
		Summary struct {
			Decks        int   `json:"decks"`
			LogBytes     int64 `json:"log_bytes"`
			IndexedBytes int64 `json:"indexed_bytes"`
			Warming      bool  `json:"warming"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil || m.Summary.Decks != 1 || m.Summary.LogBytes == 0 || m.Summary.IndexedBytes != m.Summary.LogBytes || m.Summary.Warming {
		t.Fatalf("summary: %v %+v", err, m.Summary)
	}
}
