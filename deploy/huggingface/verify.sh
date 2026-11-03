#!/usr/bin/env bash
# verify.sh -- prove the SPACE CONTRACT locally, without docker and without Hugging Face.
#
# The Dockerfile's promises are: a buildable binary (cgo on -- brawlpit's LZ4 is PARENA-compiled C), a server
# that binds 0.0.0.0:7860 from IDUNA_ADDR, state confined to a writable dir, and migrations found via
# IDUNA_ROOT. Those are all testable on this box, and this asserts them against the real server rather than
# reading the Dockerfile and hoping.
#
# It does NOT prove the image builds (no docker here) -- that is a limitation to state, not to paper over.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
port="${SPACE_PORT:-7860}"
work="$(mktemp -d)"
pid=""
cleanup() { [ -n "$pid" ] && kill "$pid" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT

# A boot check is worthless if something ELSE already answers on the port: the first version of this script
# silently measured a live instance on :7860 and failed with "MAC mismatch", because that instance minted the
# ticket with ITS secret. Refuse to run into an occupied port.
if curl -sf -o /dev/null --max-time 2 "http://127.0.0.1:$port/health"; then
    echo "verify.sh: something is already serving http://127.0.0.1:$port -- refusing to test against it." >&2
    echo "           stop it, or pick another port: SPACE_PORT=7861 $0" >&2
    exit 1
fi

echo "build (CGO_ENABLED=1, as the image does)..."
( cd "$root" && CGO_ENABLED=1 go build -o "$work/iduna" . )

# Mirrors the image's layout: IDUNA_ROOT is the base for BOTH migrations/truestore and var/ (eventlog,
# user-events, agent-secrets), so it must be writable. /app-equivalent: nothing here writes to the repo.
mkdir -p "$work/root/var"
cp -r "$root/migrations" "$work/root/migrations"
echo "boot with the image's own environment, cwd $work (the image's /data)..."
( cd "$work/root" && IDUNA_ADDR="0.0.0.0:$port" IDUNA_ROOT="$work/root" \
    SQLITE_PATH="$work/root/iduna.db" KEY_FILE="$work/root/iduna-key.json" \
    SHANKPIT_TICKET_SECRET="space-check-secret" JWT_ISSUER="http://127.0.0.1:$port" \
    BASE_URL="http://127.0.0.1:$port" exec "$work/iduna" >"$work/log" 2>&1 ) &
pid=$!

ready=0
for _ in $(seq 1 80); do
    if curl -sf -o /dev/null "http://127.0.0.1:$port/health"; then ready=1; break; fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.25
done
if [ "$ready" != 1 ]; then echo "never became healthy; log:" >&2; tail -20 "$work/log" >&2; exit 1; fi

echo "  health: $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/health")"

# The Space's whole purpose: a Roblox player can sign in and get a ticket. An INVALID body proves the game
# row and the route exist without creating an account (404 would mean the shipped build lacks the row).
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$port/api/v1/games/shankpit/guest-register" \
    -H 'Content-Type: application/json' -d 'not-json')"
echo "  guest-register route (invalid body): $code"
[ "$code" = "400" ] || { echo "expected 400 (route exists); got $code" >&2; exit 1; }

code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$port/api/v1/shankpit/ticket" \
    -H 'Content-Type: application/json' -d '{}')"
echo "  ticket without a token: $code"
[ "$code" = "401" ] || { echo "expected 401; got $code" >&2; exit 1; }

# And the real thing: a full sign-up -> ticket, on the Space's port, with the Space's env.
reg="$(curl -s -X POST "http://127.0.0.1:$port/api/v1/games/shankpit/guest-register" \
    -H 'Content-Type: application/json' -d '{"display_name":"SpaceCheck"}')"
token="$(printf '%s' "$reg" | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')"
player="$(printf '%s' "$reg" | python3 -c 'import json,sys;print(json.load(sys.stdin)["player_id"])')"
curl -s -o "$work/t.json" -w '' -X POST "http://127.0.0.1:$port/api/v1/shankpit/ticket" \
    -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d '{}'
python3 "$root/../SHANKPIT/roblox/tests/verify_ticket.py" "$work/t.json" "space-check-secret" "$player" 2>/dev/null \
    || python3 - "$work/t.json" <<'PY'
import binascii, hashlib, hmac, json, sys
a = json.load(open(sys.argv[1]))
raw = binascii.unhexlify(a["ticket"])
want = hmac.new(b"space-check-secret", raw[:20], hashlib.sha256).digest()[:16]
assert hmac.compare_digest(raw[20:], want), "MAC mismatch"
print(f"  ticket for {a['player_id']}: 36 bytes, MAC verified")
PY
echo "verify: OK (binds 0.0.0.0:$port from IDUNA_ADDR, migrations found, state in a writable dir, sign-in -> ticket works)"
