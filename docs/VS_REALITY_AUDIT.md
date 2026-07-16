# VS Reality Audit — KIKORYU VS0–VS13 vs. the Running System

*2026-07-16. Author: Claude Code, at founder direction.*

This is a hand-run three-way reconciliation in the spirit of SAGA
(HQ-SPEC-DOC-102 §2–3, `EMILY/docs/hq-specs/`): **intent** (the fourteen
founding specs, now archived verbatim at `docs/archive/kikoryu-vs-original/`)
vs. **claim ledger** (what IDUNA's current docs say — `docs/NORTHSTAR.md`,
`docs/iam-spec.md`) vs. **reality** (the code actually running on :8080).
The SAGA tooling (claim IDs, `saga.manifest.yaml`, the queues) is not built
yet; this document applies its thinking manually. Every "live" claim below
cites the exact file/route verified. Where a search found nothing, that is
stated plainly.

**Method.** All fourteen specs read in full. Repo-wide `grep`/`find` across
`/home/fatbaby/IDUNA` (all Go, SQL, HTML/JS/CSS), route table read from
`main.go`, migrations read from `migrations/truestore/`, cross-checked against
`EMILY/BACKLOG.md` (S23, S75, S127–S130, S143) and sibling-repo northstars
where a spec's concept migrated out of IDUNA.

## Status legend

- **live-but-undocumented** — running code, zero presence in NORTHSTAR.md /
  CLAUDE.md (dark matter, DOC-102 §1 failure mode 3)
- **reincarnated-elsewhere** — the concept shipped under a different name /
  for a different product, with no paper trail back to the spec
- **superseded-by-different-reality** — the system now does something
  structurally incompatible with the spec's premise
- **not-yet-built-still-relevant** — nothing found; genuinely open and
  relevant to the tournaments-platform direction

## Headline

| Status | Count | Specs |
|---|---|---|
| live-but-undocumented | 2 | VS0, VS1 (partial) |
| reincarnated-elsewhere | 6 | VS4, VS5, VS6, VS7, VS12, VS13 |
| superseded-by-different-reality | 2 | VS3, VS11 |
| not-yet-built-still-relevant | 4 | **VS2 (primary)**, VS8, VS9, VS10 |

The founder's new direction — **a social tournaments platform** — makes VS2
the near-term product thrust with VS9 (reputation) and VS10 (scoreboards) as
its supporting layers. Verdict per spec and the superseding doc for each are
below; the rewrites live in `docs/kikoryu/`.

---

## VS0 — The Ritual Gate · **live-but-undocumented**

