package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

// RacerTicketTTL is how long a minted connect ticket remains valid. Short-lived deliberately --
// it's presented once, at RC_PACKET_CONNECT, not held for a session the way the underlying IDUNA
// JWT is. Same TTL as ShankpitTicketHandler's own (see that handler's doc comment) -- no reason
// for racing's own connect window to be tighter or looser than SHANKPIT's already-proven value.
const RacerTicketTTL = 5 * time.Minute

// racerTicketPayloadLen is player_id (16 raw UUID bytes) + expires_at (4-byte little-endian unix
// timestamp) -- the portion the HMAC covers. Identical wire shape to ShankpitTicketHandler's own
// shankpitTicketPayloadLen; kept as a separate named constant (not shared) so this file reads
// standalone and a future divergence in one doesn't silently move the other.
const racerTicketPayloadLen = 16 + 4

// RacerTicketHandler mints a short-lived HMAC-signed connect ticket for an already-authenticated
// player, for the WEAKNIGHT_BEDROCK_RACERS game server to verify locally -- direct port of
// ShankpitTicketHandler's own real, proven pattern (S156-02), not a new design. Founder real-time
// (2026-08-28 BEDROCK_RACERS pivot): "build login from the beginning take it from GFD" -- GFD's
// own apps2/battlegrounds_gui login screen already authenticates via POST /api/v1/auth/email/login
// then mints a self-ticket the exact same shape this handler produces; this is that same real
// pattern, instantiated for racing instead of SHANKPIT/REDGARDEN. No character-existence gate
// (unlike RedgardenSelfTicketHandler) -- racing has no separate "character" concept yet, so any
// authenticated real player (any human, not an agent token) can mint one, matching
// ShankpitTicketHandler's own bar.
//
//	POST /api/v1/racer/ticket   (requires a valid IDUNA Bearer JWT)
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
//
// Wire format of the ticket (36 raw bytes, hex-encoded in the JSON response): player_id (16
// bytes, raw UUID) || expires_at (4 bytes, LE uint32) || hmac_sha256(secret,
// player_id||expires_at) truncated to 16 bytes. The game server has the matching secret via
// RACER_TICKET_SECRET and verifies with a constant-time comparison -- see
// packages/common/hmac_sha256.h in WEAKNIGHT_BEDROCK_RACERS for the C-side implementation
// (ported from shankpit-460's own, itself RFC-4231-verified).
type RacerTicketHandler struct {
	Secret []byte // RACER_TICKET_SECRET, shared with the racer game server
}

func (h *RacerTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(h.Secret) == 0 {
		http.Error(w, "ticket signing not configured (RACER_TICKET_SECRET unset)", http.StatusServiceUnavailable)
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

	expiresAt := time.Now().Add(RacerTicketTTL).UTC()

	payload := make([]byte, racerTicketPayloadLen)
	idBytes, _ := playerUUID.MarshalBinary() // always 16 bytes for a parsed uuid.UUID
	copy(payload[0:16], idBytes)
	binary.LittleEndian.PutUint32(payload[16:20], uint32(expiresAt.Unix()))

	mac := hmac.New(sha256.New, h.Secret)
	mac.Write(payload)
	fullMAC := mac.Sum(nil)

	ticket := append(payload, fullMAC[:16]...) // truncate to 128 bits -- see hmac_sha256.h doc

	resp := map[string]any{
		"ticket":     hex.EncodeToString(ticket),
		"expires_at": expiresAt.Unix(),
		"player_id":  playerUUID.String(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
