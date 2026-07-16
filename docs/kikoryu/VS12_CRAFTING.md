# VS12 — Crafting & Material Transformation (STATUS: reincarnated-elsewhere)

`supersedes: vs12.md` (archived at `docs/archive/kikoryu-vs-original/vs12.md`)

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS12.*

VS12 specified deterministic material transformation for KIKORYU — "nothing
from nowhere," lineage-checked inputs (VS13), crafting events
(`CraftingJobStarted`, `MaterialTransformed`, `ArtifactCreated`…), an
institutional-workshop UI.

Crafting shipped — for a **different MMO**, with no citation of this spec:
DragonsNShit (GoblinFoxDragon studio). In IDUNA:
`POST /api/v1/items` in `internal/http/handlers/mmo.go` ("S75-02/03/04/05 +
S129-05: DragonsNShit MMO API handlers") creates crafted items with
`provenance_chain[0]` set; `character_skills`
(`202606230001_mmo_schema.sql`) and the S129 inventory/equipment layer
(`202606250002_mmo_inventory.sql`) surround it. Product doctrine lives in
`GoblinFoxDragon/docs2/MMO_NORTHSTAR.md` and
`GoblinFoxDragon/docs2/INVENTORY_EQUIPMENT_NORTHSTAR.md`. The shipped model
is simpler than VS12: items are created with a provenance seed — there are no
recipes, no deterministic input-consumption ("nothing from nowhere" is *not*
enforced), no crafting event vocabulary.

**Disposition:** superseded; custody of the crafting concept transfers to the
DragonsNShit/GoblinFoxDragon line. Irrelevant to the tournaments platform.
If DragonsNShit ever hardens its economy, VS12's anti-chaos rules
(deterministic recipes, forbid infinite loops, server-authoritative
transformation) are the reference — revisit there, not here.
