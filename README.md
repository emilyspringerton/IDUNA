# IDUNA — Platform IAM & Governance Service

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
| `GET/POST` | `/api/v1/apples/...` | JWT | Golden documentation log |

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
