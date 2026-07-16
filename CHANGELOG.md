# IDUNA Changelog

## 2026-07-16
- feat(apples): S147-02/03/05 — new `PATCH /api/v1/apples/{id}` enrichment endpoint (closed field set: `gpt2_fingerprint`, `model_fingerprint`, `astrology`; `apples.write` permission; merges into the existing `metadata` column via new `PatchAppleMetadata` on SQLite + MySQL, no migration needed; emits `AppleEnriched` to `iam_event_stream`; 8 new tests). Also fixed a real concurrency bug found while verifying this live: `syncAppleToGit` raced concurrent Apple creation with no retry on push rejection — root-caused a live data-integrity gap where 9226 of 9908 Apples were missing from the APPLES git mirror (backfilled separately, `APPLES` commit `699bdd5`); added `gitSyncMu` + `gitPushWithRetry` (pull --rebase, retry once). Apple #9910, commit `c9217df`.
- docs: VS0–VS13 documentation archaeology — archived the fourteen founder-written KIKORYU founding specs verbatim at `docs/archive/kikoryu-vs-original/` (recovered from `/home/fatbaby/vs0.md`…`vs13.md`); wrote `docs/VS_REALITY_AUDIT.md`, a code-verified SAGA-style (HQ-SPEC-DOC-102) reconciliation of each spec vs. the running system — findings: VS0 identity gate and much of VS1 are live-but-undocumented (device auth, honor code, gamertag, RBAC, event-sourced audit all shipped, absent from NORTHSTAR.md); VS7/VS13/VS12/VS6/VS5/VS4 were reincarnated elsewhere without citation (M2M agent model, mmo.go provenance_chain, DragonsNShit crafting, Stripe subscription rails, stream.go SSE, FATBABY/kgraph ingest); VS3/VS11 superseded by different realities; VS2/VS8/VS9/VS10 unbuilt. Wrote superseding docs in `docs/kikoryu/` (full rewrites for VS0/1/2/7/9/10, status stubs for the rest) oriented to the founder's new direction: social tournaments platform (VS2 primary, VS9+VS10 supporting). All 16 docs registered in EMILY golden-docs-index (VS-REALITY-AUDIT + KIKORYU-VS0…VS13).
- docs: intake `iduna_roadmap.md` (founder-provided, placed at repo-tree root outside any repo) as `docs/NORTHSTAR_KIKORYU.md` — 14-version (VS0–VS13) product roadmap for KIKORYU, the single-shard MMO consumer domain named alongside FATBABY/SECWATCH since IDUNA's original IAM pivot (`iam-spec.md` §1) but never previously given a build plan. Registered in EMILY's golden-docs-index at tier 1. Reformatted for markdown only; content preserved as given.
- fix(store): `RunSQLiteMigrations` translates each migration file's SQL via `mysqlToSQLite` before applying it, but the regexes converting `AUTO_INCREMENT PRIMARY KEY` columns only matched `BIGINT`, not `INTEGER` — `202606250002_mmo_inventory.sql` and `202606250003_monitors.sql` both declare `id INTEGER ... AUTO_INCREMENT PRIMARY KEY`, which translated to invalid SQLite (`AUTOINCREMENT` before `PRIMARY KEY`). Widened `reBigintAutoIncrementPK`/`reBigintAutoIncrementOnly` to match `BIGINT|INTEGER`.
- ops: recovered `var/iduna.db` from a partial application of `202606250002_mmo_inventory.sql` — the 2026-07-16 reboot hard-killed iduna.service mid-migration (no per-statement transaction in `RunSQLiteMigrations`), leaving `items.def_id`/`items.flags` and `character_equipment` applied but unrecorded in `schema_migrations`, so every restart retried from statement 1 and hit `duplicate column name: def_id`. Manually applied the remaining `character_inventory`/`character_key_items`/`character_bag_capacity` tables (matching real `mysqlToSQLite` output) and recorded the migration.

## 2026-07-15

- fix(ops): `scripts/iduna.service` gains an `ExecStartPost` health-check loop (polls `/health` up to 30s) — `Type=simple` previously only guaranteed the process forked, not that the HTTP listener was accepting connections, so `emily-system.service`'s `After=iduna.service` ordering didn't actually mean "IDUNA is ready"

## 2026-06-27
- S138-06: /api/v1/kgraph/query proxy handler (KGraphHandler, KGRAPH_URL); wired with RequireAuth
- S137-03: research_cache table (202606270002) + /api/v1/research/cache CRUD (ResearchHandler)
- S136-02/03: vendors + supply_orders tables (202606270001); /api/v1/supply/ CRUD handler (SupplyHandler)

- S129-05: GET /api/v1/characters/:id/inventory + /equipment endpoints; 4 tests


