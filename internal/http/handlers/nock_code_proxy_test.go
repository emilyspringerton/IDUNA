package handlers

// nock_code_proxy_test.go -- real, decoupled-from-auth proof that NewNockCodeProxyHandler
// forwards requests correctly. Doesn't (and can't, from this package alone) exercise the real
// RequireCookieAuth+iduna.admin gate main.go wires in front of it -- that's proven separately,
// live, against the real running IDUNA binary (curl with no cookie -> 401, never reaches
// code-server; see EMILY/BACKLOG.md S486 for the real command and output). This test's own job
// is narrower and complementary: given a request already past that gate, does the proxy actually
// forward it to the right place, unmodified path, method, and body intact.
import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNockCodeProxy_ForwardsFullPathUnchanged(t *testing.T) {
	var gotPath, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	proxy := NewNockCodeProxyHandler(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/stable-abc123/static/out/vs/code.js", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from upstream, got %d", rec.Code)
	}
	// Real, load-bearing assertion: the path must survive the proxy hop UNCHANGED. This handler
	// is only correct mounted at a host's real root (main.go's NOCK_CODE_SERVER_HOST wiring) --
	// code-server's own server-side router has no base-path/prefix support at all, confirmed live
	// (see nock_code_proxy.go's own doc comment); this test's job is narrower, proving the proxy
	// itself does zero path mangling, not that any particular mount point works end-to-end.
	if gotPath != "/stable-abc123/static/out/vs/code.js" {
		t.Fatalf("expected the full path to reach upstream unchanged, got %q", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET, got %s", gotMethod)
	}
}

func TestNockCodeProxy_ForwardsMethodAndBody(t *testing.T) {
	var gotMethod, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy := NewNockCodeProxyHandler(upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/some-write", strings.NewReader(`{"real":"payload"}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST to reach upstream, got %s", gotMethod)
	}
	if gotBody != `{"real":"payload"}` {
		t.Fatalf("expected the real request body to reach upstream unchanged, got %q", gotBody)
	}
}

func TestNockCodeProxy_InvalidTargetPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected a panic for a malformed target URL")
		}
	}()
	NewNockCodeProxyHandler("://not-a-real-url")
}
