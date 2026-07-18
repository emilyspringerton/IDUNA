// Package mailinglist implements a never-at-rest-unencrypted store for
// public marketing signups (e.g. okemily.com's Mailchimp-gated form).
//
// Design constraint, from the founder directly: the subscriber email list
// must NEVER exist unencrypted on disk, under any circumstance — not "encrypt
// the whole DB with a machine-held key" (the key would sit right next to the
// ciphertext on the same disk, defeating the point against the realistic
// threat of a leaked backup or stolen disk image), but a genuine
// human-memorized passphrase, held only in server memory after an explicit
// unlock step, and only for as long as the process stays up.
//
// This trades operational convenience for that guarantee: every IDUNA
// restart (crash, OOM-kill, deploy) locks the vault again, and new signups
// fail closed (503) until a human runs `cmd/mailing-list-unlock` and types
// the passphrase again. That is a deliberate, accepted cost — see
// EMILY/BACKLOG.md SECTION 152/153 and the founder's explicit "never at rest
// unencrypted" instruction. It intentionally does NOT lock down the rest of
// IDUNA (auth, Apples, etc.) — only this one subsystem degrades on restart.
//
// Key derivation: Argon2id (RFC 9106 recommended parameters) over the
// passphrase + a random per-installation salt. The salt is not secret (it's
// stored alongside the ciphertext) — only the passphrase is. A canary value
// (a known plaintext, encrypted once at vault initialization) lets Unlock
// detect a wrong passphrase instead of silently producing garbage.
package mailinglist

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32 // AES-256
	saltLen      = 16
	canaryPlain  = "mailing-list-vault-canary-v1"
)

var (
	ErrLocked        = errors.New("mailing list vault is locked")
	ErrWrongPassword = errors.New("incorrect passphrase")
)

// Vault holds the derived AES-256 key only in memory, never on disk.
type Vault struct {
	mu  sync.RWMutex
	key []byte // nil when locked
}

// NewVault returns a locked vault. Call Unlock before any encrypt/decrypt.
func NewVault() *Vault {
	return &Vault{}
}

// Locked reports whether the vault currently has no key in memory.
func (v *Vault) Locked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.key == nil
}

// Unlock derives the AES key from passphrase+salt and verifies it against
// the stored canary ciphertext. Returns ErrWrongPassword on mismatch — the
// vault stays locked in that case.
func (v *Vault) Unlock(passphrase string, salt, canaryCiphertext, canaryNonce []byte) error {
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	plain, err := decryptWithKey(key, canaryCiphertext, canaryNonce)
	if err != nil || subtle.ConstantTimeCompare(plain, []byte(canaryPlain)) != 1 {
		zero(key)
		return ErrWrongPassword
	}

	v.mu.Lock()
	v.key = key
	v.mu.Unlock()
	return nil
}

// Lock discards the in-memory key. New encrypt/decrypt calls fail closed
// until Unlock is called again.
func (v *Vault) Lock() {
	v.mu.Lock()
	zero(v.key)
	v.key = nil
	v.mu.Unlock()
}

// Encrypt encrypts plaintext with the vault's in-memory key. Fails with
// ErrLocked if the vault hasn't been unlocked.
func (v *Vault) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	v.mu.RLock()
	key := v.key
	v.mu.RUnlock()
	if key == nil {
		return nil, nil, ErrLocked
	}
	return encryptWithKey(key, plaintext)
}

// Decrypt decrypts ciphertext with the vault's in-memory key. Fails with
// ErrLocked if the vault hasn't been unlocked.
func (v *Vault) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	v.mu.RLock()
	key := v.key
	v.mu.RUnlock()
	if key == nil {
		return nil, ErrLocked
	}
	return decryptWithKey(key, ciphertext, nonce)
}

// NewSalt generates a fresh random salt for first-time vault initialization
// (run once, e.g. by a setup script — see cmd/mailing-list-unlock -init).
func NewSalt() ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return salt, nil
}

// NewCanary derives a key from passphrase+salt and encrypts the canary
// plaintext with it — the one-time setup step that lets future Unlock calls
// verify a passphrase without ever storing the passphrase itself.
func NewCanary(passphrase string, salt []byte) (ciphertext, nonce []byte, err error) {
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	defer zero(key)
	return encryptWithKey(key, []byte(canaryPlain))
}

func encryptWithKey(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func decryptWithKey(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
