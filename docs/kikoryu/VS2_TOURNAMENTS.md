# VS2 — Tournaments: The Social Tournaments Platform

`supersedes: vs2.md` (archived at `docs/archive/kikoryu-vs-original/vs2.md`)
`status: not-yet-built-still-relevant — THE PRIMARY PRODUCT DIRECTION`
`supporting layers: VS9 (reputation), VS10 (scoreboards)`

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS2. Nothing of this exists in
code yet (verified: zero grep hits for tournament/poker/leaderboard/standings
across IDUNA); every prerequisite it depends on does.*

---

## What VS2 is now

The founder has set IDUNA's product evolution explicitly toward **a social
tournaments platform**. VS2 is that platform's spec. The original document
described play-money No-Limit Hold'em tournaments as one "competitive
institution"; this rewrite keeps NLHE as the launch format but promotes the
frame: **tournaments are the product, poker is the first game.** The
institutional register — structured competition, archival records, quiet
seriousness — is the identity of the platform, not a skin on a poker app.

## Foundations already live (verified, cited)

| Need | Live today | Where |
|---|---|---|
| Stable identity + permanent table name | Yes | VS0 gate: `users.gamertag` UNIQUE, JWT `gamertag` claim (`docs/kikoryu/VS0_IDENTITY_GATE.md`) |
| Conduct baseline | Yes | THE_HONOR_CODE versioned acceptance (VS0) |
| Moderation + audit rails | Yes (gaps noted) | RBAC, `iam_event_stream`, userlog, Back Office (`docs/kikoryu/VS1_IAM_MODERATION.md`) |
| Game-server / table-server auth | Yes | M2M `POST /api/v1/auth/agent`; device bridge for clients (`/auth/device/*`) |
| Event-sourced pattern | Yes | `event_store` table (device migration), userlog append-only log |
| Paid entitlements, if ever needed | Yes | Stripe webhook subscription rails (`internal/http/handlers/subscriptions.go`) |
| Missing before launch | — | Session revocation + moderator tier (VS1 gaps), the tournament engine itself, VS9/VS10 projections |

## Hard constraints (carried forward verbatim in spirit — non-negotiable)

- **Closed, non-redeemable economy.** No deposits, withdrawals, secondary
  markets; chips have zero cash value. No gambling vocabulary — Chips, Stack,
  Tournament Entry, Blind Levels, Standings.
- **Tournament-isolated chips (Model A).** Chips exist only inside a
  tournament instance; every entrant starts with an equal stack. No persistent
  bankroll, no farming, no inflation.
- **Server authority.** Cards, RNG, dealing, betting rules entirely
  server-side; clients render and submit input, nothing else.
- **One seat per identity.** Single active seat per account, join
  rate-limiting, reconnect handling that cannot mint advantage.

## The social layer (what "social tournaments platform" adds to the original)

- **Standings as the shared object** — every result projects into VS10; a
  player's tournament record is public, permanent, and cite-able.
- **Reputation-aware entry** — VS9 conduct/reliability signals surface at
  registration (abandon rates, prior suspensions) and feed director tooling.
- **Spectation tape** — a delayed, sanitized event stream of hands/standings
  for followers, seeded from the proven SSE+cursor mechanism in
  `internal/http/handlers/stream.go` (see `VS5_TRADE_TAPE.md` — this is
  VS5's honest reincarnation target).
- **Institutions, not lobbies** — recurring series, named events, and season
  archives (VS8's season-close doctrine in miniature) rather than anonymous
  one-off tables.

## Lifecycle state machine (unchanged from original — it survives review)

`CREATED → REGISTERING → STARTING → IN_PROGRESS → FINAL_TABLE → COMPLETE`

- REGISTERING: entrant list + start timer public; registration is the
  rose-gold consent moment.
- STARTING: entries lock, seats drawn, equal stacks initialized — all as
  events.
- IN_PROGRESS: blind progression, eliminations, automatic table balancing.
- FINAL_TABLE: presentation shifts; the record becomes historically notable.
- COMPLETE: standings finalized as events; VS10 projection published by a
  director (VS7 authority action).

## Technical requirements

- **Event-sourced core**: every hand, action, elimination, and balance move
  is an event; deterministic hand histories enable replay, dispute
  resolution, and the spectation tape. The projection ("True Store" tables:
  tournaments, entries, tables, seats, standings) is always recomputable.
- **Auditability**: director interventions (seat corrections, disqualifications)
  are VS7 authority actions in `iam_event_stream` with justification.
- **Integrity**: deterministic, seeded, server-held RNG with an auditable
  commitment scheme (publish seed hash at STARTING, seed at COMPLETE).

## Aesthetic

Unchanged from the original: no Vegas, no slot-machine energy, no glowing
overlays. A tournament page reads like a registry entry: name, blind level,
stack, placement. Gold for structure, rose gold only for irreversible acts
(registering, disqualifying). Garamond for ceremony, Helvetica for data.

## Done definition (v0)

1. Register/login via IDUNA (VS0 gate) and join a tournament.
2. Engine runs the full lifecycle automatically, NLHE rules + table balancing
   server-enforced.
3. Every hand and outcome in the event store; standings recomputable.
4. Standings visible via VS10 projection; results permanent.
5. Director role (VS7/VS1) can intervene, fully audited; session revocation
   works (VS1 gap closed).
