-- SHANKPIT NOCK level editor v0 (EMILY/BACKLOG.md SECTION 459, S459-01, founder real-time:
-- "so v0 it and start working dont worry about the current levels lets just go full level select
-- brawlpit repo exact model for now") -- a real, standalone-row CRUD store, mirroring
-- BRAWLPIT's own online level editor (internal/brawlpit.LevelStore /
-- 202609130001_brawlpit_levels.sql) field-for-field in shape, adapted from BRAWLPIT's 2D
-- Platform2D to SHANKPIT's own real, existing native geometry primitive.
--
-- walls_json stores an array of {id,x,y,z,sx,sy,sz,r,g,b,friction} objects -- the EXACT real
-- shape SHANKPIT/packages/map/map.h's own `Wall` struct already defines (center x/y/z, full
-- extents sx/sy/sz -- packages/map/map.c's own collision code computes bounds as center +/-
-- size/2, not min/max corners, confirmed by reading that file directly rather than assumed), so a
-- level authored here is byte-for-byte what GameMap's own real `Wall walls[100]` array expects,
-- no translation layer. width/height/depth are the editor's own real authored level dimensions
-- (S459-03, "choose level dimensions") -- SHANKPIT's native loader does not read these back (same
-- real "authoring metadata, not native state" role BRAWLPIT's own width/height already play for
-- its 2D canvas).
CREATE TABLE IF NOT EXISTS shankpit_levels (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    width           REAL     NOT NULL DEFAULT 100,
    height          REAL     NOT NULL DEFAULT 50,
    depth           REAL     NOT NULL DEFAULT 100,
    walls_json      TEXT     NOT NULL DEFAULT '[]',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_shankpit_levels_name ON shankpit_levels(name);

-- Reuses iduna.admin, same gate BRAWLPIT's own /admin/nock/api/brawlpit-levels editing surface
-- already uses -- no new permission needed. Same real, named-not-silent scope choice as that
-- table's own migration comment: a real player-facing IDUNA-login decision is a separate,
-- deferred question (EMILY/BACKLOG.md SECTION 459's own "not yet decided" list), not solved here
-- just to let v0 exist.
