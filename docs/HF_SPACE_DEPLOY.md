# Shipping IDUNA to a Hugging Face Space

Founder, 2026-11-03: *"we ship iduna to hugging face."* This is the engineering spec and the honest state of
it. It supersedes a pasted plan that was written for a hypothetical `src/stats_hub.c` rather than this repo;
where that plan was wrong about the platform, the correction is named here.

## What actually ships

`deploy/huggingface/` — three files, no new runtime code:

| File | Does |
|---|---|
| `Dockerfile` | two stages, cgo build (gcc in the build stage only), `IDUNA_ADDR=0.0.0.0:7860`, state in `/data`, migrations in `/app`, `ENTRYPOINT` recreates `var/` |
| `README.md` | the Space's own frontmatter (`sdk: docker`, `app_port: 7860`) and what the Space honestly is |
| `push.sh` | assembles a bundle from `git archive HEAD`, **refuses DBs and secrets**, force-pushes it as the Space |

Plus `verify.sh`, which proves the container contract on this box without docker.

## Why the image carries gcc

First attempt was `CGO_ENABLED=0` — SQLite here is `modernc.org/sqlite`, pure Go, so it looked free. It does
not build:

```
internal/http/handlers/brawlpit_checkpoints.go:237:25: undefined: brawlpit.CompressLZ4
internal/http/handlers/brawlpit_levels_public.go:103:26: undefined: brawlpit.CompressLZ4
```

`internal/brawlpit/lz4.go` is a deliberate cgo binding over `lz4_gen.c`, which is PARENA's own
`stdlib/compress/lz4.prn` compiled to C — the same bytes BRAWLPIT's native client links, so both ends speak
one codec (S417-03). Hand-porting that to Go to dodge a compiler would undo that decision and risk the wire
format, so the build stage installs `gcc`/`musl-dev` and the runtime stage does not. This was found by
running the build, not by reading the Dockerfile.

## The failure that only a real boot finds

The first boot with this environment died before it bound the port:

```
mailinglist: failed to open store: migrate mailinglist db: unable to open database file (14)
```

IDUNA opens its optional per-feature stores by **relative** path (`./var/mailinglist.db`, vault, kanban…), and
a fresh working directory has no `var/`. No amount of reading the Dockerfile surfaces that. So the image
creates `/data/var` at build time **and** re-creates it in the `ENTRYPOINT`, because a Space can mount an empty
volume over `/data` and shadow whatever the image baked in. `verify.sh` mirrors the same `mkdir` for the same
reason — it is a one-line mirror of a real outage, not decoration.

## Why a bundle instead of pushing the repo

1. A Space's repo root **is** the app: it needs `Dockerfile` and a `README.md` with frontmatter at the root,
   and IDUNA's root is already busy.
2. **`iduna.db` is tracked in this repo.** Verified with `git ls-files`: a straight push would publish a
   database to a public Space. `push.sh` deletes `*.db`, `*.db-wal`, `*.db-shm`, `.env*`, `webmaster.json`,
   `*key.json` and `agent-secrets.env` from the bundle and reports how many it removed.
3. `git archive HEAD` makes the Space exactly one commit — no uncommitted drift between what was tested and
   what is deployed.

The bundle is also checked for standalone buildability (`GOWORK=off go list ./...`) because the Space builds
without the monorepo's `go.work` or any sibling modules. Verified: `go.mod` has **no** local `replace`s.

## Corrections to the pasted plan

| Claim it made | Reality |
|---|---|
| "point `docker_image:` at GHCR and Spaces pull it" | No documentation for that frontmatter on Spaces; Spaces build from the repo's Dockerfile. `push.sh` uses that path only. |
| `curl -X POST https://huggingface.co` to set a secret | Not an endpoint. Secrets are set in the Space's settings, or via `huggingface_hub.add_space_secret` (which posts to `/api/spaces/{repo_id}/secrets`). |
| "free tier, permanent public URL, persistent storage options" | Free hardware **sleeps** after idle and the disk is **ephemeral**; a restart wipes SQLite *and* the ES256 key. Persistent storage is a paid Space feature. |
| `gcc ... src/stats_hub.c` | No such file. IDUNA is one Go binary built from `main.go` + `internal/`. |
| "bundle events every 30–60 s" | Correct for Roblox's outbound limits, and irrelevant to IDUNA: the Space answers *requests*, the world pushes. |

## Deploying

```bash
cd IDUNA
deploy/huggingface/verify.sh                       # local proof, no docker, no account
deploy/huggingface/push.sh --dry-run               # show the bundle, confirm 0 DBs, push nothing
HF_TOKEN=hf_xxx HF_SPACE=emilyspringerton/iduna deploy/huggingface/push.sh
```

Then, in the Space's settings, add one secret:

| Secret | Must equal |
|---|---|
| `SHANKPIT_TICKET_SECRET` | the game server's value, or every ticket this mints is refused at `PACKET_CONNECT` |

## The database that was tracked in git

`iduna.db` **was** tracked in this repo (seen by `git ls-files iduna.db`). Inspected before deciding: it is
**0 bytes with 0 tables**, so nothing — no email, no password hash, no token, no signing key — has leaked.
It is an empty file that a local run created and a commit picked up.

The risk is not what it holds today, it is what it becomes: any local run writes real rows into a tracked
file, and the next `git add -A` publishes accounts. So it is now untracked (`git rm --cached`) and ignored.
History keeps only the empty blob; there is nothing to scrub, and no rotation is needed.

`push.sh` refuses `*.db*`, `.env*`, `webmaster.json`, `*key.json` and `agent-secrets.env` regardless, because
a Space is public and a future tracked database should not become a published one.

## Verification actually run here

```
deploy/huggingface/verify.sh
  build (CGO_ENABLED=1)                              -> ok
  health                                             -> 200
  guest-register route (invalid body)                -> 400   (route + shankpit row present)
  ticket without a token                             -> 401
  sign-up -> ticket on the Space's port              -> 36 bytes, MAC verified
```

Not run, and why: **the image has never been built** (no `docker` on this box), the push has never happened
(no `HF_TOKEN`), and nothing has been fetched from `huggingface.co` (this sandbox's egress reaches github.com
but resets TLS against okemily.com, so an HF round trip would be unproven either way).

## Limits this Space has by construction

- **Ephemeral identity.** A restart is a new database and a new ES256 key: accounts vanish and tokens minted
  before it stop verifying. For integration testing and for the Roblox 460 layer that is acceptable; for real
  accounts it is not, which is why production stays in the cluster.
- **Public login surface.** No default credentials ship (`webmaster.json` is excluded), but anyone can
  register a guest account, exactly as against production.
- **Cold starts.** A sleeping Space adds tens of seconds to the first request; a lobby that polls keeps it
  warm.
- **Not the stats hub.** The esports ladder feed is `tools/shankpit_netd.c` in SHANKPIT, which serves a file
  the world writes — a Space has no filesystem from the world, so hosting it there would need a new ingest
  endpoint, not a copy of this Dockerfile.
