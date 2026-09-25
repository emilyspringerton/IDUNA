package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"iduna/internal/matchlog"
)

const testMatchLine = `{"match_id":42,"seed":1494941365,"names":["bot-wall-d","bot-mirror-d"],"kinds":[1,1],"plays":[[1,1],[3,2],[0,1],[0,3],[0,2]],"result":[0,1],"reason":0,"rounds":5,"mode":"draft","deck_ids":[238703,238700],"decks":[[84,41,68,91,91,16,82,103,103,103,44,5,5,9,9,30,30,67,27,23,94,76,76],[75,75,75,94,28,28,50,45,87,87,43,12,23,13,13,53,85,97,15,90,90,41,41]]}` + "\n"

func TestMatchReplayHandler_ListAndOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "matches.ndjson")
	os.WriteFile(p, []byte(testMatchLine), 0o644)
	h := &MatchReplayHandler{Store: &matchlog.Store{Path: p, MinRefresh: time.Nanosecond}}
	get := func(u string) (*httptest.ResponseRecorder, map[string]any) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
		var m map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		return rr, m
	}
	rr, m := get("/api/v1/games/deadweight/matches")
	if rr.Code != 200 || m["total"].(float64) != 1 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	rr, m = get("/api/v1/games/deadweight/matches/42")
	if rr.Code != 200 || m["match_id"].(float64) != 42 {
		t.Fatalf("one: %d %s", rr.Code, rr.Body)
	}
	if rr, _ = get("/api/v1/games/deadweight/matches/999"); rr.Code != 404 {
		t.Fatalf("missing match: %d", rr.Code)
	}
	if rr, _ = get("/api/v1/games/deadweight/matches/x"); rr.Code != 400 {
		t.Fatalf("bad id: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/games/deadweight/matches", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST must be refused: %d", rr.Code)
	}
}

// TestMatchReplayHandler_Replay builds the real dw_replay_dump tool from the sibling DEADWEIGHT
// checkout (skips cleanly if that checkout or a C compiler isn't present, e.g. a from-scratch CI
// clone of IDUNA alone) and verifies the /replay route actually shells out to it and forwards a
// real round-by-round trace.
func TestMatchReplayHandler_Replay(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("dw_replay_dump is only built for linux in this environment")
	}
	dwRoot := "../../../../DEADWEIGHT"
	src := []string{dwRoot + "/tools/replay_dump.c", dwRoot + "/core/match.c", dwRoot + "/core/card_rules.c"}
	for _, f := range src {
		if _, err := os.Stat(f); err != nil {
			t.Skipf("DEADWEIGHT sibling checkout not present (%s): %v", f, err)
		}
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}
	bin := filepath.Join(t.TempDir(), "dw_replay_dump")
	build := exec.Command("gcc", "-std=c99", "-I"+dwRoot+"/core", "-I"+dwRoot+"/core/runtime", "-DPARENA_NO_GRAPHICS", "-O2",
		dwRoot+"/tools/replay_dump.c", dwRoot+"/core/match.c", dwRoot+"/core/card_rules.c", "-o", bin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building dw_replay_dump: %v\n%s", err, out)
	}

	p := filepath.Join(t.TempDir(), "matches.ndjson")
	os.WriteFile(p, []byte(testMatchLine), 0o644)
	h := &MatchReplayHandler{Store: &matchlog.Store{Path: p, MinRefresh: time.Nanosecond}, ReplayBin: bin}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/games/deadweight/matches/42/replay", nil))
	if rr.Code != 200 {
		t.Fatalf("replay: %d %s", rr.Code, rr.Body)
	}
	var out struct {
		MatchID    int  `json:"match_id"`
		ReplayOK   bool `json:"replay_ok"`
		ReplayBad  bool `json:"replay_bad"`
		FinalDone  bool `json:"final_done"`
		RoundsData []struct {
			Round int   `json:"round"`
			Card  [2]int `json:"card"`
		} `json:"rounds_data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v\n%s", err, rr.Body)
	}
	if !out.ReplayOK || out.ReplayBad || !out.FinalDone {
		t.Fatalf("expected a clean, complete replay: %+v", out)
	}
	if len(out.RoundsData) != 5 {
		t.Fatalf("expected 5 replayed rounds, got %d: %s", len(out.RoundsData), rr.Body)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/games/deadweight/matches/999/replay", nil))
	if rr.Code != 404 {
		t.Fatalf("replay of unknown match: %d", rr.Code)
	}
}
