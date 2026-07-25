package vault

import (
	"database/sql"
	"errors"
	"testing"

	"iduna/internal/mailinglist"
)

func TestStore_InitVaultRefusesDoubleInit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	salt, _ := mailinglist.NewSalt()
	ct, nonce, _ := mailinglist.NewCanary("pw", salt)

	if err := s.InitVault(salt, ct, nonce); err != nil {
		t.Fatalf("first InitVault should succeed: %v", err)
	}
	if err := s.InitVault(salt, ct, nonce); err == nil {
		t.Fatal("expected second InitVault to be refused (would orphan existing items)")
	}
}

func TestStore_ItemCRUDRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Full stack: real Vault encrypt/decrypt through the real Store, not
	// just ciphertext bytes passed straight through -- this is the actual
	// reuse of internal/mailinglist.Vault the package doc promises.
	salt, _ := mailinglist.NewSalt()
	ct, nonce, _ := mailinglist.NewCanary("correct horse battery staple", salt)
	if err := s.InitVault(salt, ct, nonce); err != nil {
		t.Fatalf("InitVault: %v", err)
	}
	v := mailinglist.NewVault()
	if err := v.Unlock("correct horse battery staple", salt, ct, nonce); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	plaintext := []byte(`{"name":"AWS Root","fields":{"username":"root","password":"hunter2"}}`)
	dataCT, dataNonce, err := v.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	id, err := s.AddItem(ItemLogin, dataCT, dataNonce)
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	raw, err := s.GetRaw(id)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if raw.ItemType != string(ItemLogin) {
		t.Errorf("item_type = %q, want %q", raw.ItemType, ItemLogin)
	}
	decrypted, err := v.Decrypt(raw.DataCiphertext, raw.DataNonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("roundtrip mismatch: got %q want %q", decrypted, plaintext)
	}

	items, err := s.ListRaw()
	if err != nil {
		t.Fatalf("ListRaw: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListRaw returned %d items, want 1", len(items))
	}

	updatedPlain := []byte(`{"name":"AWS Root (rotated)","fields":{"username":"root","password":"newpass"}}`)
	updCT, updNonce, err := v.Encrypt(updatedPlain)
	if err != nil {
		t.Fatalf("Encrypt (update): %v", err)
	}
	if err := s.UpdateItem(id, ItemLogin, updCT, updNonce); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	raw2, err := s.GetRaw(id)
	if err != nil {
		t.Fatalf("GetRaw after update: %v", err)
	}
	decrypted2, err := v.Decrypt(raw2.DataCiphertext, raw2.DataNonce)
	if err != nil {
		t.Fatalf("Decrypt after update: %v", err)
	}
	if string(decrypted2) != string(updatedPlain) {
		t.Fatalf("post-update roundtrip mismatch: got %q want %q", decrypted2, updatedPlain)
	}

	if err := s.DeleteItem(id); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := s.GetRaw(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestStore_UpdateDeleteMissingItemReturnsErrNoRows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.UpdateItem(999, ItemNote, []byte("x"), []byte("y")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateItem on missing id: expected sql.ErrNoRows, got %v", err)
	}
	if err := s.DeleteItem(999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DeleteItem on missing id: expected sql.ErrNoRows, got %v", err)
	}
}

func TestStore_InitializedReflectsState(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	init, err := s.Initialized()
	if err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	if init {
		t.Fatal("expected fresh store to be uninitialized")
	}

	salt, _ := mailinglist.NewSalt()
	ct, nonce, _ := mailinglist.NewCanary("pw", salt)
	if err := s.InitVault(salt, ct, nonce); err != nil {
		t.Fatalf("InitVault: %v", err)
	}
	init, err = s.Initialized()
	if err != nil {
		t.Fatalf("Initialized: %v", err)
	}
	if !init {
		t.Fatal("expected store to report initialized after InitVault")
	}
}
