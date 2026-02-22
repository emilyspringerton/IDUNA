# IDUNA (VS0) — Social Tournament Network + Google OAuth + THE_HONOR_CODE

> Codename: **IDUNA**  
> Stack: **Go + MySQL**  
> Architecture: **Event Store (append-only) → Projectors → True Store (current state)**  
> Frontend: **Tailwind CSS + Vanilla JS** (minimal aesthetic, 3-screen funnel)

This README is the working source of truth for IDUNA VS0 behavior.
When in doubt: **keep it minimal, deterministic, replayable, and secure**.

---

## 0) One-sentence product

IDUNA is a minimal identity + tournament social layer for a persistent single-shard MMO world: users authenticate with Google, must accept THE_HONOR_CODE, then lock in a unique gamertag used as their MMO character name; tournament/social actions are evented and projected into a queryable true store.

---

## 1) Non-negotiables

1. Google OAuth is the only registration path in VS0.
2. No account becomes fully active until THE_HONOR_CODE is accepted.
3. Honor code acceptance is versioned; when current version changes, re-acceptance is required.
4. Users must choose a unique gamertag (handle) after honor acceptance.
5. Gamertag is intended to be the MMO character name in the persistent single-shard world.
6. All state changes originate as append-only events.
7. True store is derived state hydrated by projectors.
8. Projectors must be idempotent and replay-safe.
9. Frontend remains minimal and funnel-shaped.

---

## 2) System overview

### 2.1 Components

**A) Event Store service**
- Appends immutable events.
- Enforces optimistic concurrency per stream (`expected_version`).
- Emits globally ordered sequence (`global_seq`) for consumption.

**B) Projector service**
- Polls/read events ordered by `global_seq`.
- Applies events to true-store tables.
- Tracks checkpoint + applied-event idempotency.
- Supports full and partial replay.

**C) True Store (current state)**
- Query-optimized MySQL schema (`users`, `honor_code_*`, tournaments, etc).
- Updated by projector(s), not by direct mutable writes from command handlers.

**D) VS0 frontend**
- Tailwind + vanilla JS.
- 3-screen funnel: Google OAuth → Honor Code → Gamertag.

---

## 3) MySQL schema blueprint

### 3.1 Event Store tables

#### `event_streams`
- `stream_id VARBINARY(16) PRIMARY KEY`
- `stream_type VARCHAR(64) NOT NULL`
- `current_version BIGINT NOT NULL DEFAULT 0`
- timestamps

#### `events` (append-only)
- `global_seq BIGINT AUTO_INCREMENT PRIMARY KEY`
- `event_id VARBINARY(16) NOT NULL UNIQUE`
- `stream_id VARBINARY(16) NOT NULL`
- `stream_version BIGINT NOT NULL`
- `event_type VARCHAR(128) NOT NULL`
- `occurred_at TIMESTAMP(6) NOT NULL`
- `payload_json JSON NOT NULL`
- `meta_json JSON NULL`

Constraints:
- `UNIQUE(stream_id, stream_version)`
- `UNIQUE(event_id)`

Optional dedup table:

#### `command_dedup`
- `idempotency_key VARCHAR(128) PRIMARY KEY`
- `user_id VARBINARY(16) NULL`
- `request_hash VARBINARY(32) NOT NULL`
- `created_at TIMESTAMP(6) NOT NULL`

### 3.2 True Store tables (VS0 minimum)

#### `users`
- `id VARBINARY(16) PRIMARY KEY`
- `status ENUM('pending_honor','active','suspended') NOT NULL`
- `email VARCHAR(320) NULL`
- `display_name VARCHAR(64) NULL`
- `handle VARCHAR(32) NULL`
- `handle_norm VARCHAR(32) NULL`
- `avatar_url TEXT NULL`
- timestamps

Constraint:
- `UNIQUE(handle_norm)` (case-insensitive uniqueness)

#### `user_identities`
- `id BIGINT PRIMARY KEY`
- `user_id VARBINARY(16) NOT NULL`
- `provider VARCHAR(32) NOT NULL` (`google` in VS0)
- `provider_subject VARCHAR(255) NOT NULL`
- `email VARCHAR(320) NULL`
- `email_verified TINYINT(1) NOT NULL`
- timestamps

Constraint:
- `UNIQUE(provider, provider_subject)`

#### `honor_code_versions`
- `id BIGINT PRIMARY KEY`
- `version VARCHAR(32) NOT NULL`
- `sha256 CHAR(64) NOT NULL`
- `title VARCHAR(128) NOT NULL`
- `body_markdown MEDIUMTEXT NOT NULL`
- `published_at TIMESTAMP(6) NOT NULL`
- `is_current TINYINT(1) NOT NULL`

#### `honor_code_acceptances`
- `id BIGINT PRIMARY KEY`
- `user_id VARBINARY(16) NOT NULL`
- `honor_code_version_id BIGINT NOT NULL`
- `accepted_at TIMESTAMP(6) NOT NULL`
- `ip VARBINARY(16) NULL`
- `ua_hash VARBINARY(32) NULL`

### 3.3 Projector plumbing

#### `projector_checkpoints`
- `projector_name VARCHAR(128) PRIMARY KEY`
- `last_global_seq BIGINT NOT NULL DEFAULT 0`
- `updated_at TIMESTAMP(6) NOT NULL`

#### `projector_applied_events`
- `projector_name VARCHAR(128) NOT NULL`
- `global_seq BIGINT NOT NULL`
- `applied_at TIMESTAMP(6) NOT NULL`
- `PRIMARY KEY(projector_name, global_seq)`

---

## 4) Event conventions

