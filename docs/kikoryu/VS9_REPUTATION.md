# VS9 — Reputation & Credence for the Tournaments Platform

`supersedes: vs9.md` (archived at `docs/archive/kikoryu-vs-original/vs9.md`)
`status: not-yet-built-still-relevant — first supporting layer of VS2`
`consumes: event store (VS2 play events, VS1/VS7 governance events)`

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS9.*

---

## What VS9 is now

VS9 is the institutional memory of participants: stable, event-derived
signals of reliability and conduct. The original framing survives intact —
**not karma, not badges, a bureaucratic record** — but the scope narrows from
"social physics of a civilization" to what the tournaments platform needs on
day one: is this entrant reliable, and what is their conduct history?

## What exists today (verified fragments — none of them player reputation)

- `internal/http/handlers/monitors.go` + `auth.Monitor`
  (`202606250003/4_monitors.sql`): reliability tracking for *services* —
  heartbeat/cron/deadman check-ins, healthy/failing status, overdue
  computation, Slack/email alerting. Conceptually VS9's Reliability Index
  applied to infrastructure. Useful precedent, wrong subject.
- `player_fame` (read at `internal/http/handlers/player_profile.go:90`):
  Frequency/Bloc/Procurement fame for GFD players — faction reputation as a
  projection. DragonsNShit-scoped, not IDUNA-general.
- The Apples ledger: a working governance-history record for *agents*.

No reputation, credence, or dossier system for human participants exists
(grep: zero hits). This layer is genuinely open.

## The credence stack, tournaments-scoped (from the original's four axes)

1. **Reliability Index** — derived from VS2 events: abandon rate (leaving
   live tournaments), disconnect patterns, no-show rate after registration.
   The number a director sees before seating someone in a slow-structure
   event.
2. **Conduct History** — the visible record of interactions with authority:
   suspensions, warnings, disqualifications, commendations — all already
   flowing through `iam_event_stream`/userlog once VS1's moderation surfaces
   land. Queryable by directors (VS7 capability), summarized on the dossier.
3. **Competitive Integrity** — collusion/chip-dumping signals from hand
   histories (VS2's deterministic event trail makes this computable);
   multi-accounting signals from the identity layer. Replaces the original's
   "Market Integrity" axis (VS3 is off the roadmap).
4. **Authority Credibility** — for directors themselves: tenure, intervention
   history, overturn rate. Keeps VS7 authority self-auditing.

## The dossier

One page per gamertag: an archival record — tournament history (from VS10),
reliability figures, conduct record. Registry aesthetic (rows, compartments,
Garamond titles / Helvetica data), no scores-as-dopamine, no social-media
tropes. Public by default for competitive records; conduct detail visible to
directors only (VS7 visibility boundaries).

## Rules carried forward unchanged

- **Derived only.** Every signal recomputable from events; no manual edits —
  corrections happen by emitting corrective events (mirrors VS10's rule).
- **Persistence.** The dossier outlives seasons and any future world/season
  resets (VS8): reputation is the thing a participant permanently owns.
- **Predictive, not punitive.** Signals prioritize director attention; they
  do not auto-punish. Enforcement stays a human (VS7) act, on the record.

## Event vocabulary (adopted from the original, trimmed)

`ConductSignalEmitted` · `ReliabilityIndexRecalculated` ·
`GovernanceActionRecorded` · `ActorCommendationIssued` ·
`DossierSnapshotCreated`

## Done definition (v0)

Reliability Index computed from VS2 event history; conduct history queryable
by directors; dossier view live per gamertag; signals surfaced at tournament
registration and in director tooling.
