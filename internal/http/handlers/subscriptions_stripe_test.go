package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

// realStripeSig builds a real, correctly-signed Stripe-Signature header value for payload,
// matching Stripe's own documented v1 scheme exactly -- used both to prove verifyStripeSignature
// accepts a genuine signature and as the base for the tamper/replay tests below.
func realStripeSig(secret string, payload []byte, ts int64) string {
	signedPayload := strconv.FormatInt(ts, 10) + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + sig
}

func TestVerifyStripeSignature_RealValidSignatureAccepted(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"customer.subscription.created"}`)
	secret := "whsec_test123"
	sig := realStripeSig(secret, payload, time.Now().Unix())
	if err := verifyStripeSignature(payload, sig, secret, 5*time.Minute); err != nil {
		t.Fatalf("a real, correctly-signed payload should verify: %v", err)
	}
}

// TestVerifyStripeSignature_ThisIsTheRealBugFound -- the exact real vulnerability this fix
// closes: the OLD code only checked that a Stripe-Signature header was non-empty, never that it
// was actually correct. Any garbage value used to pass; it must not anymore.
func TestVerifyStripeSignature_ThisIsTheRealBugFound(t *testing.T) {
	payload := []byte(`{"id":"evt_forged","type":"customer.subscription.created","data":{"object":{"metadata":{"iduna_user_id":"victim-user","gfd_tier_id":"emily_plus"}}}}`)
	err := verifyStripeSignature(payload, "t=1700000000,v1=totally-not-a-real-signature", "whsec_test123", 5*time.Minute)
	if err == nil {
		t.Fatal("a forged Stripe-Signature header must be rejected -- this is the real gap that let anyone grant themselves a free subscription")
	}
}

func TestVerifyStripeSignature_MissingHeaderRejected(t *testing.T) {
	if err := verifyStripeSignature([]byte("{}"), "", "whsec_test123", 5*time.Minute); err == nil {
		t.Fatal("expected a real error for a missing Stripe-Signature header")
	}
}

func TestVerifyStripeSignature_TamperedPayloadRejected(t *testing.T) {
	payload := []byte(`{"id":"evt_1","data":{"object":{"metadata":{"iduna_user_id":"alice"}}}}`)
	secret := "whsec_test123"
	sig := realStripeSig(secret, payload, time.Now().Unix())
	// Same real signature, but the payload actually delivered to the handler was changed after
	// signing (e.g. a MITM swapping the target user_id) -- must fail.
	tampered := []byte(`{"id":"evt_1","data":{"object":{"metadata":{"iduna_user_id":"mallory"}}}}`)
	if err := verifyStripeSignature(tampered, sig, secret, 5*time.Minute); err == nil {
		t.Fatal("a tampered payload with a signature computed over the ORIGINAL payload must be rejected")
	}
}

func TestVerifyStripeSignature_WrongSecretRejected(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	sig := realStripeSig("whsec_attacker_controlled", payload, time.Now().Unix())
	if err := verifyStripeSignature(payload, sig, "whsec_real_secret", 5*time.Minute); err == nil {
		t.Fatal("a signature computed with the wrong secret must be rejected")
	}
}

func TestVerifyStripeSignature_StaleTimestampRejected(t *testing.T) {
	payload := []byte(`{"id":"evt_1"}`)
	secret := "whsec_test123"
	old := time.Now().Add(-1 * time.Hour).Unix()
	sig := realStripeSig(secret, payload, old)
	if err := verifyStripeSignature(payload, sig, secret, 5*time.Minute); err == nil {
		t.Fatal("a real, correctly-signed but stale (1 hour old) event must be rejected -- real replay-attack protection")
	}
}

func TestVerifyStripeSignature_MultipleV1SignaturesOneMatching(t *testing.T) {
	// Stripe's own real documented behavior during a webhook signing-secret rotation: an event
	// can briefly carry more than one v1 value (old secret + new secret) -- a match on ANY of
	// them is real, valid acceptance.
	payload := []byte(`{"id":"evt_1"}`)
	secret := "whsec_new"
	ts := time.Now().Unix()
	realSig := realStripeSig(secret, payload, ts)
	// Splice in a bogus extra v1 alongside the real one.
	header := strings.Replace(realSig, "v1=", "v1=bogus000,v1=", 1)
	if err := verifyStripeSignature(payload, header, secret, 5*time.Minute); err != nil {
		t.Fatalf("expected acceptance when at least one v1 value matches: %v", err)
	}
}
