# IDUNA — Platform IAM & Governance Service

Central trust authority for EINHORN_INDUSTRIAL. Manages user auth (Google OAuth), M2M agent auth,
ES256 JWTs, RBAC, Apples ledger, HEIMDAL sprint planning, and FCM device tokens.

**All downstream services trust only IDUNA-issued JWTs. Never trust external tokens directly.**

## Listening on

`:8080` — all HTTP endpoints below.

## Key Endpoints

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/google` | Google OAuth callback → IDUNA JWT |
| GET | `/api/v1/auth/sso/login` | IDUNA-as-SSO login page (`SSOLoginHandler`) — the one place a password is typed for email/password auth; `?redirect_uri=` (allowlisted via `SSO_ALLOWED_REDIRECT_HOSTS`) gets the JWT back as a URL fragment. Also served at the root of the dedicated `iam.okemily.com` domain (see `ops/nginx/iam-okemily.conf`) once its DNS/cert land. |
| POST | `/api/v1/auth/agent` | M2M agent auth (agent_name + agent_secret) → JWT |
| GET | `/.well-known/jwks.json` | Public key set for JWT validation |
| POST | `/api/v1/apples` | File a golden documentation Apple |
| GET | `/api/v1/apples` | List Apples (limit, apple_type, source_repo filters) |
| GET/PATCH | `/api/v1/heimdal/sprints` | HEIMDAL sprint planning (MJOLNIR → Emily Prime) |
| GET/POST | `/api/v1/push-tokens/:agent` | FCM device tokens for MJOLNIR push |
| POST | `/api/v1/intelligence/observations` | Camera observations from MJOLNIR |
| POST | `/api/v1/subscriptions` | Provision Emily+ subscription (requires subscriptions.admin) |
| GET | `/api/v1/subscriptions/me` | Get caller's subscription status (requires JWT) |
| GET/POST/PATCH/DELETE | `/api/v1/kanban/cards[/:id]` | Kanban prioritization layer over EMILY/BACKLOG.md (requires kanban.access). `PATCH .../{id} {"queue":"done"}` is a real, special action, not a literal queue: archives the item's own real BACKLOG.md line (checkbox flipped, relocated to a standing archive section), files a real completion Apple, and removes the card. |
| POST | `/services/collector` | Unified logging backend ingest — Splunk HEC-shaped (`Authorization: Splunk <IDUNA_HEC_TOKEN>`), any JSON event |
| GET | `/services/search/jobs` | Unified logging backend search — Splunk-shaped, synchronous v0 (`?search=type=x source=y q=text&regex=pattern`, requires `logs.read`) |
| GET | `/portal/logs` | Log query UI in the developer portal (same search/regex query, requires `devportal.access` + `logs.read`) |
| GET | `/admin/` | Back Office UI (admin role required) |
| GET | `/admin/kanban` | Kanban board UI (Inbox + 3 columns: Backlog/Priority/Cruise, drag-and-drop; admin role required) |
| GET/POST/PATCH/DELETE | `/admin/qr[/api/codes[/:slug]]` | Dynamic QR code registry (admin role required): every QR image encodes IDUNA's own `/q/:slug` redirect URL, never the destination directly, so retargeting a code (`PATCH`) repoints every already-printed copy with zero reprinting. |
| GET | `/q/:slug` / `/q/:slug.png` | Public (no auth) — the real redirect a phone camera hits, and the live-rendered QR image itself (embeddable/printable without an admin session). |
| GET | `/play/big_o` \| `/play/brawlpit` | Public account-creation pages (`GameSignupPageHandler`, game-parameterized — neither game has a native client with this UI yet) — drive the generic `internal/games.Registry` guest-register/guest-login/guest-upgrade API, same code path DEADWEIGHT's own native client already uses. |
| GET | `/admin/kanban/api/inbox` | Real, open (unchecked), not-yet-carded `EMILY/BACKLOG.md` items — the live bridge from the backlog file to the board (admin role required) |
| * | `console.okemily.com/` (host-scoped, `NOCK_CODE_SERVER_HOST`) | VS Code (code-server, unmodified upstream) as a NOCK tool — a plain reverse proxy (`internal/http/handlers/nock_code_proxy.go`) to a local code-server instance (`ops/systemd/nock-code-server.service`, 127.0.0.1:8892, its own auth disabled). Gated by the exact same `RequireCookieAuth`+`iduna.admin` chain as every other admin route — same Back Office login, not a separate auth system. **Corrected 2026-09-21**: originally mounted under a `/admin/nock/code/` subpath — found live (real click-through, not assumed) that code-server's own router has no subpath support at all, so it's a dedicated root-mounted host instead (`ops/nginx/console-okemily.conf` has the full story). Linked from NOCK's own "Code" tab as a plain link, not an iframe. |
| GET | `/admin/openexecutive` | Back Office status + M2M-credential-provisioning page for OpenExecutive (S506) — reuses `/api/v1/openexecutive/provision`'s own core logic (`OpenExecutiveHandler.provisionCore`) so the two can't drift. States plainly that the integration is code-merged/tested but not yet live (needs a real GCP Vertex AI project, a human-only step). Found and fixed a real, previously-undiscovered bug building this: `openexec.read`/`openexec.admin`, the provision endpoint's own documented example permissions, were never actually registered as rows (`migrations/truestore/202609210001_openexecutive_permissions.sql`) — every prior grant of either one had silently failed closed. |
| GET | `/health` | Health check |

**This table is a curated subset, not the full route table.** A SAGA audit (2026-09-07) found
`main.go` registers ~128 distinct routes — this table covered roughly 20 of them, undercounting
several whole live subsystems. Real, additional, currently-served route families not itemized
above (see the actual `mux.Handle`/`HandleFunc` calls in `main.go` for the literal paths, and
`internal/http/handlers/` for the handler file — this list names the subsystem, not every path,
so it stays true without needing a line-by-line update every time a route is added):

- **GFD MMO backend** (`/api/v1/characters`, `/items`, `/guilds`, `/world-events`,
  `/fieldoffices`, `/hats`) — `internal/http/handlers/mmo*.go` (~2200 lines). Characters,
  inventory/equipment, guilds, world events, the WOTAN hat store (see
  `BRAWLPIT/docs/WOTAN_HAT_STORE_NORTHSTAR.md`).
- **Per-game ticketing/queues/leaderboards** for SHANKPIT, WEAKNIGHT_BEDROCK_RACERS, REDGARDEN,
  and PAPERCRAFT — real, live-match/ticket/queue/leaderboard routes, one handler file per game.
- **GFD admin content tools** — `gfd-items`, `gfd-mob-drops`, `gfd-registration`,
  `gfd-mob-spawns`, `gfd-dungeon-roster`: five real CRUD admin surfaces.
- **Organizations/cluster trust** (`/api/v1/organizations`) — `internal/http/handlers/
  organizations.go`, real (see `CarePyre/docs/HIPAA_COMPLIANCE_NORTHSTAR.md` for the design).
- **White-label branding** (`GET/PUT /api/v1/branding`) — `internal/http/handlers/branding.go`.
- **GDPR export/erasure** (`/api/v1/gdpr/`) — `internal/gdpr/`.
- **Compliance-recording storage** (`/api/v1/compliance/recording`) — `internal/http/handlers/
  compliance_recording.go`.
- **Notes, supply chain, research cache, kgraph, tenants, chat messages** — `notes.go`,
  `supply.go`, `research.go`, `kgraph.go`, `tenants.go`, `chat_messages.go`.

## Auth Model

- **Humans**: Google OAuth → `user_id` → roles → JWT with `roles[]` + `permissions[]`
- **Agents**: `agent_name` + `agent_secret` → JWT with explicit `permissions[]` (no role inheritance)
- Agents are registered in `config/agents.json` and seeded by `cmd/bootstrap`
- **Reveal a "training key"** (an agent's plaintext M2M secret) from the Back Office: `/admin/agents` → "Reveal secret" on any agent row with a credential set. The `agents` table only ever stores a one-way hash — this reads the plaintext back out of `var/agent-secrets.env` instead (`internal/agentsecrets`), the one place it's still recorded (founder real-time, 2026-09-25: "it needs to reveal them to me like an admin in carepyre can summon the email password out of the void"). Only works for an agent whose secret was set via `cmd/bootstrap` or rotated via this same admin UI since this feature landed — an older secret rotated some other way genuinely isn't recoverable; "Generate Secret" makes it revealable going forward. The reveal action itself (never the secret value) is audit-logged as `iduna:admin.agent.secret_reveal`.

## Directory Layout

```
cmd/
  bootstrap/    — seeds agents + initial users from config/
  bob-agent/    — MySQL schema admin agent (destructive ops require confirm: true)
  create-admin-agent/ — one-shot CLI, provisions a new agent with `iduna.admin` for signing into
                   `/admin/login` (Back Office). `go run ./cmd/create-admin-agent -name NAME`
                   prints the plaintext secret once — it's never retrievable again. Real, existing
                   tool for the recurring "no agent has iduna.admin, how do I get in" gap; a
                   credential-granting action, so a sandboxed agent needs the human to actually
                   run it, not just be told it exists.
