// oauth.go — OAuth2-authorization-code-flow variant of the Drive client,
// alongside client.go's existing service-account flow. Used by the Back
// Office "Drive slurp" feature (EMILY/BACKLOG.md S187-03/S188-05/S189-10):
// an admin authorizes with their own Google account (Drive-scoped consent,
// not the app-identity ceremony web_ceremony.go already owns), and this
// package lists/fetches files using that per-admin access token instead of
// the service account's.
//
// Kept in this package (not a new one) since it shares FileInfo, listURL,
// fileFields, tokenEndpoint with client.go -- one real Drive-files-listing
// shape, two ways to authenticate against it.
package drive

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// OAuthScope is the Drive scope requested for the admin-consent flow --
// read-only is enough for "browse recent files, slurp one," matching the
// original spec's own "drive.readonly" ask. Broader than client.go's
// upload-oriented "drive.file" scope on purpose: the admin needs to see
// files this app didn't itself create.
const OAuthScope = "https://www.googleapis.com/auth/drive.readonly"

// OAuthToken is the access/refresh token pair returned by Google's token
// endpoint for the authorization-code flow, plus enough bookkeeping to know
// when a refresh is needed. Persisted as JSON by the caller (admin_drive_
// slurp.go); this type has no storage opinion of its own.
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Expired reports whether AccessToken needs a refresh before use. A 60s
// safety margin avoids a token expiring mid-request.
func (t *OAuthToken) Expired() bool {
	return time.Now().After(t.ExpiresAt.Add(-60 * time.Second))
}

// ExchangeCode exchanges an OAuth2 authorization code for an access+refresh
// token pair. Mirrors handlers.WebCeremonyHandler.exchangeCodeForIDToken's
// real shape (same token endpoint, same form-encoded request) but captures
// access_token/refresh_token/expires_in instead of id_token -- that
// handler's own flow never needed the refresh token, this one does
// (access_type=offline must also be set on the authorize URL that produced
// this code, or refresh_token will come back empty).
func ExchangeCode(clientID, clientSecret, code, redirectURI string) (*OAuthToken, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	body := url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	resp, err := client.PostForm(tokenEndpoint, body)
	if err != nil {
		return nil, fmt.Errorf("drive oauth: exchange request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("drive oauth: token endpoint %d: %s", resp.StatusCode, raw)
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("drive oauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("drive oauth: token response missing access_token")
	}
	if tr.RefreshToken == "" {
		return nil, fmt.Errorf("drive oauth: token response missing refresh_token -- authorize URL must request access_type=offline and prompt=consent")
	}
	return &OAuthToken{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// Refresh exchanges a still-valid refresh token for a new access token.
// Google does not re-issue a refresh_token on refresh calls -- the caller
// keeps the original RefreshToken and only replaces AccessToken/ExpiresAt.
func Refresh(clientID, clientSecret string, tok *OAuthToken) error {
	client := &http.Client{Timeout: 10 * time.Second}
	body := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {tok.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	resp, err := client.PostForm(tokenEndpoint, body)
	if err != nil {
		return fmt.Errorf("drive oauth: refresh request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drive oauth: refresh endpoint %d: %s", resp.StatusCode, raw)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return fmt.Errorf("drive oauth: decode refresh response: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("drive oauth: refresh response missing access_token")
	}
	tok.AccessToken = tr.AccessToken
	tok.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return nil
}

// ListWithToken lists Drive files (most-recent first) using a plain bearer
// access token instead of client.go's service-account JWT signing. No
// folder scoping -- an admin's own Drive has no equivalent to client.go's
// upload folderID, "recent files" means the whole Drive, matching the
// original spec ("sees a list of recent files").
func ListWithToken(accessToken string) ([]FileInfo, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("drive oauth: build list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	params := req.URL.Query()
	params.Set("q", "trashed = false")
	params.Set("fields", "files("+fileFields+")")
	params.Set("orderBy", "modifiedTime desc")
	params.Set("pageSize", "50")
	req.URL.RawQuery = params.Encode()

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive oauth: list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive oauth: list failed (%d): %s", resp.StatusCode, raw)
	}

	var result struct {
		Files []FileInfo `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("drive oauth: decode list response: %w", err)
	}
	return result.Files, nil
}

// DownloadWithToken fetches a file's raw content via Drive's alt=media
// export. Used by the slurp job to actually pull the file, not just list
// its metadata.
func DownloadWithToken(accessToken, fileID string) ([]byte, error) {
	hc := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, listURL+"/"+fileID+"?alt=media", nil)
	if err != nil {
		return nil, fmt.Errorf("drive oauth: build download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drive oauth: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive oauth: download failed (%d): %s", resp.StatusCode, raw)
	}
	return io.ReadAll(resp.Body)
}
