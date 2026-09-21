package handlers

// nock_code_proxy.go — code-server (VS Code in the browser, github.com/coder/code-server) as a
// real NOCK tool. Founder real-time: "can we add VS code to the nock tools? ... have the admin
// work through IDUNA so i can use my same login flow from back office into NOCK ... let me know
// if i need to fork the repo to do that."
//
// No fork: code-server is unmodified upstream, installed standalone (~/.local/lib, no sudo) and
// run with its OWN auth disabled (--auth=none, ops/systemd/nock-code-server.service). This file
// IS the real auth gate -- a plain reverse proxy to that local instance (127.0.0.1:8892),
// registered in main.go behind the EXACT SAME middleware.RequireCookieAuth(keys, iamStore,
// "/admin/login", AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(...)) chain that
// already protects every other /admin/nock/* route -- literally the same login flow as Back
// Office, not a parallel auth system (unlike JEWEL's own fatbaby-broker + Basic Auth precedent,
// a deliberately different choice there since IDUNA's own cookie session wasn't wired into that
// broker).
//
// Forwards the FULL incoming path UNCHANGED, with no prefix stripping. This handler is correct
// mounted at a host's real root (see main.go's NOCK_CODE_SERVER_HOST-gated registration) -- it is
// NOT safe to mount under a path prefix like /admin/nock/code/. CORRECTED (2026-09-21, live
// click-through testing): an earlier version of this comment claimed "code-server auto-detects
// its own base path from the request path itself" -- checked directly against the real running
// instance and that's false. code-server's own server-side router only recognizes fixed
// root-level paths (/, /login, /_static/*, /stable-<hash>/static/*, ...) with no prefix/base-path
// support at all; a request forwarded unchanged under any subpath 404s. httputil.ReverseProxy's
// default Director leaves the Host header untouched (matches code-server's own documented
// `Host: $host` expectation) and its own stdlib WebSocket upgrade handling (Go 1.12+) needs no
// special-casing here, unlike PRRJECT_FATBABY's own hand-rolled broker.
import (
	"net/http/httputil"
	"net/url"
)

// NewNockCodeProxyHandler builds the real reverse proxy to the local code-server instance.
// Panics on a malformed target -- target is a hardcoded, known-good constant at the one real call
// site (main.go), never user input, so a parse failure here can only be a real coding mistake,
// not a runtime condition to handle gracefully.
func NewNockCodeProxyHandler(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		panic("nock_code_proxy: invalid target URL: " + err.Error())
	}
	return httputil.NewSingleHostReverseProxy(u)
}
