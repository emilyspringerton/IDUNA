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

// PapercraftTicketTTL is how long a minted connect ticket remains valid. Short-lived
// deliberately -- it's presented once, at PC_PACKET_CONNECT, not held for a session the way the
// underlying IDUNA JWT is. Same TTL as RacerTicketHandler/ShankpitTicketHandler's own -- no real
// reason for this connect window to differ.
const PapercraftTicketTTL = 5 * time.Minute

// papercraftTicketPayloadLen is player_id (16 raw UUID bytes) + expires_at (4-byte little-endian
// unix timestamp) -- the portion the HMAC covers. Identical wire shape to every other real
// ticket handler in this file; kept as a separate named constant so this file reads standalone.
const papercraftTicketPayloadLen = 16 + 4

// PapercraftTicketHandler mints a short-lived HMAC-signed connect ticket for an already-
// authenticated player, for PAPERCRAFT's own single-node persistent game server to verify
// locally -- direct port of RacerTicketHandler's own real, proven pattern (itself a port of
// ShankpitTicketHandler, S156-02), not a new design. Unlike WEAKNIGHT_BEDROCK_RACERS' own
// matchmaking queue, PAPERCRAFT has no queue at all ("papercraft shouldnt have matches") -- a
// player mints a ticket and connects straight to the one real, always-running world server, so
// this repo gets a ticket handler only, no accompanying queue handler.
//
//	POST /api/v1/papercraft/ticket   (requires a valid IDUNA Bearer JWT)
//	  -> {"ticket": "<72 hex chars>", "expires_at": <unix seconds>, "player_id": "<uuid>"}
//
// Wire format of the ticket (36 raw bytes, hex-encoded in the JSON response): player_id (16
// bytes, raw UUID) || expires_at (4 bytes, LE uint32) || hmac_sha256(secret,
// player_id||expires_at) truncated to 16 bytes. The game server has the matching secret via
// PAPERCRAFT_TICKET_SECRET and verifies with a constant-time comparison -- see
// packages/common/hmac_sha256.h in the PAPERCRAFT repo for the C-side implementation.
type PapercraftTicketHandler struct {
	Secret []byte // PAPERCRAFT_TICKET_SECRET, shared with the game server
	// Game, when set, rejects a caller whose JWT carries a non-matching "game" claim (S241-01).
	// Empty (unset) skips the check entirely -- wired to "papercraft" in main.go.
	Game string
}

func (h *PapercraftTicketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(h.Secret) == 0 {
		http.Error(w, "ticket signing not configured (PAPERCRAFT_TICKET_SECRET unset)", http.StatusServiceUnavailable)
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
	if !gameClaimMatches(claims, h.Game) {
		http.Error(w, "this account is scoped to a different game", http.StatusForbidden)
		return
	}

	expiresAt := time.Now().Add(PapercraftTicketTTL).UTC()

	payload := make([]byte, papercraftTicketPayloadLen)
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
