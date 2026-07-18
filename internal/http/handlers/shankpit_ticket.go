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

// ShankpitTicketTTL is how long a minted connect ticket remains valid.
// Short-lived deliberately — it's presented once, at PACKET_CONNECT, not
// held for a session the way the underlying IDUNA JWT is.
const ShankpitTicketTTL = 5 * time.Minute

// shankpitTicketPayloadLen is player_id (16 raw UUID bytes) + expires_at
// (4-byte little-endian unix timestamp) — the portion the HMAC covers.
const shankpitTicketPayloadLen = 16 + 4

// ShankpitTicketHandler mints a short-lived HMAC-signed connect ticket for
// an already-authenticated player, for the shankpit-460 game server to
// verify locally (no asymmetric crypto in the C server, no blocking
// network call per connect — see shankpit-460/docs2/NORTHSTAR.md §2 and
// EMILY/BACKLOG.md S156-02).
//
//	POST /api/v1/shankpit/ticket   (requires a valid IDUNA Bearer JWT)
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
//
// Wire format of the ticket (36 raw bytes, hex-encoded in the JSON
// response): player_id (16 bytes, raw UUID) || expires_at (4 bytes, LE
// uint32) || hmac_sha256(secret, player_id||expires_at) truncated to 16
// bytes. The game server has the matching secret via SHANKPIT_TICKET_SECRET
// and verifies with a constant-time comparison; see
// packages/common/hmac_sha256.h in shankpit-460 for the C-side
// implementation and its RFC 4231 test-vector verification.
type ShankpitTicketHandler struct {
	Secret []byte // SHANKPIT_TICKET_SECRET, shared with the game server
}

func (h *ShankpitTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(h.Secret) == 0 {
		http.Error(w, "ticket signing not configured (SHANKPIT_TICKET_SECRET unset)", http.StatusServiceUnavailable)
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
		// Non-player tokens (agents, etc.) have non-UUID subjects — this
		// endpoint is player-tickets only.
		http.Error(w, "token subject is not a player id", http.StatusBadRequest)
		return
	}

	expiresAt := time.Now().Add(ShankpitTicketTTL).UTC()

	payload := make([]byte, shankpitTicketPayloadLen)
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
