# NORTHSTAR — multiple kanban boards (MULTIKANBAN-000)

Real scoping pass for kanban priority-queue card `MULTIKANBAN-000`: *"the kanban is a good
primative i want to move it up the abstraction layer i want IDUNA and IDUNA PRO to give the
ability to create multiple kanbans."* Real investigation before any code — Principle 19
(`EMILY/docs/THE_EMILY_WAY.md`) — real open questions named, real phased plan, no schema/API
changes made this pass.

## Real, current state (investigated directly, not assumed)

- `kanban_cards` (`migrations/truestore/202608260001_kanban_cards.sql`) is a **single, global,
  hardcoded table** — no board/tenant scoping column exists at all. `queue` is a hardcoded
  3-value enum (`backlog`/`priority`/`cruise`), not a foreign key to any board concept.
- `backlog_item_id` is deliberately tied to **one specific file**:
  `KanbanHandler.BacklogPath` (`EMILY_BACKLOG_PATH` env var, defaulting to the literal
  `/home/fatbaby/EMILY/BACKLOG.md`) — the whole handler's own real design assumes exactly one
  backing document, git-authoritative, singular.
- Access control is a single, shared `iduna.admin` permission gate (`RequirePermission`) — the
  same doc comment in the migration itself notes this was a deliberate choice ("internal
  sprint-planning tooling for whoever already runs the Back Office, not a separate product
  surface"), not a per-board/per-tenant ACL model.
- **This is a real, structural mismatch with the card's own "IDUNA and IDUNA PRO" framing**:
  `IDUNA_PRO` is the standalone, extracted, multi-tenant product other real customers will run —
  its own customers have no `EMILY/BACKLOG.md` at all. A kanban board backed by a specific git
  file is a real, EINHORN_INDUSTRIAL-specific design choice that doesn't generalize to arbitrary
  `IDUNA_PRO` tenants without real changes to what a "card" even points at.

## Real open questions (why this needs a founder decision, not a guess)

1. **What does a board's own cards actually track for a generic `IDUNA_PRO` tenant?** This
   monorepo's own kanban is backlog-file-item-tracking (`backlog_item_id` -> a real line in
   `BACKLOG.md`). A tenant with no such file needs cards to be self-contained (their own
   title/body/status, no external file reference) — a real, different card shape, not just "the
   same table with a `board_id` column added."
2. **One schema for both, or two real products?** Does `EMILY`'s own EINHORN_INDUSTRIAL board
   become "board #1" of a new real multi-board `kanban_boards` table (same schema, `board_id`
   added to `kanban_cards`, this org's own board keeps its file-backed behavior as one board's own
   special case), or does `IDUNA_PRO` get a genuinely separate, simpler, self-contained kanban
   implementation (no `BacklogPath`/git-sync concept at all) that only superficially resembles
   this one? The real, decisive technical question underneath: is git-backed sync (`backlog.
   ParseFile`, `syncNewItemToBacklogGitIfMissing`, `archiveBacklogItem`, real git commit/push) a
   real, general "board" feature every tenant should get, or a EINHORN_INDUSTRIAL-specific
   integration that stays special-cased to board #1 only?
3. **Who can see/edit which board?** Real, new ACL model needed — `iduna.admin` is a single,
   global permission today; multi-tenant boards need real per-board (or per-org) authorization,
   the same real shape IDUNA's own multi-tenant primitives (`store.IAMStore`) already model for
   other resources, but not yet wired to kanban at all.
4. **Does the real queue set (`backlog`/`priority`/`cruise`) stay fixed, or does a board get to
   define its own columns?** The card's own "move it up the abstraction layer" wording could mean
   either "many boards, same 3 fixed columns each" (smaller, real, buildable-now-shaped change) or
   "boards with their own custom column sets" (materially bigger — `validKanbanQueues` becomes
   real per-board data, not a fixed Go map).

## Real, phased plan (none started)

**Phase 1 — a real `kanban_boards` table + `board_id` on `kanban_cards`.** The smallest real
schema change: `kanban_boards(id, name, backlog_path NULL, owner, created_at)`, `kanban_cards`
gains `board_id INTEGER NOT NULL REFERENCES kanban_boards(id)`. EINHORN_INDUSTRIAL's own existing
board becomes a real, migrated `board_id=1` row with `backlog_path` set to the real, current
`EMILY_BACKLOG_PATH` default — zero behavior change for the one board that exists today. A new
board created with `backlog_path = NULL` gets self-contained cards (no git-file sync attempted at
all for it) — answers open question 1/2 without yet deciding whether non-file-backed cards need
their own richer body/description field (a real, separate, smaller follow-up).

**Phase 2 — per-board authorization.** Real, new permission model: board creation gated behind a
real permission (`kanban.boards.manage`?), per-board read/write ACL rows (mirroring `store.
IAMStore`'s own existing per-resource-permission shape), replacing the single global `iduna.
admin` gate — real, structural IAM work, not a quick patch.

**Phase 3 — board management UI + API.** `POST /api/v1/kanban/boards`, `GET .../boards` (list
boards the caller can see), a board-switcher on the existing `/admin/kanban` page (today a single,
implicit board with no navigation concept at all).

**Phase 4 (only if Phase 1's open question 4 resolves toward "yes")** — per-board custom columns,
a real, separate, larger schema/UI change (`validKanbanQueues`'s own hardcoded Go map becomes
real, per-board data).

## Why this isn't done in one pass

Real, founder-level product questions (does `IDUNA_PRO` even want git-file-backed sync as a real
feature, or is that EINHORN_INDUSTRIAL-only; per-board vs. per-org authorization boundary; fixed
vs. custom columns) change the real schema and API shape enough that building any one
interpretation blind risks a real, costly rework once `IDUNA_PRO`'s actual first real multi-tenant
kanban customer shows up. Real sub-tasks are logged in `EMILY/BACKLOG.md` under this card's own
section rather than folded into a single, unscoped "add multi-tenancy to kanban" checkbox.
