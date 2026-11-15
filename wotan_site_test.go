package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wotanTestSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":                  "<h1>WOTAN</h1>",
		"bedrockracers/index.html":    "<h1>Bedrock Racers</h1>",
		"bedrockracers/game.wasm":     "\x00asm\x01\x00\x00\x00",
		"nolisting/readme.txt":        "a folder with no index.html",
		".git/config":                 "[core]",
		"bedrockracers/.secret/x.txt": "hidden",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeAPI stands in for IDUNA's mux: it answers every request with "api <method> <path>", and upgrades
// /api/v1/ws the way a WebSocket handler does (hijack, 101), so the test can see a hijack pass through the site handler.
func fakeAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/ws" {
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijacker", 500)
				return
			}
			c, rw, err := hj.Hijack()
			if err != nil {
				return
			}
			rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			rw.Flush()
			c.Close()
			return
		}
		io.WriteString(w, "api "+r.Method+" "+r.URL.Path)
	})
}

func TestWotanSite_ServesFilesAndRoutesAPI(t *testing.T) {
	srv := httptest.NewServer(wotanSiteHandler(wotanTestSite(t), fakeAPI()))
	defer srv.Close()
	get := func(method, path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	cases := []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/", 200, "<h1>WOTAN</h1>"},
		{"GET", "/bedrockracers/", 200, "<h1>Bedrock Racers</h1>"},
		{"GET", "/api/v1/shankpit460/status", 200, "api GET /api/v1/shankpit460/status"},
		{"POST", "/api/v1/games/d2/guest-register", 200, "api POST /api/v1/games/d2/guest-register"},
		{"GET", "/health", 200, "api GET /health"},
		{"GET", "/services/search/jobs", 200, "api GET /services/search/jobs"},
		{"GET", "/.git/config", 404, ""},
		{"GET", "/bedrockracers/.secret/x.txt", 404, ""},
		{"GET", "/nolisting/", 404, ""},
		{"POST", "/index.html", 405, ""},
		{"GET", "/missing.html", 404, ""},
	}
	for _, c := range cases {
		code, body, _ := get(c.method, c.path)
		if code != c.code {
			t.Errorf("%s %s: status %d, want %d (body %q)", c.method, c.path, code, c.code, body)
			continue
		}
		if c.body != "" && strings.TrimSpace(body) != c.body {
			t.Errorf("%s %s: body %q, want %q", c.method, c.path, body, c.body)
		}
	}
	// a file inside a listing-less folder is still served
	if code, body, _ := get("GET", "/nolisting/readme.txt"); code != 200 || !strings.Contains(body, "no index.html") {
		t.Errorf("file in folder without index: %d %q", code, body)
	}
	// wasm must be served as application/wasm, or WebAssembly.instantiateStreaming refuses it
	if _, _, h := get("GET", "/bedrockracers/game.wasm"); h.Get("Content-Type") != "application/wasm" {
		t.Errorf("wasm content type %q", h.Get("Content-Type"))
	}
	if _, _, h := get("GET", "/bedrockracers/index.html"); h.Get("Cache-Control") != "no-cache" {
		t.Errorf("site files must revalidate, Cache-Control %q", h.Get("Cache-Control"))
	}
}

// The matchmaking WebSockets (shankpit-460, Bedrock Racers) upgrade inside IDUNA's handlers; the site handler must
// hand them the original ResponseWriter so the hijack still works through the WOTAN listener.
func TestWotanSite_WebSocketUpgradePassesThrough(t *testing.T) {
	srv := httptest.NewServer(wotanSiteHandler(wotanTestSite(t), fakeAPI()))
	defer srv.Close()
	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET /api/v1/ws HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 101") {
		t.Fatalf("upgrade through the site handler: %q", line)
	}
}

func TestWotanSite_PathTraversalStaysInside(t *testing.T) {
	dir := wotanTestSite(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("must not be served"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := wotanSiteHandler(dir, fakeAPI())
	for _, p := range []string{"/../outside.txt", "/bedrockracers/../../outside.txt", "/%2e%2e/outside.txt"} {
		req := httptest.NewRequest("GET", "http://x"+p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "must not be served") {
			t.Errorf("%s escaped the site folder", p)
		}
	}
}
