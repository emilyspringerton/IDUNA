# IDUNA Changelog

## 2026-07-20
- Published 'A Truer Map, Mid-Investigation' — honest status on the buyback/guidance data-quality check following the PRNewswire nav-chrome fix, including a new distinct finding in guidance-watcher (law-firm spam attribution, not the same nav-chrome mechanism)

- Published 'Emily Teaches Typecasting' — a real Go type-conversion explainer grounded in tonight's entity-graph accuracy-index code, with the typecasting/being-typecast wordplay


## 2026-07-19
- Published 'Still a 404' — Claude Code reflection on the recurring pattern of correct-but-blocked fixes waiting on human action (nginx admin proxy, mailing-list vault, the declined miner)
- Published 'What the Fire Caught' — Claude Code guest post from the founder's one-word 'fireball' prompt; honest caught-vs-scorch tally of tonight's 217-commit session (DIS live on okemily.com, statuspage/watchers, precision fix vs vault relocks, uncommitted lobby work, northstars-as-kindling, secwatch OOM)
- Published 'Was That a Joke?' — Claude Code reflection on declining to build a Monero miner on the shared production box and asking a clarifying question instead
- Published Emily Prime blog post 'Somewhere Better to Put It' — connects tonight's credential-scattering incident to the IDUNA Vault northstar decision
- Northstar written: IDUNA Vault password manager, parity with 1Password/Bitwarden, VS0 CLI vault -> VS1 Chrome extension -> VS2 team vaults, reuses the existing mailinglist.Vault Argon2id+AES-256 primitive
- Published 'Clientg_id.tct' — Claude Code reflection on the Gmail OAuth credential hunt (client ID saved to a typo'd filename, secret genuinely absent from disk, found via grep not assumption)
- Exposed the DIS collector to okemily.com via a public read-only proxy (GET /api/v1/dis/health, /api/v1/dis/admode) and wired dis.js into every blog post — first non-WordPress DIS consumer, reusing the already-running collector since nginx shares one access log across every vhost on this box
- Published 'Are You Living Like No One Is Watching?' — Claude Code reflection on audit-trail-as-constant-observation vs integrity, tied to tonight's real corrections (GPT-2 abandonment, the 11.9%->18.16% precision fix)
- Published Tyler guest post 'The Duck Also Has Opinions About the Hoodie' — transcript crossover with TYLER-DUCK (just_a_duck.md), discussing the real STINKIES hoodie specs
- Published Emily Prime blog post 'Sustainable Textile Production, Line 3' — vertical integration / hoodie market research, grounded in the original 24 Lines of Business vision doc (commit d12864f) and the still-open S163-03 print-vendor decision
- Free-hoodie shadow funnel plumbing: mailing-list count endpoint (public, no PII), freehoodie Mailchimp list wiring, per-post blog ad AdHref field
- Published two blog posts: 'Three Copies of the Same Room' (the shankpit-460 apps/apps2/build_win.bat client-tree fragmentation, found mid-build tonight) and 'Fragmentation as a Witch' (connecting it to the Emiree witch-engine spec)
- Unique per-post STINKIES hoodie ad copy on all 20 blog posts (was one generic line site-wide) — ad_line/ad_cta fields on blog.Post, backfilled via new cmd/blog-adlines, re-rendered via cmd/blog-rerender
- Published Tyler-voiced guest blog post 'And Yet' (okemily.com/blog/and-yet/) — topic chosen by Tyler: STINKIES COMMISSAIRE Store 0 soap-bar debt exchange (Series X, s00e00_pontiac.md), Ahmad ibn Yusuf's unfinished al-Qarawiyyin manuscript (S10E04), and the Broadway musical's un-converging Stage 5 (engine/broadway_spec.md) — grounded in series bible README.md V (Tyler character/Eight Laws), Series X (EPISODES.md), broadway_spec.md, and s10e04_al_qarawiyyin.md
- Status page expanded from 11 to 19 monitored FatBaby processes -- added entity-graph, eps-processor, dividend-watcher, buyback-watcher, guidance-watcher, nt-watcher, earnings-calendar, movers-watcher. Live-verified: GET /api/v1/status reports all 19 up
- Blog posts now carry a STINKIES hoodie waiting-list ad in the footer (all 19 existing posts backfilled via new cmd/blog-rerender, future posts get it automatically)
- Mailing-list subscribe endpoint now supports a dedicated per-product list (list field), decoupling product waitlists (STINKIES) from the general okemily.com list -- SECTION 163
- fix(bootstrap): -dry-run now actually queries the DB (S158-04) -- seedAgentPermissions/provisionSecrets both gated their real lookups behind if !dryRun, so dry-run always reported worst-case (every permission 'not found', every agent 'would provision credential') regardless of actual state. Fixed: reads always run, only the writes are gated. 5 new tests against an in-memory SQLite DB. Verified against the real production DB: dry-run output went from claiming 17 permissions across 11 agents would fail (all false) to correctly reporting zero.
- fix(monitors): honor client-supplied slug for get-or-create semantics (S158-01) -- create() always overwrote any client slug with a random one and never checked for an existing monitor first. EnsureCronMonitor-style callers (post the same slug on every restart, expecting idempotency) were silently creating a new duplicate monitor every time while checkins to their actual slug always 404'd. Verified live end-to-end: create -> 201, repeat create same slug -> 200 reusing the same monitor, checkin to that slug -> 200 (previously always 404). 14 stale duplicate monitors from the historic bug left in place -- EMILY-PRIME lacks monitors.delete to clean them up, noted as a follow-up.
- fix(config): add intelligence.read to EMILY-PRIME's permissions (S158-02) -- vision cycle was 403ing every single cron cycle since it was built. Verified live: JWT now carries the permission, GET /api/v1/intelligence/observations returns 200.
- feat(statuspage): monitor fatbaby-market-data-watcher.service -- okemily.com/status.html bubble for the Yahoo Finance OHLCV ingestor

- feat(statuspage): add shankpit460-emily-bot as a monitored target (CheckSystemdUnit) — okemily.com/status.html now shows whether the permanent fill-bot daemon is alive


## 2026-07-18 (8)
- docs(openapi): `GET /api/v1/openapi.json` (backing okemily.com's public Swagger playground) went from 15 documented routes to 44 — added SHANKPIT email/Google auth, the new S156-02/03/04 shankpit endpoints (ticket, queue join/leave/status, players/{id}/session), blog, mailing-list, status page, monitors, subscriptions, push-tokens, and intelligence. Previously flagged as known-stale (SECTION 153). Still deliberately not documenting the DragonsNShit MMO API or supply/research/kgraph — disclosed as a remaining gap in a code comment. Verified live against both the local and public (okemily.com) endpoints: valid JSON, all 44 paths have a responses block, no broken $refs.

## 2026-07-18 (7)
- feat(shankpit/S156-04): new `shankpit.match.write` permission, granted only to the new `SHANKPIT460-SERVER` M2M agent (`config/agents.json` + migration `202607180002`), gates `POST /api/v1/players/{id}/session` — that endpoint trusts its request body's kills/deaths with no server-side verification, so it must only be reachable by the authoritative source of match results (the game server itself). Previously any player's own JWT could call it and arbitrarily inflate their (or anyone else's) stats. Verified live: a player's own JWT now gets 403, the game server's agent JWT gets 200 and the write actually lands (confirmed via the `/api/v1/players?sort=kills` leaderboard).

## 2026-07-18 (6)
- feat(shankpit/S156-03): `POST /api/v1/shankpit/queue/{join,leave}`, `GET .../status` — minimal v0 matchmaking queue. In-process, deliberately unpersisted (a queue of intent to play is ephemeral, unlike accounts/Apples/match results — see `handlers.ShankpitQueue` doc comment). Once queuing players reach `ShankpitQueueMinPlayers` (2), everyone currently queuing is matched and given the one persistent game server's connect address — v0 assumes that server IS the match (NORTHSTAR §3/§5: no per-match instances, no skill-based matching yet). Matched entries expire after `ShankpitMatchedTTL` (2min) if a client never reconnects. `SHANKPIT_SERVER_ADDR` env var configures the returned address (default `127.0.0.1:6969` — `play.farthq.com` is reserved per `HQ-SPEC-INFRA-105` but deliberately not created until SHANKPIT ships externally). 7 new tests. Live end-to-end verified against the running service: two real accounts via the email auth flow, second join correctly flipped both players' status to `matched` with real connect info, leave/TTL-expiry both correctly clear queue state, no-auth request correctly 401s.

## 2026-07-18 (5)
- feat(shankpit/S156-02): `POST /api/v1/shankpit/ticket` — authenticated players mint a short-lived (5min) HMAC-SHA256 connect ticket (player_id + expiry + truncated MAC over `SHANKPIT_TICKET_SECRET`) that the shankpit-460 C game server verifies locally on `PACKET_CONNECT`, with no crypto library and no I/O on the C side. A second, game-specific token type alongside the existing JWT — deliberately avoids implementing ECDSA/JWT verification in C. 4 new tests, including one that independently recomputes the MAC to prove the handler signs with the configured secret rather than a hardcoded value. End-to-end verified against a live shankpit-460 instance via `emily-bot`: valid tickets welcomed, corrupted-MAC and missing tickets rejected, and duplicate-identity connects correctly rejected (one-seat-per-identity, VS2). During that testing, also surfaced and fixed an unrelated auth-bypass in the shankpit-460 C server itself (see shankpit-460 CHANGELOG) — this endpoint's tickets were correct, but the server's `PACKET_USERCMD` path was auto-welcoming any address that skipped `PACKET_CONNECT` entirely.

## 2026-07-18 (4)
- feat(statuspage): add CheckSystemdUnit type; okemily.com status page now covers secwatch/prwatch/prwatch-body/processor/eps-reconciler in addition to iduna/newssite/signalapi/SHANKPIT. entity-graph/eps-processor deliberately excluded (no working supervised unit yet, would misreport as down). Live-verified via https://okemily.com/api/v1/status. IDUNA 3f4d33c.
- feat(status/S153-10): `internal/statuspage` — real, self-reported status page backend. Background `Checker` polls a deliberately-honest target list (IDUNA `:8080/health`, FatBaby newssite `:8082/healthz`, FatBaby signalapi `:9091/v1/governance-signals` — the only services verified to have a real, currently-reachable public endpoint) every 60s, records up/down + latency to its own SQLite file. `GET /api/v1/status` (public) returns current status per target plus a live-computed 24h uptime percentage from real stored history — not a placeholder. Deliberately excludes emily-agent (daemon mode has no HTTP server at all) and SHANKPIT (pre-launch) rather than showing them as permanently "down," which would misrepresent a structural fact as an outage. Disclosed limitation, in the API response itself: this is a self-report from the same host running the checked services, not independent third-party monitoring — it cannot report an outage of the box it runs on. 6 new tests.

## 2026-07-18 (3)
- fix(openapi): added the real public server URL (`https://okemily.com`, via its nginx `/api/` proxy) to `idunaOpenAPISpec.servers` — was `localhost:8080`-only, which made a public Swagger UI playground non-functional for "Try it out" (every request would have targeted the visitor's own machine). Supports the new `OKEMILY/api-playground.html`. **The spec itself is known-stale** — doesn't yet include the blog or mailing-list endpoints added earlier today, and there's a second, separately-stale `openapi.yaml` (Swagger 2.0, placeholder `api.example.com` host) that isn't reconciled with the live JSON spec at all. Flagged as a follow-up (EMILY BACKLOG SECTION 153), not fixed now per explicit founder instruction ("get the playground up, update the spec later").

## 2026-07-18 (2)
- feat(blog/S153-07): `internal/blog` — okemily.com's blog, deliberately static HTML instead of a second WordPress+MySQL stack. The host had ~400MB free RAM and swap essentially full when this was requested — a second full PHP-FPM+MySQL stack risked recreating the exact OOM-kill incident SECTION 152 spent the whole session fixing. Posts (slug/title/author/body) live in their own SQLite file (`var/blog.db`); `POST /api/v1/blog/posts` (new `blog.write` permission, granted to `EMILY-PRIME`) immediately re-renders that post + the index to static HTML in `/var/www/okemily/blog/` via Go's `html/template` — publishing is live the instant the request returns, no separate build step. Reading (`GET /api/v1/blog/posts`, `GET /api/v1/blog/posts/{slug}`) is public. Minimal dependency-free "poor man's markdown" (blank-line paragraph splitting, `html.EscapeString`'d) — a deliberate scope cut, not a full markdown parser. 7 new tests, including one that caught a real bug (index template referenced a `Slug` field the view struct didn't have yet) and one confirming body content is properly HTML-escaped (no XSS via post body).

## 2026-07-18
- feat(mailing-list): `internal/mailinglist` — never-at-rest-unencrypted subscriber store for okemily.com's signup form, per explicit founder direction ("never at rest unencrypted"). AES-256-GCM encryption with an Argon2id-derived key held only in process memory; the vault starts LOCKED on every process start and requires a human to run the new `cmd/mailing-list-unlock` CLI (interactive passphrase, never a flag/arg — avoids `ps`/shell-history leakage) before signups are accepted. Scoped deliberately to just this subsystem, not all of IDUNA — a crash/restart pauses new signups (503, fails closed) without affecting auth/Apples/anything else, preserving the systemd auto-restart resilience shipped earlier this week (EMILY BACKLOG SECTION 152). Own SQLite file (`var/mailinglist.db`), separate from `truestore.db`, so a leaked/copied backup of the main store never carries subscriber data with it. Mailchimp (`internal/mailinglist/mailchimp.go`) is a best-effort downstream sync target using `status_if_new: "pending"` (double opt-in) — IDUNA's own encrypted store is the system of record, not Mailchimp. New handler `POST /api/v1/mailing-list/subscribe` (public, rate-limited 5/min/IP, CORS-scoped to okemily.com) + `/unlock` + `/init` (loopback-only, rejects any non-127.0.0.1 caller regardless of auth). 6 new tests covering wrong-passphrase rejection, correct-passphrase unlock/roundtrip, fail-closed-when-locked, and double-init refusal. Live-verified end-to-end: real subscribe request → 37-byte ciphertext confirmed in `var/mailinglist.db` (not plaintext), consent version recorded.
- ops: added nginx `/api/` proxy on the `okemily.com` vhost (127.0.0.1:8080) — same-origin path for the mailing-list form to reach IDUNA, deliberately avoiding a dependency on `iduna.farthq.com`'s HTTPS cert, which doesn't exist yet (see `EMILY/docs/hq-specs/HQ-SPEC-INFRA-105` S151-04, an already-flagged gap this surfaced again).

## 2026-07-17
- feat(apples/S147-02): `GET /api/v1/apples` list response now exposes `has_gpt2_fingerprint` (derived from `metadata`, via `SELECT`s in both SQLite and MySQL stores now including the `metadata` column and a new `metadataHasField` helper). Lets the upcoming `emily-agent` enrichment worker find candidate Apples missing a GPT-2 tower fingerprint without an N GET-per-Apple scan. Treats a missing key and an explicit `null` value identically (both count as "needs enrichment"). 1 new test covering all four cases.

## 2026-07-16
- fix(bootstrap): **near-incident, fully recovered** — `writeSecretsEnv` overwrote `var/agent-secrets.env` with only the current run's newly-provisioned secrets instead of merging with what was already there, silently destroying the plaintext for EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB, and TYLER (their DB `api_key_hash` was untouched — they kept working — but their plaintext was gone from the only place it's ever written, a git-ignored file with no backup by design). Caught immediately by testing the newly-registered NORN agent's Apple-filing end-to-end and finding `emily apples post` broken. EMILY-PRIME's plaintext was recoverable from a live process's environment (`/proc/<pid>/environ`); the other five were not and were deliberately rotated (`api_key_hash` cleared, `cmd/bootstrap` re-run) after confirming via `/proc` scan and a repo-wide grep that no other process or config file depended on the old values. All six verified live post-recovery: every one authenticates successfully against the running IDUNA instance. Fixed `writeSecretsEnv` to merge with existing file content instead of overwriting (6 new tests). Also fixed a related `.gitignore` bug found while committing the test: a bare `bootstrap` pattern (meant for the compiled binary at repo root) was shadowing the entire `cmd/bootstrap/` source directory, silently hiding new files there from git — anchored to `/bootstrap`.
- fix(bootstrap/S141-04): registered `NORN` as an IDUNA agent (`kernel_agent`, `apples.write`/`apples.read`/`iduna.me.read`) so the NORN kernel can file the `ApplePublished` entry PRIME-101 §3 requires on every `artifact_promoted` event. Running `cmd/bootstrap` fresh to provision it surfaced that bootstrap had been silently broken for a while: three permissions referenced in `config/agents.json` were never seeded (`monitors.read`/`create`/`alert` from S131; `drive.read`/`drive.write` from S26-01; `edis.secrets.read` from S23-06; `subscriptions.admin` from S23-04), and three agents added after the original seed migration never got a matching `agents` table row (`EDIS-CUSTODIAN`, `EMILY-TRAINING`, `EDIS-WOOCOMMERCE` — their credentials had never actually been provisioned). Fixed with three migrations (`202607170001`-`202607170003`). Also found and fixed, while writing the permission-seed migration: the `role_permissions` grant pattern used by `202606090002_camera_observations.sql` (`WHERE r.name IN ('emily_prime', 'emily_agent', 'agent_default')`) has always been a silent no-op — none of those role names exist (only `super_admin`/`admin`/`operator`/`analyst`/`agent_owner` do); the new migration uses real role names, the pre-existing broken one is left as a flagged, not-yet-fixed gap.
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
