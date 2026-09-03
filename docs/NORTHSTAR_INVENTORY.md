# NORTHSTAR — IDUNA Inventory (electronics/component tracking + AI guidance)

**Status:** Draft v0.1 — northstar only, no implementation yet
**Date:** 2026-09-03
**Founder framing, verbatim, three related priority-queue cards treated as one real feature, not
three separate ones:**
- `AI-ITL`: "I need to create an inventory of all of my electronics equipment so ai can help
  guide me in terms of what components i have and what i need and what is possible etc - IDUNA
  inventory system"
- `II-001`: "web app super simple to start - IDUNA inventory system for example i have 2
  rasbberyy pi b2 and 2 zero i think? also i have at least 1 ada feather with some kinda
  packetmodule etc"
- `II-102`: "design IDUNA inventory system affordances simple but useful"

## 1. What this actually is, in one sentence

A real, personal electronics/component inventory (what you own, in what quantity, where it is)
that an LLM (Emily Prime, or a Claude Code session like this one) can query directly — "what can
I build with what I already have," "what am I missing for X," "is this project even possible
with my current parts" — the real differentiator `AI-ITL` names over a plain spreadsheet.

## 2. Real, concrete seed inventory, named by the founder, not invented

`II-001`'s own example is the real v0 test data, not a hypothetical:
- 2× Raspberry Pi (Model B2 — founder's own "i think?" honestly carried over, not silently
  corrected to a guessed exact model)
- 2× Raspberry Pi Zero
- 1× (at least) Adafruit Feather with "some kinda packet module" — almost certainly an
  Adafruit Feather with an RFM9x LoRa radio (the real, common Adafruit Feather + packet-radio
  combo), but named honestly as uncertain rather than assumed, matching the founder's own
  uncertainty. Real, deliberate design consequence below (§4): the data model must tolerate
  "I'm not 100% sure of the exact model" as a first-class, valid entry, not force a rigid
  part-number field the founder can't fill in confidently.

This is also the real connective tissue to this monorepo's own existing hardware-facing work:
`FLASH`/`image-builder-rpi` (S213, the HypriotOS/Raspberry Pi distro thread) already needs to know
which real Pi hardware exists to target — this inventory is the real, authoritative place that
answer should live, not re-asked every time.

## 3. Real placement: an IDUNA app, same pattern as `drive`/`blog`/Vault

Same real "monorepo-custom app hosted inside IDUNA behind the portal" pattern
`IDUNA_PRO`'s own extraction-scoping doc already named for `drive`/`blog`/`vault`/`promptoverse` —
this is founder-personal, single-operator data with no multi-tenant story, so it does NOT belong
in `IDUNA_PRO` (that repo's whole reason to exist is the opposite: generic, tenant-facing
primitives). Real, concrete shape, mirroring `internal/http/handlers/drive.go`'s own established
convention:
- New `internal/inventory/` package (store + query logic).
- New `internal/http/handlers/inventory.go` (HTTP handlers), wired into the existing router the
  same way `drive.go`/`blog.go` already are — reuses IDUNA's own existing session/JWT auth,
  no new auth mechanism.
- New migration `migrations/truestore/<timestamp>_inventory.sql`, matching
  `202606140001_drive_sync_log.sql`'s own real, established naming convention.
- A new nav entry in the dev portal (`internal/http/handlers/portal.go`), same as every other
  sibling app.

## 4. Real data model, v0

One real table, kept deliberately flat and simple — `II-102`'s own "simple but useful" framing,
not a normalized parts-taxonomy database:

```sql
CREATE TABLE inventory_items (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,       -- "Raspberry Pi Zero", free text, not a rigid part number
    category        TEXT NOT NULL,       -- 'sbc' | 'radio' | 'sensor' | 'power' | 'cable' | 'misc' (open-ended, not an enum constraint — a new category shouldn't be a schema migration)
    quantity        INTEGER NOT NULL DEFAULT 1,
    location        TEXT,                -- free text ("desk drawer", "bin 3") -- real, optional, not GPS/barcode-precise
    notes           TEXT,                -- the real place "some kinda packet module" honesty lives -- uncertainty is a valid, first-class note, not a blocker to entering the item at all
    tags            TEXT,                -- comma-separated free tags ("wireless", "3.3v", "needs-soldering") -- real, simple, matches this feature's own "start simple" framing rather than a many-to-many tag table v0 doesn't need yet
    acquired_at     TEXT,                -- ISO date, optional
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
```

