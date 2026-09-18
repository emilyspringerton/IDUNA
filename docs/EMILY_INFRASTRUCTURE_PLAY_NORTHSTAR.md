# The Infrastructure Play — Three Business Lines, One Real Technical Base

Registered as `EMILY-INFRA-PLAY-NORTH`. Real, honest scoping — this is a business-direction
document, not an implementation plan. No code changes follow from this doc alone.

## Where this came from

Founder real-time, 2026-09-18: "we want to allow for the possibility of a big infrastructure
play - one prong is specializing in migrating LARGE organizations off of MS SQL into cloud or
ORCL or even potentially on prem but its more of a cloud play" — plus two shorter follow-ups
naming the other two lines: "businesses -lines hardened workstation replacement" and "Zero trust
IAM system to prevent security risks from internal agents (agents never get real keys)."

Alongside the direction itself, the founder pasted two long blocks of AI-generated pitch/strategy
content (a security-market-validation pitch, and a "modern NeXT" three-pillar company strategy).
Per this monorepo's own established discipline for a pasted external spec (`LO/NORTHSTAR.md`'s
own S208-01 review, `DEADWEIGHT/NORTHSTAR.md`'s own critical pass on its source transcript): read
in full, evaluated critically, not rubber-stamped. See "What got corrected, not just copied"
below — the founder's own explicit `(needs check for hallucination)` note on one of the three
pitched pillars is honored directly, not smoothed over.

## The three lines, honestly graded by how real each one is today

### 1. Hardened workstation / "Citrix killer" — earliest-stage, real technical seeds exist

A lightweight, zero-trust local/remote workplace environment. The real technical seeds already
in this monorepo: `PITVIPER` (SDL2 terminal emulator with Emily Prime integration hooks),
`DUNG`/`BURROW` (a unified terminal+editor, PARENA-native, NORTHSTAR-only — no code yet), `SAND`
(a PARENA-native code editor, named only). None of these are a workstation-replacement product
today — they're editor/terminal tooling, a real but partial piece of the picture. No remote-
streaming/session-broker layer (the actual "Citrix killer" claim) exists anywhere in this
monorepo yet. **Status: vision, not scoped.** Real next step, if this line gets prioritized,
would be its own NORTHSTAR — not attempted here.

### 2. Zero-trust IAM for agents — the most mature line, already has its own real doc

`IDUNA/docs/EMILY_FOR_BUSINESS_NORTHSTAR.md` (S243-02) already did the real, grounded work here —
this doc doesn't repeat it, it cross-references it. That doc's own "What IDUNA actually has
today" section is the accurate, checked version of the pitch's own "agents never get real keys"
claim: agents authenticate via their own scoped `agent_secret` to `POST /api/v1/auth/agent` and
receive a JWT with **explicit, enumerated `permissions[]`, no role inheritance** — an agent never
holds the JWT signing key, another agent's secret, or a raw database credential. Secret rotation
is logged as an event, never the plaintext (verified live). That's real, and it's a genuine
differentiator versus IAM products that treat machine identity as an afterthought.

That same doc also already names the real gap between "internal backbone with zero-trust-flavored
properties" and "a sellable zero-trust product": no multi-tenancy, no self-serve onboarding, no
continuous device/posture verification, no compliance attestation. Nothing in this pass changes
that status — **Status: real internal capability, not yet a sellable external product**, exactly
as that doc already concluded.

### 3. MS SQL migration play (the big one) — least mature as a product, most real as a technical base

This is the line with genuinely new, concrete engineering behind it as of this same week
(`EMILY/BACKLOG.md` SECTIONS 498/499/207-10) — not a repurposed old primitive, real work built
for exactly this direction:

- **`project-mssql!`** (`PARENA/stdlib/log/projector.prn`) — shells out to FreeTDS's `tsql` CLI,
  the real MSSQL/TDS connectivity primitive, live-verified (a real `tsql` binary against an
  unreachable host fails fast, never hangs).
