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
| POST | `/api/v1/auth/agent` | M2M agent auth (agent_name + agent_secret) → JWT |
| GET | `/.well-known/jwks.json` | Public key set for JWT validation |
| POST | `/api/v1/apples` | File a golden documentation Apple |
| GET | `/api/v1/apples` | List Apples (limit, apple_type, source_repo filters) |
| GET/PATCH | `/api/v1/heimdal/sprints` | HEIMDAL sprint planning (MJOLNIR → Emily Prime) |
| GET/POST | `/api/v1/push-tokens/:agent` | FCM device tokens for MJOLNIR push |
| POST | `/api/v1/intelligence/observations` | Camera observations from MJOLNIR |
| POST | `/api/v1/subscriptions` | Provision Emily+ subscription (requires subscriptions.admin) |
| GET | `/api/v1/subscriptions/me` | Get caller's subscription status (requires JWT) |
| GET/POST/PATCH/DELETE | `/api/v1/kanban/cards[/:id]` | Kanban prioritization layer over EMILY/BACKLOG.md (requires kanban.access) |
| POST | `/services/collector` | Unified logging backend ingest — Splunk HEC-shaped (`Authorization: Splunk <IDUNA_HEC_TOKEN>`), any JSON event |
| GET | `/services/search/jobs` | Unified logging backend search — Splunk-shaped, synchronous v0 (`?search=type=x source=y q=text&regex=pattern`, requires `logs.read`) |
| GET | `/portal/logs` | Log query UI in the developer portal (same search/regex query, requires `devportal.access` + `logs.read`) |
| GET | `/admin/` | Back Office UI (admin role required) |
| GET | `/admin/kanban` | Kanban board UI (Inbox + 3 columns: Backlog/Priority/Cruise, drag-and-drop; admin role required) |
| GET | `/admin/kanban/api/inbox` | Real, open (unchecked), not-yet-carded `EMILY/BACKLOG.md` items — the live bridge from the backlog file to the board (admin role required) |
| GET | `/health` | Health check |

## Auth Model

- **Humans**: Google OAuth → `user_id` → roles → JWT with `roles[]` + `permissions[]`
- **Agents**: `agent_name` + `agent_secret` → JWT with explicit `permissions[]` (no role inheritance)
- Agents are registered in `config/agents.json` and seeded by `cmd/bootstrap`

## Directory Layout

```
cmd/
  bootstrap/    — seeds agents + initial users from config/
  bob-agent/    — MySQL schema admin agent (destructive ops require confirm: true)
internal/
  auth/         — JWT issuance, validation, Google OAuth flow
  http/handlers/ — route handlers (apples, heimdal, push-tokens, intelligence, admin)
  store/        — database layer (SQLite truestore + migrations)
migrations/
  truestore/    — SQL migrations (timestamp-prefixed, append-only)
config/
  agents.json   — registered agents and their permissions
```

## Database

SQLite at `var/truestore.db` (default). Migrations in `migrations/truestore/` are applied in
filename order at startup. **Never edit migration files after they've been applied — add new ones.**

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

Real code paths that DO emit events today: `GoogleAuthHandler`/`AgentAuthHandler`/
`LocalAuthHandler`/`AdminLoginHandler`/`PortalHandler.LocalLogin` (every real login surface —
`iduna:auth.*.success`/`.failure`) and `AdminHandler.userAction`/`agentAction` suspend/activate
(`iduna:admin.user.suspend`/`.unsuspend`, `iduna:admin.agent.suspend`/`.unsuspend`). Every
emission point is nil-safe (an unset `EventLog` field is a no-op, not a panic) and fire-and-forget
(a logging-backend outage never breaks the real auth/admin flow it's observing). Real, honest,
not yet done: HEIMDAL sprint transitions, Apple postings, and role/permission grant/revoke don't
emit events yet (`EMILY/BACKLOG.md` SECTION 226, S226-04).

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

## Founder Real-Time Direction

Whenever the founder gives real-time direction — a new ask, a correction, a "can we also..." —
route it through `emily observe -s info "Founder real-time: <summary>"` first, even if it isn't
this repo's usual domain, then sprint-plan it into `EMILY/BACKLOG.md` (`emily backlog curate`,
scoped into a real SECTION/sub-item, not just a one-line log), and only then implement. See
`EMILY/docs/THE_EMILY_WAY.md` Principle 18 ("Pave the Cow Paths").

## Frame-Break Reframing

Founder-sourced prompting technique (REDGARDEN/NORTHSTAR.md §28, full origin in
REDGARDEN/docs2/MULTI_AGENT_RD_RESEARCH_NOTES.md §5): given a request, name the underlying
structural/systemic pattern it's one instance of — one level of abstraction up — as an added
lens during planning/triage/judgment calls. Use it to spot the general case behind a specific
ask. It augments judgment, it does not replace doing the work: direct, concrete execution of
the literal task asked for still happens every time.

## Commit Protocol (standing instruction)

Always commit and push completed work immediately — don't wait to be asked. This is the default for every repo in this monorepo.

Every commit — human-written or produced by automated code paths (git-commit helpers in emily-agent, emily.cli, IDUNA handlers, etc.) — must carry the active `emily session` fingerprint as a `session: <tag>` trailer (blank line, then the trailer). This was silently missing from several independently-implemented automated commit helpers across the monorepo until an audit on 2026-08-10 (founder, real-time: "where in the fuck is my llm session id anywhere"). If you add a new automated git-commit code path anywhere, wire in the session tag the same way — don't assume an existing helper already does it.