Real, deliberate choices, named explicitly:
- `category` and `tags` are free text, not foreign keys into a taxonomy table — a real, honest v0
  boundary matching this monorepo's own "don't over-engineer past what's actually needed"
  discipline (the same judgment call `pentest/pcap.prn`'s own opaque `I32` handle already made
  over a richer struct). Real, separate, later work if search/filtering ever genuinely needs it.
- No barcode/UPC field, no vision-based auto-cataloging in v0 — named directly in §6 as a real,
  deferred stretch goal, not silently promised here.

## 5. Real API surface — the actual point of `AI-ITL`

Plain CRUD first (`GET/POST/PATCH/DELETE /api/v1/inventory/items`), same REST shape every other
IDUNA handler already uses — nothing novel there. The real, load-bearing addition `AI-ITL`
actually asks for:

- `GET /api/v1/inventory/items` returning the full real inventory as JSON — this alone is enough
  for Emily Prime (or a Claude Code session, exactly like this one) to answer "what do I have"
  questions directly, no new AI-specific endpoint required for that half.
- A real, second, genuinely new capability: `POST /api/v1/inventory/query` taking a free-text
  question ("can I build a LoRa weather station with what I have?") and the real, current
  inventory contents, and returning an LLM-generated answer — the actual "ai can help guide me...
  what is possible" ask, not just a raw data dump. Real, honest scope: this is a real LLM call
  (the same class of infrastructure `EMILY_FOR_BUSINESS_NORTHSTAR.md`'s own Emily-Prime-as-a-
  service framing already assumes exists), not a hand-rolled recommendation engine — the inventory
  table is the real, structured context fed into an ordinary prompt, no separate "compatibility
  graph" data structure invented for this.

## 6. Real, phased plan

**Phase 1 — the real `II-001` ask ("web app super simple to start")**: the schema above, plain
CRUD handlers, and a real, minimal web UI — one list view (search/filter by category or tag,
free-text search over `name`/`notes`), one add/edit form. No auth complexity beyond IDUNA's own
existing session cookie. Seed data: the real founder-named items in §2, entered by hand as the
first real proof the schema holds up.

**Phase 2 — the real `II-102` ask ("affordances simple but useful")**, built only once Phase 1's
real shape is proven, not designed in the abstract ahead of it:
- Quick-add: a single free-text input ("2x pi zero") that a small LLM call parses into
  name+quantity, rather than always requiring the full form — real, useful, low-effort given
  Phase 1's own API already exists to call into.
- A low-quantity/"you might be out" visual flag (quantity ≤ some real, user-set threshold per
  item) — a real, simple, useful affordance named directly by "useful," not scope-crept into a
  full alerting system.
- Tag-based browsing (click a tag, see everything with it) — free given `tags` is already just
  text, no new backend work.

**Phase 3 — the real `AI-ITL` ask ("ai can help guide me")**: the `/inventory/query` endpoint
from §5, plus a real, minimal chat-style UI panel on the inventory page itself (ask a question,
get an answer grounded in the real, current inventory — not a separate app, a panel bolted onto
Phase 1's own page).

**Real, deferred stretch goals, named honestly, not promised for v0**: barcode/UPC lookup,
photo-based cataloging (take a picture of a bin, let a vision-capable Claude session identify and
bulk-add items — a real, genuinely exciting future capability given this monorepo already has
Claude Code access, but a real, separate, later scoping pass, not this one), and any
multi-location/multi-user story (this is explicitly a single-operator personal inventory today).

## 7. Real, open questions, not resolved here

- Exact Raspberry Pi models: the founder's own "i think?" on the B2 count is left as an honest
  uncertainty in the seed data (§2), not resolved by guessing — a real, later correction pass by
  the founder, not blocking Phase 1 from shipping.
- Where do future photo/attachment uploads live, if Phase 3+ ever needs them — the same real GCS
  bucket `KUBERNETES_MIGRATION.md`'s own backup pipeline already uses is the obvious real
  candidate, not decided here.

## Related

- `IDUNA/docs/NORTHSTAR_PASSWORD_MANAGER.md` — the real, direct structural precedent this doc's
  own format follows (a named, scoped sibling-feature NORTHSTAR living inside IDUNA, not the
  whole-IDUNA doc).
- `IDUNA/docs/EMILY_FOR_BUSINESS_NORTHSTAR.md` — the real "monorepo-custom app behind the portal"
  categorization (`drive`/`blog`/`vault`/`promptoverse`) this feature's own placement (§3) follows.
- `FLASH`/`image-builder-rpi` (S213) — the real, existing hardware-facing thread this inventory's
  own real Raspberry Pi seed data (§2) is the natural, authoritative answer for.
