package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

func queueHandlerWithAuth(keys *jwt.Keys, fn http.HandlerFunc) http.Handler {
	return middleware.RequireAuth(keys)(fn)
}

func doQueueReq(t *testing.T, h http.Handler, method, path, token string) queueStatusResponse {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: status = %d, body = %s", method, path, rec.Code, rec.Body.String())
	}
	var s queueStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return s
}

type queueStatusResponse struct {
	State         string `json:"state"`
	QueuePosition int    `json:"queue_position"`
	QueueSize     int    `json:"queue_size"`
	ServerAddr    string `json:"server_addr"`
}

func TestShankpitQueue_JoinAloneStaysQueuing(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	joinH := queueHandlerWithAuth(keys, q.Join)

	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	resp := doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", token)

	if resp.State != "queuing" {
		t.Fatalf("state = %q, want queuing (alone, below ShankpitQueueMinPlayers=%d)", resp.State, handlers.ShankpitQueueMinPlayers)
	}
	if resp.QueuePosition != 1 || resp.QueueSize != 1 {
		t.Errorf("position/size = %d/%d, want 1/1", resp.QueuePosition, resp.QueueSize)
	}
}

func TestShankpitQueue_TwoPlayersMatch(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	joinH := queueHandlerWithAuth(keys, q.Join)
	statusH := queueHandlerWithAuth(keys, q.Status)

	tokenA := makeAgentToken(t, keys, uuid.New().String(), nil)
	tokenB := makeAgentToken(t, keys, uuid.New().String(), nil)

	respA := doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", tokenA)
	if respA.State != "queuing" {
		t.Fatalf("A after joining alone: state = %q, want queuing", respA.State)
	}

	respB := doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", tokenB)
	if respB.State != "matched" {
		t.Fatalf("B after 2nd join: state = %q, want matched (ShankpitQueueMinPlayers=%d reached)", respB.State, handlers.ShankpitQueueMinPlayers)
	}
	if respB.ServerAddr != "127.0.0.1:6969" {
		t.Errorf("B server_addr = %q, want 127.0.0.1:6969", respB.ServerAddr)
	}

	// A must also flip to matched on its next status poll — matching is
	// "everyone currently queuing," not just the request that tipped the count.
	statusA := doQueueReq(t, statusH, http.MethodGet, "/api/v1/shankpit/queue/status", tokenA)
	if statusA.State != "matched" {
		t.Fatalf("A after B's join: state = %q, want matched", statusA.State)
	}
	if statusA.ServerAddr != "127.0.0.1:6969" {
		t.Errorf("A server_addr = %q, want 127.0.0.1:6969", statusA.ServerAddr)
	}
}

func TestShankpitQueue_StatusNotQueued(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	statusH := queueHandlerWithAuth(keys, q.Status)

	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	resp := doQueueReq(t, statusH, http.MethodGet, "/api/v1/shankpit/queue/status", token)

	if resp.State != "not_queued" {
		t.Fatalf("state = %q, want not_queued (never joined)", resp.State)
	}
}

func TestShankpitQueue_Leave(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	joinH := queueHandlerWithAuth(keys, q.Join)
	leaveH := queueHandlerWithAuth(keys, q.Leave)
	statusH := queueHandlerWithAuth(keys, q.Status)

	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", token)
	leaveResp := doQueueReq(t, leaveH, http.MethodPost, "/api/v1/shankpit/queue/leave", token)
	if leaveResp.State != "not_queued" {
		t.Fatalf("leave response state = %q, want not_queued", leaveResp.State)
	}

	statusResp := doQueueReq(t, statusH, http.MethodGet, "/api/v1/shankpit/queue/status", token)
	if statusResp.State != "not_queued" {
		t.Fatalf("status after leave: state = %q, want not_queued (must not linger)", statusResp.State)
	}
}

func TestShankpitQueue_MatchedEntryExpiresAfterTTL(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	clock := time.Now()
	q.NowFunc = func() time.Time { return clock }
	joinH := queueHandlerWithAuth(keys, q.Join)
	statusH := queueHandlerWithAuth(keys, q.Status)

	tokenA := makeAgentToken(t, keys, uuid.New().String(), nil)
	tokenB := makeAgentToken(t, keys, uuid.New().String(), nil)
	doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", tokenA)
	doQueueReq(t, joinH, http.MethodPost, "/api/v1/shankpit/queue/join", tokenB)

	// Advance the clock past ShankpitMatchedTTL — a matched entry nobody
	// ever connected on top of must not pin state forever.
	clock = clock.Add(handlers.ShankpitMatchedTTL + time.Second)

	statusResp := doQueueReq(t, statusH, http.MethodGet, "/api/v1/shankpit/queue/status", tokenA)
	if statusResp.State != "not_queued" {
		t.Fatalf("state after TTL expiry = %q, want not_queued", statusResp.State)
	}
}

func TestShankpitQueue_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	joinH := queueHandlerWithAuth(keys, q.Join)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/queue/join", nil)
	rec := httptest.NewRecorder()
	joinH.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token presented)", rec.Code)
	}
}

func TestShankpitQueue_RejectsNonUUIDSubject(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	q := handlers.NewShankpitQueue("127.0.0.1:6969")
	joinH := queueHandlerWithAuth(keys, q.Join)

	token := makeAgentToken(t, keys, "EMILY_PRIME", nil) // agent-style subject, not a player UUID
	req := httptest.NewRequest(http.MethodPost, "/api/v1/shankpit/queue/join", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	joinH.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (non-UUID subject)", rec.Code)
	}
}
