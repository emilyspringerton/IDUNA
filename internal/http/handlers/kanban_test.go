package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"

	"github.com/google/uuid"
)

func newTestKanbanDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE kanban_cards (
		id              INTEGER  PRIMARY KEY AUTOINCREMENT,
		backlog_item_id VARCHAR(32) NOT NULL,
		title           VARCHAR(200) NOT NULL,
		queue           VARCHAR(16) NOT NULL DEFAULT 'backlog',
		position        INTEGER NOT NULL DEFAULT 0,
		board_id        INTEGER NOT NULL DEFAULT 1,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create kanban_cards table: %v", err)
	}
	// MULTIKANBAN-000 Phase 1: real kanban_boards table, board 1 = EINHORN_INDUSTRIAL, matching
	// the real migration (202609040004_kanban_boards.sql) exactly.
	_, err = db.Exec(`CREATE TABLE kanban_boards (
		id           INTEGER  PRIMARY KEY AUTOINCREMENT,
		name         VARCHAR(100) NOT NULL,
		backlog_path VARCHAR(500),
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create kanban_boards table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO kanban_boards (id, name, backlog_path) VALUES (1, 'EINHORN_INDUSTRIAL', '/home/fatbaby/EMILY/BACKLOG.md')`); err != nil {
		t.Fatalf("seed board 1: %v", err)
	}
	return db
}

func kanbanHandlerWithAuth(keys *jwt.Keys, db *sql.DB) http.Handler {
	h := &handlers.KanbanHandler{DB: db}
	return middleware.RequireAuth(keys)(h)
}

type kanbanCardOut struct {
	ID            int64  `json:"id"`
	BacklogItemID string `json:"backlog_item_id"`
	Title         string `json:"title"`
	Queue         string `json:"queue"`
	Position      int    `json:"position"`
}

func postKanbanCard(t *testing.T, h http.Handler, token, backlogItemID, title, queue string) int64 {
	t.Helper()
	payload := map[string]string{"backlog_item_id": backlogItemID, "title": title}
	if queue != "" {
		payload["queue"] = queue
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kanban/cards", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return resp.ID
}

func listKanbanCards(t *testing.T, h http.Handler, token, queueFilter string) []kanbanCardOut {
	t.Helper()
	url := "/api/v1/kanban/cards"
	if queueFilter != "" {
		url += "?queue=" + queueFilter
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out []kanbanCardOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return out
}

func TestKanban_CreateDefaultsToBacklogQueue(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	postKanbanCard(t, h, token, "S202-27", "Body blocking", "")

	cards := listKanbanCards(t, h, token, "")
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	if cards[0].Queue != "backlog" {
		t.Errorf("expected default queue 'backlog', got %q", cards[0].Queue)
	}
	if cards[0].BacklogItemID != "S202-27" || cards[0].Title != "Body blocking" {
		t.Errorf("unexpected card content: %+v", cards[0])
	}
}

func TestKanban_MoveCardBetweenQueues(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	id := postKanbanCard(t, h, token, "S202-27", "Body blocking", "")

	body, _ := json.Marshal(map[string]any{"queue": "priority"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/kanban/cards/"+jsonInt(id), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body.String())
	}

	priorityCards := listKanbanCards(t, h, token, "priority")
	if len(priorityCards) != 1 || priorityCards[0].ID != id {
		t.Fatalf("expected the card to now be in the priority queue, got %+v", priorityCards)
	}
	backlogCards := listKanbanCards(t, h, token, "backlog")
	if len(backlogCards) != 0 {
		t.Fatalf("expected the backlog queue to be empty after the move, got %+v", backlogCards)
	}
}

// TestKanban_EmitsUnifiedLogEvents -- kanban card 3243242 ("ensure kanban does log streaming
// and checks in to the unified log"): create/move/complete each land a real event in the same
// unified logging backend every other real IDUNA code path already emits into, same real
// userlog.NewFileEventLog(t.TempDir())-backed test convention TestApplesCreate_EmitsEvent
// already established.
func TestKanban_EmitsUnifiedLogEvents(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	eventLog, err := userlog.NewFileEventLog(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileEventLog: %v", err)
	}
	t.Cleanup(func() { _ = eventLog.Close() })
	kh := &handlers.KanbanHandler{DB: db, EventLog: eventLog}
	h := middleware.RequireAuth(keys)(kh)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)

	id := postKanbanCard(t, h, token, "S1-01", "Test card", "")

	moveBody, _ := json.Marshal(map[string]any{"queue": "priority"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/kanban/cards/"+jsonInt(id), bytes.NewReader(moveBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", rec.Code, rec.Body.String())
	}

	doneBody, _ := json.Marshal(map[string]any{"queue": "done"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/kanban/cards/"+jsonInt(id), bytes.NewReader(doneBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	recs, err := eventLog.ReadFrom(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 events (create/move/complete), got %d: %+v", len(recs), recs)
	}
	wantTypes := []string{"iduna:kanban.card.create", "iduna:kanban.card.move", "iduna:kanban.card.complete"}
	for i, want := range wantTypes {
		if recs[i].Event.Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, recs[i].Event.Type, want)
		}
	}
	if !strings.Contains(string(recs[1].Event.Data), `"queue":"priority"`) {
		t.Errorf("move event should carry the real target queue, got: %s", recs[1].Event.Data)
	}
	if !strings.Contains(string(recs[2].Event.Data), `"backlog_item_id":"S1-01"`) {
		t.Errorf("complete event should carry the real backlog_item_id, got: %s", recs[2].Event.Data)
	}
}

