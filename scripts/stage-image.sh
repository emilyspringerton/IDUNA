#!/usr/bin/env bash
# stage-image.sh CTX -- stage the iduna image build context into CTX: git-tracked files (no var/ state, docs, tests,
# sdk), the prebuilt PARENA compiler + stdlib that NOCK shells out to (STOPGAP: prebuilt, not built in the image), and
# ops/docker/iduna.Dockerfile as CTX/Dockerfile. Shared by build-image.sh (Cloud Build -> Artifact Registry) and the
# GitHub release workflow (docker -> GHCR), so the two images cannot drift.
set -euo pipefail
CTX="${1:?usage: stage-image.sh CTX}"
SRC="$(cd "$(dirname "$0")/.." && pwd)"
PARENA="${PARENA_DIR:-$HOME/PARENA}"
[ -x "$PARENA/parena" ] || { echo "stage-image: no PARENA compiler at $PARENA/parena (make -C \$PARENA_DIR parena)" >&2; exit 1; }
mkdir -p "$CTX"
git -C "$SRC" ls-files -z -- . ':(exclude)var' ':(exclude)var-backups' ':(exclude)docs' ':(exclude)tests' ':(exclude)sdk' \
  | (cd "$SRC" && xargs -0 -r tar cf - 2>/dev/null) | tar xf - -C "$CTX"
mkdir -p "$CTX/.parena"; cp "$PARENA/parena" "$CTX/.parena/"; cp -r "$PARENA/stdlib" "$CTX/.parena/"
cp "$SRC/ops/docker/iduna.Dockerfile" "$CTX/Dockerfile"
