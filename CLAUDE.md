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
| GET | `/admin/` | Back Office UI (admin role required) |
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
```

## Apples

Apples are the golden documentation audit trail. Filed by emily-agent after each RSI cycle.
Also backed up to `github.com/emilyspringerton/APPLES` via `emily sync --apples-git-dir`.

Apple types: `improvement`, `observation`, `audit`, `escalation`, `completion`, `backlog_completion`.

## HEIMDAL Sprints

Sprint lifecycle: `pending` → `queued` → `in_progress` → `complete` | `blocked`.  
MJOLNIR submits requirements (pending) → Emily Prime translates to RSI item (queued) →
Claude Code executes → Emily Prime patches on completion (complete/blocked).

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

