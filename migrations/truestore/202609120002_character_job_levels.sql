-- Per-job character leveling (kanban priority queue, GFD-124433, founder: "when you are a lvl
-- 10 warrior in gfd and you switch to RDM for the first time you go back to lvl 1 and you can
-- level up to 5 and switch back to your level 10 war or to the lvl 1 mnk separate lvls/job").
--
-- Real, confirmed-live bug this closes: `characters.level`/`current_xp` (202606230001_mmo_schema)
-- are ONE pair per character, shared across every job -- apps2/mud's cmdSetJob never touched
-- them at all when switching jobs, so a level-10 WAR who switches to RDM just keeps showing
-- level 10 on RDM too, with no way to level each job independently. Same real "one row per
-- (character, X) pair" shape `character_skills` (same migration file) already established for
-- an analogous per-character-per-thing need.
--
-- job is a real job.JobID string (WAR/RDM/etc., GoblinFoxDragon's own server/job package is the
-- authoritative list -- deliberately not FK'd/enum-constrained here, same "don't couple to
-- another repo's Go constants" reasoning gfd_items.go's own duplicated category list already
-- documents). Real migration path for characters that existed before this table: apps2/mud seeds
-- a job's first-ever row from the character's own legacy level/current_xp columns the first time
-- that job is played after this ships, rather than a one-time backfill script here -- see
-- apps2/mud/main.go's own loadJobXP doc comment for the real mechanics. characters.level/
-- current_xp are NOT retired: apps2/mud keeps mirroring the currently-active job's own level
-- into them on every change, so any other consumer reading a character's plain "level" (e.g. the
-- MMO API's own GetCharacter response) keeps showing a real, meaningful number.
CREATE TABLE IF NOT EXISTS character_job_levels (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    character_id    CHAR(36) NOT NULL,        -- FK -> characters.character_id
    job             VARCHAR(4) NOT NULL,      -- e.g. "WAR", "RDM"
    level           INTEGER  NOT NULL DEFAULT 1,
    current_xp      INTEGER  NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (character_id) REFERENCES characters(character_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_character_job_levels_char_job ON character_job_levels(character_id, job);

-- Reuses the same M2M-agent-only write gate handleUpdateLevel already established (a client
-- self-reporting its own level/XP is a cheat vector) -- see handleUpdateJobLevel's own doc
-- comment in internal/http/handlers/mmo.go.