**Spec claimed:** Google OAuth → versioned THE_HONOR_CODE acceptance ("We
Agree", rose-gold consent metal) → permanent gamertag lock-in → device-auth
bridge for game clients; ANON→HONOR→HANDLE→READY state machine on `/me`;
gold/rose-gold "front office" aesthetic.

**Reality (verified):** Most of this is running, none of it documented.
- Device-auth bridge is fully wired: `internal/http/handlers/device.go`
  registers `/auth/device/start`, `/auth/device/poll`, `/auth/token/exchange`,
  `/device`, `/device/confirm` (registered at `main.go:140` via
  `deviceH.Register(mux)`), with per-IP `WindowRateLimiter`s on start/confirm.
- Honor-code and gamertag gating is server-enforced at token exchange:
  `internal/auth/device/service.go` returns `ErrHonorCodeRequired` /
  `ErrHandleRequired` / `ErrAccountSuspended` when `!usr.HonorAccepted` or the
  handle is empty (service.go:197–201).
- Schema shipped: `migrations/truestore/202602220001_device_auth.sql`
  (`device_auth_requests`, `exchange_codes`, and an `event_store` table) and
  `202602220002_iam_rbac.sql` (`users.gamertag` UNIQUE,
  `honor_accepted_current`, `honor_code_sha`, `honor_code_version`,
  `honor_code_text`). `auth.User` carries `HonorAccepted`, `HonorCurrentSHA`,
  `HonorCurrentVer`, `HonorCurrentText` (`internal/auth/types.go`).
- The JWT carries a `gamertag` claim (`internal/http/handlers/auth.go:82`;
  echoed by `/api/v1/identities/me`, `me.go:72`).
- The ceremonial web front-end exists and is served: `index.html` ("Google
  OAuth → THE_HONOR_CODE → unique gamertag", honor screen + gamertag picker),
  `app.js` (client drives HONOR→HANDLE→READY status transitions), `styles.css`
  (`--gold: #b89b62`, `--consent-rose: #b76e79`, Cormorant Garamond) — served
  at `/` by `main.go:210–212`.

**Divergences:** `app.js` calls `/auth/google/start`, `/me`, `/me/handle` —
none of those paths are registered in `main.go` (the live identity endpoint is
`/api/v1/identities/me`), so the ceremony page's API bindings are stale/dormant
— **unverified whether the web ceremony completes end-to-end**. The explicit
four-state `state` field on `/me` was not found; gating is enforced as errors
at device exchange rather than as a queryable state machine. Gold hex drifted
from spec `#C6A75E` to `#b89b62`.

**Claim-ledger gap:** `docs/NORTHSTAR.md` (2026-06-13) mentions none of this —
no device auth, no honor code, no gamertag. Textbook dark matter.

**Superseded by:** `docs/kikoryu/VS0_IDENTITY_GATE.md` (full rewrite).

---

## VS1 — IAM & Moderation ("Back Office" / Aunt Sally) · **live-but-undocumented (partial)**

**Spec claimed:** email magic-link/OTP auth; session revocation (individual +
global); RBAC tiers user/moderator/admin; abuse rate-limiting; honor-code
re-acceptance on version bump; Aunt Sally admin console with user search,
suspension, handle management, audit-log viewer; event-sourced non-destructive
history.

**Reality (verified):** The administrative skeleton shipped, under different
shapes:
- RBAC is live: `roles`/`permissions`/`user_roles`/`role_permissions` +
  agent-scoped `agents`/`agent_permissions` (`202602220002_iam_rbac.sql`),
  enforced by `internal/http/middleware` permission checks.
- Event-sourced audit is live twice over: the `iam_event_stream` ledger (same
  migration; `RoleRevoked` etc. emitted by `internal/store/store.go`), and the
  `userlog` append-only event log with a `local_users` projection
  (`internal/userlog/`, `202606180001_local_users.sql`) — VS1's
  "non-destructive history" principle, genuinely implemented.
- Back Office admin UI is live at `/admin` (`internal/http/handlers/admin.go`,
  `admin_login.go`; role assign/revoke verbs at admin.go:115–121), requires
  `iduna.admin`.
- Suspension exists as data: `users.status` ENUM
  `ACTIVE|SUSPENDED|BANNED|PENDING`; device exchange refuses suspended users
  (`ErrAccountSuspended`).
- Email auth exists but as **email+password**, not magic links/OTP:
  `/api/v1/auth/local` (`local_auth.go`, bcrypt) and
  `/api/v1/auth/email/register|login` for SHANKPIT players
  (`player_email_auth.go`). Auth routes are rate-limited
  (`authRateLimit`, main.go:251).
- Honor re-acceptance on version bump: `honor_code_version`/`honor_code_sha`
  are stored per-user and checked (`HonorCurrentSHA` in store layer), so the
  data model supports it; a forced re-acceptance flow was not verified.

**Found nothing:** magic links / OTP; session revocation (JWTs are stateless;
`refresh.go` re-issues but nothing invalidates — no token denylist or session
store found); a `moderator` tier (roles found in seeds are admin/analyst-type,
no moderation-specific role or capability set).

**Claim-ledger gap:** NORTHSTAR.md documents the Back Office and RBAC in one
line each; the userlog/event-store layer, local auth, suspension semantics and
the missing-revocation gap appear nowhere.

**Superseded by:** `docs/kikoryu/VS1_IAM_MODERATION.md` (full rewrite; session
revocation and the moderator tier are called out as the open items the
tournaments platform will actually need).

---

## VS2 — Competitive Institutions (Poker Tournaments) · **not-yet-built-still-relevant — THE PRIMARY DIRECTION**

**Spec claimed:** play-money NLHE multi-table tournaments; tournament-isolated
chips (Model A, equal starting stacks, zero cash value); lifecycle
CREATED→REGISTERING→STARTING→IN_PROGRESS→FINAL_TABLE→COMPLETE; server-
authoritative cards/RNG; deterministic hand histories as events; single active
seat per account; gamertag as table identity; standings projected to VS10;
institutional (anti-Vegas) aesthetic.

**Reality:** **Found nothing.** `grep -rni "tournament|poker|holdem|
leaderboard|scoreboard|standings"` across all Go and SQL in IDUNA returns zero
hits. No tables, no handlers, no lifecycle code.

**Why still relevant:** This is the founder's declared near-term product
thrust — the social tournaments platform. Every prerequisite VS2 listed now
exists: stable identity + gamertag (VS0, live), RBAC + audit trail (VS1,
live), M2M game-server auth (live), event-store pattern (live in userlog and
device flows). The spec's constraints (closed non-redeemable economy,
tournament-isolated chips, server authority) survive review unchanged and are
carried forward.

**Superseded by:** `docs/kikoryu/VS2_TOURNAMENTS.md` (full rewrite — the
platform-thrust document; generalizes "poker tournaments" to "tournaments as
the institutional product," NLHE first).

---

## VS3 — Play Money Stock Market Game · **superseded-by-different-reality**

**Spec claimed:** paper-trading game, $100k virtual bankroll, daily-close
fills, 2–4 week seasons, leaderboards by % return, event-sourced
orders/positions, Aunt Sally governance of the tradable universe.

**Reality:** **Found nothing in IDUNA** (no orders, portfolios, seasons,
fills). The market-data energy went somewhere structurally different:
`PRRJECT_FATBABY` is a real SEC/PR financial **signal pipeline** (entity
graph, signalapi, event store — see `PRRJECT_FATBABY/CLAUDE.md`), i.e. the
company builds market *intelligence products*, not a market *game*. IDUNA's
only finance-adjacent surfaces are proxies to that world:
`/api/v1/kgraph/query` (kgraph.go, S138-06) and the research cache
(research.go, S137-03).

**Verdict:** The premise (a game economy fed by market data) was displaced by
a revenue-bearing data business. Not on the roadmap. A paper-trading game
could someday ride the tournaments platform as one more tournament *format* —
if reopened, it should be a VS2 format, not a standalone system.

**Superseded by:** `docs/kikoryu/VS3_MARKET_GAME.md` (status stub).

---

## VS4 — Archive Intake API Gateway · **reincarnated-elsewhere**

**Spec claimed:** ingest workers pulling upstream market/news data,
normalizers, canonical hashing/dedup, provenance on every item, cache-first
read-only API — to feed VS3.

**Reality:** No ingest workers, normalizers, OHLC or news tables in IDUNA
(grep: nothing). But the concept — "pull upstream data, normalize, store with
provenance, serve read-only" — is exactly what `PRRJECT_FATBABY` does for
SEC/PR feeds, and what the EINHORN INDEX knowledge graph does for crawled
entities (`EMILY/docs/NORTHSTAR_INDEX.md`; IDUNA proxies it at
`/api/v1/kgraph/query`, `internal/http/handlers/kgraph.go`). Same idea,
different consumer (Emily Prime intelligence instead of a market game), no
paper trail back to VS4.

**Superseded by:** `docs/kikoryu/VS4_ARCHIVE_INTAKE.md` (status stub).

---

## VS5 — "Follow My Trades" Export Streams · **reincarnated-elsewhere (mechanism only)**

**Spec claimed:** opt-in, sanitized, replayable public event streams of VS3
trades; export projector + outbox; cursor pagination + SSE; anti-sniping
delay; strict PII rules.

**Reality:** The *product* (trade tape) doesn't exist — VS3 doesn't exist. The
*mechanism* shipped: `GET /api/v1/stream/user-events`
(`internal/http/handlers/stream.go`) is an SSE stream over an append-only
event log with `from_seq` cursor replay — precisely VS5's replayable-stream
architecture, currently exporting `local_user.*` events for Colab notebook
consumers. No opt-in/visibility model, no sanitization layer, no delay.

**Why it matters:** a **tournament tape** (live hand-history/standings feed
for spectators, with delay) is a natural social feature of the tournaments
platform, and stream.go is its proven seed.

**Superseded by:** `docs/kikoryu/VS5_TRADE_TAPE.md` (status stub).

---

## VS6 — Dual/Multibox License (Stripe) · **reincarnated-elsewhere**

**Spec claimed:** Stripe Checkout + Billing Portal subscriptions granting
concurrent-session entitlements (DUALBOX/MULTIBOX_3/MULTIBOX_5); webhooks as
canonical truth; event-sourced license lifecycle; game-server enforcement
(`ERR_MULTIBOX_LIMIT`).

**Reality:** Stripe-backed subscription entitlements shipped — for different
products: `internal/http/handlers/subscriptions.go` serves
`POST /api/v1/subscriptions` (provision, `subscriptions.admin`),
`GET /api/v1/subscriptions/me`, `GET /api/v1/subscriptions/tiers` (GFD tiers),
and `POST /api/v1/subscriptions/stripe` — a **Stripe webhook handler**
(`GFD_STRIPE_WEBHOOK_SECRET`), exactly VS6's "webhooks as canonical truth"
rule. Backing migrations: `202606140002_user_subscriptions.sql` (Emily+,
S23-04 — note NORTHSTAR.md still lists this as *pending*; it is done per
`EMILY/BACKLOG.md` S23-04 `[x]`) and `202606240001_gfd_subscription_tiers.sql`.
No concurrency entitlements, no session counting, no multibox tiers.

**Verdict:** the billing rails VS6 demanded exist and are proven. Multibox
specifically is not on the roadmap; paid tournament entitlements (e.g. entry
passes, premium tiers) would reuse this exact machinery.

**Superseded by:** `docs/kikoryu/VS6_LICENSING.md` (status stub).

---

## VS7 — Governance & World Authority (Agents vs. Unagents) · **reincarnated-elsewhere — genuine architectural cousin, not a false cognate**

**Spec claimed:** two-class identity model — Unagents (default players) vs.
Agents (bounded-authority actors: GM_AGENT, MOD_AGENT, SYS_AGENT); least
authority, full auditability, instant revocability, visibility boundaries;
Agent Registry, authority console, governance log.

**Reality:** IDUNA's entire production auth model **is** this concept, grown
from the SYS_AGENT corner: `agents` are first-class identities distinct from
users, with an accountable human owner (`agents.owner_user_id`,
`202602220002_iam_rbac.sql`), **explicit direct permissions with no role
inheritance** (`agent_permissions`; per `CLAUDE.md`: "JWT with explicit
permissions[] (no role inheritance)") — that is least-authority, literally.
M2M exchange at `POST /api/v1/auth/agent` (`internal/http/handlers/auth.go:107`,
`internal/auth/agent.go`), registry at `/api/v1/agents` + `config/agents.json`
seeding, lifecycle statuses `ACTIVE|SUSPENDED|DECOMMISSIONED` (instant
revocability), and governance events `AgentCreated`/`AgentSuspended`/
`AuthorityActionExecuted` specified in `iam-spec.md` §4.2 with the
`iam_event_stream` ledger shipped. Every named agent (EMILY_PRIME,
EDIS-CUSTODIAN, bob-agent, `saga` when it lands) is a VS7 Agent in all but
vocabulary.

**What's genuinely absent:** the *human* authority classes (GM_AGENT/MOD_AGENT
for in-world game masters and moderators), visibility boundaries, and the
authority-actions console. The tournaments platform will need exactly these
(tournament directors are MOD_AGENT/GM_AGENT hybrids).

**Superseded by:** `docs/kikoryu/VS7_AGENT_AUTHORITY.md` (full rewrite —
reconciles the vocabularies and specs the missing human-authority classes as
tournament-director roles).

---

## VS8 — Auraborialis Reboot Protocol · **not-yet-built-still-relevant (doctrine reincarnated in SHANKPIT)**

**Spec claimed:** cyclical world resets via non-destructive re-projection;
permanent `cycle_id`; identity/entitlements/roles persist, world state and
standings reset; constitutional multi-agent authorization; prior cycles remain
queryable "halls of history."

**Reality:** **Nothing in IDUNA** — no cycle/epoch/continuity identifiers in
any schema or handler (grep hits were unrelated: `server_epoch` timestamp in
me.go, drive sync log). The *doctrine*, however, was independently restated as
SHANKPIT's **season lineage** (`SHANKPIT/docs2/NORTHSTAR.md` Layer 4: "when a
season ends and a world is rebooted, the world does not start from zero" —
snapshots, lineage chains, visible scars). Doc-level only there too, per that
northstar.

**Why still (moderately) relevant:** a tournaments platform runs **seasons**;
season close/archive/reset with identity persisting is Auraborialis in
miniature. VS10's `cycle_id` binding should be designed season-first so a
later full protocol slots in.

**Superseded by:** `docs/kikoryu/VS8_AURABORIALIS.md` (status stub).

---

## VS9 — Credence & Reputation Layer · **not-yet-built-still-relevant (supporting layer for tournaments)**

**Spec claimed:** bureaucratic institutional memory, not karma — Reliability
Index, Governance History, Market Integrity, Agent Credibility; Dossier
identity persisting across cycles; reputation derived only from events;
stability signals feeding VS7 moderation.

**Reality:** **No reputation/credence/dossier system found** (grep: zero hits
on reputation|credence|reliability-as-player-metric|dossier). Three genuine
fragments exist:
- `internal/http/handlers/monitors.go` + `auth.Monitor`
  (`202606250003/4_monitors.sql`) — a *reliability index for services*:
  heartbeat/cron/deadman check-ins, healthy/failing status, overdue
  computation, alerting. VS9's Reliability Index concept, applied to
  infrastructure instead of players.
- `player_fame` (read by `internal/http/handlers/player_profile.go:90` —
  Frequency/Bloc/Procurement fame for GFD players) — faction reputation as a
  queryable projection.
- The Apples ledger — a working "governance history" for *agents* (every
  meaningful action filed, queryable by actor).

**Why still relevant:** tournaments need it directly — abandon/disconnect
rates, single-seat enforcement history, conduct records feeding moderation.

**Superseded by:** `docs/kikoryu/VS9_REPUTATION.md` (full rewrite scoped to
the tournaments platform).

---

## VS10 — Scoreboard / Records Projection Layer · **not-yet-built-still-relevant (supporting layer for tournaments)**

**Spec claimed:** the "Recognized Narrative of Reality" — derived, immutable,
cycle-aware scoreboard projections (`scoreboard_snapshots`,
`leaderboard_entries`, `contexts`); corrections only via new events +
reprojection; Agent-published official snapshots; read-only public API.

**Reality:** **No scoreboard/leaderboard tables or handlers found** (grep:
zero). Fragments: `players.go` serves a public records projection for SHANKPIT
(`GET /api/v1/players/{id}` — kills, deaths, kd_ratio, sessions), and
`player_profile.go` enriches it (job, fame, apples_count). These are
per-player stat projections, not ranked, published, snapshot-versioned
scoreboards.

**Why still relevant:** standings are the visible product of a tournaments
platform — VS2's spec explicitly projects final placements into VS10. This is
the second supporting layer to build.

**Superseded by:** `docs/kikoryu/VS10_SCOREBOARDS.md` (full rewrite).

---

## VS11 — TYLER Cutscene Publishing API · **superseded-by-different-reality**

**Spec claimed:** episode-packet authoring/packaging/publishing pipeline —
immutable content-addressed packets (dialogue/camera/stage/audio tracks),
platform adapters (THE_CITY_OF_LIGHT, Unity, web), POWDERHORN sponsor blocks,
VS7-permissioned publishing.

**Reality:** TYLER became a different thing entirely: a TV-series bible +
episode scripts repo (`TYLER/`, 82 episodes through S10E10) with video
compilation via `MoneyPrinterTurbo` (flat-stream, not packet syndication) —
see `TYLER/README.md` and EMILY BACKLOG SECTION 140-adjacent builds. IDUNA's
sole trace: `202606070001_tyler_agent.sql` registers a `tyler-agent` with
`tyler.rsi.write` permission — TYLER is a VS7-style *agent* in IDUNA, not a
syndication API. No episode/packet/cutscene code anywhere in IDUNA (grep:
only that migration).

**Verdict:** not on the IDUNA roadmap; the packet-syndication idea belongs to
the TYLER repo's future if anywhere. (POWDERHORN_COFFEE_ROASTERS survives
independently as the STINKIES COMMISSAIRE coffee thread, EMILY BACKLOG
SECTION 140.)

