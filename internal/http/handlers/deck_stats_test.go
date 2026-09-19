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
