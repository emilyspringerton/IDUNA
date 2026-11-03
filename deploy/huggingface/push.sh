#!/usr/bin/env bash
# push.sh -- assemble IDUNA into a Hugging Face Space and push it, from the repo, reproducibly.
#
# WHY A BUNDLE AND NOT `git push origin`:
#   1. A Space is its own git repo whose root IS the app. Pushing IDUNA's history would ship everything
#      including `iduna.db` (was TRACKED until 2026-11-03) to a PUBLIC place.
#   2. The Space needs a Dockerfile and a README with frontmatter at its root; neither belongs at the
#      root of IDUNA.
#   3. `git archive HEAD` means the Space is exactly one commit of this repo -- no uncommitted drift.
#
# Hugging Face builds the image from the Dockerfile pushed here, so no local container runtime is needed.
#
# Usage:
#   deploy/huggingface/push.sh --check          # verify a token + report whether the Space exists
#   deploy/huggingface/push.sh --dry-run        # build the bundle, scan it, push nothing
#   HF_SPACE=user/name deploy/huggingface/push.sh
#
# The token is read from $HF_TOKEN, or from IDUNA/var/hf-token (gitignored under var/). It is never echoed.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
here="$root/deploy/huggingface"

mode=push
case "${1:-}" in
    --dry-run) dry=1; mode=dry ;;
    --check)   mode=check ;;
    "")        dry=0 ;;
    *) echo "usage: push.sh [--check|--dry-run]" >&2; exit 2 ;;
esac

token="${HF_TOKEN:-}"
if [ -z "$token" ] && [ -f "$root/var/hf-token" ]; then
    token="$(tr -d '\r\n' < "$root/var/hf-token")"
fi

if [ "$mode" = check ]; then
    space="${HF_SPACE:-}"
    if [ -z "$token" ]; then
        echo "no token: set HF_TOKEN, or write one to $root/var/hf-token (gitignored)." >&2
        echo "  create it at https://huggingface.co/settings/tokens with write scope -- never paste it into chat." >&2
        exit 3
    fi
    who="$(curl -sS -H "Authorization: Bearer $token" https://huggingface.co/api/whoami-v2)"
    name="$(printf '%s' "$who" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("name",""))' 2>/dev/null)"
    if [ -z "$name" ]; then
        echo "token rejected by whoami-v2: $(printf '%s' "$who" | head -c 200)" >&2
        exit 1
    fi
    echo "token ok: authenticated as $name"
    if [ -z "$space" ]; then
        echo "HF_SPACE unset -- a target would be $name/iduna" >&2
        exit 0
    fi
    code="$(curl -s -o /tmp/hf_space.json -w '%{http_code}' -H "Authorization: Bearer $token" \
        "https://huggingface.co/api/spaces/$space")"
    if [ "$code" = 404 ]; then
        echo "space $space does not exist yet -- create it (sdk: docker) or let push.sh create it"
    elif [ "$code" = 200 ]; then
        python3 - "$space" <<'PY'
import json, sys
d = json.load(open("/tmp/hf_space.json"))
print(f"space {sys.argv[1]}: exists | private={d.get('private')} | sdk={d.get('sdk')} | "
      f"stage={d.get('runtime', {}).get('stage')} | hardware={d.get('runtime', {}).get('hardware', {}).get('current')}")
PY
    else
        echo "space lookup returned $code" >&2
    fi
    exit 0
fi

if [ "$mode" = push ]; then
    : "${HF_SPACE:?set HF_SPACE, e.g. user/iduna}"
    [ -n "$token" ] || { echo "no token: set HF_TOKEN or write $root/var/hf-token (see --check)" >&2; exit 3; }
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
git -C "$root" archive --format=tar HEAD | tar -x -C "$work"

