#!/usr/bin/env bash
set -euo pipefail

# Generate deterministic, plain-text IDUNA source construct artifacts from the
# repository's tracked files. The same Git tree produces byte-for-byte identical
# output regardless of runner, timestamp, or filesystem ordering.

export LC_ALL=C

OUT="${1:-IDUNA_CONSTRUCT.txt}"
MANIFEST="${2:-IDUNA_MANIFEST.txt}"

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

TREE_SHA="$(git rev-parse --verify HEAD^{tree} 2>/dev/null || printf 'unknown')"
TMP_FILES="$(mktemp)"
trap 'rm -f "$TMP_FILES"' EXIT

# Use tracked files only so untracked local artifacts cannot perturb the output.
# Exclude generated construct/manifest outputs, common dependency/cache/build
# directories, and VCS metadata if any matching paths are tracked accidentally.
git ls-files -z \
  ':(exclude)IDUNA_CONSTRUCT*.txt' \
  ':(exclude)IDUNA_MANIFEST*.txt' \
  ':(exclude)DRAGONFLY_CONSTRUCT*.txt' \
  ':(exclude)DRAGONFLY_MANIFEST*.txt' \
  ':(exclude)SHANKPIT_CONSTRUCT*.txt' \
  ':(exclude).git/**' \
  ':(exclude)vendor/**' \
  ':(exclude)node_modules/**' \
  ':(exclude)artifacts/**' \
  ':(exclude)build/**' \
  ':(exclude)dist/**' \
  ':(exclude)_site/**' \
  | sort -z > "$TMP_FILES"

{
  printf 'IDUNA MANIFEST\n'
  printf 'schema_version: 1\n'
  printf 'tree_sha: %s\n' "$TREE_SHA"
  printf 'source: git ls-files\n'
  printf '\n'
  printf 'files:\n'
} > "$MANIFEST"

COUNT=0
while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue
  SHA="$(sha256sum "$file" | awk '{print $1}')"
  SIZE="$(wc -c < "$file" | tr -d ' ')"
  MODE="$(git ls-files -s -- "$file" | awk '{print $1}')"
  printf '%s  %s  %s  %s\n' "$SHA" "$SIZE" "$MODE" "$file" >> "$MANIFEST"
  COUNT=$((COUNT + 1))
done < "$TMP_FILES"

{
  printf '\n'
  printf 'total_files: %s\n' "$COUNT"
} >> "$MANIFEST"

{
  printf 'IDUNA CONSTRUCT\n'
  printf 'schema_version: 1\n'
  printf 'tree_sha: %s\n' "$TREE_SHA"
  printf 'source: git ls-files\n'
  printf 'total_files: %s\n' "$COUNT"
  printf '\n'
} > "$OUT"

while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue
  SHA="$(sha256sum "$file" | awk '{print $1}')"
  SIZE="$(wc -c < "$file" | tr -d ' ')"
  MODE="$(git ls-files -s -- "$file" | awk '{print $1}')"

  {
    printf -- '--- FILE START: %s ---\n' "$file"
    printf 'sha256: %s\n' "$SHA"
    printf 'size_bytes: %s\n' "$SIZE"
    printf 'git_mode: %s\n' "$MODE"
  } >> "$OUT"

  if [ -s "$file" ] && ! grep -Iq . "$file"; then
    {
      printf 'encoding: base64\n'
      printf -- '--- CONTENT START ---\n'
      base64 "$file"
      printf '\n'
      printf -- '--- CONTENT END ---\n'
      printf -- '--- FILE END: %s ---\n' "$file"
      printf '\n'
    } >> "$OUT"
  else
    {
      printf 'encoding: text\n'
      printf -- '--- CONTENT START ---\n'
      cat "$file"
      # Normalize the delimiter onto a fresh line even if a source file lacks a
      # trailing newline. This keeps file boundaries unambiguous and stable.
      printf '\n'
      printf -- '--- CONTENT END ---\n'
      printf -- '--- FILE END: %s ---\n' "$file"
      printf '\n'
    } >> "$OUT"
  fi
done < "$TMP_FILES"
