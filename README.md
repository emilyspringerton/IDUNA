# IDUNA — Platform IAM & Governance Service

## Current Status (2026-08-26)

Live user account creation exists at **okemily.com/** (Google OAuth → honor code → permanent
gamertag, real end-to-end, `internal/http/handlers/web_ceremony.go`) for the platform-wide
identity system. Separately, `POST /api/v1/auth/email/register` now takes an optional
`character_name` and atomically creates a real DragonsNShit test account (real login + a real
playable character in one call) — see `CHANGELOG.md` 2026-08-04. Both are real, tested, and live.

**New since (see `CHANGELOG.md` for the full trail)**: a real developer notebook portal
(`/portal`, Google-SSO-gated, `devportal.access`); a **kanban prioritization layer** over
`EMILY/BACKLOG.md` — a real 3-column drag-and-drop board at `/admin/kanban` (`iduna.admin`) plus
a bearer-gated agent/CLI API at `/api/v1/kanban/cards` (`kanban.access`, see `emily.cli`'s own
`emily kanban` command); `/admin/login` and `/portal` both restyled to the real IDUNA cream/gold
ceremony design system (Cormorant Garamond + Spectral).

**2026-09-24 — IDUNA as SSO**: `GET /api/v1/auth/sso/login` is now the one place an email/password
is typed for IDUNA player auth — a two-pane login page (form left, brand right, same cream/gold
ceremony design system) that any site can redirect to instead of rendering its own form
(`internal/http/handlers/sso_login.go`). `redirect_uri` is allowlisted (`SSO_ALLOWED_REDIRECT_HOSTS`);
on success the page hands a JWT back via a URL fragment, never a query string. Meant to be reached
at a real, dedicated domain (`iam.okemily.com`, `ops/nginx/iam-okemily.conf`) so the browser's own
address bar shows IDUNA's identity while a password is typed — **DNS/cert for that domain are not
live yet** (queued: `sudo-queue/91-iam-okemily-sso-domain.sh`), so until then this is reachable the
same-origin-proxy way any caller's own `/api/` path already uses (e.g.
`wotan.okemily.com/api/v1/auth/sso/login`). WOTAN's `store.html` is the first real caller — it no
longer renders its own email/password form.

**2026-09-27 — NOCK video editor (phone uploads)**: a new **Video** tab in NOCK (`/admin/nock/#video`,
`iduna.admin`). Mint a short-lived phone link in NOCK; it's shown as a QR code; any phone camera
opens `BASE_URL/nock/upload/<token>` and uploads straight from its camera roll — no app, no IDUNA
login on the phone (the link token is the only credential: expiry, optional upload cap, revocable).
Uploads land in a clip library (ffprobe-checked — non-video is rejected and deleted; each clip gets a
browser-safe 720p H.264 proxy + thumbnail so iPhone HEVC previews anywhere); mark in/out, build a
timeline, and render it to one H.264/AAC MP4 via ffmpeg (mixed portrait/landscape, frame rates and
audio/no-audio sources are normalized, letterboxed not cropped). **Status**: tested (store + HTTP
tests against real ffmpeg, plus a Playwright walkthrough — phone viewport uploading a rotated HEVC
`.MOV` and an H.264 `.mp4`, then cut + render on desktop) against the real handlers in a local
harness; **not yet deployed** to the live IDUNA. **Needs on the host**: `ffmpeg`/`ffprobe` on PATH,
`NOCK_VIDEO_DIR` (default `var/nock-videos`), `NOCK_VIDEO_MAX_MB` (default 4096), and nginx
`client_max_body_size` raised to match for `/nock/upload/` + `/admin/nock/api/videos`. **Limits
(v0)**: cuts only — no transitions, titles, audio mixing or multiple tracks; one render at a time;
not tested on real physical phones yet (Playwright iPhone emulation only).

---

IDUNA is the central trust authority for the EINHORN_INDUSTRIAL / FARTHQ ecosystem. It sits between external identity providers (Google OAuth) and all downstream services and agents. Downstream services never trust external tokens directly — they exclusively trust IDUNA-issued ES256 JWTs.

IDUNA is intentionally not owned by any single agent. It is shared infrastructure.

---

## Architecture

```
Google OAuth → IDUNA → ES256 JWT → [FATBABY · SECWATCH · SIGNALAPI · ...]
                  ↑
              agents authenticate with api_key M2M credentials
              Bob (db_agent) manages schema
              Bootstrap seeds agents from config/agents.json
```

### Trust model

- **Human users** authenticate via Google OAuth → IDUNA maps `google_subject` to an internal `user_id` and issues a JWT with `roles[]` and `permissions[]`.
- **Agents** authenticate via M2M (`POST /api/v1/auth/agent`, `agent_name` + `agent_secret`) → IDUNA issues a JWT with the agent's explicit `permissions[]`.
- **Downstream services** validate IDUNA JWTs using the public JWKS at `/.well-known/jwks.json`. They never call Google.

### Agent permissions

Agents do **not** inherit permissions via roles. They receive explicit grants from `agent_permissions` only. This is intentional — minimum necessary authority by design.

System agent identities and their permissions are declared in `config/agents.json` and applied by `cmd/bootstrap`.

---

## Startup sequence

### Prerequisites

- MySQL 8.0+ running and accessible
- `MYSQL_DSN` environment variable set (see below)
- `ANTHROPIC_API_KEY` set (for agent endpoints that use Claude)

### Step 1 — Set environment variables

```bash
export MYSQL_DSN="user:pass@tcp(host:3306)/iduna?parseTime=true"
export ANTHROPIC_API_KEY="sk-ant-..."
export JWT_SECRET="$(openssl rand -hex 32)"   # for device auth legacy flow
export JWT_ISSUER="https://iam.yourhost.internal"
export BASE_URL="http://localhost:8080"
```

### Step 2 — Run bootstrap (idempotent)

```bash
go run ./cmd/bootstrap
```

Bootstrap does exactly three things and exits:
1. Runs all pending DB migrations from `migrations/truestore/`
2. Seeds agent permissions from `config/agents.json`
3. Generates API key secrets for any agents that don't have one yet, writes them to `var/agent-secrets.env`

**Safe to re-run on every deploy.** Already-applied migrations and already-provisioned credentials are skipped. Pass `-rotate` to regenerate all secrets.

### Step 3 — Source agent secrets

```bash
source var/agent-secrets.env
```

This file contains `IDUNA_SECRET_<AGENTNAME>=<plaintext>` for each agent. Never commit it — it is git-ignored.

### Step 4 — Start IDUNA

```bash
go run .
# or
go build -o iduna . && ./iduna
```

IDUNA listens on `:8080` by default (`PORT` env var to override).

### Step 5 — Start Bob

```bash
MYSQL_DSN="..." IDUNA_AGENT_SECRET="${IDUNA_SECRET_BOB}" go run ./cmd/bob-agent
```

Bob is the DB admin agent. He runs on `:8083` by default.

### Step 6 — Start other agents

Each agent uses its IDUNA credential to authenticate and receive a JWT:

```bash
# Emily agent (FATBABY-EMILY)
IDUNA_BASE_URL="http://localhost:8080" \
IDUNA_AGENT_NAME="FATBABY-EMILY" \
IDUNA_AGENT_SECRET="${IDUNA_SECRET_FATBABY_EMILY}" \
go run ./cmd/emily-agent   # in PRRJECT_FATBABY

# Jon Stockwell
IDUNA_BASE_URL="http://localhost:8080" \
IDUNA_AGENT_NAME="JON" \
IDUNA_AGENT_SECRET="${IDUNA_SECRET_JON}" \
go run ./cmd/jon-agent   # in PRRJECT_FATBABY
```

---

## Configuration as code

### `config/agents.json`

Declares all system agents, their types, and their minimum necessary permissions. Edit this file to change what an agent can do, then re-run `cmd/bootstrap` to apply.

```json
{
  "system_user_id": "00000000-0000-4000-8000-000000000001",
  "agents": [
    {
      "id": "00000003-0000-4000-8000-000000000002",
      "name": "FATBABY-EMILY",
      "type": "llm_agent",
      "permissions": ["fatbaby.operator", "governance.admin", ...]
    }
  ]
}
```

**Agent IDs are deterministic** (fixed UUIDs matching the seed migration). This makes bootstrap fully idempotent — the same config produces the same result on every run.

### Adding a new agent

1. Add a new migration in `migrations/truestore/` seeding the agent row (with `NULL` `api_key_hash`).
2. Add the agent entry to `config/agents.json` with its permissions.
3. Re-run `cmd/bootstrap`.

---

## Key environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `MYSQL_DSN` | ✓ | — | MySQL DSN: `user:pass@tcp(host:3306)/dbname?parseTime=true` |
| `ANTHROPIC_API_KEY` | ✓ (agents) | — | Anthropic API key for Bob and other LLM agents |
| `JWT_SECRET` | ✓ | — | Legacy device auth JWT signing secret |
| `JWT_ISSUER` | — | `https://iam.farthq.internal` | JWT `iss` claim |
| `BASE_URL` | — | `http://localhost:8080` | Public base URL (used in device flow) |
| `PORT` | — | `8080` | IDUNA listen port |
| `KEY_FILE` | — | `./iduna-key.json` | ES256 key pair file (generated if absent) |
| `GOOGLE_CLIENT_ID` | — | — | Google OAuth client ID (required for human login) |
| `IDUNA_ROOT` | — | `.` | Path to IDUNA repo root (for bootstrap) |

---

## Migrations

Migrations live in `migrations/truestore/` named `YYYYMMDDNNNN_description.sql`. Applied in filename order. Once applied, never modified — SHA-256 is recorded in `schema_migrations`.

| Migration | Description |
|---|---|
| `202602220001_device_auth.sql` | Device auth flow, exchange codes, event store |
| `202602220002_iam_rbac.sql` | Users, roles, permissions, agents, IAM event stream |
| `202606010001_agent_credentials.sql` | `api_key_hash` column on agents table |
| `202606020001_apples.sql` | Golden documentation log (HQ-SPEC-IAM-096) |
| `202606030001_system_seeds.sql` | System owner user, agent stubs, agent-scoped permissions |

---

## HTTP endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/api/v1/auth/agent` | none | M2M agent authentication → JWT |
| `GET` | `/.well-known/jwks.json` | none | Public key set for JWT verification |
| `GET` | `/api/v1/auth/google` | none | Google OAuth redirect |
| `GET` | `/api/v1/auth/callback` | none | Google OAuth callback |
| `POST` | `/api/v1/device/start` | none | Device flow initiation |
| `GET` | `/api/v1/device/poll` | device | Device flow poll |
| `POST` | `/api/v1/device/confirm` | user | Confirm device auth |
| `POST` | `/api/v1/device/exchange` | user | Exchange device code for JWT |
| `GET` | `/api/v1/me` | JWT | Own identity + permissions |
| `GET` | `/admin/...` | JWT (iduna.admin) | Admin UI |
| `GET` | `/admin/kanban` | cookie (iduna.admin) | Kanban board — 3 columns (Backlog/Priority/Cruise), drag-and-drop, over `EMILY/BACKLOG.md` |
| `GET/POST/PATCH/DELETE` | `/api/v1/kanban/cards[/:id]` | JWT (kanban.access) | Kanban board, agent/CLI half |
| `GET` | `/portal` | cookie (devportal.access) | Developer notebook portal (Jupyter/SARENA_NOTEBOOK) |
| `GET/POST` | `/api/v1/blog/posts[/:slug]` | none (GET) / JWT (POST, blog.write) | okemily.com blog |
| `GET/POST` | `/api/v1/apples/...` | JWT | Golden documentation log |
| `GET` | `/admin/qr` | cookie (iduna.admin) | Dynamic QR code registry — create/retarget/delete codes, preview live-rendered images |
| `GET/POST/PATCH/DELETE` | `/admin/qr/api/codes[/:slug]` | cookie (iduna.admin) | Same registry, JSON API |
| `GET` | `/q/:slug` | none | The real redirect a phone camera/printed flyer hits — 302 to the code's current `target_url` |
| `GET` | `/q/:slug.png` | none | The live-rendered QR image itself, embeddable/printable with no admin session |
| `GET` | `/play/big_o` \| `/play/brawlpit` | none | Public account-creation pages — create/resume a guest account via `/api/v1/games/{game}/guest-*`, no native client required for either game yet |
| `POST` | `/api/v1/games/{game}/guest-register` \| `guest-login` \| `guest-upgrade` | none / player JWT | Generic per-game guest-account flow (`internal/games.Registry`) — DEADWEIGHT, BIG_O, and BRAWLPIT all ride this same code path |
| `POST` | `/admin/nock/api/shankpit-widgets/import-gltf` | cookie (iduna.admin) | NOCK glTF → SHANKPIT widget bridge: multipart `file` (.glb / embedded .gltf, same input as the Animations tab importer), `name`, optional `scale`, `preview=1` to convert without saving. One axis-aligned box wall per mesh node (world-space bounds, full node hierarchy transforms, `baseColorFactor` color); nodes named `door*` become scriptless doors. Exact for cube blockouts, lossy for rotated/detailed meshes (bounding box only). Unit-tested; not yet tried against a real Blender export. Also a button in NOCK's Widgets tab. |

---

## NOCK — animator and rig tools

NOCK (`/admin/nock`, gated by `iduna.admin`) is the in-house asset toolset: textures, levels, and
a character library of GOLDENBAND `.gmesh` / `.gskel` / `.gband` assets imported from glTF. As of
2026-09-27 it can also **author animation** and **move meshes and clips between rigs**, which were
the two Blender jobs it previously couldn't do.

- **Animator** (character library → *Animate*, on any row with a rig): Blender-style pose mode +
  dope sheet. Click a joint, drag the gizmo (R rotate / G move), and keys are set at the playhead
  (auto-key, or I to key by hand). Per-key interpolation is linear, smooth, or step. Keyframes are the
  stored source (`nock_animations.keyframes_json`) and the server bakes them to a `.gband` on
  save. Imported clips open with editable keys derived from their baked motion and only ever
  save as a new row.
- **Rig tools** (character library → *Rig tools*): pick a target rig, review the automatic bone
  map (exact / normalised name, canonical body part across Mixamo, Unreal, Rigify and Biped
  naming, then chain order for spines and fingers), override any joint, then:
  - *Remap mesh*: re-skin a mesh onto the target rig, either by keeping its weights through the
    bone map or with new proximity weights for an unskinned or unrelated mesh. Optionally fits
    the mesh to the rig's size.
  - *Retarget clip*: move an animation onto the target rig. This works in world space, so the rigs
    can use different bone-axis conventions, and root motion is scaled to the target's height.
- **CLI** (file-based, no server): `go run ./cmd/nock rig-map | rig-remap-mesh | rig-retarget |
  anim-bake | anim-keys` (`nock help` for flags).
- **HTTP**: `GET/POST /admin/nock/api/animations/{id}/keyframes`, `GET .../{id}/bone-map?target=`,
  `POST .../{id}/remap-mesh`, `POST .../{id}/retarget`.

**Verified** against the real 65-joint Unreal-style mannequin and its mocap clips, plus a Mixamo-named
copy of that rig:
- The automatic bone map matched all 65 joints correctly.
- Retargeting a real walk onto the renamed rig reproduced every joint rotation to within 0.05°.
- A name-based mesh remap kept every artist weight.
- The full animate → save → remap → retarget flow was driven in headless Chromium.

**Limits:**
- Proximity weights are distance envelopes, not Blender's heat-diffusion weights: the dominant
  joint agreed with the artist's weights on 71% of the mannequin's vertices, so they're a starting
  point, not a finished skin.
- Retargeting needs matching rest poses (T to T, A to A) and has no IK or foot locking.
- There is no weight painting, skeleton building, or mesh modelling yet.

### Real UX screenshot testing (2026-09-28)

`/admin/nock` has no `ErrorBoundary`, so an uncaught render error in any one tab's component
blanks the *entire* app (this is what "SHANKPIT levels is down, just a blank screen" turned out to
look like, live). `frontend/nock/scripts/ux_screenshot_test.mjs` (`npm run test:ux-screenshots`,
inside `frontend/nock/`) logs in for real through `/admin/login` (a dedicated agent,
`SCREENSHOT-CI`, provisioned the same way EDDY/HOUSE/BOOTS already are — no cookie forging), clicks
through all 17 real nav tabs against a real running server, screenshots each one, and fails if any
tab renders near-empty or throws. Must target a real `*.okemily.com` host (the session cookie is
domain-scoped) — `https://okemily.com` by default. Screenshots land in
`frontend/nock/scripts/ux-screenshots/` (gitignored). Not a pixel-diff — it catches "did this
render real content with zero crashes," not subtle visual regressions.

---

## Bob — database admin agent

Bob (`cmd/bob-agent`) is IDUNA's DB specialist. He:
- Runs schema migrations on demand
- Inspects table structure, row counts, indexes
- Performs read-only data queries
- Reports DB health

Bob is a **Tier-2 Specialist Agent** — Emily Prime can dispatch tasks to him. He has no LLM autonomy beyond his defined tool set. His authority is bounded to `bob.db.admin` — he cannot affect application logic, agent permissions, or other services.

---

## The Rose Gold Protocol

Irreversible DB operations (permanent bans, audit log modification, agent decommissioning) are styled in Rose Gold (#B76E79) in the admin UI. Bob requires explicit confirmation before executing them. The audit log (`iam_event_stream`) is append-only and must never be deleted or modified.

---

## Implemented IAM Surface

- Google ID token exchange at `POST /api/v1/auth/google`
- Agent M2M credential exchange at `POST /api/v1/auth/agent`
- Public signing keys at `/.well-known/jwks.json` and `/api/v1/jwks`
- Identity and entitlement lookup at `GET /api/v1/identities/me`
- Back Office admin ledgers at `/admin` (users, agents, audit events, Apples)
  - Login page: `http://localhost:8080/admin/login` — sign in with an agent that has `iduna.admin` permission
  - To grant admin to an agent: assign the `iduna.admin` role via the IDUNA CLI or directly in the DB:
    `INSERT INTO role_assignments(user_id,role_id,operator_id) ...` (see migrations)
  - To authenticate as EMILY for admin access, use her agent credentials configured in `EMILY_SECRET`
- Apples golden documentation log at `POST/GET /api/v1/apples`

## Documentation

- `docs/iam-spec.md` — platform IAM and governance architecture
- `golden.md` — HQ-SPEC-IAM-096 Apples spec
- `openapi.yaml` — implemented API contract
- `CHANGELOG.md` — implementation history
