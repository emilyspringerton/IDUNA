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

// RedgardenPlayerTicketHandler mints a real REDGARDEN connect ticket for a real DragonsNShit
// character's own player_id -- the Battlegrounds entry point (REDGARDEN_GUI_NORTHSTAR.md
// Milestone 3, GoblinFoxDragon/apps2/mud's own `battlegrounds` command).
//
// This is deliberately a THIRD handler, not a relaxation of RedgardenTicketHandler's own
// bot-only scoping: that handler's whole trust model is "the redgarden.ticket.mint permission
// can never be used to mint a ticket impersonating a real human player's identity, even if the
// agent secret leaked" -- widening its own provider check would break that guarantee for
// REDGARDEN-BOTS. This handler is gated behind a SEPARATE permission
// (redgarden.player-ticket.mint), granted only to the DRAGONSNSHIT-MUD agent, and checks the
// OPPOSITE condition: the player_id must belong to a real `characters` row (a real DragonsNShit
// identity), not a `redgarden_bot`-provider one. DragonsNShit's `apps2/mud` has no OAuth login
// of its own (a real, separate, undesigned question -- see REDGARDEN_GUI_NORTHSTAR.md's own
// note on this), so like RedgardenTicketHandler it mints on behalf of a player_id supplied in
// the request body rather than the caller's own JWT the way ShankpitTicketHandler does.
//
//	POST /api/v1/redgarden/player-ticket   (requires redgarden.player-ticket.mint)
//	  body: {"player_id": "<uuid>"}
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
type RedgardenPlayerTicketHandler struct {
	DB     *sql.DB
	Secret []byte
}

func (h *RedgardenPlayerTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "characters not available", http.StatusServiceUnavailable)
		return
	}
	var count int
	err = h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM characters WHERE player_id = ?`, playerUUID.String(),
	).Scan(&count)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(w, "player_id has no registered DragonsNShit character", http.StatusNotFound)
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