// TestKanban_PatchPositionReordersColumn -- S207-68 ("i should have the
// ability to sort the cards in a column"). The kanban board's own UI
// (kanban_page.go) reorders a column entirely via repeated
// PATCH .../cards/{id} {"queue":..., "position":...} calls -- this proves
// that real contract end to end: three cards land in position order 0,1,2
// on creation, a real PATCH re-numbers all three to the reverse order, and
// GET reflects the new order, not creation order.
func TestKanban_PatchPositionReordersColumn(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	idA := postKanbanCard(t, h, token, "S207-01", "Card A", "priority")
	idB := postKanbanCard(t, h, token, "S207-02", "Card B", "priority")
	idC := postKanbanCard(t, h, token, "S207-03", "Card C", "priority")

	before := listKanbanCards(t, h, token, "priority")
	if len(before) != 3 || before[0].ID != idA || before[1].ID != idB || before[2].ID != idC {
		t.Fatalf("expected creation order A,B,C before reorder, got %+v", before)
	}

	// Reverse the order: C=0, B=1, A=2.
	for newPos, id := range []int64{idC, idB, idA} {
		body, _ := json.Marshal(map[string]any{"queue": "priority", "position": newPos})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/kanban/cards/"+jsonInt(id), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch position for card %d: status = %d, body = %s", id, rec.Code, rec.Body.String())
		}
	}

	after := listKanbanCards(t, h, token, "priority")
	if len(after) != 3 || after[0].ID != idC || after[1].ID != idB || after[2].ID != idA {
		t.Fatalf("expected reversed order C,B,A after reorder, got %+v", after)
	}
}

func TestKanban_QueueFilterScopesList(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	postKanbanCard(t, h, token, "S202-27", "Body blocking", "priority")
	postKanbanCard(t, h, token, "S202-28", "Ant hero rig", "cruise")
	postKanbanCard(t, h, token, "S202-29", "Kanban layer", "priority")

	priorityCards := listKanbanCards(t, h, token, "priority")
	if len(priorityCards) != 2 {
		t.Fatalf("want 2 priority cards, got %d: %+v", len(priorityCards), priorityCards)
	}
	cruiseCards := listKanbanCards(t, h, token, "cruise")
	if len(cruiseCards) != 1 || cruiseCards[0].BacklogItemID != "S202-28" {
		t.Fatalf("want 1 cruise card (S202-28), got %+v", cruiseCards)
	}
}

