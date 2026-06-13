# IDUNA — Northstar

*Last updated: 2026-06-13*

---

## Three-Sentence Version

IDUNA is the central trust authority for EINHORN_INDUSTRIAL: IAM (Google OAuth + ES256 JWT), RBAC,
M2M agent credentials, Apples audit ledger, and HEIMDAL sprint planning interface.
Every agent authenticates through IDUNA; every meaningful system event is recorded as an Apple.
The Back Office is the clerical audit terminal — institutional, high-density, no dashboards.

---

## What IDUNA Is

IDUNA is not a product. It is the backbone. All agents (Emily Prime, FatBaby-Emily, EDIS-CUSTODIAN,
TYLER) authenticate via IDUNA's M2M credential system. All human users authenticate via Google OAuth
→ IDUNA-issued ES256 JWT. Every consumer service (newssite, signalapi, EDIS) verifies JWTs against
IDUNA's public key. No consumer service talks to Google directly.

---

## Core Systems

### IAM Layer
- Google OAuth 2.0 → user identity resolution → RBAC role assignment → ES256 JWT issuance
- Hierarchical RBAC: capabilities flow down from resource → role → user/agent
- M2M agent auth: `POST /api/v1/auth/agent` → JWT with agent capabilities
- Back Office admin UI: institutional aesthetic (`#B76E79` rose gold for destructive actions only)

### Apples Ledger
- Append-only audit trail of every RSI cycle, observation, escalation, and completion
- `POST /api/v1/apples` — filed by agents after every meaningful action
- `GET /api/v1/apples` — queryable by type, repo, agent, date range
- Git-authoritative backup: synced to APPLES repo via `emily sync --apples-git-dir`
- Apple types: `improvement` · `observation` · `audit` · `escalation` · `completion`

### HEIMDAL Sprint Interface
- MJOLNIR submits product requirements → IDUNA stores as `heimdal_sprints`
- Emily Prime fetches queued sprints, translates via haiku → RSI roadmap item + FCM push
- Sprint lifecycle: `pending` → `queued` → `in_progress` → `complete` | `blocked`
- Emily Prime patches sprint status on completion/block + FCM push to MJOLNIR

---

## Endpoints (port 8080)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/google` | Google OAuth token exchange → IDUNA JWT |
| POST | `/api/v1/auth/agent` | M2M agent credential → JWT |
| GET | `/api/v1/apples` | Query Apples ledger |
| POST | `/api/v1/apples` | File a new Apple |
| GET | `/api/v1/heimdal/sprints` | List HEIMDAL sprints |
| POST | `/api/v1/heimdal/sprints` | Create sprint requirement |
| PATCH | `/api/v1/heimdal/sprints/:id` | Update sprint status |
| GET | `/api/v1/agents` | List registered agents |
| GET | `/back-office/` | Admin UI (Back Office) |

---

## Architecture

```
Google OAuth
    ↓
IDUNA (:8080)
    ├── MySQL TrueStore (users, roles, capabilities, agents, apples, heimdal_sprints)
    ├── ES256 key pair (iduna-key.json)
    ├── Back Office admin UI
    └── Agent registry (config/agents.json → seeded at bootstrap)
         ↕
Emily Prime (:8086) — files Apples, reads HEIMDAL sprints
MJOLNIR (Android) — reads Apple feed, submits HEIMDAL requirements
newssite / signalapi / EDIS — verify JWTs against IDUNA public key
```

---

## Status (2026-06-13)

All spec checklist items complete. Running at `:8080`.

**Pending / known gaps:**
- MJOLNIR device token registration endpoint (for FCM push targeting)
- Aggregate token stats endpoint (MJOLNIR sparkline, deferred)
- Emily+ subscription resource (S23-04 dependency)
- `iduna agents register` CLI command (S5, deferred)

---

## Key Files

| Path | What it is |
|------|------------|
| `main.go` | HTTP server entrypoint, router |
| `internal/store/` | MySQL TrueStore — all IAM + Apples + HEIMDAL persistence |
| `migrations/` | Schema migrations (bootstrap runs these at startup) |
| `config/agents.json` | Agent registry seed (seeded at bootstrap) |
| `golden.md` | Implementation spec (Apples schema, endpoints, checklist) |
| `docs/iam-spec.md` | Full IAM architecture spec |
| `openapi.yaml` | OpenAPI 3.0 spec for all endpoints |

---

## Related Repos

| Repo | Relationship |
|------|--------------|
| `EMILY` | Primary Apple filer; reads HEIMDAL sprints each cron cycle |
| `APPLES` | Git-authoritative Apple backup |
| `MJOLNIR` | Reads Apple feed; submits HEIMDAL requirements |
| `PRRJECT_FATBABY` | Agents authenticate via IDUNA M2M |
| `EDIS` | EDIS-CUSTODIAN agent registered in IDUNA |
