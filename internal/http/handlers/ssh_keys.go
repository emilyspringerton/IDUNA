package handlers

// ssh_keys.go — SSH_TRANSPORT_IDENTITY_SPEC.md §3 / Stage 5: real, permanent binding of an SSH
// public key fingerprint to a character. Stage 4 (§2, shipped same day) already accepts any
// offered key (trust-on-first-use) for the CONNECTION -- this file is what actually remembers
// which key belongs to which character across connections.
//
// Routes (registered in mmo.go's ServeHTTP/routeCharacters):
//   GET    /api/v1/ssh-keys?fingerprint=...          — resolve a fingerprint to its character_id
//   GET    /api/v1/characters/:id/ssh-keys           — list a character's own active keys
//   POST   /api/v1/characters/:id/ssh-keys           — bind (or reactivate) a fingerprint
//   DELETE /api/v1/characters/:id/ssh-keys?fingerprint=... — revoke a key
//
// A real SHA256 SSH fingerprint (ssh.FingerprintSHA256's own format, "SHA256:" + unpadded
// standard base64) can legitimately contain a literal "/" -- every route that takes one as input
// uses a query parameter, not a path segment, so there's nothing to percent-encode/decode.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"iduna/internal/http/middleware"
)

// routeSSHKeysLookup handles the one top-level (not character-scoped) SSH key route: resolving a
// fingerprint to whichever character (if any) it's actively bound to. This is the real lookup an
// SSH connection needs before it even has a character_id to ask anything else about.
func (h *MMOHandler) routeSSHKeysLookup(w http.ResponseWriter, r *http.Request, path string) {
	if path != "/api/v1/ssh-keys" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	fingerprint := strings.TrimSpace(r.URL.Query().Get("fingerprint"))
	if fingerprint == "" {
		mmoWriteError(w, http.StatusBadRequest, "fingerprint query parameter required")
		return
	}
	var characterID string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT character_id FROM character_ssh_keys WHERE fingerprint = ? AND revoked_at IS NULL`,
		fingerprint).Scan(&characterID)
	if err == sql.ErrNoRows {
		mmoWriteError(w, http.StatusNotFound, "fingerprint not bound to any character")
		return
	}
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"character_id": characterID})
}

// SSHKeyEntry is one of a character's own bound (non-revoked) SSH keys, per §3.2's "list bound
// keys" requirement. PublicKey is included (not just the fingerprint) so a player reviewing their
// own key list can actually recognize which physical key file each entry corresponds to.
type SSHKeyEntry struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
	CreatedAt   string `json:"created_at"`
}

// handleListSSHKeys returns every active (non-revoked) key bound to this character. Same
// "reading your own data isn't a cheat vector" reasoning as handleGetJobLevels -- no agent-only
// restriction.
func (h *MMOHandler) handleListSSHKeys(w http.ResponseWriter, r *http.Request, id string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT fingerprint, public_key, created_at FROM character_ssh_keys
		 WHERE character_id = ? AND revoked_at IS NULL ORDER BY created_at`, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []SSHKeyEntry{}
	for rows.Next() {
		var e SSHKeyEntry
		if err := rows.Scan(&e.Fingerprint, &e.PublicKey, &e.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

type bindSSHKeyRequest struct {
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key"`
}

// handleBindSSHKey implements §3.1's "permanently" binding plus §3.2's "add an additional public
// key." Agent-only: the real "client" here is GoblinFoxDragon's own MUD server, acting on a
// player's behalf only after its own claim-flow or `key-add` command logic decided a bind should
// happen -- never a player's own raw HTTP call (same reasoning handleUpdateJobLevel's own doc
// comment already establishes for a different self-reporting cheat vector).
//
// Real reactivation-vs-conflict logic, matching the migration's own "permanently" design: if this
// exact fingerprint already has a row for THIS SAME character (even revoked), it's reactivated
// (revoked_at cleared) rather than erroring -- a player who revoked a key and wants it back gets
// their own history back, not a confusing "already exists" error. If the fingerprint already
// belongs to a DIFFERENT character, that's refused with 409 -- a fingerprint can never move
// between characters, by design (this is also what makes §3.1's own "different key, same claimed
// name -> refused" acceptance bullet real: two different physical keys can never converge onto
// one binding row by accident).
func (h *MMOHandler) handleBindSSHKey(w http.ResponseWriter, r *http.Request, id string) {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if _, isAgent := claims["agent_name"]; !isAgent {
			mmoWriteError(w, http.StatusForbidden, "ssh-key binding is agent-only")
			return
		}
	}

	var req bindSSHKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	req.PublicKey = strings.TrimSpace(req.PublicKey)
	if req.Fingerprint == "" || req.PublicKey == "" {
		mmoWriteError(w, http.StatusBadRequest, "fingerprint and public_key required")
		return
	}

	var exists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM characters WHERE character_id = ?`, id).Scan(&exists); err != nil {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}

	var existingCharID string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT character_id FROM character_ssh_keys WHERE fingerprint = ?`, req.Fingerprint).Scan(&existingCharID)
	switch {
	case err == sql.ErrNoRows:
		// Brand-new fingerprint -- real, fresh bind.
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := h.DB.ExecContext(r.Context(),
			`INSERT INTO character_ssh_keys (character_id, fingerprint, public_key, created_at) VALUES (?, ?, ?, ?)`,
			id, req.Fingerprint, req.PublicKey, now); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case err != nil:
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	case existingCharID != id:
		mmoWriteError(w, http.StatusConflict, "fingerprint already bound to a different character")
		return
	default:
		// Same character re-adding/reactivating its own previously-revoked key.
		if _, err := h.DB.ExecContext(r.Context(),
			`UPDATE character_ssh_keys SET revoked_at = NULL WHERE fingerprint = ? AND character_id = ?`,
			req.Fingerprint, id); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRevokeSSHKey implements §3.2's "revoke a key other than the one in use" -- the "other
// than the one in use" half of that requirement is enforced by the CALLER (GoblinFoxDragon's own
// `key-revoke` command knows which fingerprint authenticated the current session and refuses to
// even make this call for it), not here: this endpoint has no concept of "the session currently
// using this key," it only knows the durable binding. Agent-only, same reasoning as
// handleBindSSHKey. A revoke of an already-revoked or never-bound fingerprint is not an error --
// idempotent, matching DELETE's own general HTTP convention.
func (h *MMOHandler) handleRevokeSSHKey(w http.ResponseWriter, r *http.Request, id string) {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if _, isAgent := claims["agent_name"]; !isAgent {
			mmoWriteError(w, http.StatusForbidden, "ssh-key revocation is agent-only")
			return
		}
	}
	fingerprint := strings.TrimSpace(r.URL.Query().Get("fingerprint"))
	if fingerprint == "" {
		mmoWriteError(w, http.StatusBadRequest, "fingerprint query parameter required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := h.DB.ExecContext(r.Context(),
		`UPDATE character_ssh_keys SET revoked_at = ? WHERE fingerprint = ? AND character_id = ? AND revoked_at IS NULL`,
		now, fingerprint, id); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
