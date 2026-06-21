package drive_test

import (
	"encoding/json"
	"testing"

	"iduna/internal/drive"
)

func validSvcAccountJSON(t *testing.T, email, key string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]string{
		"client_email": email,
		"private_key":  key,
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	return string(b)
}

func TestNewBadJSON(t *testing.T) {
	_, err := drive.New("not json", "")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestNewMissingClientEmail(t *testing.T) {
	j := `{"private_key":"pk","token_uri":"https://oauth2.googleapis.com/token"}`
	_, err := drive.New(j, "")
	if err == nil {
		t.Fatal("expected error for missing client_email, got nil")
	}
}

func TestNewMissingPrivateKey(t *testing.T) {
	j := `{"client_email":"svc@example.iam.gserviceaccount.com","token_uri":"https://oauth2.googleapis.com/token"}`
	_, err := drive.New(j, "")
	if err == nil {
		t.Fatal("expected error for missing private_key, got nil")
	}
}

func TestNewDefaultTokenURI(t *testing.T) {
	// When token_uri is absent, New should use the hardcoded Google endpoint.
	// New should succeed for valid client_email + private_key even without token_uri.
	j, _ := json.Marshal(map[string]string{
		"client_email": "svc@example.iam.gserviceaccount.com",
		"private_key":  "placeholder",
	})
	// New succeeds (token_uri defaulted); actual OAuth fails only at Upload/List call time.
	client, err := drive.New(string(j), "folder-123")
	if err != nil {
		t.Fatalf("New with absent token_uri: %v", err)
	}
	if client == nil {
		t.Fatal("New returned nil client")
	}
}

func TestUploadTooLarge(t *testing.T) {
	j := validSvcAccountJSON(t, "svc@example.iam.gserviceaccount.com", "key")
	client, err := drive.New(j, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	big := make([]byte, drive.MaxUploadBytes+1)
	_, err = client.Upload("file.bin", "application/octet-stream", big)
	if err == nil {
		t.Fatal("expected error for oversized upload, got nil")
	}
}
