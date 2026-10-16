-- SHANKPIT NOCK Materials -- per-material mass density (founder real-time, 2026-10-16: "ok we need to
-- calculate mass of the different materials, have it configurable on the NOCK material; just make it up
-- for now so we can calculate rigid body physics for the geometry that becomes disconnected").
--
-- kg per cubic metre. Consumed natively by SHANKPIT's destructible-brick rigid bodies
-- (packages/simulation/brick_bodies.h): a chunk of wall or glass that falls away, and every shard
-- thrown from a shot-out cell, gets mass = density x volume (a 0.2 m brick shard ~ 15 kg).
--
-- 0 = unset: the native engine then uses its own built-in per-kind default (brick 1900, concrete 2400,
-- wood 600, glass 2500), which is also what the seeded rows below carry -- "just make it up" numbers
-- close to the real ones, editable in /admin/nock's Materials screen.
ALTER TABLE shankpit_materials ADD COLUMN density REAL NOT NULL DEFAULT 0;

UPDATE shankpit_materials SET density = 1900 WHERE name = 'brick';
UPDATE shankpit_materials SET density = 2400 WHERE name = 'concrete';
UPDATE shankpit_materials SET density = 600  WHERE name = 'wood';
UPDATE shankpit_materials SET density = 2500 WHERE name = 'glass';
UPDATE shankpit_materials SET density = 7800 WHERE name = 'metal';
