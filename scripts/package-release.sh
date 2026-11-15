#!/usr/bin/env bash
# package-release.sh TARGET OUT_DIR [VERSION] -- a run-anywhere IDUNA bundle, no cloud needed.
#   TARGET = linux (x86_64, glibc) | windows (x86_64) | linux-arm64 (static musl: Alpine, EmilyOS on a Raspberry Pi 3)
# With WOTAN_SITE_SRC=<a WOTAN checkout> the bundle also carries the WOTAN site in wotan/, which RUN serves on
# 127.0.0.1:8090 with /api/ answered by the same IDUNA (wotan_site.go): the arcade, sign-in, stats and matchmaking in
# one program.
#
# Why (2026-11-15): the Cloud Build path (deploy.yml -> build-image.sh) has failed on every push since 2026-11-04 with
# "The billing account for the owning project is disabled in state closed", so nothing new could ship. This bundle is
# the same binary plus the working-directory assets the image carries (ops/docker/iduna.Dockerfile): unzip, RUN, and
# IDUNA serves on http://127.0.0.1:8080 with an embedded SQLite (var/iduna.db), migrations applied at startup and its
# ES256 signing key generated on first run (iduna-key.json). The cgo part is the PARENA-compiled LZ4 codec
# (internal/brawlpit/lz4_gen.c); Windows builds it with mingw.
set -euo pipefail
OS="${1:?usage: package-release.sh linux|windows|linux-arm64 OUT_DIR [VERSION]}"; OUT="${2:?OUT_DIR}"; VERSION="${3:-dev}"
SRC="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
STAGE="$(mktemp -d)"; trap 'rm -rf "$STAGE"' EXIT
D="$STAGE/IDUNA"; mkdir -p "$D/var"
LDFLAGS="-s -w"
case "$OS" in
  linux)
    (cd "$SRC" && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOWORK=off go build -trimpath -ldflags "$LDFLAGS" -o "$D/iduna" .)
    cp "$SRC/packaging/RUN.sh" "$D/RUN.sh"; chmod +x "$D/RUN.sh" "$D/iduna" ;;
  windows)
    (cd "$SRC" && CGO_ENABLED=1 CC="${MINGW_CC:-x86_64-w64-mingw32-gcc}" GOOS=windows GOARCH=amd64 GOWORK=off \
       go build -trimpath -ldflags "$LDFLAGS" -o "$D/iduna.exe" .)
    # CRLF so cmd.exe reads every line of RUN.bat
    sed 's/$/\r/' "$SRC/packaging/RUN.bat" > "$D/RUN.bat" ;;
  linux-arm64)
    # Static against musl so the one binary runs on Alpine (EmilyOS) and on any arm64 glibc distro. zig is the C
    # cross-compiler for the cgo LZ4 codec (pip install ziglang); ZIG overrides how it is invoked.
    ZCC="$STAGE/zcc"; printf '#!/bin/sh\nexec %s cc -target aarch64-linux-musl "$@"\n' "${ZIG:-python3 -m ziglang}" > "$ZCC"
    chmod +x "$ZCC"
    (cd "$SRC" && CGO_ENABLED=1 CC="$ZCC" GOOS=linux GOARCH=arm64 GOWORK=off \
       go build -trimpath -ldflags "$LDFLAGS -linkmode external -extldflags -static" -o "$D/iduna" .)
    cp "$SRC/packaging/RUN.sh" "$D/RUN.sh"; chmod +x "$D/RUN.sh" "$D/iduna" ;;
  *) echo "package-release: OS must be linux or windows" >&2; exit 2 ;;
esac
(cd "$SRC" && cp index.html app.js styles.css openapi.yaml "$D/" && cp -r static migrations config "$D/")
if [ -n "${WOTAN_SITE_SRC:-}" ]; then
  # The deployable site only: what WOTAN's own deploy stages (no scripts, ops, docs or tests).
  git -C "$WOTAN_SITE_SRC" ls-files -z | (cd "$WOTAN_SITE_SRC" && grep -zvE '^(\.|scripts/|ops/|docs/|node_modules/)|\.(md|ps1|sh|mjs)$' \
    | xargs -0 -r tar cf -) | (mkdir -p "$D/wotan" && tar xf - -C "$D/wotan")
  [ -f "$D/wotan/index.html" ] || { echo "package-release: WOTAN_SITE_SRC has no index.html" >&2; exit 1; }
  git -C "$WOTAN_SITE_SRC" rev-parse --short HEAD > "$D/wotan/.wotan-version" 2>/dev/null || true
fi
cp "$SRC/packaging/README_RUN.txt" "$D/README.txt"
cp "$SRC/packaging/iduna.env.example" "$D/iduna.env.example"
cp "$SRC/packaging/bundle-defaults.conf" "$D/bundle.env"
echo "$VERSION" > "$D/VERSION"
touch "$D/var/.keep"
case "$OS" in linux-arm64) NAME="IDUNA_linux_arm64.zip" ;; *) NAME="IDUNA_${OS}_x86_64.zip" ;; esac
rm -f "$OUT/$NAME"
(cd "$STAGE" && zip -qr -X "$OUT/$NAME" IDUNA)
echo "$OUT/$NAME"
