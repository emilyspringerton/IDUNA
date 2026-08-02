package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

func newTestChatDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE chat_messages (
		id            INTEGER  PRIMARY KEY AUTOINCREMENT,
		channel       VARCHAR(16) NOT NULL,
		sender_name   VARCHAR(64) NOT NULL,
		sender_source VARCHAR(16) NOT NULL,
		body          VARCHAR(512) NOT NULL,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create chat_messages table: %v", err)
	}
	return db
}

func chatHandlerWithAuth(keys *jwt.Keys, db *sql.DB) http.Handler {
	h := &handlers.ChatMessagesHandler{DB: db}
	return middleware.RequireAuth(keys)(h)
}

func TestChatMessages_PostThenList(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestChatDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := chatHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{
		"channel": "say", "sender_name": "Tyler", "sender_source": "mud", "body": "hello from the mud",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages?since_id=0", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
	var msgs []struct {
		ID           int64  `json:"id"`
		Channel      string `json:"channel"`
		SenderName   string `json:"sender_name"`
		SenderSource string `json:"sender_source"`
		Body         string `json:"body"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if msgs[0].SenderName != "Tyler" || msgs[0].Body != "hello from the mud" || msgs[0].Channel != "say" || msgs[0].SenderSource != "mud" {
		t.Errorf("unexpected message content: %+v", msgs[0])
	}
}

func TestChatMessages_SinceIDExcludesOlder(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestChatDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := chatHandlerWithAuth(keys, db)

	post := func(sender, body string) int64 {
		b, _ := json.Marshal(map[string]string{"channel": "battlegrounds", "sender_name": sender, "sender_source": "battlegrounds", "body": body})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var resp struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		return resp.ID
	}
	firstID := post("Alice", "first message")
	post("Bob", "second message")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages?since_id="+jsonInt(firstID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var msgs []struct {
		SenderName string `json:"sender_name"`
	}
	json.Unmarshal(rec.Body.Bytes(), &msgs)
	if len(msgs) != 1 || msgs[0].SenderName != "Bob" {
		t.Fatalf("since_id should exclude the first message, got %+v", msgs)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestChatMessages_RejectsInvalidChannel(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestChatDB(t)
	token := makeAgentToken(t, keys, uuid.New().String(), nil)
	h := chatHandlerWithAuth(keys, db)

	body, _ := json.Marshal(map[string]string{"channel": "not-a-real-channel", "sender_name": "X", "sender_source": "mud", "body": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid channel", rec.Code)
	}
}

func TestChatMessages_RequiresAuth(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	db := newTestChatDB(t)
	h := chatHandlerWithAuth(keys, db)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/messages", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (no token)", rec.Code)
	}
}
