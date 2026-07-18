package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"iduna/internal/http/middleware"

	"github.com/google/uuid"
)

// ShankpitQueueMinPlayers is the smallest queue size that triggers a match
// (S156-03 v0: "keep it simple to start" — first-N-in, no skill-based
// matching; see shankpit-460/docs2/NORTHSTAR.md §3).
const ShankpitQueueMinPlayers = 2

// ShankpitMatchedTTL bounds how long a matched entry is kept for a client
// that joined but never polled status again — prevents an abandoned match
// slot from pinning a "matched" player forever.
const ShankpitMatchedTTL = 2 * time.Minute

type shankpitQueueEntry struct {
	playerID  string
	queuedAt  time.Time
	matched   bool
	matchedAt time.Time
}

// ShankpitQueue holds v0's entire matchmaking state in process memory —
// deliberately not persisted. A queue of intent to play is inherently
// ephemeral (unlike accounts, Apples, or match results): a restart
// legitimately clears it, players just rejoin. v0 also assumes the one
// persistent game server IS the match (no per-match server instances yet,
// see NORTHSTAR §3/§5) — "matched" just means "go connect now."
type ShankpitQueue struct {
	mu         sync.Mutex
	entries    []*shankpitQueueEntry
	ServerAddr string // where matched players should connect, e.g. "127.0.0.1:6969"

	// NowFunc is overridable in tests; nil means time.Now.
	NowFunc func() time.Time
}

func NewShankpitQueue(serverAddr string) *ShankpitQueue {
	return &ShankpitQueue{ServerAddr: serverAddr}
}

func (q *ShankpitQueue) now() time.Time {
	if q.NowFunc != nil {
		return q.NowFunc()
	}
	return time.Now()
}

func (q *ShankpitQueue) find(playerID string) *shankpitQueueEntry {
	for _, e := range q.entries {
		if e.playerID == playerID {
			return e
		}
	}
	return nil
}

// prune drops matched entries past ShankpitMatchedTTL. Caller must hold mu.
func (q *ShankpitQueue) prune(now time.Time) {
	kept := q.entries[:0]
	for _, e := range q.entries {
		if e.matched && now.Sub(e.matchedAt) > ShankpitMatchedTTL {
			continue
		}
		kept = append(kept, e)
	}
	q.entries = kept
}

// matchIfReady matches every currently-queuing entry once the queuing count
// reaches ShankpitQueueMinPlayers. Caller must hold mu.
func (q *ShankpitQueue) matchIfReady(now time.Time) {
	queuing := 0
	for _, e := range q.entries {
		if !e.matched {
			queuing++
		}
	}
	if queuing < ShankpitQueueMinPlayers {
		return
	}
	for _, e := range q.entries {
		if !e.matched {
			e.matched = true
			e.matchedAt = now
		}
	}
}

type shankpitQueueStatus struct {
	State         string `json:"state"` // "not_queued" | "queuing" | "matched"
	QueuePosition int    `json:"queue_position,omitempty"`
	QueueSize     int    `json:"queue_size"`
	ServerAddr    string `json:"server_addr,omitempty"`
}

// statusFor computes the caller's status. Caller must hold mu.
func (q *ShankpitQueue) statusFor(playerID string, now time.Time) shankpitQueueStatus {
	q.prune(now)
	entry := q.find(playerID)
	queuingCount := 0
	position := 0
	for _, e := range q.entries {
		if !e.matched {
			queuingCount++
			if e.playerID == playerID {
				position = queuingCount
			}
		}
	}
	if entry == nil {
		return shankpitQueueStatus{State: "not_queued", QueueSize: queuingCount}
	}
	if entry.matched {
		return shankpitQueueStatus{State: "matched", QueueSize: queuingCount, ServerAddr: q.ServerAddr}
	}
	return shankpitQueueStatus{State: "queuing", QueuePosition: position, QueueSize: queuingCount}
}

// shankpitQueuePlayerID extracts and validates the caller's player_id from
// JWT claims already placed in context by middleware.RequireAuth — same
// sub-must-be-a-player-UUID rule as ShankpitTicketHandler, so an agent
// token (e.g. EMILY_PRIME) can't accidentally occupy a queue slot.
func shankpitQueuePlayerID(r *http.Request) (id string, status int, msg string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return "", http.StatusUnauthorized, "unauthorized"
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", http.StatusUnauthorized, "token has no subject"
	}
	if _, err := uuid.Parse(sub); err != nil {
		return "", http.StatusBadRequest, "token subject is not a player id"
	}
	return sub, 0, ""
}

func writeShankpitQueueJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Join adds the caller to the queue (idempotent — rejoining while already
// queuing/matched is a no-op) and immediately checks whether the queue has
// reached ShankpitQueueMinPlayers, matching everyone currently queuing if so.
func (q *ShankpitQueue) Join(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	playerID, status, msg := shankpitQueuePlayerID(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}

	now := q.now()
	q.mu.Lock()
	q.prune(now)
	if q.find(playerID) == nil {
		q.entries = append(q.entries, &shankpitQueueEntry{playerID: playerID, queuedAt: now})
	}
	q.matchIfReady(now)
	resp := q.statusFor(playerID, now)
	q.mu.Unlock()

	writeShankpitQueueJSON(w, resp)
}

// Leave removes the caller from the queue entirely, whether they were still
// queuing or already matched (treated as "done with this session").
func (q *ShankpitQueue) Leave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	playerID, status, msg := shankpitQueuePlayerID(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}

	now := q.now()
	q.mu.Lock()
	q.prune(now)
	kept := q.entries[:0]
	for _, e := range q.entries {
		if e.playerID != playerID {
			kept = append(kept, e)
		}
	}
	q.entries = kept
	q.mu.Unlock()

	writeShankpitQueueJSON(w, shankpitQueueStatus{State: "not_queued"})
}

// Status reports the caller's current queue state without mutating it
// (aside from TTL-based pruning) — meant to be polled after Join.
func (q *ShankpitQueue) Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	playerID, status, msg := shankpitQueuePlayerID(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}

	now := q.now()
	q.mu.Lock()
	resp := q.statusFor(playerID, now)
	q.mu.Unlock()

	writeShankpitQueueJSON(w, resp)
}