**Superseded by:** `docs/kikoryu/VS11_TYLER_SYNDICATION.md` (status stub).

---

## VS12 — Crafting & Material Transformation · **reincarnated-elsewhere**

**Spec claimed:** deterministic material transformation ("nothing from
nowhere"), transformative/constructive/cosmetic crafting, lineage-checked
inputs (VS13), event vocabulary (`CraftingJobStarted`, `MaterialTransformed`,
`ArtifactCreated`…), institutional-workshop UI.

**Reality:** Crafting shipped — for **DragonsNShit** (the GoblinFoxDragon
MMO), not KIKORYU, with zero reference to VS12:
`POST /api/v1/items` in `internal/http/handlers/mmo.go` (header: "S75-02/03/
04/05 + S129-05: DragonsNShit MMO API handlers") — "craft item;
provenance_chain[0] set"; `character_skills` and the S129 inventory/equipment
layer (`202606250002_mmo_inventory.sql`,
`GoblinFoxDragon/docs2/INVENTORY_EQUIPMENT_NORTHSTAR.md`). Simpler than VS12:
no recipes, no deterministic input-consumption rule, no crafting events — an
item is created with a provenance seed, not transformed from consumed inputs.

**Superseded by:** `docs/kikoryu/VS12_CRAFTING.md` (status stub pointing at
the DragonsNShit lineage).

---

## VS13 — Source Material Lineage & Provenance · **reincarnated-elsewhere**

**Spec claimed:** the ontological rule — no artifact without origin; four
source types (Harvested/Dropped/Crafted/Issued); `MaterialSplit/Merged/
Consumed/Transformed` events; `materials` + `material_lineage` reference
chains; event store as the authority for the existence of matter.

**Reality:** Independently reinvented as `items.provenance_chain`
(`202606230001_mmo_schema.sql`: "Every item has a UUID and a full provenance
chain as JSON… array of {actor_id, action, at}"), maintained by mmo.go:
creation seeds `provenance_chain[0]`, `POST /api/v1/items/:id/transfer`
appends, `GET /api/v1/items/:id` returns the full chain, deletion is soft.
Built for DragonsNShit under S75-03 (EMILY BACKLOG SECTION 75, `[x]`), with
**zero documented connection to VS13**. Materially simpler: an in-row JSON
custody log, not a lineage graph — no split/merge, no source-type taxonomy,
no separate material events.

**Verdict:** the concept lives; its home is GoblinFoxDragon/DragonsNShit
(`GoblinFoxDragon/docs2/MMO_NORTHSTAR.md` names item provenance as a core
system). Not a tournaments-platform concern.

**Superseded by:** `docs/kikoryu/VS13_MATERIAL_LINEAGE.md` (status stub).

---

## Cross-cutting findings

1. **NORTHSTAR.md is itself divergent.** Dated 2026-06-13, it omits the whole
   VS0 identity-gate surface, the player/MMO/monitor/supply/research/kgraph
   handler families, and still lists S23-04 subscriptions as *pending* though
   the code and BACKLOG mark it done. In DOC-102 terms it is golden+diverged
   and due for its own supersession pass (out of scope here).
2. **The dark-matter direction dominated.** The system repeatedly built VS
   concepts without citing them (VS0 gate, VS7 agents, VS13 provenance) —
   exactly the failure mode SAGA's coverage heuristics are meant to catch.
3. **The tournaments platform is well-founded.** Every VS2 prerequisite is
   live today; the gaps are precisely VS2 itself, plus VS9/VS10 and two VS1
   holes (session revocation, moderator tier).

## Suggested backlog follow-ups (NOT filed — noted here per task scope)

- Tournaments engine v0 (VS2 rewrite §roadmap): tournament registry +
  lifecycle state machine + NLHE engine, event-sourced.
- Scoreboard projection tables + read-only API (VS10 rewrite).
- Conduct/reliability signal capture for tournament play (VS9 rewrite).
- VS1 gaps: session revocation (token denylist or short-TTL + refresh
  invalidation) and a `moderator`/tournament-director role.
- Repair or retire the stale `app.js` endpoint bindings (`/me`, `/me/handle`,
  `/auth/google/start`) so the VS0 web ceremony verifiably completes.
- NORTHSTAR.md supersession pass to absorb this audit's dark matter.

---

*Originals: `docs/archive/kikoryu-vs-original/vs0.md` … `vs13.md` (verbatim).
Summary roadmap: `docs/NORTHSTAR_KIKORYU.md`. Superseding docs:
`docs/kikoryu/`.*
