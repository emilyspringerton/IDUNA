# VS11 — TYLER Cutscene Publishing API (STATUS: superseded-by-different-reality)

`supersedes: vs11.md` (archived at `docs/archive/kikoryu-vs-original/vs11.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS11.*

VS11 specified an episode-packet syndication pipeline: immutable,
content-addressed "Episode Packets" (dialogue/camera/stage/audio tracks),
platform adapters (THE_CITY_OF_LIGHT, Unity, web), POWDERHORN sponsor blocks
as governed metadata, publishing as a VS7-permissioned action.

**Reality went another way.** TYLER became a TV-series repo — series bible +
82 episode scripts + universe engine (`TYLER/README.md`,
`TYLER/universe_engine.md`) — with video produced by MoneyPrinterTurbo's
flat-stream compilation, not packet syndication. IDUNA's only trace of TYLER
is `migrations/truestore/202606070001_tyler_agent.sql`: a registered
`tyler-agent` with `tyler.rsi.write` — i.e. TYLER is an IDUNA *agent* (VS7's
live model), not a syndication API. No episode/packet/cutscene code exists in
IDUNA (verified).

**Disposition:** no longer part of the IDUNA roadmap and irrelevant to the
tournaments platform. The packet format, provenance-signed manifests, and
adapter model remain a good design; if episode syndication is ever wanted, it
belongs in the TYLER repo, consuming IDUNA only for agent auth and audit.
POWDERHORN_COFFEE_ROASTERS lives on independently via STINKIES COMMISSAIRE
(EMILY/BACKLOG.md SECTION 140). Revisit only if the founder reopens it.
