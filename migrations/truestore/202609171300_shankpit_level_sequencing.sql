-- SHANKPIT NOCK level editor: real story level sequencing, Phase 1 (EMILY/BACKLOG.md S473,
-- SHANKPIT/docs2/specs/STORY_LEVEL_SEQUENCING_NORTHSTAR.md, founder real-time: "lets not work on
-- voxworld this is a legacy world ... we need a way to string 2 levels together ... we need the
-- loading points or whatever the opposite of the spawners is").
--
-- level_exits_json stores an array of {id,x,y,z,radius} objects -- a placed exit trigger volume,
-- the "opposite of a spawner." Same real "no cross-reference to validate" simplicity
-- characters_json already established. Same JSON-blob-column pattern every other scriptable
-- object kind on this table already uses.
--
-- next_level_id is nullable: NULL is a real, honest "end of the story" or "not part of a chain"
-- state, not an error. v0 is a real CHAIN (one next level per level), not a general branching
-- graph -- see the NORTHSTAR doc for why. Deliberately no FOREIGN KEY constraint (SQLite FKs are
-- off by default in this codebase's own connection setup, matching every other cross-row
-- reference in this table, e.g. shankpit_level_objects.ref_level_id) -- validated in Go instead
-- (LevelStore.validateNextLevelID).
--
-- is_story_start mirrors is_default_queue's own exact real shape: exactly one level may hold it
-- at a time, enforced in Go (LevelStore.SetStoryStartLevel), not a SQL partial unique index.
ALTER TABLE shankpit_levels ADD COLUMN level_exits_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE shankpit_levels ADD COLUMN next_level_id INTEGER;
ALTER TABLE shankpit_levels ADD COLUMN is_story_start INTEGER NOT NULL DEFAULT 0;
