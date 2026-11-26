// Copied verbatim from WOTAN/ops/vultr/api.go @ WOTAN 5857564 (wotan-vultr). Keep the two in step.
package main

// Thin JSON-over-HTTPS client shared by the Vultr and Cloudflare calls. The bearer token is held in memory only: it is
// never put in a URL, an argument, a log line or an error message, and TLS verification uses the system roots (never
// skipped).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type api struct {
	base  string // e.g. https://api.vultr.com/v2 (overridable in tests)
	token string // may be empty for public endpoints
	hc    *http.Client
}

func newAPI(base, token string) *api {
	return &api{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Timeout: 60 * time.Second}}
}

// do sends body (if non-nil) as JSON and decodes a 2xx response into out (if non-nil). Non-2xx returns an error with
// the status and a trimmed body; bodies from these APIs never echo the caller's token.
func (a *api) do(method, path string, body, out interface{}) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.base+path, rd)
	if err != nil {
		return err
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decoding response: %v", method, path, err)
		}
	}
	return nil
}
