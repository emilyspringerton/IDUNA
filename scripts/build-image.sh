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

# Found live (2026-10-05, SHANKPIT): the CI SA (github-ci, see EMILY/gitops/CI_SETUP.md) is
# deliberately scoped to cloudbuild.builds.editor/artifactregistry.writer, not a project
# Viewer/Owner. Two consequences: (1) unpinned staging dir triggers a project-scoped
# storage.buckets.list call the CI SA's bucket-scoped bindings don't satisfy (403, misleadingly
# reported as a serviceusage error) -- --gcs-source-staging-dir skips that list; (2) `builds
# submit` can't stream the default (outside-the-project) logs bucket -- --async skips the wait,
# we poll `builds describe` (status only, never logs) ourselves.
BUILD_ID=$(gcloud builds submit "$CTX" --project "$PROJECT" \
  --tag "us-central1-docker.pkg.dev/$PROJECT/emily/iduna:$TAG" \
  --gcs-source-staging-dir="gs://${PROJECT}_cloudbuild/source" \
  --async --format="value(id)")

echo "submitted build $BUILD_ID, polling for completion..."
while true; do
  STATUS=$(gcloud builds describe "$BUILD_ID" --project "$PROJECT" --format="value(status)")
  case "$STATUS" in
    SUCCESS) echo "iduna:$TAG"; exit 0 ;;
    FAILURE|INTERNAL_ERROR|TIMEOUT|CANCELLED|EXPIRED) echo "build $BUILD_ID: $STATUS" >&2; exit 1 ;;
    *) sleep 5 ;;
  esac
done