- **`database/mssql-util.prn`** (`PARENA/stdlib/database/`) — real T-SQL-to-Postgres/Oracle type
  transpile: `UNIQUEIDENTIFIER`→`UUID`, `DATETIME2`→`TIMESTAMP` (7→6 fractional digits, truncated
  not rounded), `BIT`→`BOOLEAN` (with a genuinely invalid value reported as a real error, never
  silently coerced — the kind of correctness discipline an enterprise data-migration customer
  would actually need to trust).
- **`RedisStreamSink`/`RedisStreamConsumer`** (`PRRJECT_FATBABY/internal/eventsink/`) — real,
  tested zero-downtime cutover mechanics: a new consumer joining an existing Redis Streams
  consumer group picks up exactly the remaining, un-acked work, with zero gap and zero
  duplication. This is the real, general mechanism a live customer database migration would need
  for "don't miss a single event during cutover" — proven on our own infrastructure this week,
  not yet proven against a live MSSQL source.

**Important, honest framing**: PRRJECT_FATBABY's own event log isn't MSSQL-sourced — it's NDJSON.
So this is NOT "we already migrated our own MSSQL database as a live case study." What IS real:
the same zero-downtime consumer-group mechanism, and the same MSSQL connectivity/type-transpile
primitives, are the actual building blocks a real customer migration would need — we built and
proved the mechanism on our own infrastructure before ever pointing it at a customer's database,
which is a genuinely defensible "we don't ask customers to be our first test" position.

**Real, honest gaps before this is a sellable migration product**, named directly, not glossed
over:
- **No CDC (change-data-capture) mechanism.** A real "migrate a large org off MS SQL with zero
  downtime" engagement needs continuous replication of a LIVE, changing production database (the
  standard tools here are things like Debezium, SQL Server's own CDC/Change Tracking features, or
  a custom log-shipping reader) — not just a one-shot type-transpile of already-extracted rows.
  What's built today transpiles VALUES; it does not yet capture ongoing CHANGES from a live MSSQL
  instance. This is the single largest real gap between what exists and what the pitch describes.
- **No schema-migration tooling** — table/index/constraint/stored-procedure translation is a real,
  separate, much larger problem than the three scalar value conversions built so far.
- **No customer-facing tooling, UI, or packaging at all** — everything built so far is
  library-level PARENA/Go code, not a product.
- **Cloud-first scope, not yet decided in detail.** The founder's own framing ("more of a cloud
  play") points at Postgres-on-managed-cloud (Cloud SQL, RDS, etc.) as the primary target, with
  Oracle and on-prem as secondary/opportunistic targets — real, deliberate prioritization, not
  fully scoped into a phased plan yet.

**Status: the most product-shaped of the three lines is still the least-built as an external
product** — but it's the one with real, fresh, test-verified engineering under it, which the
other two don't currently have to the same degree.

## What got corrected, not just copied, from the pasted pitch content

Per this doc's own opening framing — applying critical review, not rubber-stamping:

