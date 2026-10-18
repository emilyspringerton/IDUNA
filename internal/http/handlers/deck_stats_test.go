package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestDeckStatsHandlerBotDecks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "decks.ndjson")
	var b strings.Builder
	for k := 1; k <= 30; k++ {
		fmt.Fprintf(&b, `{"event":"draft","deck_id":%d,"t":5,"player":"bot-wall-d","kind":1,"cards":[%d,1,1,2,2,3]}`+"\n", k, k%90+4)
		for g := 0; g < k; g++ {
			fmt.Fprintf(&b, `{"event":"match","deck_id":%d,"match_id":%d,"player":"bot-wall-d","kind":1,"result":"win","rounds":5}`+"\n", k, k*100+g)
		}
		fmt.Fprintf(&b, `{"event":"match","deck_id":%d,"match_id":%d,"player":"bot-wall-d","kind":1,"result":"loss","rounds":5}`+"\n", k, k*100+99)
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	h := &DeckStatsHandler{Store: &deckstats.Store{Path: p, MinRefresh: time.Nanosecond}}
	get := func(u string) (int, map[string]any) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
		var m map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		return rr.Code, m
	}
	code, top := get("/api/v1/games/deadweight/bot-decks?tier=5&min_games=3&n=4")
	decks, _ := top["decks"].([]any)
	if code != 200 || len(decks) == 0 || len(decks) > 4 || top["tier"].(float64) != 5 || top["tiers"].(float64) != 5 || top["eligible"].(float64) != 29 {
		t.Fatalf("tier 5: %d %v", code, top)
	}
	first := decks[0].(map[string]any)
	if len(first["cards"].([]any)) != 6 || first["win_rate"].(float64) < 0.9 {
		t.Fatalf("a top-tier deck should be a long streak with its cards: %v", first)
	}
	_, bottom := get("/api/v1/games/deadweight/bot-decks?tier=1&min_games=3&n=4")
	if bottom["score_max"].(float64) >= top["score_min"].(float64) {
		t.Fatalf("tier 1 reaches the champions: %v vs %v", bottom["score_max"], top["score_min"])
	}
	for _, u := range []string{"/bot-decks", "/bot-decks?tier=0", "/bot-decks?tier=6", "/bot-decks?tier=2&tiers=11", "/bot-decks?tier=2&n=0", "/bot-decks?tier=2&min_games=0"} {
		if code, _ := get("/api/v1/games/deadweight" + u); code != http.StatusBadRequest {
			t.Fatalf("%s should be a 400, got %d", u, code)
		}
	}
}
