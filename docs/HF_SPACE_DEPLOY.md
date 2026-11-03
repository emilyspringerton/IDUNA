# Shipping IDUNA to a Hugging Face Space

Founder, 2026-11-03: *"we ship iduna to hugging face."* This supersedes a pasted plan written for a
hypothetical `src/stats_hub.c` rather than this repo; where that plan was wrong about the platform, the
correction is named at the end.

## What ships

`deploy/huggingface/`, four files and no new runtime code:

| File | Does |
|---|---|
| `Dockerfile` | two stages; cgo in the build stage, gcc-free runtime; `/app` read-only, `/data` writable |
| `README.md` | the Space's own frontmatter (`sdk: docker`, `app_port: 7860`) and an honest account of what it is |
| `push.sh` | bundles `git archive HEAD`, refuses DBs/secrets/credentials, creates the Space, pushes |
| `verify.sh` | proves the runtime contract on a box with no container runtime |

## Why a bundle and not `git push origin`

1. A Space's repo root **is** the app: it needs `Dockerfile` + frontmatter `README.md` at the root, and IDUNA's
   root is already busy.
2. A Space repo is **public**. `iduna.db` was tracked in this repo until today — verified: the only committed
   blob is **0 bytes with 0 tables**, so nothing leaked, but any local run writes real accounts into it and the
   next `git add -A` would have published them. It is untracked and ignored now.
3. `git archive HEAD` makes the Space exactly one commit of this repo.

`push.sh` deletes `*.db*`, `.env*`, `webmaster.json`, `*key.json` and `agent-secrets.env` from every bundle, and
then **refuses to publish** if any credential-shaped string survives (PEM header at line start, `hf_`,
`ghp_`, `github_pat_`, `sk-`, `AKIA`, `xox[baprs]-`). Both directions are checked: a real PEM header is caught,
while `internal/drive/client.go`'s prose *about* PKCS#8 is not.

## Why the image carries gcc

`CGO_ENABLED=0` looked free (SQLite is `modernc.org/sqlite`, pure Go). It does not build:

```
internal/http/handlers/brawlpit_checkpoints.go:237:25: undefined: brawlpit.CompressLZ4
interface conversion: ... internal/http/handlers/brawlpit_levels_public.go:103:26: undefined: brawlpit.CompressLZ4
```

`internal/brawlpit/lz4.go` is a deliberate cgo binding over `lz4_gen.c` — PARENA's own
`stdlib/compress/lz4.prn` compiled to C, the same bytes BRAWLPIT's native client links (S417-03). Hand-porting
that codec to Go to avoid a compiler would undo that decision, so gcc is in the build stage only.

## The four failures only a real boot finds

None of these is visible by reading the Dockerfile, and each would have shipped a Space that never answers.

**1. A fresh working directory has no `var/`.**

```
mailinglist: failed to open store: migrate mailinglist db: unable to open database file (14)
```

**2. `IDUNA_ROOT` is the base for BOTH migrations and `var/`.** `main.go` derives `migrations/truestore` *and*
`var/` (Apples eventlog, user-events, `agent-secrets.env`) from it and writes the latter constantly, so it must
be writable. Pointing it at a read-only `/app` dies:

```
userlog: write seq tmp: open /app/var/user-events/state/latest-seq.tmp: permission denied
```

**3. `cp -a` cannot preserve ownership as a non-root uid** — and chained with `&&`, the failed copy skipped
`exec /app/iduna`. The server never started at all.

**4. A seeded `/data` can already hold files the app cannot overwrite** (a mounted volume, a re-used Space).
Failing the boot over present-but-stale migrations is absurd, so the seed is best-effort and the exec
unconditional.

The resulting split:

| Path | Role |
|---|---|
| `/app` | read-only: `iduna` + a pristine `migrations/` (a source, never written) |
| `/data` | `IDUNA_ROOT` and cwd: `iduna.db`, `iduna-key.json`, `var/`, working `migrations/` |

The `ENTRYPOINT` creates `$IDUNA_ROOT/var` and `$IDUNA_ROOT/migrations`, copies only newer migrations
(`cp -R -u`), says so on stderr if that is refused, and starts regardless — because a Space can mount an empty
volume over `/data` and shadow anything the image baked in.

## A real instance, running on this box

`docker` is absent here (and so are podman, buildah, nerdctl — checked, not assumed). But Hugging Face builds
the image from the pushed Dockerfile, so that was never the blocker. The runtime contract was made real in
exactly this shape, as an unprivileged UID:

```
var/space/app/    iduna + migrations, mode bits carry NO write permission   (0 writable entries)
var/space/root/   owned by uid 65534 -> var/, iduna.db, iduna-key.json, migrations/
  serving uid 65534, port :7860
  health                                              -> 200
  guest-register                                      -> player <uuid>
  ticket                                              -> 36 bytes, MAC verified, same player
  kill + restart, same token                          -> HTTP 200, SAME player_id
```

Registering a player, minting a ticket and restarting leaves **0 writable entries** in the read-only tree:
nothing leaked a write into `/app`. The account surviving the restart is real here because the disk is real —
a **free Space will not** give you that, since its storage is ephemeral and a restart is also a fresh ES256 key.

### Two ways this verification lied before it was fixed

Both looked green, which is why they are written down:

- **Root ignores write bits.** The first read-only check ran as root with `[ -w ]`, true for root regardless of
  mode: it "proved" the tree read-only while proving nothing. The claim now rests on mode bits *and* uid 65534.
