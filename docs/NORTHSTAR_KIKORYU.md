# KIKORYU — Product Roadmap

*KIKORYU is IDUNA's single-shard MMO consumer domain — named as a peer of FATBABY and SECWATCH
since IDUNA's original IAM pivot (see `iam-spec.md` §1) but, until this roadmap, never given its
own build plan. Fourteen versions (VS0–VS13) across four phases, each building stable identity
first and authoring "reality" last.*

*2026-07-16 update: a code-verified reconciliation of these fourteen specs against the running
system now lives at `docs/VS_REALITY_AUDIT.md`, with per-spec superseding documents in
`docs/kikoryu/` and the founder-written originals archived verbatim at
`docs/archive/kikoryu-vs-original/`. Near-term product thrust: social tournaments platform
(VS2, supported by VS9 + VS10).*

---

## Phase 1: Foundations of Identity and Authority (VS0 – VS2)

**VS0 — The Ritual Gate:** Focuses on identity, gating, and game handoff for the single-shard MMO, KIKORYU.
Features: Google OAuth, THE_HONOR_CODE acceptance (using Rose Gold "consent metal"), permanent gamertag reservation, and device auth polling for game clients.
Visuals: "Gold leaf on parchment under northern light" — calm, sacred, and ethereal.

**VS1 — IAM & Moderation:** Operationalizes Identity and Access Management for support and moderation.
Features: Email magic links/OTP, session management (revoke/logout_all), and the first iteration of the "Back Office" (Aunt Sally) UI for user searches, suspensions, and audit logs.

**VS2 — Competitive Institutions (Poker):** Introduces the first competitive layer built on stable identity.
Features: Play-money No-Limit Texas Hold'em (MTT) tournaments. Chips are purely fictional, non-redeemable, and isolated to tournament instances to prevent economy exploits.

## Phase 2: The Economic and Data Layer (VS3 – VS6)

**VS3 — Economic Simulation (Market Game):** A paper trading game allowing players to manage virtual portfolios and compete in seasons.
Features: Orders fill at daily close prices; leaderboards rank players by return and equity.

**VS4 — Archive Intake (API Gateway):** An institutional ingest layer that normalizes upstream market and news data to feed the VS3 market game.
Features: Proxy workers pull data from multiple sources (RSS/APIs) and normalize them into internal formats, preserving provenance and attribution.

**VS5 — Trade Tape Export (Follow My Trades):** Exposes a sanitized, permissioned event stream of VS3 trading activity for social feeds or external dashboards.
Features: Opt-in "TradeFilled" events appear in streams with an optional delay to prevent "sniping".

**VS6 — Monetized Permissions (Licenses):** Introduces the KIKORYU Dual/Multibox License.
Features: Stripe integration for subscription-based licenses that grant users the right to operate multiple concurrent sessions, enforced by the game server.

## Phase 3: Governance and Continuity (VS7 – VS9)

**VS7 — Governance & World Authority:** Transforms the system into an operational governance infrastructure.
Features: Establishes "Agents" (privileged actors with bounded authority like GMs and Mods) vs. "Unagents" (normal players). Every privileged action is audited in the Event Store.

**VS8 — Auraborialis (Reboot Protocol):** A cosmology reset mechanic that allows for new world cycles without erasing the archive.
Features: The "Event Store" remains immutable while "True Store" world state projections (economies, rankings) are reinitialized for a new epoch.

**VS9 — Credence & Reputation:** Establishes institutional memory of behavior through stability signals for governance.
Features: Reliability indices and historical credibility metrics replace traditional "karma" to help the world feel self-stabilizing.

## Phase 4: Reality Authorship (VS10 – VS13+)

**VS10 — Scoreboard / Records Projection:** A socially authoritative projection of reality summaries derived from events.
Features: Cycle-aware ledgers for poker, market seasons, and governance actions. These are recomputable summaries of "Recognized Narrative of Reality".

**VS11 — TYLER Cutscene Publishing:** A "story syndication" layer for publishing Forever Drama episodes to multiple platforms.
Features: Portable episode packets containing manifest data, timing, and transcripts, with tasteful sponsorship integration (e.g., Powderhorn Coffee Roasters).

**VS12 — Crafting & Material Transformation:** Transitions players from consumers to authors of the world state through reality transformation.
Features: Players reconfigure existing materials into new forms (artifacts, props, devices) based on deterministic transformation rules and recipes.

**VS13 — Material Lineage & Provenance (Bonus Layer):** The "anti-chaos backbone" of the crafting system.
Features: Every material unit carries a traceable ancestry (Harvested, Dropped, or Crafted), ensuring that no artifact can be "magically spawned" without causal integrity.

---

*Ingested 2026-07-16 from `iduna_roadmap.md` (founder-provided, placed at `/home/fatbaby/` root).
Reformatted for markdown structure only — content and wording preserved as given, except one
obvious paste artifact corrected ("OBOBOBVS0" → "VS0" in the VS0 heading). Cross-references
confirmed against existing canon: KIKORYU already named as a peer consumer domain to FATBABY/
SECWATCH in `iam-spec.md` §1 and `cmd/bob-agent/prompt.go`; VS11's "Powderhorn Coffee Roasters"
matches the existing STINKIES COMMISSAIRE coffee blend (EMILY BACKLOG.md SECTION 140). No backlog
items have been written against this roadmap yet — this is registration/intake only.*
