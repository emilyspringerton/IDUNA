package mailinglist

import "testing"

func TestVault_WrongPassphraseRejected(t *testing.T) {
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}
	ct, nonce, err := NewCanary("correct horse battery staple", salt)
	if err != nil {
		t.Fatalf("NewCanary: %v", err)
	}

	v := NewVault()
	if !v.Locked() {
		t.Fatal("expected new vault to be locked")
	}

	if err := v.Unlock("wrong passphrase", salt, ct, nonce); err != ErrWrongPassword {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
	if !v.Locked() {
		t.Fatal("vault must stay locked after a failed unlock attempt")
	}
}

func TestVault_CorrectPassphraseUnlocksAndRoundtrips(t *testing.T) {
	salt, _ := NewSalt()
	ct, nonce, err := NewCanary("correct horse battery staple", salt)
	if err != nil {
		t.Fatalf("NewCanary: %v", err)
	}

	v := NewVault()
	if err := v.Unlock("correct horse battery staple", salt, ct, nonce); err != nil {
		t.Fatalf("Unlock with correct passphrase failed: %v", err)
	}
	if v.Locked() {
		t.Fatal("expected vault to be unlocked")
	}

	email := []byte("test@example.com")
	ciphertext, nonce2, err := v.Encrypt(email)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plain, err := v.Decrypt(ciphertext, nonce2)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(plain) != string(email) {
		t.Fatalf("roundtrip mismatch: got %q want %q", plain, email)
	}
}

func TestVault_EncryptFailsWhenLocked(t *testing.T) {
	v := NewVault()
	if _, _, err := v.Encrypt([]byte("x")); err != ErrLocked {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if _, err := v.Decrypt([]byte("x"), []byte("y")); err != ErrLocked {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
}

func TestVault_LockDiscardsKey(t *testing.T) {
	salt, _ := NewSalt()
	ct, nonce, _ := NewCanary("pw", salt)

	v := NewVault()
	if err := v.Unlock("pw", salt, ct, nonce); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	v.Lock()
	if !v.Locked() {
		t.Fatal("expected vault to be locked after Lock()")
	}
	if _, _, err := v.Encrypt([]byte("x")); err != ErrLocked {
		t.Fatalf("expected ErrLocked after Lock(), got %v", err)
	}
}

func TestStore_InitVaultRefusesDoubleInit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	salt, _ := NewSalt()
	ct, nonce, _ := NewCanary("pw", salt)

	if err := s.InitVault(salt, ct, nonce); err != nil {
		t.Fatalf("first InitVault should succeed: %v", err)
	}
	if err := s.InitVault(salt, ct, nonce); err == nil {
		t.Fatal("expected second InitVault to be refused (would orphan existing data)")
	}
}

func TestStore_AddSubscriberAndMarkSynced(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.AddSubscriber([]byte("ciphertext"), []byte("nonce"), "v1")
	if err != nil {
		t.Fatalf("AddSubscriber: %v", err)
	}
	if err := s.MarkMailchimpSynced(id); err != nil {
		t.Fatalf("MarkMailchimpSynced: %v", err)
	}
}
