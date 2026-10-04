#!/usr/bin/env bash
# build-image.sh [TAG] — Cloud Build the iduna image from git-tracked files (no var/ state).
set -euo pipefail
SRC="$(cd "$(dirname "$0")/.." && pwd)"
TAG="${1:-$(git -C "$SRC" rev-parse --short HEAD)}"
PROJECT="${PROJECT:-project-d24a71e9-2daf-4b2d-917}"
CTX="$(mktemp -d)"; trap 'rm -rf "$CTX"' EXIT
git -C "$SRC" ls-files -z -- . ':(exclude)var' ':(exclude)var-backups' ':(exclude)docs' ':(exclude)tests' ':(exclude)sdk' | (cd "$SRC" && xargs -0 -r tar cf - 2>/dev/null) | tar xf - -C "$CTX"
mkdir -p "$CTX/.parena"; cp "${PARENA_DIR:-$HOME/PARENA}/parena" "$CTX/.parena/"; cp -r "${PARENA_DIR:-$HOME/PARENA}/stdlib" "$CTX/.parena/"
cp "$SRC/ops/docker/iduna.Dockerfile" "$CTX/Dockerfile"
gcloud builds submit "$CTX" --project "$PROJECT" --tag "us-central1-docker.pkg.dev/$PROJECT/emily/iduna:$TAG"
echo "iduna:$TAG"