- **"PARENA... never touches the stack... eliminates buffer overflows" is an overclaim, corrected
  here.** PARENA's real, checked guarantee (`PARENA/NORTHSTAR.md`) is **compile-time region-typed
  memory safety** — a region-rank escape check that prevents use-after-free/dangling-reference
  bugs without a garbage collector or manual free. That is a real, large, legitimate class of
  memory-safety CVEs (a large fraction of real-world C/C++ zero-days trace back to exactly this
  bug class) — a genuinely strong, honest security story on its own. It is NOT the same claim as
  "never touches the stack" (the emitted C code still uses the stack for ordinary function calls,
  like any C program) or "immune to buffer overflows" (no array-bounds-checking guarantee is
  established anywhere in this codebase's own docs — checked directly, not found). The real claim
  is narrower and still worth making; the overclaimed version would be a real, checkable
  liability the moment a technical buyer's own engineer looked closely.
- **Specific "confirmed incidents" cited in the pasted market-validation pitch (a named Google
  Threat Intelligence Group case, a specific "under 10 hours" breach timeline) are NOT verified
  by this doc and are deliberately not restated here as established fact.** The general trend
  they're gesturing at — AI-assisted and increasingly autonomous attack tooling is a real,
  widely-discussed, growing security concern — is real and fine to reference at that general
  level; specific unverified incident claims are not repeated as fact.
- **The "Ad-Garbage Phone OS" idea is treated as explicitly speculative, not a committed pillar** —
  the founder's own `(needs check for hallucination)` note is honored directly. Real, concrete
  technical doubt worth naming: modern mobile OSes (iOS/Android) don't generally allow an
  app/ROM-level layer to intercept and rewrite what OTHER apps' own embedded ad SDKs report
  (that's not "scraping the phone" from outside — it's each app's own bundled SDK code running
  inside that app's own sandbox) — the mechanism as pitched doesn't map cleanly onto how mobile ad
  telemetry actually works. Not rejected outright, just correctly labeled: **unresolved, not a
  real third pillar today**, distinct from line 1 (hardened workstation) which at least has real
  precedent code to build from.

## Real, honest bottom line

Of the three lines, the DB-migration play is the one worth putting real, near-term engineering
weight behind — not because it's the most finished (it isn't), but because it's the one that
already has fresh, tested, non-hypothetical code under it, and a clear, narrow next real gap (CDC)
rather than an entirely-unscoped vision (line 1) or an already-well-understood "needs
multi-tenancy and compliance attestation before it's sellable" gap list (line 2, already
documented). The zero-trust IAM line is real and valuable but is IDUNA's own already-tracked
story, not new work this doc should duplicate. The hardened-workstation line and the phone-OS idea
both stay at "named, not scoped" — real ambition, no real claim to have started building them as
products.

## Open questions for the founder — not resolved here, by design

1. **Priority and resourcing**: does the DB-migration line get dedicated, sustained engineering
   time now, or does it stay opportunistic alongside the ongoing PRRJECT_FATBABY K8s migration
   work it's growing out of?
2. **CDC approach**: build a custom MSSQL change-reader, or FFI/shell-bind an existing real tool
   (Debezium is Java/Kafka-Connect-shaped — a real, heavier dependency than this monorepo's own
   "shell out to a CLI" convention elsewhere; SQL Server's native CDC feature reads from
   transaction-log tables and could plausibly be queried the same `tsql`-shell-out way
   `project-mssql!` already does) — a real, undecided architecture choice, not guessed at here.
3. **First real target**: a friendly pilot customer/org, or continue proving the mechanism
   further on internal infrastructure first?
4. **How this relates to `IDUNA_PRO`/`EMILY_FOR_BUSINESS`'s own existing licensing** (`EMILY_FOR_BUSINESS/LICENSE.md`,
   the Emily License v0) — is a DB-migration service offering a services engagement (consulting-
   shaped, license question mostly moot) or a licensed software product (the existing license
   terms would need real review against this specific use case) — not decided here.

## Related

- `IDUNA/docs/EMILY_FOR_BUSINESS_NORTHSTAR.md` — the real, existing, deeper analysis for line 2
  (zero-trust IAM); this doc cross-references it rather than duplicating it.
- `EMILY/BACKLOG.md` SECTIONS 498, 499, 207-10 — the real, shipped engineering this doc's own line
  3 analysis is grounded in.
- `PARENA/NORTHSTAR.md` — the real, checked source for what PARENA's region-typed memory model
  actually guarantees (used above to correct the pasted pitch's own overclaim).
- `PITVIPER`, `DUNG`, `SAND` — the real, partial technical seeds for line 1, each still
  NORTHSTAR-only or narrower than a workstation-replacement product.
