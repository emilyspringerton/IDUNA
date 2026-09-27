// Package handlers: MixforgeCookiesHandler -- receives a real, sensitive YouTube cookies.txt
// export (Netscape format) from the founder's own browser, uploaded by a small, real Chrome
// extension (MIXFORGE/tools/cookie-exporter), and writes it to disk so MIXFORGE's room server
// (MIXFORGE_YTDLP_COOKIES) can hand it to yt-dlp as a real, working workaround for YouTube's own
// bot-detection wall on that server's IP (confirmed live, 2026-09-27 -- see MIXFORGE/NORTHSTAR.md's
// own "Phase 5 scoping" section).
//
// Real, deliberate scope correction made mid-build (founder real-time: first "build the cookie
// export tool as a vs code extension for console.okemily.com", then "fork our own chrome so we
// can just defeat the security boundaries"): neither is the right shape. A VS Code extension
// hosted at console.okemily.com runs on THIS server, nowhere near the founder's own local
// browser where the real YouTube login lives. Forking Chromium to "defeat security boundaries" is
// solving a problem that doesn't exist -- same-origin policy blocks a webpage's own JS from
// reading another origin's cookies, but a browser EXTENSION with the `cookies` permission scoped
// to youtube.com has sanctioned, official API access to exactly those cookies already (the same
// real mechanism every existing "export cookies.txt" extension already uses). This endpoint is
// the real, minimal receiving side of that real, small extension.
//
// Real, honest auth: a single, constant-time-compared bearer token (MIXFORGE_COOKIE_UPLOAD_TOKEN),
// same real pattern LogsHandler.HandleCollector already establishes for IDUNA_HEC_TOKEN -- not
// IDUNA's own admin-session cookie auth, because the extension's own POST is a plain
// cross-origin fetch with no IDUNA session available to it. An empty/unset token disables the
// endpoint entirely, same fail-closed default as HEC.
package handlers

import (
	"crypto/subtle"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MixforgeCookiesHandler serves POST /api/v1/mixforge/cookies.
type MixforgeCookiesHandler struct {
	Token    string // required; empty disables the endpoint entirely
	FilePath string // where the uploaded cookies.txt gets written (atomically)
}

func (h *MixforgeCookiesHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	if h.Token == "" || h.FilePath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "disabled: MIXFORGE_COOKIE_UPLOAD_TOKEN / MIXFORGE_COOKIES_FILE_PATH not configured",
		})
		return
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) ||
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(h.Token)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid token"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20)) // 4MB is very generous for a cookies.txt
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "could not read body"})
		return
	}
	// Real, minimal sanity check -- a Netscape cookies.txt either starts with the standard
	// comment header or has at least one real tab-separated 7-field line. Not a full parser:
	// this endpoint's only job is "does this look like a cookies.txt, not something else
	// entirely," the same narrow judgment call as any upload endpoint's own content sniff.
	text := string(body)
	looksReal := strings.HasPrefix(text, "# Netscape HTTP Cookie File") ||
		strings.HasPrefix(text, "# HTTP Cookie File") ||
		strings.Count(strings.SplitN(text, "\n", 2)[0], "\t") >= 6
	if !looksReal {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "does not look like a real Netscape cookies.txt"})
		return
	}

	// Atomic write (temp file + rename) so room_server.mjs's own yt-dlp invocation, which may be
	// reading this exact path concurrently, never sees a half-written file.
	dir := filepath.Dir(h.FilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create directory"})
		return
	}
	tmp := h.FilePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not write file"})
		return
	}
	if err := os.Rename(tmp, h.FilePath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not finalize file"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "bytes": len(body)})
}
