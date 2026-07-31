package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

// RedgardenSelfTicketHandler mints a REDGARDEN connect ticket for the caller's own player_id,
// the self-service counterpart to RedgardenPlayerTicketHandler (which mints on behalf of a
// player_id in the request body, restricted to the DRAGONSNSHIT-MUD agent -- see that handler's
// doc comment for why that's a separate, deliberately-scoped endpoint, not something this one
// relaxes).
//
// REDGARDEN_GUI_NORTHSTAR.md's own open gap: "No GUI login path exists yet end-to-end (a player
// still has to run REDGARDEN's client by hand with the printed command)". This is the endpoint
// that closes it -- REDGARDEN's own apps/arena client authenticates via POST
// /api/v1/auth/email/login (existing), gets a real player JWT, then calls this endpoint with
// that JWT directly instead of going through apps2/mud's telnet `battlegrounds` command.
//
// Same "use the caller's own JWT, not a request-body player_id" trust model as
// ShankpitTicketHandler -- a player can only ever mint a ticket for themselves.
//
//	POST /api/v1/redgarden/self-ticket   (requires a valid IDUNA Bearer JWT)
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
//	  -> 404 if the authenticated player has no registered DragonsNShit character yet
type RedgardenSelfTicketHandler struct {
	DB     *sql.DB
	Secret []byte // REDGARDEN_TICKET_SECRET, shared with apps/arena_server
}

func (h *RedgardenSelfTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(h.Secret) == 0 {
		http.Error(w, "ticket signing not configured (REDGARDEN_TICKET_SECRET unset)", http.StatusServiceUnavailable)
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		http.Error(w, "token has no subject", http.StatusUnauthorized)
		return
	}
	playerUUID, err := uuid.Parse(sub)
	if err != nil {
		// Non-player tokens (agents, etc.) have non-UUID subjects -- this endpoint is
		// player-tickets only.
		http.Error(w, "token subject is not a player id", http.StatusBadRequest)
		return
	}

	if h.DB == nil {
		http.Error(w, "characters not available", http.StatusServiceUnavailable)
		return
	}
	var count int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM characters WHERE player_id = ?`, playerUUID.String(),
	).Scan(&count); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(w, "no registered DragonsNShit character for this player -- create one via apps2/mud telnet first", http.StatusNotFound)
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
