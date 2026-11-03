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
// The caller may be a bare-UUID-subject player JWT or a game player token
// (player_id claim, subject "guest:<uuid>" etc.) -- see ticketPlayerID.
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
	// Game, when set, rejects a caller whose JWT carries a non-matching "game" claim (S241-01).
	// Empty (unset) skips the check entirely -- wired to "shankpit" in main.go.
	Game string
}

// ticketPlayerID answers "which player is this token for?", and the answer is NOT simply the JWT
// subject. Two real token shapes reach this endpoint:
//
//   - a Google/legacy player JWT, whose subject IS the player UUID (how shankpit-460 has always
//     authenticated);
//   - a game player token from /api/v1/games/{game}/guest-register|guest-login|email-login|sso-exchange
//     (`playerToken` in game_online.go), whose subject is "<provider>:<uuid>" and which carries the
//     identity in its own `player_id` claim.
//
// Found live 2026-11-03 while wiring the Roblox 460 layer's login: only the first shape worked, so
// the ONE login a Roblox player can actually perform (guest-register, no password, no OAuth inside
// the engine) minted a token this endpoint then refused with "token subject is not a player id".
// The `player_id` claim is authoritative when present because that is the claim every game route
// already trusts (guest-upgrade, set-display-name, verify); the subject stays a fallback so no
// existing Google player is affected. A provider-prefixed subject is read for the id it names --
// never as a whole string -- and a token that resolves to no UUID at all (an agent's name, a local
// account's "local:<uid>") is still refused, because a ticket is a player's.
//
// The returned problem is empty on success; it doubles as the HTTP error body so a client can tell
// "no token" from "a token that is not a player's".
func ticketPlayerID(claims map[string]any) (uuid.UUID, string) {
	if pid, _ := claims["player_id"].(string); strings.TrimSpace(pid) != "" {
		if u, err := uuid.Parse(strings.TrimSpace(pid)); err == nil {
			return u, ""
		}
		return uuid.Nil, "player_id claim is not a player id"
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return uuid.Nil, "token has no subject"
	}
	if u, err := uuid.Parse(sub); err == nil {
		return u, ""
	}
	if i := strings.IndexByte(sub, ':'); i > 0 {
		if u, err := uuid.Parse(strings.TrimSpace(sub[i+1:])); err == nil {
			return u, ""
		}
	}
	// Agents ("EMILY_PRIME") and local accounts ("local:7") land here: a ticket is a player's, and
	// neither of those is one.
	return uuid.Nil, "token subject is not a player id"
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
	playerUUID, problem := ticketPlayerID(claims)
	if problem != "" {
		status := http.StatusBadRequest
		if problem == "token has no subject" {
			status = http.StatusUnauthorized
		}
		http.Error(w, problem, status)
		return
	}
	if !gameClaimMatches(claims, h.Game) {
		http.Error(w, "this account is scoped to a different game", http.StatusForbidden)
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
