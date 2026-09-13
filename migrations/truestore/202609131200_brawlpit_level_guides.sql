-- S418-01: NOCK guide-based snapping (BRAWLPIT Levels tab), founder real-time full requirements
-- doc, core principle: "No grid. Ever. ... snapping exists solely to align against guides the
-- author placed deliberately."
--
-- guides_json stores an array of {axis, coord, locked, is_mirror_axis} objects -- see
-- internal/brawlpit.Guide's own real shape. This is AUTHORING METADATA ONLY (the doc's own
-- 1.4: "Guides are level data and save with the level. They are authoring metadata -- the game
-- client must never load or care about them.") -- deliberately never surfaced in
-- LevelStore.Export/ExportDoc, so BRAWLPIT's own native loader can never see it even by accident.
ALTER TABLE brawlpit_levels ADD COLUMN guides_json TEXT NOT NULL DEFAULT '[]';
