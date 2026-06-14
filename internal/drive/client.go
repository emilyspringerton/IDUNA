// Package drive provides a minimal Google Drive API client using service account
// credentials. No external SDK required — uses stdlib crypto + net/http only.
//
// Auth flow:
//  1. Parse service account JSON → client_email + RSA private key
//  2. Sign RS256 JWT for https://oauth2.googleapis.com/token
//  3. Exchange JWT for short-lived Bearer access token (1h)
//  4. Use Bearer token for Drive API v3 calls
//
// The access token is refreshed automatically on each operation (stateless).
// Caller should cache the Client across requests; the per-call token fetch
// adds ~100ms latency. For high-throughput use, add a token cache.
package drive

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const (
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	driveScope    = "https://www.googleapis.com/auth/drive.file"
	uploadURL     = "https://www.googleapis.com/upload/drive/v3/files"
	listURL       = "https://www.googleapis.com/drive/v3/files"
	fileFields    = "id,name,mimeType,size,webViewLink,createdTime,modifiedTime"

	// MaxUploadBytes is the upload size limit for multipart upload. Larger files
	// should use resumable upload (not yet implemented). 32 MB covers most
	// training JSONL files in early experiments.
	MaxUploadBytes = 32 << 20 // 32 MB
)

// Client is a service-account-authenticated Google Drive API v3 client.
// Safe for concurrent use after construction.
type Client struct {
	creds    serviceAccountCreds
	folderID string // default parent folder for uploads; empty = Drive root
	hc       *http.Client
}

