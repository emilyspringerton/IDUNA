#!/usr/bin/env bash
# IDUNA, run anywhere. Settings come from iduna.env next to this file (copy iduna.env.example); none are required.
# With the WOTAN site in wotan/ (release bundles carry it) the arcade is served too, /api/ answered by this IDUNA.
cd "$(dirname "$0")"
set -a; [ -f iduna.env ] && . ./iduna.env; set +a
export IDUNA_ADDR="${IDUNA_ADDR:-127.0.0.1:8080}"
if [ -z "${WOTAN_SITE_DIR:-}" ] && [ -f wotan/index.html ]; then export WOTAN_SITE_DIR=wotan; fi
export WOTAN_ADDR="${WOTAN_ADDR:-127.0.0.1:8090}"
mkdir -p var
echo "IDUNA $(cat VERSION 2>/dev/null) on http://${IDUNA_ADDR}  (log: iduna.log, data: var/iduna.db). Ctrl+C stops it."
[ -n "${WOTAN_SITE_DIR:-}" ] && echo "WOTAN arcade: http://${WOTAN_ADDR}/"
exec ./iduna >> iduna.log 2>&1