- **`verify.sh` measured someone else.** With a live instance already on :7860, its boot failed to bind and its
  `curl` answered from the *other* server, whose ticket MAC used a different secret — surfacing as
  `MAC mismatch`. It refuses an occupied port now; tested against a stub that answers `/health` (exit 1).

## Deploying

```bash
cd IDUNA
deploy/huggingface/verify.sh                # no docker, no account; picks a free port or refuses
deploy/huggingface/push.sh --dry-run        # bundle report + credential scan, pushes nothing
deploy/huggingface/push.sh --check          # authenticates, reports whether the Space exists (exit 3 without a token)
echo '<token>' > IDUNA/var/hf-token         # gitignored under var/; or export HF_TOKEN
HF_SPACE=<user>/iduna deploy/huggingface/push.sh
```

`push.sh` creates the Space itself on a first push (`POST /api/repos/create`, `sdk: docker`), so there is no UI
step, and the token is never echoed or written into the bundle. Watch the image build at
`https://huggingface.co/spaces/<user>/iduna?logs=container`.

Then set one Space secret:

| Secret | Must equal |
|---|---|
| `SHANKPIT_TICKET_SECRET` | the game server's value, or every minted ticket is refused at `PACKET_CONNECT` |

## The build stage and the runtime stage must agree on libc

The binary built on *this* box is glibc-linked (`ldd` → `/lib/x86_64-linux-gnu/libc.so.6`). That is not what
ships: the image builds inside `golang:1.25-alpine`, so the artifact is musl-linked and the `alpine:3.21`
runtime has the matching loader. Both stages being alpine is a requirement, not a style choice — a glibc binary
under alpine fails to exec with a bare `not found`, which reads like a missing file rather than a wrong libc.

Rather than remember that, the build asserts it: after `go build`, the stage greps the artifact for
`libc.so.6` and fails with an explicit message if it is there. Verified in the direction that matters — the
host's own build contains that string, so the check would reject it.

## What the builder does first, checked without a builder

Hugging Face's first attempt fails on one of three things, all of which were checked from here:

| Risk | Result |
|---|---|
| base tag missing (`golang:1.25-alpine`, `alpine:3.21`) | both resolve on Docker Hub (`HTTP 200`, real manifest digests) |
| cold module cache, no `go.work` above the bundle | `GOWORK=off` + empty `GOMODCACHE` builds the bundle from `git archive HEAD` alone |
| no CA trust store in the runtime image | `alpine` ships none, and Go reads `/etc/ssl/certs` on Linux — the runtime stage now installs `ca-certificates` |

The third one is a build that looks green and then dies at runtime with `x509: certificate signed by unknown
authority` on the first outbound call (OAuth/JWKS), which is why it is in the image rather than in a ticket.

## What has and has not been exercised

Run here: the Space-contract boot (`verify.sh` — health 200, `guest-register` route present, ticket 401 without
a token, sign-up → ticket with an independently recomputed MAC), the rootless run above, the bundle dry-run
(819 files, ~11.6 MiB, clean scan), and `--check` without a token (exit 3, instructions, no silent skip).

Not run: **the image has never been built** (no container runtime on this box) and **nothing has been pushed**
(no `HF_TOKEN`; `whoami-v2` answers 401 from here). `huggingface.co` itself is reachable — TLS 1.3 to
`3.170.185.14`, `GET /api/spaces` → 200 — so the publish path is blocked on a credential, not on the network.

Also not done: the `GOLDEN_DOCS` snapshot cannot be resynced from a box that has not cloned it, and the
completion Apple for this work could not be filed because no `IDUNA_AGENT_SECRET` exists here
(`emily apples post` answered `IDUNA_AGENT_SECRET not set`).

## Limits this Space has by construction

- **Ephemeral identity.** A restart, sleep-wake or rebuild is a fresh database and a fresh ES256 key: guest
  accounts vanish and tokens minted before it stop verifying. Fine for the Roblox 460 integration testing it is
  for; not a home for real accounts, which is why production stays in the cluster.
- **Sleeps when idle.** The next request after a quiet spell pays a cold start. A lobby or ladder that polls
  keeps it warm.
- **Public login surface.** No default credentials ship (`webmaster.json` is excluded), but anyone can register
  a guest account here, exactly as against production.
- **Not the esports stats hub.** `SHANKPIT/tools/shankpit_netd.c` serves a file the running world writes; a
  Space has no path to that file, so hosting the ladder there needs a real ingest endpoint, not this Dockerfile.

## Corrections to the pasted plan

| It claimed | Reality |
|---|---|
| "point `docker_image:` at GHCR and Spaces pull it" | No documentation for that frontmatter on Spaces; they build from the repo's Dockerfile. `push.sh` uses only that path. |
| `curl -X POST https://huggingface.co` to set a secret | Not an endpoint. Secrets are Space settings, or `huggingface_hub.add_space_secret` (`/api/spaces/{repo_id}/secrets`). |
| "free tier, permanent URL, persistent storage options" | Free hardware sleeps and its disk is ephemeral; persistent storage is paid. |
| `gcc ... src/stats_hub.c` | No such file. IDUNA is one Go binary from `main.go` + `internal/`. |
| "bundle events every 30–60 s" | Right for Roblox's outbound limits, irrelevant to IDUNA: the Space answers requests, the world pushes. |
