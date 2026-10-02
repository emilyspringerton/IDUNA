-- Destructible-brick damage persisted with a level (founder real-time, 2026-10-02: "the destructable
-- brick is basically like mining in minecraft ... it needs to save that geometry"). Stored as the
-- compact record list the game's own brick engine already uses (wall index, cell key, hp) rather than
-- as baked boxes: authored walls stay intact and editable in NOCK, the 100-wall native cap is never
-- hit by carved geometry, and the vertices/edges/faces/normals are regenerated deterministically from
-- the box + cell grid on load. '[]' = undamaged, which is every pre-existing level.
ALTER TABLE shankpit_levels ADD COLUMN brick_damage_json TEXT NOT NULL DEFAULT '[]';
