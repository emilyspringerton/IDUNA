# VS13 — Source Material Lineage & Provenance (STATUS: reincarnated-elsewhere)

`supersedes: vs13.md` (archived at `docs/archive/kikoryu-vs-original/vs13.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS13.*

VS13 specified the ontological rule — no artifact without origin — via four
canonical source types (Harvested/Dropped/Crafted/Issued), material events
(`MaterialSplit/Merged/Consumed/Transformed/Issued`), and a
`materials` + `material_lineage` reference-chain schema.

The idea was **independently reinvented** for DragonsNShit with zero paper
trail back to this spec: `items.provenance_chain`
(`migrations/truestore/202606230001_mmo_schema.sql` — "Every item has a UUID
and a full provenance chain as JSON… array of {actor_id, action, at}"),
maintained by `internal/http/handlers/mmo.go`: creation seeds
`provenance_chain[0]`, `POST /api/v1/items/:id/transfer` appends,
`GET /api/v1/items/:id` returns the full chain, deletes are soft. Built under
S75-03 (EMILY/BACKLOG.md SECTION 75, marked done). Item provenance is a named
core system in `GoblinFoxDragon/docs2/MMO_NORTHSTAR.md`.

The shipped model is a **custody log**, not VS13's lineage graph: an in-row
JSON append trail with no split/merge semantics, no source-type taxonomy, no
material events, no ancestry across transformations.

**Disposition:** superseded; the concept's living home is
DragonsNShit/GoblinFoxDragon. Irrelevant to the tournaments platform. If
DragonsNShit's economy ever needs dupe-proofing at VS13's level of rigor
(split/merge lineage, event-store-as-authority-for-matter), the original spec
is the strongest design on file — revisit it from that repo.
