#!/usr/bin/env bash
# release-smoke.sh BUNDLE.zip -- unzip a run-anywhere bundle (package-release.sh), start it through its own RUN.sh,
# and check what a player touches: IDUNA's /health, the WOTAN arcade page and a game's wasm through the WOTAN
# listener, an API read and a guest account through that same origin, and the shankpit-460 matchmaking WebSocket
# upgrading there. Linux bundles only (it runs the binary).
set -euo pipefail
ZIP="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
T="$(mktemp -d)"; PID=""
cleanup() { [ -n "$PID" ] && kill "$PID" 2>/dev/null; wait 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT
(cd "$T" && unzip -q "$ZIP")
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }
IP="$(free_port)"; WP="$(free_port)"
(cd "$T/IDUNA" && IDUNA_ADDR="127.0.0.1:$IP" WOTAN_ADDR="127.0.0.1:$WP" ./RUN.sh) & PID=$!
for _ in $(seq 1 120); do curl -sf "http://127.0.0.1:$IP/health" >/dev/null 2>&1 && break; sleep 0.5; done
fail() { echo "SMOKE FAIL: $*" >&2; tail -20 "$T/IDUNA/iduna.log" >&2 || true; exit 1; }
ok=0
check() { ok=$((ok+1)); echo "ok $ok - $1"; }
curl -sf "http://127.0.0.1:$IP/health" | grep -q '"ok":true' || fail "IDUNA /health"; check "IDUNA /health"
W="http://127.0.0.1:$WP"
if [ -f "$T/IDUNA/wotan/index.html" ]; then
  curl -sf "$W/" | grep -qi "wotan" || fail "arcade index"; check "WOTAN arcade index through the WOTAN listener"
  ct="$(curl -sf -o /dev/null -w '%{content_type}' "$W/bedrockracers/bedrockracers.wasm")"
  [ "$ct" = "application/wasm" ] || fail "wasm content type $ct"; check "Bedrock Racers wasm served as application/wasm"
  curl -sf "$W/api/v1/shankpit460/status" | grep -q '"kill_limit"' || fail "API through the WOTAN origin"
  check "shankpit-460 matchmaking status through the WOTAN origin"
  curl -sf -X POST -H 'Content-Type: application/json' -d '{}' "$W/api/v1/games/shankpit460/guest-register" \
    | grep -q '"token"' || fail "guest account through the WOTAN origin"; check "guest account through the WOTAN origin"
  python3 - "$WP" <<'PY' || fail "matchmaking WebSocket upgrade"
import socket, sys
s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=5)
s.sendall(b"GET /api/v1/shankpit460/play HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
          b"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nOrigin: http://127.0.0.1\r\n\r\n")
line = s.recv(64).split(b"\r\n")[0]
sys.exit(0 if line.startswith(b"HTTP/1.1 101") else 1)
PY
  check "shankpit-460 matchmaking WebSocket upgrades through the WOTAN origin"
  code="$(curl -s -o /dev/null -w '%{http_code}' "$W/.wotan-version")"
  [ "$code" = "404" ] || fail "dot-files must not be served ($code)"; check "dot-files are not served"
fi
echo "smoke: $ok checks passed ($(basename "$ZIP"))"
