#!/usr/bin/env bash
# sync-empire-wasm.sh -- copy the EMPIRE game module the referee runs (internal/empire/empire.wasm) from WOTAN, where it is built from PARENA and served to
# browsers (WOTAN/scripts/build-empire.sh). The two copies must be the same sim version or the gateway refuses a page's QUEUE; TestEmbeddedMatchesWotan
# (EMPIRE_WOTAN_DIR=...) checks it.
#   scripts/sync-empire-wasm.sh [path/to/WOTAN]
set -euo pipefail
cd "$(dirname "$0")/.."
WOTAN="${1:-${EMPIRE_WOTAN_DIR:-../WOTAN}}"
cp "$WOTAN/empire/empire.wasm" internal/empire/empire.wasm
echo "copied $(wc -c < internal/empire/empire.wasm) bytes from $WOTAN/empire/empire.wasm"
sha256sum internal/empire/empire.wasm
