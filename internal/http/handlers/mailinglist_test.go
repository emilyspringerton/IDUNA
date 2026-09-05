package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/mailinglist"
)

func mailingListExportHandler(t *testing.T, keys *jwt.Keys) (http.Handler, *mailinglist.Store, *mailinglist.Vault) {
	t.Helper()
	store, err := mailinglist.Open(t.TempDir() + "/mailinglist.db")
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	vault := mailinglist.NewVault()
	h := &handlers.MailingListHandler{Store: store, Vault: vault}
	protected := middleware.RequireAuth(keys)(middleware.RequirePermission("mailinglist.export")(http.HandlerFunc(h.Export)))
	return protected, store, vault
}

func unlockTestVault(t *testing.T, store *mailinglist.Store, vault *mailinglist.Vault) {
	t.Helper()
	salt, err := mailinglist.NewSalt()
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}
	ct, nonce, err := mailinglist.NewCanary("correct horse battery staple", salt)
	if err != nil {
		t.Fatalf("NewCanary: %v", err)
	}
	if err := store.InitVault(salt, ct, nonce); err != nil {
		t.Fatalf("InitVault: %v", err)
	}
	if err := vault.Unlock("correct horse battery staple", salt, ct, nonce); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

// TestMailingListExport_RequiresPermission -- a caller without
// mailinglist.export must never see subscriber data, even authenticated.
func TestMailingListExport_RequiresPermission(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h, _, _ := mailingListExportHandler(t, keys)
	token := makeAgentToken(t, keys, "some-agent", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailing-list/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestMailingListExport_FailsClosedWhenLocked -- same fail-closed posture as
// subscribe: a locked vault must never leak a partial/garbage export.
func TestMailingListExport_FailsClosedWhenLocked(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h, _, _ := mailingListExportHandler(t, keys)
	token := makeAgentToken(t, keys, "admin-agent", []string{"mailinglist.export"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailing-list/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
}

// TestMailingListExport_JSONDecryptsRealSubscribers -- the real, direct
// answer to "saves your list in IDUNA in case you need to export it later":
// stored ciphertext comes back as real plaintext email addresses.
func TestMailingListExport_JSONDecryptsRealSubscribers(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h, store, vault := mailingListExportHandler(t, keys)
	unlockTestVault(t, store, vault)

	for _, email := range []string{"alice@example.com", "bob@example.com"} {
		ct, nonce, err := vault.Encrypt([]byte(email))
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if _, err := store.AddSubscriber(ct, nonce, "v1", "general"); err != nil {
			t.Fatalf("AddSubscriber: %v", err)
		}
	}

	token := makeAgentToken(t, keys, "admin-agent", []string{"mailinglist.export"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailing-list/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Count       int `json:"count"`
		Subscribers []struct {
			Email string `json:"email"`
		} `json:"subscribers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 2 || len(out.Subscribers) != 2 {
		t.Fatalf("expected 2 subscribers, got %+v", out)
	}
	if out.Subscribers[0].Email != "alice@example.com" || out.Subscribers[1].Email != "bob@example.com" {
		t.Fatalf("unexpected decrypted emails: %+v", out.Subscribers)
	}
}

// TestMailingListExport_CSVFormat -- ?format=csv returns a real CSV, not
// just JSON with a different Content-Type slapped on.
func TestMailingListExport_CSVFormat(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h, store, vault := mailingListExportHandler(t, keys)
	unlockTestVault(t, store, vault)

	ct, nonce, _ := vault.Encrypt([]byte("csv-user@example.com"))
	if _, err := store.AddSubscriber(ct, nonce, "v1", "general"); err != nil {
		t.Fatalf("AddSubscriber: %v", err)
	}

	token := makeAgentToken(t, keys, "admin-agent", []string{"mailinglist.export"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailing-list/export?format=csv", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "id,email,consent_version,consented_at,source,mailchimp_synced") {
		t.Errorf("missing CSV header, got: %s", body)
	}
	if !strings.Contains(body, "csv-user@example.com") {
		t.Errorf("missing decrypted email in CSV body, got: %s", body)
	}
}

// TestMailingListExport_SkipsUndecryptableRowSurvivesRest -- one row with
// mismatched ciphertext/nonce (a real, if rare, corruption case) must not
// take down the whole export; every other real subscriber still comes out.
func TestMailingListExport_SkipsUndecryptableRowSurvivesRest(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	h, store, vault := mailingListExportHandler(t, keys)
	unlockTestVault(t, store, vault)

	// Corrupt row: nonce doesn't match the ciphertext it was sealed with.
	ct1, _, _ := vault.Encrypt([]byte("good1@example.com"))
	_, badNonce, _ := vault.Encrypt([]byte("irrelevant"))
	if _, err := store.AddSubscriber(ct1, badNonce, "v1", "general"); err != nil {
		t.Fatalf("AddSubscriber corrupt: %v", err)
	}
	ct2, nonce2, _ := vault.Encrypt([]byte("good2@example.com"))
	if _, err := store.AddSubscriber(ct2, nonce2, "v1", "general"); err != nil {
		t.Fatalf("AddSubscriber good: %v", err)
	}

	token := makeAgentToken(t, keys, "admin-agent", []string{"mailinglist.export"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mailing-list/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Count       int `json:"count"`
		Subscribers []struct {
			Email string `json:"email"`
		} `json:"subscribers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 1 || len(out.Subscribers) != 1 || out.Subscribers[0].Email != "good2@example.com" {
		t.Fatalf("expected only the one decryptable subscriber to survive, got %+v", out)
	}
}