internal/
  auth/         — JWT issuance, validation, Google OAuth flow
  http/handlers/ — route handlers (apples, heimdal, push-tokens, intelligence, admin, mmo*,
                   organizations, branding, gdpr, compliance_recording, notes, supply,
                   research, kgraph, tenants, chat_messages, and more — see the endpoint
                   list above for what's actually live; this directory has grown well past
                   what a short list here could stay accurate against)
  store/        — database layer (SQLite truestore + migrations)
  gdpr/         — GDPR export/erasure pipeline
  blog/, vault/, tenantprovision/, honorcode/, statuspage/, backlog/ — additional real
                   packages backing specific route families above; not exhaustively
                   itemized here for the same reason as http/handlers/ above
migrations/
  truestore/    — SQL migrations (timestamp-prefixed, append-only)
config/
  agents.json   — registered agents and their permissions
```

## Database

SQLite, default path controlled by `IDUNA_DB_PATH`. **Real, found-live correction (2026-09-07
SAGA audit)**: the live, actually-used database file on this box is `var/iduna.db`, not
`var/truestore.db` — `truestore.db` sits at a few KB, untouched since early August, while
`iduna.db` is the real, actively-growing (20+ MB) file every handler above actually reads/writes.
Whatever value `IDUNA_DB_PATH` is set to in this environment's own systemd unit/env file governs
which path is real; don't assume the bare word "truestore" in a path always means the live DB.
Migrations in `migrations/truestore/` are applied in filename order at startup against whichever
path is actually configured. **Never edit migration files after they've been applied — add new
ones.**

## Key Env Vars

```
IDUNA_DB_PATH        — default: ./var/truestore.db
IDUNA_JWT_PRIVATE_KEY — ES256 private key (PEM)
IDUNA_JWT_KEY_ID      — key ID embedded in JWTs
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
GOOGLE_REDIRECT_URI
GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON — service account key JSON for Drive API (optional; Drive disabled if absent)
GOOGLE_DRIVE_FOLDER_ID            — Google Drive folder ID for training artifact uploads (optional; root if absent)
IDUNA_HEC_TOKEN       — bearer token for POST /services/collector (unified logging backend ingest); unset disables ingest entirely
EMILY_BACKLOG_PATH    — path to EMILY/BACKLOG.md for the kanban board's two-way sync (inbox read + new-card write-back); default /home/fatbaby/EMILY/BACKLOG.md
SSO_ALLOWED_REDIRECT_HOSTS — comma-separated hostnames /api/v1/auth/sso/login's redirect_uri is allowed to target; default "wotan.okemily.com,localhost,127.0.0.1"
```

## Apples

Apples are the golden documentation audit trail. Filed by emily-agent after each RSI cycle.
Also backed up to `github.com/emilyspringerton/APPLES` via `emily sync --apples-git-dir`.

Apple types: `improvement`, `observation`, `audit`, `escalation`, `completion`, `backlog_completion`.

## HEIMDAL Sprints

Sprint lifecycle: `pending` → `queued` → `in_progress` → `complete` | `blocked`.  
MJOLNIR submits requirements (pending) → Emily Prime translates to RSI item (queued) →
Claude Code executes → Emily Prime patches on completion (complete/blocked).

## Unified Logging Backend

"One real place to jump to and grab the logs" (founder real-time, 2026-09-02) — a real,
Splunk-shaped event log, separate from the user-event log above (that one is scoped to IDUNA
local users specifically). Backed by `internal/userlog.FileEventLog` (the same real NDJSON
append-only log the user-event log itself uses, just a separate root dir), stored under
`var/eventlog/` — real, checked-not-assumed reason this reuses `userlog` rather than
`PRRJECT_FATBABY`'s own `eventstore` package (the more "original" implementation of the identical
shape): IDUNA's real CI checks out this repo standalone with no `go.work`/sibling-repo present,
confirmed via `GOWORK=off go build ./...`. See `internal/http/handlers/logs.go` for the real
scope: ingest (`POST /services/collector`, Splunk HEC's own real endpoint path/auth/payload/
response shape) and search (`GET /services/search/jobs`, Splunk's own real endpoint path,
deliberately synchronous — a real, narrow SPL subset: `type=`/`source=`/`q=` terms plus a real,
separate `regex=` parameter, RE2-based so it's not a ReDoS vector). A real log query UI lives in
the developer portal at `/portal/logs` (`internal/http/handlers/portal.go`'s own `Logs` method) —
same search/regex query, rendered as a real HTML form + results table; requires IDUNA itself to
be up (a real, deliberately accepted limitation while migration is planned, named on the page
itself, not hidden).

Real code paths that emit events today (S226-01 through S226-04, `EMILY/BACKLOG.md` SECTION 226 —
closed): `GoogleAuthHandler`/`AgentAuthHandler`/`LocalAuthHandler`/`AdminLoginHandler`/
`PortalHandler.LocalLogin` (every real login surface — `iduna:auth.*.success`/`.failure`);
`AdminHandler.userAction`/`agentAction` suspend/activate (`iduna:admin.user.suspend`/`.unsuspend`,
`iduna:admin.agent.suspend`/`.unsuspend`) and the same handler's role assign/revoke
(`iduna:admin.role.assign`/`.revoke`), agent permission grant/revoke
(`iduna:admin.agent_permission.grant`/`.revoke`), and agent secret rotation
(`iduna:admin.agent.secret_rotate` — records that a rotation happened, never the freshly-generated
plaintext); `HeimdalHandler.submit`/`.patch` (`iduna:heimdal.submit`, `iduna:heimdal.transition`
with real `from_status`/`to_status`); `ApplesHandler.create` (`iduna:apples.create`);
`KanbanHandler.create`/`.update`/`.completeCard` (`iduna:kanban.card.create`/`.move`/`.complete`
— kanban card 3243242, "ensure kanban does log streaming and checks in to the unified log").
Every
emission point is nil-safe (an unset `EventLog` field is a no-op, not a panic) and fire-and-forget
(a logging-backend outage never breaks the real auth/admin/heimdal/apples flow it's observing).

## Emily for Business — product-scoping (not implementation)

`docs/EMILY_FOR_BUSINESS_NORTHSTAR.md` (S243-02, 2026-09-03) — real, grounded scoping input for
the founder's own framing "IDUNA IS THE PRODUCT BASICALLY ZERO TRUST SECURITY AGENT NATIVE."
Names the real tension against this file's own standing "IDUNA is not a product, it is the
backbone" framing (`docs/NORTHSTAR.md`), maps IDUNA's real existing primitives onto a zero-trust
pitch, and names what's genuinely missing (multi-tenancy, self-serve onboarding, continuous
posture verification, compliance attestation) plus open questions for a founder-level decision.
Not an implementation plan — no code changes follow from this doc alone.

## Model Repositories — git-lfs Integration

Founder real-time, 2026-09-22: "we need to integrate the model repository with git lfs and each
model repository should have a git integration that can be turned off (on by default)."

Every RL checkpoint registry ("model repository" — `internal/brawlpit.CheckpointStore`, shared by
BRAWLPIT and DEADWEIGHT via its own `Game` field, and `internal/shankpit.CheckpointStore`) now
syncs each newly-created checkpoint blob into a real sibling repo checkout on this box
(`/home/fatbaby/<BRAWLPIT|DEADWEIGHT|SHANKPIT>`), under `models/rl-checkpoints/`, git-lfs-tracked
— `internal/modelgit.Syncer`. Fire-and-forget (a git/network failure never fails or blocks the
real checkpoint upload), reusing `internal/gitsync.PushWithRetry` (extracted from apples.go's own
production-proven Apples-git-sync idiom) for the actual add→commit→push→retry-on-rebase.

**On by default, per-game toggle**: `<GAME>_MODEL_GIT_DISABLED` (any non-empty value) turns the
integration off for that one game; unset (the default) is enabled — a `modelgit.Syncer{}` zero
value is enabled by construction, matching the founder's own "on by default" requirement without
needing a constructor at every call site. `<GAME>_MODEL_GIT_REPO_DIR` overrides the destination
repo path per game if the default sibling-checkout path is ever wrong for a given deployment.

## Migrations Checklist

- Migration filenames: `YYYYMMDDNNNN_description.sql`
- Never modify an applied migration
- `cmd/bootstrap` runs all pending migrations at startup

## Related Repos

- `EMILY` — Emily Prime agent (primary Apple filer, HEIMDAL processor)
- `PRRJECT_FATBABY` — signal pipeline (downstream JWT consumer)
- `MJOLNIR` — Android app (HEIMDAL submitter, push token registrar)

## Apple Filing Protocol

After any meaningful change, file an Apple:
```bash
emily apples post -t completion "<title>" "<body with commit hash>"
```
Then mark the item done in EMILY/BACKLOG.md and commit: `git add BACKLOG.md && git commit && git push`

## CHANGELOG Protocol

After any meaningful change, update CHANGELOG.md:
```bash
emily changelog add IDUNA "<what changed>"
# or manually: append a dated bullet under ## YYYY-MM-DD in IDUNA/CHANGELOG.md
```

## Golden Doc Registration

If you create a new NORTHSTAR.md, architecture spec, or mission-critical design doc in this repo,
append a row to `EMILY/context/golden-docs-index.md` so Emily Prime picks it up on the next cycle:
```
| NAME | <repo>/path/to/doc.md | 1 | <budget-or-0> | one-line description |
```
Then commit and push EMILY:
```bash
cd /home/fatbaby/EMILY && git add context/golden-docs-index.md && git commit -m "golden-index: add NAME" && git push
```

## SHANKPIT Level Registry Doubles as Living Documentation (standing instruction)

Founder real-time, 2026-09-17: "write into your claude files that when you verify levels write
them into the registry and leave them there as an example designers can use to try to figure out
how to use the feature without blowing a bunch of tokens asking for help." This registry
(`shankpit_levels`/`shankpit_widgets` tables, served under `/admin/nock/api/shankpit-levels` etc.,
gated by `iduna.admin`) is currently being treated as a development/staging registry — "assume
this is the development level registry we are developing in the open." Whenever a
SHANKPIT-level-editor feature is verified here, the verification should land as a real,
clearly-named level/widget row in this same live registry (e.g. `TUTORIAL_DOOR`) rather than a
disposable local JSON export, so it stays discoverable to designers via the NOCK UI afterward. See
`SHANKPIT/CLAUDE.md`'s own copy of this instruction for the full rationale. A separate registry
for real user-created content, and cloning this one's contents (including story levels) forward
into it, is a real possibility the founder named for later — not something to build preemptively.

## Founder Real-Time Direction

Whenever the founder gives real-time direction — a new ask, a correction, a "can we also..." —
route it through `emily observe -s info "Founder real-time: <summary>"` first, even if it isn't
this repo's usual domain, then sprint-plan it into `EMILY/BACKLOG.md` (`emily backlog curate`,
scoped into a real SECTION/sub-item, not just a one-line log), and only then implement. See
`EMILY/docs/THE_EMILY_WAY.md` Principle 18 ("Pave the Cow Paths").

## README Reality — SAGA reconciliation (standing instruction, monorepo-wide)

Founder real-time, 2026-09-18: if a change of yours **substantially changes the claim of this project's core README**,
then per SAGA protocols (`EMILY/docs/SAGA_SYSTEM_AUDIT_2026-07-18.md`, HQ-SPEC-DOC-102: intent ↔ claim ledger ↔ reality)
you **must update `README.md` in the same unit of work** so it reflects current reality. The README is the project's public
claim; it must not lag behind the code.

- **When it applies:** a capability is added or removed; status moves ("design only" → "working", "planned" → "shipped");
  the stack, build, run or install steps change; a claim in the README is now false or stale; or you add a **meaningful,
  genuinely interesting piece of kit** (a new tool, engine capability, protocol, pipeline, game system). For that last case
  especially: put it in the README — what it is, how to run it, and its honest status and limits.
- **When it does not:** ordinary fixes, refactors and small features that leave the README's claims true.
- **How:** re-read the README against what you just changed; fix or delete stale lines (including "not built yet" notes that
  are now built); verify any new claim by actually running it, and mark anything untested as untested; commit the README
  with (or immediately after) the change, and mention it in the CHANGELOG entry.

## Frame-Break Reframing

Founder-sourced prompting technique (REDGARDEN/NORTHSTAR.md §28, full origin in
REDGARDEN/docs2/MULTI_AGENT_RD_RESEARCH_NOTES.md §5): given a request, name the underlying
structural/systemic pattern it's one instance of — one level of abstraction up — as an added
lens during planning/triage/judgment calls. Use it to spot the general case behind a specific
ask. It augments judgment, it does not replace doing the work: direct, concrete execution of
the literal task asked for still happens every time.

## CONSTRUCT File Generation (standing instruction, monorepo Principle 21)

**IDUNA auto-generates deterministic CONSTRUCT and MANIFEST files on every push** via `scripts/generate_iduna_construct.sh` (integrated into CI). The CONSTRUCT is a plaintext snapshot of all tracked source files with SHA256 hashes, sizes, and git modes — used for reproducible builds, audit trails, and offline access.

Key features of IDUNA's implementation:
- Uses `git ls-files` for determinism (same git tree always produces identical output)
- Includes a MANIFEST file with per-file metadata (sha256, size, git mode)
- Base64-encodes binary files automatically
- Excludes generated artifacts (CONSTRUCT files themselves, build/, dist/, vendor/, etc.)

No manual work needed — generation is automatic. See the main `CLAUDE.md`'s "Principle 21: CONSTRUCT Files" section for the full monorepo pattern and how it integrates with releases.

## Commit Protocol (standing instruction)

Always commit and push completed work immediately — don't wait to be asked. This is the default for every repo in this monorepo.

Every commit — human-written or produced by automated code paths (git-commit helpers in emily-agent, emily.cli, IDUNA handlers, etc.) — must carry the active `emily session` fingerprint as a `session: <tag>` trailer (blank line, then the trailer). This was silently missing from several independently-implemented automated commit helpers across the monorepo until an audit on 2026-08-10 (founder, real-time: "where in the fuck is my llm session id anywhere"). If you add a new automated git-commit code path anywhere, wire in the session tag the same way — don't assume an existing helper already does it.
