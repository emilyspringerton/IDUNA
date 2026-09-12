package handlers_test

// SSH_TRANSPORT_IDENTITY_SPEC.md §3 / Stage 5 tests for ssh_keys.go. Reuses newInventoryDB's
// real `characters` table (mmo_inventory_test.go) plus a fresh character_ssh_keys table, and the
// agent/player token helpers mmo_position_test.go already established -- same convention every
// mmo_*_test.go in this package follows.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
)

func newSSHKeysDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newInventoryDB(t)
	_, err := db.Exec(`
		CREATE TABLE character_ssh_keys (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			character_id  TEXT NOT NULL,
			fingerprint   TEXT NOT NULL UNIQUE,
			public_key    TEXT NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			revoked_at    DATETIME
		)`)
	if err != nil {
		t.Fatalf("create character_ssh_keys: %v", err)
	}
	return db
}

func bindSSHKey(t *testing.T, h http.Handler, token, characterID, fingerprint, publicKey string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"fingerprint": fingerprint, "public_key": publicKey})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+characterID+"/ssh-keys", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func listSSHKeys(t *testing.T, h http.Handler, token, characterID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/characters/"+characterID+"/ssh-keys", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func revokeSSHKey(t *testing.T, h http.Handler, token, characterID, fingerprint string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/characters/"+characterID+"/ssh-keys?fingerprint="+url.QueryEscape(fingerprint), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func lookupSSHFingerprint(t *testing.T, h http.Handler, token, fingerprint string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ssh-keys?fingerprint="+url.QueryEscape(fingerprint), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A real fingerprint contains "/" and "+" (ssh.FingerprintSHA256's own unpadded standard base64)
// -- used throughout these tests specifically so a naive path-segment implementation would break
// on it and a query-parameter one (what's actually built) doesn't.
const testFingerprintWithSlash = "SHA256:ab/cd+ef01234567890123456789012345678901234"

func TestBindAndResolveSSHKey_SameKeyTwiceSameCharacterNoPrompt(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-1")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-20", "DRAGONSNSHIT-MUD")

	rec := bindSSHKey(t, h, token, "char-ssh-1", testFingerprintWithSlash, "ssh-ed25519 AAAA... test")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("bind: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// §3.1 acceptance: "Same key twice -> same character, no prompt." The real client-side
	// behavior this enables is a lookup, not a re-bind -- verified here as the lookup route.
	for i := 0; i < 2; i++ {
		lookup := lookupSSHFingerprint(t, h, token, testFingerprintWithSlash)
		if lookup.Code != http.StatusOK {
			t.Fatalf("lookup %d: expected 200, got %d: %s", i, lookup.Code, lookup.Body.String())
		}
		var resp map[string]string
		json.Unmarshal(lookup.Body.Bytes(), &resp)
		if resp["character_id"] != "char-ssh-1" {
			t.Errorf("lookup %d: character_id = %q, want char-ssh-1", i, resp["character_id"])
		}
	}
}

func TestLookupSSHFingerprint_UnboundReturns404(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()

	h := &handlers.MMOHandler{DB: db}
	rec := lookupSSHFingerprint(t, h, "", "SHA256:never-bound-anything")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unbound fingerprint, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBindSSHKey_DifferentCharacterSameFingerprintRefused(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-2a")
	seedCharacterForInv(t, db, "char-ssh-2b")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-21", "DRAGONSNSHIT-MUD")

	fp := "SHA256:shared-key-attempted-on-two-characters"
	rec1 := bindSSHKey(t, h, token, "char-ssh-2a", fp, "ssh-ed25519 AAAA... a")
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first bind: expected 204, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// §3.1 acceptance: a fingerprint can never be reassigned to a different character -- not
	// "different key, same claimed name" literally (that's enforced by characters.name's own
	// UNIQUE constraint, exercised at the CreateCharacter layer, not here), but the identical
	// underlying principle: one identity primitive (name OR fingerprint) never silently maps to
	// two different characters.
	rec2 := bindSSHKey(t, h, token, "char-ssh-2b", fp, "ssh-ed25519 AAAA... b")
	if rec2.Code != http.StatusConflict {
		t.Fatalf("second bind (different character, same fingerprint): expected 409, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// The fingerprint must still resolve to the ORIGINAL character, not be silently reassigned.
	lookup := lookupSSHFingerprint(t, h, token, fp)
	var resp map[string]string
	json.Unmarshal(lookup.Body.Bytes(), &resp)
	if resp["character_id"] != "char-ssh-2a" {
		t.Fatalf("fingerprint resolves to %q after a refused rebind attempt, want it to still be char-ssh-2a", resp["character_id"])
	}
}

func TestSSHKey_AddSecondThenRevokeFirstStillResolvesToSameCharacter(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-3")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-22", "DRAGONSNSHIT-MUD")

	fp1 := "SHA256:first-key-for-char-ssh-3"
	fp2 := "SHA256:second-key-for-char-ssh-3"
	bindSSHKey(t, h, token, "char-ssh-3", fp1, "ssh-ed25519 AAAA... 1")
	bindSSHKey(t, h, token, "char-ssh-3", fp2, "ssh-ed25519 AAAA... 2")

	list := listSSHKeys(t, h, token, "char-ssh-3")
	var entries []handlers.SSHKeyEntry
	json.Unmarshal(list.Body.Bytes(), &entries)
	if len(entries) != 2 {
		t.Fatalf("expected 2 active keys before revoke, got %d: %+v", len(entries), entries)
	}

	// §3.1/§3.2 acceptance: "Second key added, then first revoked, still resolves to same
	// character."
	revoke := revokeSSHKey(t, h, token, "char-ssh-3", fp1)
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke: expected 204, got %d: %s", revoke.Code, revoke.Body.String())
	}

	lookupRevoked := lookupSSHFingerprint(t, h, token, fp1)
	if lookupRevoked.Code != http.StatusNotFound {
		t.Fatalf("revoked fingerprint should no longer resolve, got %d", lookupRevoked.Code)
	}

	lookupStillGood := lookupSSHFingerprint(t, h, token, fp2)
	var resp map[string]string
	json.Unmarshal(lookupStillGood.Body.Bytes(), &resp)
	if resp["character_id"] != "char-ssh-3" {
		t.Fatalf("second key should still resolve to char-ssh-3, got %q", resp["character_id"])
	}

	listAfter := listSSHKeys(t, h, token, "char-ssh-3")
	var entriesAfter []handlers.SSHKeyEntry
	json.Unmarshal(listAfter.Body.Bytes(), &entriesAfter)
	if len(entriesAfter) != 1 || entriesAfter[0].Fingerprint != fp2 {
		t.Fatalf("expected exactly the second key to remain active, got %+v", entriesAfter)
	}
}

func TestBindSSHKey_ReactivatesOwnPreviouslyRevokedKey(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-4")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-23", "DRAGONSNSHIT-MUD")

	fp := "SHA256:reactivate-me"
	bindSSHKey(t, h, token, "char-ssh-4", fp, "ssh-ed25519 AAAA... orig")
	revokeSSHKey(t, h, token, "char-ssh-4", fp)

	// Re-adding the SAME fingerprint for the SAME character must succeed (reactivate), not
	// error as "already exists" -- a real UNIQUE constraint on fingerprint means a naive INSERT
	// would fail here if this weren't handled explicitly.
	rec := bindSSHKey(t, h, token, "char-ssh-4", fp, "ssh-ed25519 AAAA... orig")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reactivate: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	lookup := lookupSSHFingerprint(t, h, token, fp)
	if lookup.Code != http.StatusOK {
		t.Fatalf("reactivated key should resolve again, got %d", lookup.Code)
	}
}

func TestBindSSHKey_PlayerJWTRejected(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-5")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makePlayerToken(t, keys, "player-1")

	rec := bindSSHKey(t, h, token, "char-ssh-5", "SHA256:whatever", "ssh-ed25519 AAAA...")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-agent JWT, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeSSHKey_PlayerJWTRejected(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-6")

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	agentToken := makeAgentTokenWithName(t, keys, "agent-uuid-24", "DRAGONSNSHIT-MUD")
	bindSSHKey(t, h, agentToken, "char-ssh-6", "SHA256:whatever", "ssh-ed25519 AAAA...")

	playerToken := makePlayerToken(t, keys, "player-1")
	rec := revokeSSHKey(t, h, playerToken, "char-ssh-6", "SHA256:whatever")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-agent JWT, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSSHKeys_EmptyForCharacterWithNoKeys(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "char-ssh-7")

	h := &handlers.MMOHandler{DB: db}
	rec := listSSHKeys(t, h, "", "char-ssh-7")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []handlers.SSHKeyEntry
	json.Unmarshal(rec.Body.Bytes(), &entries)
	if len(entries) != 0 {
		t.Fatalf("expected zero keys, got %+v", entries)
	}
}

func TestBindSSHKey_CharacterNotFound(t *testing.T) {
	db := newSSHKeysDB(t)
	defer db.Close()

	keys, _ := jwt.GenerateKeys()
	h := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	token := makeAgentTokenWithName(t, keys, "agent-uuid-25", "DRAGONSNSHIT-MUD")

	rec := bindSSHKey(t, h, token, "does-not-exist", "SHA256:whatever", "ssh-ed25519 AAAA...")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
