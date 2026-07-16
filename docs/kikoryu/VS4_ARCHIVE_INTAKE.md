# VS4 — Archive Intake API Gateway (STATUS: reincarnated-elsewhere)

`supersedes: vs4.md` (archived at `docs/archive/kikoryu-vs-original/vs4.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS4.*

VS4 specified an upstream data gateway — ingest workers, normalizers,
content-hash dedup, provenance on every item, cache-first read-only API —
built to feed the VS3 market game. **Nothing of it exists in IDUNA**
(verified: no ingest/normalizer/OHLC/news code).

The concept was reinvented, without citation, where the data actually earns
revenue:

- **PRRJECT_FATBABY** — the SEC/PR signal pipeline is exactly "pull upstream,
  normalize to canonical events, store with provenance, serve read-only," for
  intelligence consumers instead of a game.
- **EINHORN INDEX** (`EMILY/docs/NORTHSTAR_INDEX.md`) — crawl-and-normalize
  knowledge graph; IDUNA proxies it at `/api/v1/kgraph/query`
  (`internal/http/handlers/kgraph.go`, S138-06) with a research cache
  alongside (`research.go`, S137-03).

**Disposition:** superseded. IDUNA will not grow its own ingest layer; the
tournaments platform has no upstream-market-data dependency. If VS3 is ever
revived as a tournament format, its price feed should be a thin consumer of
FATBABY's existing pipeline — VS4's fairness rule (evaluate seasons on stored
snapshots, never live recomputation) is the one clause to carry forward.
