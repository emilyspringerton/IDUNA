# VS10 — Scoreboards & Records for the Tournaments Platform

`supersedes: vs10.md` (archived at `docs/archive/kikoryu-vs-original/vs10.md`)
`status: not-yet-built-still-relevant — second supporting layer of VS2`
`consumes: VS2 tournament events; publishes: the official record`

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS10.*

---

## What VS10 is now

VS10 is the records layer: the "Recognized Narrative of Reality," where raw
tournament events become the official, citable standings. For a **social**
tournaments platform this is not decoration — the public record *is* the
social product: what entrants share, compare, and build history against.

## What exists today (verified fragments)

- `internal/http/handlers/players.go`: `GET /api/v1/players/{id}` — public
  kills/deaths/kd_ratio/sessions projection for SHANKPIT players; enriched by
  `player_profile.go` (job, fame, apples_count). Per-player stat projections —
  the right *kind* of thing, but not ranked, versioned, or published records.
- No scoreboard/leaderboard tables or handlers exist in IDUNA (grep: zero
  hits). This layer is genuinely open.

## Structural rules carried forward unchanged (they survive review whole)

- **Projection, not truth.** Scoreboards are derived from the event store and
  always recomputable. Never manually edited: corrections are new events plus
  a rebuild.
- **Published snapshots.** A standings view becomes *official* only when a
  director (VS7 capability, e.g. `scoreboards.publish`) publishes a snapshot;
  publication is itself an audited authority action.
- **Context-bound.** Every scoreboard belongs to a context — a tournament, a
  series, a season. Season/cycle identifiers should be first-class from v0
  (`season_id` now, forward-compatible with a fuller VS8 `cycle_id` if a
  reset protocol ever lands), so archived seasons remain queryable forever.
- **Server truth only.** Generated from the event store server-side; nothing
  client-supplied.

## Schema (adopted from the original)

- `contexts` — target of a scoreboard: game type, tournament/series/season id,
  status (`in_progress`/`complete`/`archived`).
- `scoreboard_snapshots` — computed metadata + `payload_json` at a point in
  time; the published-official flag and publisher identity live here.
- `leaderboard_entries` — rank + primary/secondary metrics per gamertag per
  context.

## API shape

- Public read-only: list contexts; latest *published* scoreboard per context;
  historical snapshots (the archive of past seasons and completed events);
  per-gamertag record (feeds the VS9 dossier).
- Privileged: enqueue recompute; publish snapshot — both director-gated and
  event-logged.

## Aesthetic

Ledgers and tabulated records, muted tones, mechanical rhythm. No glowing
numbers, no esports overlays. A completed tournament's page should read like
an entry in an institution's annual register.

## Done definition (v0)

Standings for every VS2 tournament derived from events; director-published
official snapshots; permanent public archive of completed contexts;
per-gamertag competitive record consumed by the VS9 dossier.
