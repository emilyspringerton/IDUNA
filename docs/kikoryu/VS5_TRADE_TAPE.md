# VS5 — "Follow My Trades" Export Streams (STATUS: reincarnated-elsewhere, mechanism only)

`supersedes: vs5.md` (archived at `docs/archive/kikoryu-vs-original/vs5.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS5.*

VS5 specified opt-in, sanitized, replayable public streams of VS3 trading
activity: export projector + outbox, cursor pagination + SSE, anti-sniping
delay, hard PII rules. The *product* was never built (VS3 doesn't exist). The
*mechanism* shipped without citation: `GET /api/v1/stream/user-events`
(`internal/http/handlers/stream.go`) is an SSE stream over an append-only
event log with `from_seq` cursor replay — VS5's architecture exactly,
currently carrying `local_user.*` events for Colab consumers. No opt-in
model, sanitization layer, or delay exists.

**Disposition:** superseded as specified; **reincarnation target identified.**
The tournaments platform (VS2) wants a **tournament tape** — a delayed,
sanitized spectator stream of hands/eliminations/standings. When built, seed
it from stream.go's pattern and carry forward VS5's non-negotiables verbatim:
opt-in visibility per player, configurable delay (anti-sniping becomes
anti-ghosting at a poker table — same rule, higher stakes), zero PII in the
export, and replayability from any cursor. Specified as a social feature in
`VS2_TOURNAMENTS.md`; no separate build track.