## 2026-06-25
- feat(monitors): granular RBAC (monitors.read/create/delete/alert/admin), monitor kinds (heartbeat/cron/deadman), GET/:id PATCH/:id POST/:id/recover endpoints, EMILY-PRIME gains monitors.read+create+alert — all tests pass
- Alerting backend: check-in monitors (unique URLs, configurable timeout, site-down Slack+email alerts); monitors migration, IAMStore methods, MonitorsHandler
- migration 202606250002: character_equipment, character_inventory, character_key_items, character_bag_capacity tables; ALTER items ADD def_id + flags

- feat: S128-04 cluster heartbeat — POST /api/v1/agents/heartbeat, GET ?active=true&type=emily_cluster, migration + store impl (Apple #3863)


## 2026-06-24
- feat: S125-05 GET /api/v1/players/{slug}/profile — job+faction_rep+trapx_activity (Apple #3658)
- feat: S127-05 GET/PATCH /api/v1/fieldoffices — in-memory FO snapshot store for district overlay (Apple #3651)
- feat: S126-10 GET /api/v1/players/{slug}/profile — PlayerProfileHandler, display_name/job/fame/last_scene/apples_count, 6 tests (Apple #3554)
- feat: S126-09 per-IP rate limit on auth endpoints — IPRateLimiter 10 req/min, /auth/local + /auth/register wrapped, 429+Retry-After (Apple #3552)
- feat: S126-08 POST /api/v1/auth/refresh — JWT refresh endpoint, RefreshHandler, 7 tests (Apple #3550)
- feat: S125-01 POST /api/v1/auth/register — open GFD registration, free_trial tier, JWT response (Apple #3504)
- feat: S124-02 subscription_tiers migration, GFDTier struct, ListSubscriptionTiers/GetGFDUserTier/SetGFDUserTier/RecordStripeEvent IAMStore methods, /tiers + /stripe webhook handlers (Apple #3497)

## 2026-06-23
- feat: S76-06 PATCH /api/v1/characters/:id/skills (UPSERT skill value, cap 110); GET /api/v1/characters/:id/skills (list all skills)
- feat: S76-04 GET /api/v1/characters/:id/items (list non-destroyed items by owner)
- feat: S76-03 PATCH /api/v1/characters/:id/gold — atomic conditional gold deduction; 409 on insufficient balance

- feat: S75-01 MMO schema (characters/items/guilds/world_events/scene_state migration); S75-02/03/04/05 MMO API handlers (characters CRUD+position, items provenance, guilds, world events); wired into main.go with RequireAuth


## 2026-06-21
- test: S66-01 drive.Client test suite (Apple #2404)
- test: S62-01 auth.Subscription.IsActive() 7-case test suite (Apple #2395)
- test: S56-02 subscriptions handler test suite — 5 tests (Apple #2382)
- test: S56-01 push_tokens handler test suite — 5 tests (Apple #2380)
- test: S53-02 intelligence handler test suite — 4 tests (Apple #2367)
- test: S53-01 HEIMDAL handler test suite — 5 tests (Apple #2365)
- feat: S48-01 GET /api/v1/players leaderboard endpoint (Apple #2338)

- feat: S45-01 POST /api/v1/players/{id}/session stat update endpoint (Apple #2308)


## 2026-06-20
- feat: S43-05 email+password SHANKPIT player auth POST /api/v1/auth/email/{register,login} (Apple #1893)
- feat: S43-03 SHANKPIT Google OAuth flow /api/v1/auth/google/shankpit (Apple #1890)
- feat: S43-02 SHANKPIT player registry — POST/GET /api/v1/players/{register,{id}} (Apple #1888)
- feat: S44-06 GET /api/v1/stream/user-events SSE stream endpoint for Colab (Apple #1882)

- feat: S44-04 GET /api/v1/agents + distributed Emily cluster registry (Apple #1877)


## 2026-06-18
- feat: OpenAPI spec + Python einhorn_sdk + Colab observability (Apple #1446)

- feat: webmaster uid=0, user CRUD, event log + SQLite/MySQL projectors (Apple #1445)


## 2026-06-16

- ApplesHandler: auto-sync every Apple to APPLES git repo via APPLES_GIT_DIR goroutine (Apple #585)


## 2026-06-14
- feat(apples): GET /api/v1/apples/stats/daily-tokens?days=N — daily aggregate token stats from Apple metadata; DailyTokenStat type in auth/types.go; DailyTokenStats store method (SQLite + MySQL); max 90 days; zero-pads missing days; requires apples.read — unblocks MJOLNIR token spend sparkline (M4 complete)
- feat(subscriptions): Emily+ subscription gate (S23-04) — user_subscriptions table (migration 202606140002), UpsertUserSubscription + GetUserSubscription store methods, SubscriptionHandler (/api/v1/subscriptions POST + /me GET), GetEffectivePermissions now appends cap.query.full for active subscribers, EDIS-WOOCOMMERCE agent registered (subscriptions.admin)
- feat(drive): Google Drive API integration — internal/drive/client.go (stdlib-only service account auth: RS256 JWT → Bearer token → Drive v3 REST), DriveHandler (/api/v1/drive/upload, /api/v1/drive/files, /api/v1/drive/files/{id}); drive.write + drive.read permissions; degraded-mode 503 when GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON not set
- feat(agents): EMILY-TRAINING agent registered (drive.write, drive.read, apples.write/read) — drives GPT-2 fine-tuning pipeline
- migration: 202606140001_drive_sync_log.sql — Drive sync audit table
- feat(env): GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON + GOOGLE_DRIVE_FOLDER_ID env vars wired into main.go

## 2026-06-03

### Documentation rereview — IAM/API alignment

- Rewrote `openapi.yaml` around the implemented IAM surface: Google ID token exchange, agent M2M exchange, JWKS, `/api/v1/identities/me`, Apples, and Back Office entry points.
- Refreshed `README.md` into a current project overview and documentation index.
- Marked the IAM and Apples implementation checklists complete in repository, with live Apple publication called out as a deployment-time verification step.

### Bootstrap: config-as-code agent provisioning

**Problem:** No way to bring IDUNA online without manually setting up agent permissions in the admin UI. IDUNA needs MySQL → Bob needs IDUNA → classic chicken-and-egg.

**Solution:** `cmd/bootstrap` — a narrow, one-shot CLI tool (no LLM, no HTTP server) that:
1. Runs all pending DB migrations
2. Seeds agent permissions from `config/agents.json`
3. Generates API key secrets for any agents not yet provisioned
4. Writes secrets to `var/agent-secrets.env`

**`config/agents.json`** — declarative, git-committed definition of all system agents (EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB) and their minimum-necessary permissions. Edit + re-run bootstrap to change an agent's authority. No admin UI required.

**`migrations/truestore/202606030001_system_seeds.sql`** — new migration seeding:
- System owner user (`system@einhorn.internal`) for agent FK constraint
- System agent stubs with fixed deterministic UUIDs
- New agent-scoped permissions: `fatbaby.operator`, `emily-prime.operator`, `emiree.super`, `bob.db.admin`, `signalapi.read`, `jon.setups.write`

**Startup sequence** (documented in README):
```
go run ./cmd/bootstrap   # migrate + seed + generate secrets
source var/agent-secrets.env
go run .                  # start IDUNA
go run ./cmd/bob-agent    # Bob comes online
# then: start FATBABY-EMILY, JON, EMILY-PRIME with their IDUNA credentials
```

**`var/agent-secrets.env`** is git-ignored. Each agent's env var is `IDUNA_SECRET_<AGENTNAME>`.

Bootstrap is idempotent: safe to re-run on every deploy. Pass `-rotate` to regenerate all secrets.

## 2026-06-02

### HQ-SPEC-IAM-096 — Apples: Golden Documentation Log Streaming

Apples are structured records emitted by agents at the end of each recursive
self-improvement run. They form the paper trail that closes the RSI loop.

**Database**
- Migration `202606020001_apples.sql`: `apples` table (append-only, FK to agents)
- Permissions seed: `apples.write`, `apples.read`, `apples.admin`
- super_admin and analyst role assignments

**Store**
- `auth.AppleRecord` type added to `internal/auth/agent.go`
- `IAMStore` interface: `AppendApple`, `ListApples`, `GetApple`
- `MySQLStore` implementation: `AppendApple` runs in a transaction that also
  emits `ApplePublished` to `iam_event_stream`

**API**
- `POST /api/v1/apples` — submit a new Apple (requires `apples.write`)
- `GET  /api/v1/apples` — list Apples with filters (requires `apples.read`)
- `GET  /api/v1/apples/{id}` — full Apple with body and metadata (requires `apples.read`)
- Auth: Bearer JWT from existing M2M agent auth flow

**Admin UI (Back Office)**
- `/admin/apples` — filterable ledger (source_repo, agent_id, apple_type)
- `/admin/apples/{id}` — full detail view: body as preformatted text, metadata JSON block
- Nav bar updated with Apples link

**Tests**
- 9 handler tests covering: success create, missing permission, missing fields,
  no auth, list, filter by repo, get by id, not found, apples.admin permission

---

## 2026-06-01

### HQ-SPEC-IAM-095 — Agent M2M credential authentication

- `POST /api/v1/auth/agent` credential exchange endpoint
- Migration: `api_key_hash` column on agents table
- `SetAgentCredential` / `AuthenticateAgent` store methods
- `/api/v1/jwks` endpoint

### Back Office admin UI

- `/admin` overview, `/admin/users`, `/admin/agents`, `/admin/audit`
- IAM events audit log viewer
