-- NOCK level editor: per-level floor tint (kanban #533, founder real-time: "nock tools levels we need
-- to be able to tint the floor so its not transparent it needs to be color selectable per level and
-- alpha channel setable for partially transparency"). JSON {"r","g","b","a"} (0..1), '' = untinted
-- (the engine's own default floor). Set via a dedicated setter like `enclosed`, not threaded through
-- CreateLevel/UpdateLevel's positional signature.
ALTER TABLE shankpit_levels ADD COLUMN floor_tint_json TEXT NOT NULL DEFAULT '';
