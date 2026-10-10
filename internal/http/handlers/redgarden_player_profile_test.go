package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

func newRedgardenProfileDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE players (player_id TEXT PRIMARY KEY, display_name TEXT NOT NULL)`,
		`CREATE TABLE player_game_stats (player_id TEXT, game TEXT, wins INTEGER DEFAULT 0, losses INTEGER DEFAULT 0,
			matches_played INTEGER DEFAULT 0, last_played_at DATETIME, PRIMARY KEY (player_id, game))`,
		`CREATE TABLE redgarden_player_matches (id INTEGER PRIMARY KEY AUTOINCREMENT, player_id TEXT NOT NULL,
			hero_id INTEGER NOT NULL DEFAULT -1, result TEXT NOT NULL, played_at TEXT NOT NULL DEFAULT (datetime('now')))`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func rgGameResult(t *testing.T, h http.Handler, tok, pid, game, result string, hero *int) {
	t.Helper()
	b := map[string]any{"player_id": pid, "game": game, "result": result}
	if hero != nil {
		b["hero_id"] = *hero
	}
	raw, _ := json.Marshal(b)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/game-result", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("game-result %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRedgardenProfileAndLeaderboard(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newRedgardenProfileDB(t)
	pid := uuid.New().String()
	_, _ = db.Exec(`INSERT INTO players VALUES (?, 'Ada')`, pid)

	agent := makeAgentToken(t, keys, "REDGARDEN-BOTS", []string{"redgarden.match.write"})
	write := middleware.RequireAuth(keys)(middleware.RequirePermission("redgarden.match.write")(&handlers.RedgardenGameResultHandler{DB: db}))
	h5, h7 := 5, 7
	rgGameResult(t, write, agent, pid, "redgarden-arena", "win", &h5)
	rgGameResult(t, write, agent, pid, "redgarden-arena", "win", &h5)
	rgGameResult(t, write, agent, pid, "redgarden", "loss", &h7)
	rgGameResult(t, write, agent, pid, "redgarden-arena", "loss", nil) // hero not reported

	// leaderboard sums BOTH game strings (regression: arena results used to be invisible)
	lb := httptest.NewRecorder()
	(&handlers.RedgardenLeaderboardHandler{DB: db}).ServeHTTP(lb, httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/leaderboard", nil))
	var lbr struct {
		Leaderboard []struct {
			PlayerID      string `json:"player_id"`
			Wins, Losses  int
			MatchesPlayed int `json:"matches_played"`
		} `json:"leaderboard"`
	}
	_ = json.Unmarshal(lb.Body.Bytes(), &lbr)
	if len(lbr.Leaderboard) != 1 || lbr.Leaderboard[0].MatchesPlayed != 4 {
		t.Fatalf("leaderboard = %s", lb.Body.String())
	}

	// q filter
	lb2 := httptest.NewRecorder()
	(&handlers.RedgardenLeaderboardHandler{DB: db}).ServeHTTP(lb2, httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/leaderboard?q=zzz", nil))
	if !bytes.Contains(lb2.Body.Bytes(), []byte(`"leaderboard":[]`)) {
		t.Fatalf("q=zzz should be empty: %s", lb2.Body.String())
	}

	// profile
	rec := httptest.NewRecorder()
	(&handlers.RedgardenPlayerHandler{DB: db}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/players/"+pid, nil))
	if rec.Code != 200 {
		t.Fatalf("profile %d: %s", rec.Code, rec.Body.String())
	}
	var pr struct {
		DisplayName string  `json:"display_name"`
		Wins        int     `json:"wins"`
		Losses      int     `json:"losses"`
		Matches     int     `json:"matches"`
		WinRate     float64 `json:"win_rate"`
		Heroes      []struct {
			HeroID  int `json:"hero_id"`
			Wins    int `json:"wins"`
			Matches int `json:"matches"`
		} `json:"heroes"`
		Recent []struct{ Result string } `json:"recent"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	if pr.DisplayName != "Ada" || pr.Wins != 2 || pr.Losses != 2 || pr.Matches != 4 || pr.WinRate != 0.5 {
		t.Fatalf("profile totals: %+v", pr)
	}
	if len(pr.Heroes) != 2 || pr.Heroes[0].HeroID != 5 || pr.Heroes[0].Wins != 2 {
		t.Fatalf("heroes (unreported hero must be excluded): %+v", pr.Heroes)
	}
	if len(pr.Recent) != 4 || pr.Recent[0].Result != "loss" {
		t.Fatalf("recent newest-first: %+v", pr.Recent)
	}

	// unknown player -> 404; bad id -> 400
	nf := httptest.NewRecorder()
	(&handlers.RedgardenPlayerHandler{DB: db}).ServeHTTP(nf, httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/players/"+uuid.New().String(), nil))
	if nf.Code != 404 {
		t.Fatalf("unknown = %d", nf.Code)
	}
	bad := httptest.NewRecorder()
	(&handlers.RedgardenPlayerHandler{DB: db}).ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/redgarden/players/nope", nil))
	if bad.Code != 400 {
		t.Fatalf("bad id = %d", bad.Code)
	}
}

func TestRedgardenSelfTicket_GameTokenNeedsNoCharacter(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	secret := []byte("s")
	db := newTestCharactersDB(t) // empty: no DragonsNShit character for this player
	pid := uuid.New()
	h := redgardenSelfTicketHandlerWithAuth(keys, db, secret)

	mk := func(game string, perms []string) string {
		tok, err := jwt.Sign(keys, map[string]any{
			"sub": "guest:" + pid.String(), "player_id": pid.String(), "game": game, "permissions": perms,
			"iss": "https://test.internal", "aud": "farthq-ecosystem", "exp": time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call := func(tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/redgarden/self-ticket", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := call(mk("redgarden", []string{"redgarden.play"}))
	if rec.Code != 200 {
		t.Fatalf("guest token: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ticket   string `json:"ticket"`
		PlayerID string `json:"player_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	raw, _ := hex.DecodeString(resp.Ticket)
	if len(raw) != 36 {
		t.Fatalf("ticket len %d", len(raw))
	}
	var got uuid.UUID
	copy(got[:], raw[:16])
	if got != pid {
		t.Fatalf("ticket embeds %s, want %s", got, pid)
	}

	// another game's token, or redgarden without the play permission, never gets a ticket
	if r := call(mk("deadweight", []string{"deadweight.play"})); r.Code == 200 {
		t.Fatalf("deadweight token must not mint a redgarden ticket")
	}
	if r := call(mk("redgarden", nil)); r.Code != http.StatusForbidden {
		t.Fatalf("no play perm = %d", r.Code)
	}
}