# NEVER ship state, credentials or a webmaster seed. `iduna.db` is tracked in this repo's history, so this
# is not defensive theatre -- it is the difference between a test Space and a published database.
removed="$(find "$work" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' \
    -o -name '.env' -o -name '.env.*' -o -name 'webmaster.json' -o -name '*key.json' \
    -o -name 'agent-secrets.env' \) -print -delete | wc -l)"
rm -rf "$work/var" "$work/deploy"

cp "$here/Dockerfile" "$work/Dockerfile"
cp "$here/README.md" "$work/README.md"

# Verify the bundle is buildable *in isolation*, the way the Space will see it: no go.work above it.
# A missing toolchain is NOT a bundle problem, and blaming one for the other sends you hunting a phantom
# go.work -- the first version of this did exactly that when `go` was simply off PATH.
if ! command -v go >/dev/null 2>&1; then
    echo "push.sh: no 'go' on PATH -- cannot check the bundle resolves (install Go, or fix PATH)." >&2
    echo "        Nothing has been pushed." >&2
    exit 1
fi
if ! out="$( cd "$work" && GOWORK=off GOFLAGS=-mod=mod go list ./... 2>&1 )"; then
    echo "push.sh: the bundle does not resolve as a standalone Go module:" >&2
    printf '%s\n' "$out" | tail -5 >&2
    echo "        (go.work or a local replace is leaking in; a Space builds without either)" >&2
    exit 1
fi

# A Space repo is PUBLIC. Deleting paths is not enough on its own -- a key pasted into a source file or a
# fixture would sail through -- so scan for credential shapes and refuse to publish if one is found. The
# PEM pattern is anchored to line start: a real pasted key begins at column 0, while prose that merely
# names the header (internal/drive/client.go documents PKCS#8) does not. Both directions are tested.
hits="$(grep -rIEl '^-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----|hf_[A-Za-z0-9]{30,}|ghp_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|sk-[A-Za-z0-9]{32,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}' "$work" || true)"
if [ -n "$hits" ]; then
    echo "push.sh: REFUSING -- credential-shaped strings in:" >&2
    printf '%s\n' "$hits" >&2
    exit 1
fi

bytes="$(du -sk "$work" | cut -f1)"
files="$(find "$work" -type f | wc -l)"
echo "bundle: $files files, ${bytes} KiB, $removed state/secret files removed, credential scan clean"
if [ "$mode" = dry ]; then
    echo "dry run -- nothing pushed. Remaining state-shaped files, if any:"
    find "$work" -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '.env' \
        -o -name '*.pem' -o -name '*key.json' -o -name 'webmaster.json' \) -print || true
    exit 0
fi

# Create the Space on first push, so this is one command rather than a UI step plus a git push.
if [ "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" \
        "https://huggingface.co/api/spaces/$HF_SPACE")" = 404 ]; then
    echo "creating space $HF_SPACE (sdk: docker) ..."
    curl -sS -X POST https://huggingface.co/api/repos/create \
        -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
        -d "{\"name\":\"${HF_SPACE##*/}\",\"organization\":\"$( [ "${HF_SPACE%%/*}" != "$(printf '%s' "$HF_SPACE" | cut -d/ -f1)" ] && echo "" || echo "${HF_SPACE%%/*}" )\",\"type\":\"space\",\"sdk\":\"docker\",\"private\":false}" \
        -o /dev/null -w '  create: %{http_code}\n' || true
fi

( cd "$work"
  git init -q
  git config user.email "gitops@einhorn.industrial"
  git config user.name "IDUNA Space GitOps"
  git add -A
  git commit -q -m "IDUNA Space: $(git -C "$root" rev-parse --short HEAD)"
  git push --force "https://user:${token}@huggingface.co/spaces/${HF_SPACE}" HEAD:main )
echo "pushed $(git -C "$root" rev-parse --short HEAD) to https://huggingface.co/spaces/${HF_SPACE}"
echo "watch the build: https://huggingface.co/spaces/${HF_SPACE}?logs=container"