- IDs: UUID/ULID as `VARBINARY(16)` in MySQL.
- Event rows are immutable; corrections happen via new events.
- Event types: PascalCase (`HonorCodeAccepted`).
- Payload keys: camelCase (`honorCodeSha256`).
- Canonical read order: `ORDER BY global_seq ASC`.

---

## 5) VS0 event vocabulary (minimum)

### Identity/Auth
- `UserRegistered`
  - `{ userId, provider:"google", providerSub, email, emailVerified }`

### Honor code
- `HonorCodeAccepted`
  - `{ userId, honorCodeSha256, honorCodeVersion }`

### Gamertag
- `GamertagSet`
  - `{ userId, handle, handleNorm }`

---

## 6) Projector rules (absolute)

1. Apply event in a true-store transaction.
2. Insert idempotency marker (`projector_applied_events`).
3. If duplicate marker exists, skip safely.
4. Apply projection writes.
5. Advance checkpoint to current `global_seq`.
6. Commit transaction.

No direct mutable true-store writes from command handlers.

---

## 7) Append semantics

### Optimistic concurrency
Append flow per stream transaction:
1. `SELECT ... FOR UPDATE` stream row.
2. Verify `current_version == expected_version`.
3. Insert one or more events with incremented `stream_version`.
4. Update `event_streams.current_version`.
5. Commit.

On mismatch: return `409 CONFLICT`.

### Global ordering
- `events.global_seq` is canonical order for all consumers.

---

## 8) Replay infrastructure

### Full replay
1. Stop projectors.
2. Truncate/rebuild true-store projection tables.
3. Reset checkpoints to `0`.
4. Clear applied-event markers.
5. Restart projectors from sequence start.

### Partial replay
- Reset one projector (or version projectors, e.g. `UserProjector_v2`).
- Rebuild only owned projection tables.
- Re-run from selected sequence.

---

## 9) API contract (VS0)

All responses JSON.

### Auth
- `GET /auth/google/start` → `{ "url": "..." }`
- `POST /auth/google/callback` body `{ code, redirect_uri }`
  - returns token/session + user + honor payload.

### Honor
- `POST /honor-code/accept` body `{ sha256 }`

### Me
- `GET /me`
  - returns `user` and `honor_code.required`.

### Gamertag
- `GET /gamertag/check?handle=Foo` → `{ available, reason? }`
- `POST /me/handle` body `{ handle }`

### Honor gate behavior
For write endpoints when honor is not current/accepted:
- `HTTP 403`
- `code = HONOR_CODE_REQUIRED`
- include current honor payload for client routing.

---

## 10) Gamertag rules (VS0)

- Length: 3–16
- Allowed chars: `[A-Za-z0-9_]`
- Normalize `handleNorm = lower(handle)`
- Enforce uniqueness by `UNIQUE(handle_norm)`
- Reserved examples: `admin`, `moderator`, `system`, `root`, `support`, `iduna`
- VS0 policy: once set, treat as permanent

---

## 11) Frontend behavior and aesthetic lock

### Funnel states
1. Login (Google OAuth)
2. Honor code acceptance
3. Gamertag selection + availability
4. Complete → redirect `/town`

### Aesthetic constraints
- Matte black + zinc palette
- Single accent color (cyan)
- Monospace typography
- Sparse layout / negative space
- Always-visible status/debug line
- No framework build pipeline for VS0

---

## 12) Recommended repository layout

```text
/cmd
  /eventstore-api
  /projector
  /replay
/internal
  /auth
  /eventstore
  /projectors
  /truestore
  /http
  /util
/migrations
  /eventstore
  /truestore
/public
  index.html
  app.js
```

---

## 13) Environment variables (minimum)

- `IDUNA_HTTP_ADDR=:8080`
- `IDUNA_MYSQL_DSN_EVENTSTORE=.../iduna_eventstore?...`
- `IDUNA_MYSQL_DSN_TRUESTORE=.../iduna_truestore?...`
- `IDUNA_JWT_SECRET=...` (if JWT mode)
- `IDUNA_BASE_URL=https://...`
- `GOOGLE_CLIENT_ID=...`
- `GOOGLE_CLIENT_SECRET=...`
- `IDUNA_PROJECTOR_BATCH=500`
- `IDUNA_PROJECTOR_POLL_MS=100`

---

## 14) Local development workflow

1. Start MySQL, create databases:
   - `iduna_eventstore`
   - `iduna_truestore`
2. Run migrations for both schemas.
3. Start projector service.
4. Start HTTP API service.
5. Serve `/public` frontend.
6. Validate end-to-end funnel.

---

## 15) Testing expectations

Unit tests:
- handle normalization + validation
- honor gate decision logic
- append concurrency mismatch behavior

Integration tests:
- `UserRegistered` projects into `users`
- `HonorCodeAccepted` transitions status/gate state
- `GamertagSet` enforces unique `handle_norm`

---

## 16) Security baseline

- Verify Google token signature, issuer, audience.
- Use PKCE for public clients in later phases (recommended).
- Rate-limit OAuth endpoints and write operations.
- Never log raw OAuth/JWT tokens.
- Retain minimal PII (Google `sub` is canonical external identity).

---

## 17) VS0 done definition

A user can:
- authenticate with Google,
- accept THE_HONOR_CODE,
- set a unique gamertag,
- and enter the world.

System guarantees:
- append-only event ledger,
- replayable projections,
- case-insensitive gamertag uniqueness,
- honor code version enforcement.

---

## 18) Post-VS0 roadmap

- Tournament lifecycle events and bracket projections.
- Follow graph + personalized activity feed.
- Team entrants and additional tournament formats.
- CDC/binlog streaming option when polling is insufficient.
- Moderator/report workflows and gamertag governance.
