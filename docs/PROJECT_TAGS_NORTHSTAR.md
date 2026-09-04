# NORTHSTAR — project tags / PM automations page (PMPX-000)

Real scoping pass for kanban priority-queue card `PMPX-000`: *"build out additional project
management automations like a page showing the tags for the different projects etc - auto
researching on a -000 card."* A real, exploratory seed card (the `-000` suffix matches this
session's own observed convention for root/not-yet-broken-down asks, e.g. `IN-000`/`MULTIKANBAN-
000`) rather than a concrete spec — Principle 19 (`EMILY/docs/THE_EMILY_WAY.md`), real
investigation before any code, no code written this pass.

## Real, current state (investigated directly, not assumed)

- **"Projects" is not a formalized concept anywhere in this monorepo's own data model.** Checked
  directly: no `projects` table, no tag column on `kanban_cards` or the Apples ledger. Checked for
  any existing "tag" concept and found none relevant (`mmo_schema.sql`'s own `tag` column is a
  linkshell short-code, `gamertag` is a username field — unrelated).
- The closest real, existing analogs to "a project": `EMILY/BACKLOG.md`'s own `SECTION` numbers
  (e.g. `S202`, a real, already-used grouping the whole backlog/kanban/Apples system already keys
  off), and Apples' own `source_repo` field (every Apple already carries which real repo it came
  from). Neither is a real "tag" system (many-to-many, freeform) — both are single-value,
  structural groupings already baked into other fields.
- `MULTI_KANBAN_NORTHSTAR.md` (this same session) already scoped a related, real gap: kanban
  boards have no per-board grouping today either. A real "tags for projects" page and multi-board
  kanban are adjacent, possibly overlapping asks (a "project" could just mean "a kanban board" once
  that exists) — worth deciding together, not independently, once the founder weighs in on either.

## Real open questions (why this needs a founder decision, not a guess)

1. **What counts as "a project" here?** A real `SECTION` in BACKLOG.md (already exists, zero new
   schema)? A real repo (`source_repo`, already exists on Apples, not on kanban cards)? A new,
   freeform concept the founder has in mind that doesn't map cleanly onto either existing
   grouping? The literal ask ("a page showing the tags for the different projects") assumes tags
   already exist to be shown — they don't, so this card's own real first step is defining what a
   tag/project actually IS here before any page can show them.
2. **Is this the same ask as `MULTIKANBAN-000`'s own board concept, or genuinely separate?** If
   "project" turns out to mean "kanban board" once multi-board support exists, this card may not
   need its own new grouping primitive at all — just a real UI view over that one.
3. **What's the real, concrete first automation, beyond "a page"?** The card's own "etc" and
   "additional... automations" wording is open-ended — a real, scoped v1 needs one concrete
   feature named (a tag-filtered kanban/Apples view? auto-tagging new cards by their own
   `SECTION` prefix? something else?), not an unbounded "PM automations" umbrella.

## Real, minimal, buildable-now option (if the founder wants the smallest real v1)

Given `SECTION` and `source_repo` already exist as real, structural groupings, the cheapest real
"tags for projects" page needs **zero new schema**: a real, new read-only dashboard
(`GET /admin/projects` or similar) aggregating existing kanban cards + Apples by their own already
-real `SECTION`/`source_repo` values — a real, small SQL `GROUP BY`, not a new tagging system. This
answers the literal "a page showing tags for the different projects" ask directly, deferring the
harder "what's a real freeform tag" question until it's actually needed.

## Why this isn't done in one pass

The card's own `-000` framing and "etc"/open-ended wording mark it as a real seed for further
discussion, not a spec — building blind risks inventing a tagging system the founder didn't
actually want (freeform tags) when a much smaller, real, existing-data dashboard (Option above)
might fully satisfy the literal ask. Real sub-tasks are logged in `EMILY/BACKLOG.md` under this
card's own section rather than folded into a single, unscoped "build PM automations" checkbox.
