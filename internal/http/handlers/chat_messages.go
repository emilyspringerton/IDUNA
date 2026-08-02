package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"iduna/internal/http/middleware"
)

// ChatMessagesHandler relays chat between GoblinFoxDragon's apps2/mud (telnet, real say/yell/
// guild chat) and REDGARDEN's Battlegrounds GUI client -- two separate processes/protocols with
// no channel of their own. Any authenticated caller (any valid IDUNA JWT -- agent or player) may
// post or poll; deliberately no identity linkage to players/characters (see the migration's own
// doc comment for why: apps2/mud's telnet identity isn't unified with a real IDUNA player_id).
//
//	POST /api/v1/chat/messages   {"channel": "say", "sender_name": "Tyler", "body": "hi"}
//	  -> 201, {"id": 42}
//	GET  /api/v1/chat/messages?since_id=0&limit=50
//	  -> [{"id":1,"channel":"say","sender_name":"Tyler","sender_source":"mud","body":"hi","created_at":"..."}]
type ChatMessagesHandler struct {
	DB *sql.DB
}

type chatMessage struct {
	ID           int64  `json:"id"`
	Channel      string `json:"channel"`
	SenderName   string `json:"sender_name"`
	SenderSource string `json:"sender_source"`
	Body         string `json:"body"`
	CreatedAt    string `json:"created_at"`
}

var validChatChannels = map[string]bool{
	"say": true, "yell": true, "guild": true, "battlegrounds": true,
}
var validChatSources = map[string]bool{"mud": true, "battlegrounds": true}

func (h *ChatMessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "chat not available", http.StatusServiceUnavailable)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.post(w, r)
	case http.MethodGet:
		h.list(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *ChatMessagesHandler) post(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel      string `json:"channel"`
		SenderName   string `json:"sender_name"`
		SenderSource string `json:"sender_source"`
		Body         string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !validChatChannels[body.Channel] {
		http.Error(w, "channel must be one of: say, yell, guild, battlegrounds", http.StatusBadRequest)
		return
	}
	if !validChatSources[body.SenderSource] {
		http.Error(w, "sender_source must be 'mud' or 'battlegrounds'", http.StatusBadRequest)
		return
	}
	if body.SenderName == "" || len(body.SenderName) > 64 {
		http.Error(w, "sender_name required, max 64 chars", http.StatusBadRequest)
		return
	}
	if body.Body == "" {
		http.Error(w, "body required", http.StatusBadRequest)
		return
	}
	if len(body.Body) > 512 {
		body.Body = body.Body[:512]
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO chat_messages (channel, sender_name, sender_source, body) VALUES (?, ?, ?, ?)`,
		body.Channel, body.SenderName, body.SenderSource, body.Body)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

func (h *ChatMessagesHandler) list(w http.ResponseWriter, r *http.Request) {
	sinceID := int64(0)
	if v := r.URL.Query().Get("since_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			sinceID = parsed
		}
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT id, channel, sender_name, sender_source, body, created_at
		 FROM chat_messages WHERE id > ? ORDER BY id ASC LIMIT ?`,
		sinceID, limit)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []chatMessage{}
	for rows.Next() {
		var m chatMessage
		if err := rows.Scan(&m.ID, &m.Channel, &m.SenderName, &m.SenderSource, &m.Body, &m.CreatedAt); err != nil {
			continue
		}
		out = append(out, m)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
