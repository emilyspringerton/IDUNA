-- IDUNA Notebook, Phase 1 (kanban IN-000/IN-001: "we need an icloud like affordances for
-- creating notes for iduna and iduna pro research... notebooks are sarena based but actually
-- just advertised as regular notes"; full real scoping in docs/IDUNA_NOTEBOOK_NORTHSTAR.md).
--
-- owner_subject is the raw real JWT `sub` claim string (not a numeric FK into `users`) --
-- deliberate, matching this scoping pass's own real finding that this monorepo's own subject
-- shape varies by auth path (a real, already-known, separate gap: local-auth's own "local:<N>"
-- subjects don't resolve through GET /api/v1/identities/me the same way Google-OAuth subjects
-- do). Storing the raw subject string sidesteps that gap entirely rather than depending on it --
-- ownership is "whoever's real JWT sub created this note," full stop, regardless of which real
-- auth path issued that token.
CREATE TABLE IF NOT EXISTS notes (
    id            INTEGER  PRIMARY KEY AUTOINCREMENT,
    owner_subject VARCHAR(255) NOT NULL,
    title         VARCHAR(200) NOT NULL,
    body          TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notes_owner ON notes(owner_subject, updated_at DESC);