type serviceAccountCreds struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// New constructs a Client from a service account JSON key string and an
// optional Drive folder ID for uploads. serviceAccountJSON is the raw contents
// of a Google service account key file (the JSON downloaded from IAM console).
func New(serviceAccountJSON, folderID string) (*Client, error) {
	var creds serviceAccountCreds
	if err := json.Unmarshal([]byte(serviceAccountJSON), &creds); err != nil {
		return nil, fmt.Errorf("drive: parse service account JSON: %w", err)
	}
	if creds.ClientEmail == "" {
		return nil, fmt.Errorf("drive: service account JSON missing client_email")
	}
	if creds.PrivateKey == "" {
		return nil, fmt.Errorf("drive: service account JSON missing private_key")
	}
	if creds.TokenURI == "" {
		creds.TokenURI = tokenEndpoint
	}
	return &Client{
		creds:    creds,
		folderID: folderID,
		hc:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// FileInfo is the Drive API file resource subset we care about.
type FileInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	Size         string `json:"size"` // Drive returns size as a string
	WebViewLink  string `json:"webViewLink"`
	CreatedTime  string `json:"createdTime"`
	ModifiedTime string `json:"modifiedTime"`
}

// Upload streams data to Google Drive as a new file under the configured folder.
// mimeType should be "application/jsonl", "application/octet-stream", etc.
// Returns the created file's metadata including shareable WebViewLink.
func (c *Client) Upload(filename, mimeType string, data []byte) (*FileInfo, error) {
	if len(data) > MaxUploadBytes {
		return nil, fmt.Errorf("drive: file too large for multipart upload (%d > %d bytes)", len(data), MaxUploadBytes)
	}

	token, err := c.accessToken()
	if err != nil {
		return nil, err
	}

	// Build multipart/related body: metadata part + media part.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Part 1: file metadata JSON.
	metaHeader := textproto.MIMEHeader{}
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, err := mw.CreatePart(metaHeader)
	if err != nil {
		return nil, fmt.Errorf("drive: create meta part: %w", err)
	}
	meta := map[string]any{"name": filename}
	if c.folderID != "" {
		meta["parents"] = []string{c.folderID}
	}
	if err := json.NewEncoder(metaPart).Encode(meta); err != nil {
		return nil, fmt.Errorf("drive: encode metadata: %w", err)
	}

	// Part 2: file data.
	dataHeader := textproto.MIMEHeader{}
	dataHeader.Set("Content-Type", mimeType)
	dataPart, err := mw.CreatePart(dataHeader)
	if err != nil {
		return nil, fmt.Errorf("drive: create data part: %w", err)
	}
	if _, err := dataPart.Write(data); err != nil {
		return nil, fmt.Errorf("drive: write data part: %w", err)
	}
	mw.Close()

	uploadTarget := uploadURL + "?uploadType=multipart&fields=" + url.QueryEscape(fileFields)
	req, err := http.NewRequest(http.MethodPost, uploadTarget, &body)
	if err != nil {
		return nil, fmt.Errorf("drive: build upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "multipart/related; boundary="+mw.Boundary())

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive: upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive: upload failed (%d): %s", resp.StatusCode, errBody)
	}

	var info FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("drive: decode upload response: %w", err)
	}
	return &info, nil
}

// List returns files in the configured folder, optionally filtered by an
// additional Drive query expression (e.g., `name contains 'checkpoint'`).
// Pass empty string for no extra filter.
func (c *Client) List(extraQuery string) ([]FileInfo, error) {
	token, err := c.accessToken()
	if err != nil {
		return nil, err
	}

	q := "trashed = false"
	if c.folderID != "" {
		q += fmt.Sprintf(" and '%s' in parents", c.folderID)
	}
	if extraQuery != "" {
		q += " and " + extraQuery
	}

	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("drive: build list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	params := req.URL.Query()
	params.Set("q", q)
	params.Set("fields", "files("+fileFields+")")
	params.Set("orderBy", "createdTime desc")
	params.Set("pageSize", "100")
	req.URL.RawQuery = params.Encode()

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive: list: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Files []FileInfo `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("drive: decode list response: %w", err)
	}
	return result.Files, nil
}

// GetFile returns metadata for a single file by Drive file ID.
func (c *Client) GetFile(fileID string) (*FileInfo, error) {
	token, err := c.accessToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, listURL+"/"+fileID+"?fields="+url.QueryEscape(fileFields), nil)
	if err != nil {
		return nil, fmt.Errorf("drive: build get request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive: get file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("drive: file not found: %s", fileID)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive: get file failed (%d): %s", resp.StatusCode, body)
	}

	var info FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("drive: decode get response: %w", err)
	}
	return &info, nil
}

// accessToken fetches a short-lived Bearer token from Google's OAuth2 endpoint
// using a service account JWT. Tokens expire after 1 hour; callers should not
// cache them across requests (latency cost: ~100ms per call).
func (c *Client) accessToken() (string, error) {
	now := time.Now().Unix()
	claims := map[string]any{
		"iss": c.creds.ClientEmail,
		"sub": c.creds.ClientEmail,
		"aud": c.creds.TokenURI,
		"scope": driveScope,
		"iat": now,
		"exp": now + 3600,
	}

	// Encode header and payload as base64url.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("drive: marshal claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signing := header + "." + payload

	// Parse RSA private key (PKCS#8 PEM format from Google service account JSON).
	privKey, err := parseRSAPrivateKey(c.creds.PrivateKey)
	if err != nil {
		return "", err
	}

	// Sign with RS256 (PKCS1v15 + SHA256).
	h := sha256.New()
	h.Write([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, h.Sum(nil))
	if err != nil {
		return "", fmt.Errorf("drive: sign JWT: %w", err)
	}
	jwtStr := signing + "." + base64.RawURLEncoding.EncodeToString(sig)

	// Exchange JWT for access token.
	resp, err := c.hc.PostForm(c.creds.TokenURI, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwtStr},
	})
	if err != nil {
		return "", fmt.Errorf("drive: token request: %w", err)
	}
	defer resp.Body.Close()

	var tr struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("drive: decode token response: %w", err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("drive: token error %s: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("drive: empty access token in response")
	}
	return tr.AccessToken, nil
}

// parseRSAPrivateKey decodes a PKCS#8 or PKCS#1 PEM private key.
// Google service accounts always use PKCS#8 (`-----BEGIN PRIVATE KEY-----`),
// but we handle PKCS#1 (`-----BEGIN RSA PRIVATE KEY-----`) for completeness.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	// Google puts literal \n in the JSON; normalize to real newlines.
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("drive: failed to decode PEM block from private key")
	}

	switch block.Type {
	case "PRIVATE KEY": // PKCS#8
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("drive: parse PKCS8 private key: %w", err)
		}
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("drive: expected RSA key, got %T", key)
		}
		return rk, nil
	case "RSA PRIVATE KEY": // PKCS#1
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("drive: unknown PEM block type: %s", block.Type)
	}
}
