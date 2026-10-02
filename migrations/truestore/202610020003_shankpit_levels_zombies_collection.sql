-- ZOMBIES level repository (founder real-time, 2026-10-02: "have a new repository of zombie levels ...
-- the same affordances for levels should work for zombies"). Zombie levels live in the same table so
-- every level affordance (edit in NOCK, clone, export, brick-damage snapshots) works unchanged; the
-- `collection` column is the "repository": 'levels' (default, every pre-existing row) or 'zombies'.
-- is_zombie_default marks the one level the ZOMBIES sandbox loads (exactly one among the zombies
-- collection, enforced in Go like is_story_start / is_default_queue). No default => the client falls back to "nextown".
ALTER TABLE shankpit_levels ADD COLUMN collection TEXT NOT NULL DEFAULT 'levels';
ALTER TABLE shankpit_levels ADD COLUMN is_zombie_default INTEGER NOT NULL DEFAULT 0;
