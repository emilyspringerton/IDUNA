#!/usr/bin/env bash
# push.sh -- assemble IDUNA into a Hugging Face Space and push it, from the repo, reproducibly.
#
# WHY A BUNDLE AND NOT `git push origin`:
#   1. A Space is its own git repo whose root IS the app. Pushing IDUNA's history would ship everything
#      including `iduna.db` (TRACKED in this repo today -- it holds real rows) to a PUBLIC place.
#   2. The Space needs a Dockerfile and a README with frontmatter at its root; neither belongs at the
#      root of IDUNA.
#   3. `git archive HEAD` means the Space is exactly one commit of this repo -- no uncommitted drift.
#
# There is NO prebuilt-image path used here. Spaces build from the Dockerfile in the repo; the
# `docker_image:` frontmatter that some write-ups suggest has no documentation behind it, so this does not
# rely on it.
#
# Usage:
#   HF_TOKEN=hf_xxx HF_SPACE=user/space deploy/huggingface/push.sh
#   deploy/huggingface/push.sh --dry-run        (build the bundle, report it, push nothing)
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
here="$root/deploy/huggingface"
dry=0
[ "${1:-}" = "--dry-run" ] && dry=1

if [ "$dry" = 0 ]; then
    : "${HF_TOKEN:?set HF_TOKEN (a Hugging Face token with write scope)}"
    : "${HF_SPACE:?set HF_SPACE, e.g. emilyspringerton/iduna}"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
git -C "$root" archive --format=tar HEAD | tar -x -C "$work"

# NEVER ship state, credentials or a webmaster seed. `iduna.db` is tracked upstream, so this is not
# defensive theatre -- it is the difference between a test Space and a published database.
removed="$(find "$work" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' \
    -o -name '.env' -o -name '.env.*' -o -name 'webmaster.json' -o -name '*key.json' \
    -o -name 'agent-secrets.env' \) -print -delete | wc -l)"
rm -rf "$work/var" "$work/deploy"

cp "$here/Dockerfile" "$work/Dockerfile"
cp "$here/README.md" "$work/README.md"

# Verify the bundle is buildable *in isolation*, the way the Space will see it: no go.work above it.
if [ -d "$work" ] && ! ( cd "$work" && GOWORK=off GOFLAGS=-mod=mod go list ./... >/dev/null 2>&1 ); then
    echo "push.sh: the bundle does not resolve as a standalone Go module" >&2
    echo "        (go.work or a local replace is leaking in; a Space builds without either)" >&2
    exit 1
fi

# A Space repo is PUBLIC. Deleting paths is not enough on its own -- a key pasted into a source file or a
# fixture would sail through -- so scan for credential shapes and refuse to publish if one is found. The
# PEM pattern is anchored to line start: a real pasted key begins at column 0, while prose that merely
# names the header (internal/drive/client.go documents PKCS#8) does not. Both directions are tested below.
# runs on --dry-run too, so the check gets exercised without a token.
hits="$(grep -rIEl '^-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----|hf_[A-Za-z0-9]{30,}|ghp_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|sk-[A-Za-z0-9]{32,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}' "$work" || true)"
if [ -n "$hits" ]; then
    echo "push.sh: REFUSING -- credential-shaped strings in:" >&2
    printf '%s\n' "$hits" >&2
    exit 1
fi

bytes="$(du -sk "$work" | cut -f1)"
files="$(find "$work" -type f | wc -l)"
echo "bundle: $files files, ${bytes} KiB, $removed state/secret files removed, credential scan clean"
if [ "$dry" = 1 ]; then
    echo "dry run -- nothing pushed. Remaining state-shaped files, if any:"
    find "$work" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '.env' \
        -o -name '*.pem' -o -name '*key.json' -o -name 'webmaster.json' \) -print || true
    exit 0
fi

( cd "$work"
  git init -q
  git config user.email "gitops@einhorn.industrial"
  git config user.name "IDUNA Space GitOps"
  git add -A
  git commit -q -m "IDUNA Space: $(git -C "$root" rev-parse --short HEAD)"
  git push --force "https://user:${HF_TOKEN}@huggingface.co/spaces/${HF_SPACE}" HEAD:main )
echo "pushed $(git -C "$root" rev-parse --short HEAD) to https://huggingface.co/spaces/${HF_SPACE}"
