# IDUNA — FARTHQ Platform IAM & Governance Service

IDUNA is the FARTHQ central trust authority for identity, authentication,
authorization, auditability, and agent governance. Consumer services such as
FATBABY, KIKORYU, and SECWATCH validate IDUNA-issued JWTs rather than trusting
external OAuth providers directly.

## Implemented IAM Surface

- Google ID token exchange at `POST /api/v1/auth/google`, returning a signed
  one-hour IDUNA ecosystem JWT with roles and flattened permissions.
- Agent machine-to-machine credential exchange at `POST /api/v1/auth/agent`,
  returning a signed JWT for ACTIVE agents with bounded capabilities.
- Public signing keys at `/.well-known/jwks.json` and `/api/v1/jwks`.
- Unified identity and entitlement lookup at `GET /api/v1/identities/me`.
- Back Office HTML ledgers for users, agents, audit events, and Apples under
  `/admin`.
- Apples golden documentation log streaming at `POST /api/v1/apples`,
  `GET /api/v1/apples`, and `GET /api/v1/apples/{id}`.

## Documentation

- `docs/iam-spec.md` — approved platform IAM and governance architecture.
- `golden.md` — HQ-SPEC-IAM-096 Apples implementation specification and current
  checklist state.
- `openapi.yaml` — implemented API contract for IAM, JWKS, identity, Apples,
  and Back Office entry points.
- `CHANGELOG.md` — implementation log for IAM agent credentials and Apples.

## Local Development

```bash
go test ./...
```

Runtime configuration expects a MySQL DSN and signing key path:

```bash
MYSQL_DSN='user:pass@tcp(localhost:3306)/iduna?parseTime=true' \
KEY_FILE='./iduna-key.json' \
JWT_ISSUER='https://iam.farthq.internal' \
BASE_URL='http://localhost:8080' \
GOOGLE_CLIENT_ID='<google-client-id>' \
go run .
```

Apply migrations from `migrations/truestore/` before starting a fresh database.