// TestKanban_BoardIDScopesListAndCreate -- MULTIKANBAN-000 Phase 1 (full scoping in
// docs/MULTI_KANBAN_NORTHSTAR.md): a card created on a real, second board (board_id=2, no
// backlog_path -- self-contained, no git-file to sync) is invisible to board 1's own default
// listing and vice versa; the real, existing default-board behavior (every caller from before
// this pass, which never sends board_id at all) stays completely unchanged.
func TestKanban_BoardIDScopesListAndCreate(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	if _, err := db.Exec(`INSERT INTO kanban_boards (id, name, backlog_path) VALUES (2, 'Second Board', NULL)`); err != nil {
		t.Fatalf("seed board 2: %v", err)
	}
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	// Real, existing-caller-shaped card -- no board_id in the request body at all.
	postKanbanCard(t, h, token, "S1-01", "Board 1 card", "")

	// Real, new-caller-shaped card -- explicit board_id=2.
	board2Body, _ := json.Marshal(map[string]any{
		"backlog_item_id": "SELF-01", "title": "Board 2 card", "board_id": 2,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kanban/cards", bytes.NewReader(board2Body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create on board 2 status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The real, existing default listing (no ?board_id=) must show ONLY the board-1 card --
	// the real backward-compatibility guarantee this whole phase depends on.
	defaultCards := listKanbanCards(t, h, token, "")
	if len(defaultCards) != 1 || defaultCards[0].BacklogItemID != "S1-01" {
		t.Fatalf("default (board 1) listing should show only the board-1 card, got %+v", defaultCards)
	}

	// A real, explicit ?board_id=2 request shows only board 2's own card.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/kanban/cards?board_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var board2Cards []kanbanCardOut
	if err := json.Unmarshal(rec.Body.Bytes(), &board2Cards); err != nil {
		t.Fatalf("decode board-2 listing: %v", err)
	}
	if len(board2Cards) != 1 || board2Cards[0].BacklogItemID != "SELF-01" {
		t.Fatalf("board-2 listing should show only the board-2 card, got %+v", board2Cards)
	}
	t.Logf("PASS: board 1 and board 2 stay real, independently scoped card lists")
}

func TestKanban_NewCardLandsAtEndOfItsColumn(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	postKanbanCard(t, h, token, "S202-27", "first", "priority")
	postKanbanCard(t, h, token, "S202-28", "second", "priority")

	cards := listKanbanCards(t, h, token, "priority")
	if len(cards) != 2 {
		t.Fatalf("want 2 cards, got %d", len(cards))
	}
	if cards[0].Position >= cards[1].Position {
		t.Errorf("expected the second card to land after the first (position %d >= %d)", cards[0].Position, cards[1].Position)
	}
}

func TestKanban_RejectsInvalidQueue(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{"backlog_item_id": "S202-27", "title": "x", "queue": "not-a-real-queue"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kanban/cards", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid queue", rec.Code)
	}
}

func TestKanban_DeleteRemovesCard(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	id := postKanbanCard(t, h, token, "S202-27", "Body blocking", "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kanban/cards/"+jsonInt(id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}

	cards := listKanbanCards(t, h, token, "")
	if len(cards) != 0 {
		t.Fatalf("expected the card to be gone, got %+v", cards)
	}
}

func TestKanban_DeleteUnknownCardReturns404(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kanban/cards/999", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown card id", rec.Code)
	}
}

func TestKanban_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	h := kanbanHandlerWithAuth(keys, db)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kanban/cards", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token)", rec.Code)
	}
}

func TestKanban_RejectsMissingBacklogItemID(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestKanbanDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := kanbanHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{"title": "no id given"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kanban/cards", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a missing backlog_item_id", rec.Code)
	}
}
