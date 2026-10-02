# MIXFORGE Cookie Exporter

A small, real Chrome extension (Manifest V3) — reads your own `youtube.com` session cookies via
Chrome's sanctioned `cookies` permission (no security boundary defeated; this is the API's own
intended purpose, the same one every existing "export cookies.txt" extension already uses) and
uploads them, as a real Netscape-format `cookies.txt`, straight to your own IDUNA instance.

## Why this exists

MIXFORGE's DJ room (`room.html`) downloads queued YouTube tracks server-side via `yt-dlp`.
YouTube intermittently bot-detects that server's own IP (`Sign in to confirm you're not a bot`) —
confirmed live, not assumed. Real browser cookies from a real, logged-in session are yt-dlp's own
documented way past that wall. This extension is the real, minimal tool to get them there safely:
your cookies never leave your own browser except straight to your own IDUNA instance, over HTTPS,
gated by a bearer token only you have.

**Not a VS Code extension, not a forked browser.** Both were considered and rejected mid-build —
see `MIXFORGE/NORTHSTAR.md`'s own "Phase 5 scoping" section for the full reasoning. A VS Code
extension hosted on the server can't reach your local browser's cookie store at all (wrong
machine). Forking Chromium to "defeat security boundaries" solves a problem that doesn't exist —
same-origin policy blocks a *webpage's* JS from reading another site's cookies, but an
*extension* with the right permission already has sanctioned access, no boundary to break.

## Install (unpacked — this isn't published to the Chrome Web Store)

**Auto-released:** this directory lives in IDUNA (`extensions/mixforge-cookie-exporter`) and IDUNA's CI (`.github/workflows/extension-release.yml`) tests it, packages it deterministically and cuts a GitHub Release `ext-mixforge-cookie-exporter-v<major>.<minor>.0` on every green `main` push that touches `extensions/**`. Download the zip from the latest such release and unzip it, then use it as below. (Zip only; a signed `.crx` needs a private-key secret that isn't provisioned.)

1. `chrome://extensions` → enable **Developer mode** (top right) → **Load unpacked** → select
   the unzipped directory.
2. Click the extension's icon → **Configure endpoint / token**.
3. Endpoint: `https://okemily.com/api/v1/mixforge/cookies` (pre-filled). Token: the value of
   `MIXFORGE_COOKIE_UPLOAD_TOKEN` in `MIXFORGE/var/mixforge-secrets.env` on the server.
4. Save. Make sure you're logged into YouTube in this same browser.

## Use

Click the extension icon → **Export & upload to IDUNA**. It reads your `youtube.com` cookies,
converts them to Netscape format, and POSTs them to IDUNA, which writes them to
`MIXFORGE_COOKIES_FILE_PATH` (default `MIXFORGE/var/ytdlp-cookies.txt`) — the exact path
`room_server.mjs`'s own `MIXFORGE_YTDLP_COOKIES` env var already points at, so the next queued
download picks them up automatically, no restart needed.

## Real, honest limits

- Cookies expire / get invalidated (password change, explicit sign-out elsewhere, YouTube's own
  session rotation) — re-run the export whenever downloads start failing again.
- The uploaded file is a real, sensitive credential bundle (equivalent to your YouTube login) —
  treat `MIXFORGE_COOKIE_UPLOAD_TOKEN` and the resulting `ytdlp-cookies.txt` file with the same
  care as any other secret in this monorepo (`var/*.env`, `agent-secrets.env`).
- Server-side validation is a real but narrow sanity check (looks like a Netscape cookies.txt),
  not a full parse — the receiving endpoint trusts the bearer token, not the file's own contents,
  for its actual security boundary.

## Files

- `manifest.json` — Manifest V3, `cookies` + `storage` permissions, scoped `host_permissions`.
- `cookies.js` — the real, pure cookie→Netscape-line conversion logic (`cookies_test.mjs` covers
  it: subdomain cookies vs. host-only cookies vs. session cookies, run with plain `node`, no
  browser needed).
- `popup.html`/`popup.js` — the one-click export+upload UI.
- `options.html`/`options.js` — endpoint + token configuration (`chrome.storage.local`).
