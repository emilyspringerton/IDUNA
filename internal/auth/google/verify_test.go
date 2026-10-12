package google

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// This file exercises the real signature-verification path against a locally generated RSA key
// and a fake JWKS server, instead of mocking it away — the previous version of this file
// (TestGooglePackageCompiles) tested a strings.Split call and nothing about Google token
// verification at all, which is exactly how a real bug (VerifyPKCS1v15 was called with the hash
// identifier for SHA-224 while actually hashing with SHA-256, so every real id_token failed
// "crypto/rsa: input must be hashed message") shipped unnoticed: every other call site always
// substitutes the VerifyToken test seam and never calls Verify() for real. Found live, 2026-10-12,
// via a real sign-in attempt once the redirect_uri host bug (fixed the same day) stopped blocking
// it earlier in the flow.

const testKid = "test-key-1"

func newJWKSServer(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	jwk := map[string]any{
		"kty": "RSA",
		"kid": testKid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big64(pub.E)),
	}
	body, err := json.Marshal(map[string]any{"keys": []any{jwk}})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

// big64 encodes a small int (the RSA public exponent) as big-endian bytes the way the real
// Google JWKS "e" field does (typically 3 bytes for 65537), trimmed of leading zero bytes.
func big64(e int) []byte {
	b := []byte{byte(e >> 16), byte(e >> 8), byte(e)}
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

func signToken(t *testing.T, priv *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": testKid, "typ": "JWT"}
	hJSON, _ := json.Marshal(header)
	cJSON, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hJSON) + "." + base64.RawURLEncoding.EncodeToString(cJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func withFakeGoogle(t *testing.T, pub *rsa.PublicKey) {
	t.Helper()
	srv := newJWKSServer(t, pub)
	t.Cleanup(srv.Close)
	origURL, origKeys, origFetchAt := googleCertsURL, jwkCache.keys, jwkCache.fetchAt
	googleCertsURL = srv.URL
	jwkCache.keys = nil
	jwkCache.fetchAt = time.Time{}
	t.Cleanup(func() {
		googleCertsURL = origURL
		jwkCache.keys = origKeys
		jwkCache.fetchAt = origFetchAt
	})
}

func testKeyPair(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func baseClaims(aud string) map[string]any {
	return map[string]any{
		"sub": "12345", "email": "ada@example.com", "email_verified": true, "name": "Ada",
		"iss": "https://accounts.google.com", "aud": aud, "exp": time.Now().Add(time.Hour).Unix(),
	}
}

func TestVerify_ValidTokenAccepted(t *testing.T) {
	priv := testKeyPair(t)
	withFakeGoogle(t, &priv.PublicKey)
	tok := signToken(t, priv, baseClaims("client-123"))

	claims, err := Verify(context.Background(), tok, "client-123")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Sub != "12345" || claims.Email != "ada@example.com" || !claims.EmailVerified || claims.Name != "Ada" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestVerify_TamperedSignatureRejected(t *testing.T) {
	priv := testKeyPair(t)
	withFakeGoogle(t, &priv.PublicKey)
	tok := signToken(t, priv, baseClaims("client-123"))
	tok = tok[:len(tok)-4] + "xxxx" // corrupt the signature bytes

	if _, err := Verify(context.Background(), tok, "client-123"); err == nil {
		t.Fatal("want error for tampered signature, got nil")
	}
}

func TestVerify_WrongAudienceRejected(t *testing.T) {
	priv := testKeyPair(t)
	withFakeGoogle(t, &priv.PublicKey)
	tok := signToken(t, priv, baseClaims("someone-elses-client"))

	if _, err := Verify(context.Background(), tok, "client-123"); err == nil {
		t.Fatal("want aud mismatch error, got nil")
	}
}

func TestVerify_ExpiredTokenRejected(t *testing.T) {
	priv := testKeyPair(t)
	withFakeGoogle(t, &priv.PublicKey)
	claims := baseClaims("client-123")
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	tok := signToken(t, priv, claims)

	if _, err := Verify(context.Background(), tok, "client-123"); err == nil {
		t.Fatal("want expired error, got nil")
	}
}

func TestVerify_WrongIssuerRejected(t *testing.T) {
	priv := testKeyPair(t)
	withFakeGoogle(t, &priv.PublicKey)
	claims := baseClaims("client-123")
	claims["iss"] = "https://evil.example"
	tok := signToken(t, priv, claims)

	if _, err := Verify(context.Background(), tok, "client-123"); err == nil {
		t.Fatal("want issuer mismatch error, got nil")
	}
}
