package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// RedgardenTicketTTL mirrors ShankpitTicketTTL — short-lived, presented once
// at PACKET_CONNECT, not held for a session.
const RedgardenTicketTTL = 5 * time.Minute

// redgardenTicketPayloadLen is player_id (16 raw UUID bytes) + expires_at
// (4-byte little-endian unix timestamp) — same wire format as shankpit's
// ticket (see shankpit_ticket.go), which REDGARDEN's C server already
// verifies unmodified (apps/server/src/main.c's TICKET_PAYLOAD_LEN=20,
// TICKET_MAC_LEN=16).
const redgardenTicketPayloadLen = 16 + 4

// RedgardenTicketHandler mints a connect ticket on behalf of an
// already-registered player_id, rather than for the caller's own player_id
// the way ShankpitTicketHandler does. REDGARDEN bots have no OAuth login —
// there is no human sitting behind them to produce a real per-player JWT —
// so they authenticate as the REDGARDEN-BOTS M2M agent
// (redgarden.ticket.mint permission, checked by the caller in main.go via
// middleware.RequirePermission) and mint on behalf of a player_id supplied
// in the request body.
//
// Scoped tightly on purpose: only mints for players registered under
// provider="redgarden_bot" (see players.go's handleRegister). This means
// the redgarden.ticket.mint permission can never be used to mint a ticket
// impersonating a real human player's identity, even if the agent secret
// leaked — the blast radius is capped to bot-provider rows only.
//
//	POST /api/v1/redgarden/ticket   (requires redgarden.ticket.mint)
//	  body: {"player_id": "<uuid>"}
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
type RedgardenTicketHandler struct {
	DB     *sql.DB
	Secret []byte
}

func (h *RedgardenTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(h.Secret) == 0 {
		http.Error(w, "ticket signing not configured (REDGARDEN_TICKET_SECRET unset)", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		PlayerID string `json:"player_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	playerUUID, err := uuid.Parse(body.PlayerID)
	if err != nil {
		http.Error(w, "player_id must be a valid UUID", http.StatusBadRequest)
		return
	}

	if h.DB == nil {
		http.Error(w, "players not available", http.StatusServiceUnavailable)
		return
	}
	var provider string
	err = h.DB.QueryRowContext(r.Context(),
		`SELECT provider FROM players WHERE player_id = ?`, playerUUID.String(),
	).Scan(&provider)
	if err == sql.ErrNoRows {
		http.Error(w, "player not registered", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if provider != "redgarden_bot" {
		http.Error(w, "ticket minting is only available for redgarden_bot-provider players", http.StatusForbidden)
		return
	}

	expiresAt := time.Now().Add(RedgardenTicketTTL).UTC()

	payload := make([]byte, redgardenTicketPayloadLen)
	idBytes, _ := playerUUID.MarshalBinary() // always 16 bytes for a parsed uuid.UUID
	copy(payload[0:16], idBytes)
	binary.LittleEndian.PutUint32(payload[16:20], uint32(expiresAt.Unix()))

	mac := hmac.New(sha256.New, h.Secret)
	mac.Write(payload)
	fullMAC := mac.Sum(nil)

	ticket := append(payload, fullMAC[:16]...) // truncate to 128 bits — see hmac_sha256.h doc

	resp := map[string]any{
		"ticket":     hex.EncodeToString(ticket),
		"expires_at": expiresAt.Unix(),
		"player_id":  playerUUID.String(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
