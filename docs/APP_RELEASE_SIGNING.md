# App Release Signing (S528)

Founder real-time: "we need an app repository in IDUNA signed in github somehow with emily
session new for the build before each build and we can use session tokens in the app binaries
figure out how to sign them with gpg keys or something."

This is the real, current state of that -- what's built, what's live, and the one manual step
still needed before a real CI pipeline can use it.

## What exists

- **The app-release registry** — `internal/apps/release_store.go` + `internal/http/handlers/
  app_releases.go`, live at `/api/v1/app-releases`. Same real shape as SHANKPIT's own checkpoint
  registry: SQLite metadata + an on-disk blob per release, keyed by `(app_slug, platform)`, one
  `is_latest` row per pair. Public `GET` (list/latest/download); `POST` (publish) needs the
  `apps.releases.write` permission.
- **A real GPG signing key** — RSA 4096, `EINHORN_INDUSTRIAL App Releases <releases@okemily.com>`,
  expires 2028-09-21. Public key: [`einhorn-app-releases-public.asc`](einhorn-app-releases-public.asc)
  (in this repo, safe to distribute — anyone can use it to verify a release was really signed by
  this key, without needing to trust the IDUNA HTTP endpoint at all). Key ID:
  `A9BEEFEC4DF16C2E` / fingerprint `9329A2D4D82C22E3153C34B2A9BEEFEC4DF16C2E`.
- **A dedicated M2M agent** — `APP-RELEASES-CI` (`config/agents.json`), holding only
  `apps.releases.write` and nothing else — least privilege, so a leaked CI secret can only publish
  a new release, never touch any other IDUNA surface.
- **`emily session new` as a build-provenance tag** — the CLI's own existing
  `sess-YYYYMMDD-HHMM-<8hex>` fingerprint, generated fresh per build and stored on the release row
  as `session_tag`, plus `github_commit_sha`/`github_run_url` recording exactly which GitHub
  Actions run produced the artifact. This is a deliberate second, unrelated reuse of that
  command's own tag shape (cheap, legible, collision-resistant) as a build ID — not a claim that a
  CI build is an LLM conversation session. Nothing about `session new` requires network access, so
  it runs standalone in a CI runner with no IDUNA connectivity needed for that step.

**Live-verified end to end** (this session, 2026-09-22): real agent-JWT auth against
`APP-RELEASES-CI`, a real `dw_gui` binary GPG-signed and uploaded via `multipart/form-data`,
downloaded back byte-for-byte identical, and `gpg --verify` reported **"Good signature"** against
the downloaded binary and the stored signature. The test row was deleted afterward — this doc
describes the mechanism, not a permanent test artifact left in the registry.

## What a real CI publish step looks like

```bash
# 1. Fresh build-provenance tag (no network needed for this step)
SESSION_TAG=$(emily session new)   # or: emily session current, if reusing an existing session

# 2. Build the binary as normal (DW_VERSION etc unchanged)
scripts/build.sh --gui

# 3. Sign it (private key imported from a GitHub Actions secret -- see "Manual step" below)
gpg --batch --yes --detach-sign --armor -o build/dw_gui.sig build/dw_gui

# 4. Get a real agent JWT
TOKEN=$(curl -s -X POST "$IDUNA_BASE_URL/api/v1/auth/agent" \
  -H 'Content-Type: application/json' \
  -d "{\"agent_name\":\"APP-RELEASES-CI\",\"agent_secret\":\"$APP_RELEASES_CI_SECRET\"}" \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['access_token'])")

# 5. Publish
curl -s -X POST "$IDUNA_BASE_URL/api/v1/app-releases" \
  -H "Authorization: Bearer $TOKEN" \
  -F "binary=@build/dw_gui;filename=dw_gui" \
  -F "app_slug=deadweight" \
  -F "platform=linux_x86_64" \
  -F "version=$DW_VERSION" \
  -F "session_tag=$SESSION_TAG" \
  -F "gpg_signature=$(cat build/dw_gui.sig)" \
  -F "gpg_key_id=A9BEEFEC4DF16C2E" \
  -F "github_commit_sha=$GITHUB_SHA" \
  -F "github_run_url=https://github.com/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
```

Anyone can then verify a downloaded release independent of trusting IDUNA's own HTTP endpoint:

```bash
curl -s "$IDUNA_BASE_URL/api/v1/app-releases/latest?app=deadweight&platform=linux_x86_64" \
  | python3 -c "import json,sys;r=json.load(sys.stdin);open('r.sig','w').write(r['gpg_signature']);print(r['id'])"
curl -s "$IDUNA_BASE_URL/api/v1/app-releases/<id>/download" -o dw_gui
gpg --import einhorn-app-releases-public.asc   # once
gpg --verify r.sig dw_gui
```

## Manual step still needed (human-only, not something a sandboxed agent can do)

The **private** key (`var/gpg-releases/einhorn-app-releases-private.asc.DO_NOT_COMMIT` on this
box, `chmod 600`, gitignored — never committed, never will be) needs to be added as a GitHub
Actions secret on each repo whose CI will sign a release (DEADWEIGHT first):

1. `gh secret set EINHORN_APP_RELEASES_GPG_PRIVATE_KEY < var/gpg-releases/einhorn-app-releases-private.asc.DO_NOT_COMMIT`
   (or paste it into the repo's Settings → Secrets → Actions UI) — a real, credential-granting
   action, same class of human-only step as every other secret in this monorepo
   (`GOOGLE_CLIENT_SECRET`, `IDUNA_AGENT_SECRET`, etc.).
2. Also add `APP_RELEASES_CI_SECRET` (the value already sitting in `var/agent-secrets.env` as
   `IDUNA_SECRET_APP_RELEASES_CI`) as a secret on the same repo(s).
3. A CI step imports the key (`echo "$EINHORN_APP_RELEASES_GPG_PRIVATE_KEY" | gpg --batch --import`)
   before the sign step above.

Until that's done, the registry and signing mechanism are real and verified (per the live test
above), but no CI workflow actually calls them yet — DEADWEIGHT's `ci.yml` wiring is the real,
concrete next step once the secrets exist.

## Key rotation

The key expires 2028-09-21. Before then, generate a new key, publish its public half here
alongside (not replacing) this one, and start recording the new key's ID in new
`app_releases.gpg_key_id` rows — old releases stay verifiable against the old public key
indefinitely, matching the general append-only, never-invalidate-history spirit the rest of this
monorepo's registries already follow (checkpoints, levels, etc. are never deleted on a policy
change, either).
