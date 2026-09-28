package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
)

const sampleCookiesTxt = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tabc123\n"

func postCookies(h *handlers.MixforgeCookiesHandler, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mixforge/cookies", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rr := httptest.NewRecorder()
	h.Handle(rr, req)
	return rr
}

func TestMixforgeCookiesWritesFileWithEnvToken(t *testing.T) {
	out := filepath.Join(t.TempDir(), "var", "ytdlp-cookies.txt")
	h := &handlers.MixforgeCookiesHandler{Token: "tok", FilePath: out}

	rr := postCookies(h, "Bearer tok", sampleCookiesTxt)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != sampleCookiesTxt {
		t.Fatalf("cookies file = %q, %v", got, err)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0600 {
		t.Fatalf("cookies file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestMixforgeCookiesTokenFromSecretsFile(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "mixforge-secrets.env")
	out := filepath.Join(dir, "ytdlp-cookies.txt")
	h := &handlers.MixforgeCookiesHandler{TokenFile: secrets, FilePath: out}

	// No secrets file yet: disabled, not open.
	if rr := postCookies(h, "Bearer anything", sampleCookiesTxt); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing secrets file: want 503, got %d", rr.Code)
	}

	// Token appears on disk: picked up without rebuilding the handler (no restart).
	if err := os.WriteFile(secrets, []byte("# comment\nOTHER=x\nexport MIXFORGE_COOKIE_UPLOAD_TOKEN=\"filetok\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if rr := postCookies(h, "Bearer wrong", sampleCookiesTxt); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", rr.Code)
	}
	if rr := postCookies(h, "Bearer filetok", sampleCookiesTxt); rr.Code != http.StatusOK {
		t.Fatalf("file token: want 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Env token takes precedence over the file.
	h.Token = "envtok"
	if rr := postCookies(h, "Bearer filetok", sampleCookiesTxt); rr.Code != http.StatusUnauthorized {
		t.Fatalf("env should override file: want 401, got %d", rr.Code)
	}
}

func TestMixforgeCookiesRejects(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ytdlp-cookies.txt")
	h := &handlers.MixforgeCookiesHandler{Token: "tok", FilePath: out}

	if rr := postCookies(h, "", sampleCookiesTxt); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: want 401, got %d", rr.Code)
	}
	if rr := postCookies(h, "Bearer tok", "<html>not cookies</html>"); rr.Code != http.StatusBadRequest {
		t.Fatalf("junk body: want 400, got %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mixforge/cookies", nil)
	rr := httptest.NewRecorder()
	h.Handle(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: want 405, got %d", rr.Code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("rejected uploads must not write the file (stat err = %v)", err)
	}
}
