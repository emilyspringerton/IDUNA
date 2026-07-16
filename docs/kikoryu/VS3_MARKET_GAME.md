# VS3 — Play Money Stock Market Game (STATUS: superseded-by-different-reality)

`supersedes: vs3.md` (archived at `docs/archive/kikoryu-vs-original/vs3.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS3.*

VS3 specified a paper-trading game: virtual bankrolls, daily-close order
fills, 2–4 week seasons, leaderboards by % return. **None of it was built**
(verified: no orders/portfolios/seasons code or tables in IDUNA).

The market-data energy went somewhere structurally different: PRRJECT_FATBABY
is a real SEC/PR **financial signal pipeline** — the company sells market
*intelligence*, it does not run a market *game*. IDUNA's only finance-adjacent
surfaces proxy that world (`/api/v1/kgraph/query` → EINHORN INDEX,
`internal/http/handlers/kgraph.go`; research cache, `research.go`).

**Disposition:** no longer part of the roadmap. If the founder ever reopens
it, a paper-trading competition should be implemented as a **tournament
format on the VS2 platform** (season = tournament context, VS10 standings,
VS9 integrity signals), not as a standalone system — the original's hard
constraints (no real assets, no real execution, "entertainment only"
disclaimer) carry forward verbatim in that case.
