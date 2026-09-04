# NORTHSTAR — IDUNA Notebook: an iCloud-Notes-shaped notes product (IN-000 / IN-001)

Real scoping pass for kanban priority-queue cards `IN-000` ("IDUNA NOTEBOOK - we need an icloud
like affordances for creating notes for iduna and iduna pro research") and `IN-001` ("vs 0 build
it out in IDUNA IDUNA NOTEBOOK... notebooks are sarena based but actually just advertised as
regular notes"). Real investigation before any code — Principle 19 (`EMILY/docs/THE_EMILY_WAY.md`)
— no code written this pass.

## Real, current state (investigated directly, not assumed)

- `SARENA_NOTEBOOK` already exists as a real **developer-portal tool-list entry**
  (`internal/http/handlers/portal.go`) — a Linode-Cloud-Manager-style links page (Jupyter,
  SARENA_NOTEBOOK) gated behind `devportal.access`, a permission "granted to nobody by default"
  per its own migration comment. This is real, dev-audience tooling, not a general-user product.
- `JEWEL` (`github.com/emilyspringerton/JEWEL`, this monorepo's own real, shipped v0) is the real
  thing "SARENA-based" technically means today: a real Jupyter kernel/backend for PARENA
  (`ipykernel.kernelbase.Kernel` subclass, shells out to the real `parena build` compiler + gcc
  **per cell**) — a genuine CODE notebook (write PARENA, run it, see output), served behind HTTP
  Basic Auth pending Google OAuth wiring.
- **Real, decisive mismatch this card itself names explicitly**: `IN-001`'s own wording —
  "notebooks are sarena based but actually just advertised as regular notes" — confirms the
  founder already knows JEWEL's own real code-execution UI is NOT what this card wants presented
  to users. The real ask is an **iCloud Notes-shaped experience**: plain prose notes (title, body,
  maybe folders/tags), no visible code cells, no "run this cell" chrome — even though the real
  backend storage/sync tech underneath may itself be implemented in PARENA (the literal "sarena
  based" half of the sentence), the same way iCloud Notes itself is CloudKit-backed without
  surfacing CloudKit as a concept to its own users.
- No notes CRUD API, schema, or UI exists anywhere in IDUNA today — checked directly, no
  `notes`/`note` table in any real migration, no `NotesHandler`.

## Real open questions (why this needs a founder decision, not a guess)

1. **Does "notes" reuse JEWEL's own real backend at all, or is it a genuinely separate,
   simpler CRUD feature that merely happens to be implemented in PARENA (an internal
   implementation-language choice, invisible to the end user)?** These are very different real
   builds: the first means adapting JEWEL's own kernel/cell-execution model to hide its own real
   code-running nature (real, awkward — JEWEL's whole reason for existing is code execution); the
   second means a plain, new `notes` table + REST API + UI, with "sarena based" satisfied purely
   by writing that backend's own logic (if any real logic beyond CRUD is needed) as a PARENA
   program compiled via `parena build`/`burrow build`, the same "generate once, commit, call by
   name" pattern this monorepo already uses everywhere else. Real, current read: the second is
   almost certainly the real intent, given the card's own explicit "NOT advertised as a notebook"
   framing — but this is a real, founder-level call, not assumed here.
2. **"For IDUNA and IDUNA PRO research" — whose notes are these?** Personal notes tied to one
   real IDUNA identity (a self-serve, per-user feature, the real iCloud Notes analogy), or a
   shared, team-visible research log (closer to a wiki/Confluence-shaped feature)? Changes the
   real data model (owner-scoped vs. shared) and the real ACL story.
3. **Where does this live — the existing gated developer portal, a new general-user surface, or
   both `IDUNA` (internal) and `IDUNA_PRO` (external product) each getting their own real,
   separately-scoped implementation** (the same real "one schema vs. two real products" question
   `MULTI_KANBAN_NORTHSTAR.md` already raised for kanban boards, worth deciding consistently
   across both asks rather than independently)?

## Real, phased plan (none started)

**Phase 1 — a real, minimal notes CRUD feature, personal/owner-scoped (the "iCloud Notes"
literal shape).** New `notes` table (`id, owner_user_id, title, body, created_at, updated_at`),
a real `NotesHandler` (`GET/POST/PATCH/DELETE /api/v1/notes[/:id]`), gated behind the caller's
own real IDUNA identity (JWT `sub`), not a shared admin permission — gets `IN-000`'s own literal
"creating notes" ask working end to end with the smallest real schema. Deliberately answers open
question 1 toward "separate CRUD feature," the real, smaller build — reversible if the founder's
own real answer is "no, it should genuinely be JEWEL-backed."

**Phase 2 — a real, simple UI.** A real, new page (not the gated dev portal — a real "Notes"
surface reachable the same way other self-serve IDUNA features are), list + edit view, no code-
cell chrome at all, matching the card's own explicit "advertised as regular notes" instruction.

**Phase 3 — the real "sarena based" backend, if genuinely needed.** Only if Phase 1's plain CRUD
turns out to need real, non-trivial server-side logic (search, tagging, some transform) — write
that piece as a real PARENA program compiled via `parena build`, following this monorepo's own
established "generate once, commit, call by name" mod-integration precedent, rather than
Go-native. Real, honest: plain CRUD likely doesn't need this at all; named as a real, conditional
phase, not assumed necessary.

**Phase 4 — `IDUNA_PRO` extraction, once IDUNA's own v0 is real and used.** Whether this becomes
a real, general `IDUNA_PRO` product feature (self-serve tenant notes) is a separate, later
decision — matches this repo's own established pattern of proving a feature inside IDUNA first,
extracting into `IDUNA_PRO` once real.

## Why this isn't done in one pass

Real, founder-level product questions (does this reuse JEWEL's own code-notebook backend or not;
personal vs. shared notes; where it's surfaced) change the actual schema/UI/backend-language
choice enough that guessing wrong means real rework. Real sub-tasks are logged in
`EMILY/BACKLOG.md` under this card's own section rather than folded into a single, unscoped
"build IDUNA Notebook" checkbox.
