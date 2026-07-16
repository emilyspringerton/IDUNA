# KIKORYU VS0–VS13 — Original Founding Specs (ARCHIVED)

**Status: superseded — historical record. Do not edit these files.**

These fourteen documents (`vs0.md` … `vs13.md`) are the founder-written original
vision for KIKORYU and the IDUNA "civilization stack," written before IDUNA
pivoted into the ecosystem-wide IAM / trust-authority service it is today
(`docs/iam-spec.md`, HQ-SPEC-IAM-094). They were recovered from the repo-tree
root (`/home/fatbaby/vs0.md` … `vs13.md`) and archived here verbatim on
2026-07-16 — including paste artifacts ("OBOBOB" etc.), which are preserved
deliberately. SAGA doctrine (HQ-SPEC-DOC-102) is append-only: old documents are
superseded, never edited or deleted.

| File | Original title |
|---|---|
| vs0.md | The Ritual Gate — identity, THE_HONOR_CODE, gamertag, device auth |
| vs1.md | IAM & Moderation — Back Office (Aunt Sally), magic links, RBAC |
| vs2.md | Competitive Institutions — play-money NLHE poker tournaments |
| vs3.md | Play Money Stock Market Game — paper trading, seasons |
| vs4.md | Archive Intake — upstream market/news data gateway |
| vs5.md | Follow My Trades — sanitized trade-tape export streams |
| vs6.md | Dual/Multibox License — Stripe-backed concurrency entitlements |
| vs7.md | Governance & World Authority — Agents vs. Unagents |
| vs8.md | Auraborialis Reboot Protocol — cyclical world resets |
| vs9.md | Credence & Reputation Layer — institutional memory of actors |
| vs10.md | Scoreboard / Records Projection Layer |
| vs11.md | TYLER Cutscene Publishing API — episode packet syndication |
| vs12.md | Crafting & Material Transformation |
| vs13.md | Source Material Lineage & Provenance |

## Where the living versions are

- **`docs/VS_REALITY_AUDIT.md`** — the code-verified reconciliation of each
  spec against the running system (what shipped, what was reinvented under
  another name, what was superseded, what remains open).
- **`docs/kikoryu/`** — the superseding documents, one per VS. VS0/1/2/7/9/10
  are full rewrites; VS3/4/5/6/8/11/12/13 are short supersession/status
  documents. Each carries a `supersedes: vs<N>.md` line tracing back here.
- **`docs/NORTHSTAR_KIKORYU.md`** — the summary roadmap ingested 2026-07-16
  from the founder's `iduna_roadmap.md`.

## Why superseded

The system evolved past these specs without ever reconciling the paper trail:
parts of VS0 shipped and run today undocumented, VS13's provenance idea was
independently reinvented for DragonsNShit (`internal/http/handlers/mmo.go`),
and the founder has since set the near-term product thrust to a **social
tournaments platform** (VS2, supported by VS9 + VS10). The audit and the
`kikoryu/` docs are the honest, current statement of each spec's fate.
